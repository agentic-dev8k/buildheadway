package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

const browserEvidenceRecordLimit = 32 << 20

func readEvidenceJSON(root *os.Root, name string, limit int, value any) error {
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("saved browser evidence record must be regular; symlinks are not accepted")
	}
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := ReadLimited(file, int64(limit))
	if err != nil {
		return fmt.Errorf("saved browser evidence record exceeds %d bytes", limit)
	}
	if err = json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("invalid saved browser evidence record: %w", err)
	}
	return nil
}

// Decode only the fields needed for inspection, without migrating a checkpoint
// or interpreting legacy history, model settings or report-controlled paths.
func InspectBrowserEvidence(ctx context.Context, repo, id string, lastCheck bool, reportID string) (BrowserEvidenceReport, error) {
	result := BrowserEvidenceReport{Artifacts: []BrowserArtifact{}, Integrity: "not_recorded"}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	repo, err := filepath.Abs(repo)
	if err != nil {
		return result, err
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return result, err
	}
	defer root.Close()
	kind, file := "runs", "state.json"
	if lastCheck {
		kind, file = "checks", "report.json"
		var latest struct {
			ID string `json:"id"`
		}
		if err = readEvidenceJSON(root, ".forge/last-check.json", browserEvidenceRecordLimit, &latest); err != nil {
			return result, err
		}
		id = latest.ID
	} else if reportID != "" {
		kind, file, id = "browser-checks", "report.json", reportID
	} else if id == "" || id == "latest" {
		var latest struct {
			RunID string `json:"run_id"`
		}
		if err = readEvidenceJSON(root, ".forge/latest.json", 4096, &latest); err != nil {
			return result, err
		}
		id = latest.RunID
	}
	if !validRunID.MatchString(id) {
		return result, errors.New("invalid saved browser evidence ID")
	}
	relative := filepath.Join(".forge", kind, id)
	dir, err := root.OpenRoot(relative)
	if err != nil {
		return result, err
	}
	defer dir.Close()
	var record struct {
		Version int           `json:"version"`
		ID      string        `json:"id"`
		RunID   string        `json:"run_id"`
		Status  string        `json:"status"`
		Checks  []CheckResult `json:"check_results"`
		Results []CheckResult `json:"results"`
	}
	if err = readEvidenceJSON(dir, file, browserEvidenceRecordLimit, &record); err != nil {
		return result, err
	}
	checks := record.Checks
	if kind == "runs" {
		if record.RunID != id || record.Version < 1 || record.Version > stateVersion {
			return result, errors.New("browser evidence checkpoint identity or version does not match")
		}
	} else {
		if record.ID != id || record.Version != 1 {
			return result, errors.New("browser evidence report identity or version does not match")
		}
		checks = record.Results
	}
	if len(checks) > 4096 {
		return result, errors.New("browser evidence record contains more than 4096 checks")
	}
	_, rows, auditErr := auditBrowserEvidence(ctx, repo, filepath.Join(repo, relative), checks)
	if auditErr != nil && !errors.Is(auditErr, errBrowserEvidence) {
		return result, auditErr
	}
	result.ID, result.Kind, result.Directory, result.RecordedStatus, result.Artifacts = id, kind, filepath.ToSlash(relative), record.Status, rows
	if len(rows) > 0 {
		result.Integrity = "valid"
	}
	for _, row := range rows {
		if row.Integrity == "invalid" {
			result.Integrity = "invalid"
			break
		}
		if row.Integrity == "not_recorded" {
			result.Integrity = "partial"
		}
	}
	return result, nil
}
func FormatBrowserEvidence(result BrowserEvidenceReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Browser evidence · %s · %s\nRecorded status: %s · integrity: %s\n", result.Kind, result.ID, result.RecordedStatus, result.Integrity)
	for _, row := range result.Artifacts {
		fmt.Fprintf(&out, "[%s] %s · check passed: %t\n", row.Integrity, row.Command, row.CheckPassed)
		if row.Evidence != nil {
			name := strings.ReplaceAll(row.Evidence.Screenshot, `\`, "/")
			fmt.Fprintf(&out, "  %s/%s\n  SHA256 %s · %d bytes · %d×%d\n", result.Directory, name, row.Evidence.SHA256, row.Evidence.Bytes, row.Evidence.Width, row.Evidence.Height)
		}
		if row.Problem != "" {
			fmt.Fprintf(&out, "  %s\n", row.Problem)
		}
	}
	if len(result.Artifacts) == 0 {
		out.WriteString("No browser screenshot evidence was recorded.\n")
	}
	out.WriteString("Integrity inspection does not rerun checks or approve the application.")
	return out.String()
}

type browserEvidenceOptions struct {
	Project, Run, Report string
	LastCheck, JSON      bool
}

func parseBrowserEvidenceOptions(args []string, project string, output io.Writer) (browserEvidenceOptions, error) {
	o := browserEvidenceOptions{Project: project, Run: "latest"}
	f := flag.NewFlagSet("buildheadway browser evidence", flag.ContinueOnError)
	f.SetOutput(output)
	f.StringVar(&o.Project, "project", project, "Project folder")
	f.StringVar(&o.Run, "run", "latest", "Saved goal ID")
	f.StringVar(&o.Report, "report", "", "Saved browser verify report ID")
	f.BoolVar(&o.LastCheck, "last-check", false, "Inspect the latest standalone check")
	f.BoolVar(&o.JSON, "json", false, "Print JSON")
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if f.NArg() != 0 {
		return o, errors.New("browser evidence accepts flags only; use --run ID or --report ID")
	}
	selectors := 0
	f.Visit(func(v *flag.Flag) {
		if v.Name == "run" || v.Name == "report" || v.Name == "last-check" && o.LastCheck {
			selectors++
		}
	})
	if selectors > 1 || o.Run == "" || hasFlag(f, "report") && o.Report == "" {
		return o, errors.New("select one nonempty --run ID, --report ID or --last-check")
	}
	return o, nil
}
func hasFlag(f *flag.FlagSet, name string) bool {
	found := false
	f.Visit(func(v *flag.Flag) {
		if v.Name == name {
			found = true
		}
	})
	return found
}
func runBrowserEvidenceCommand(args []string) int {
	o, err := parseBrowserEvidenceOptions(args, ".", os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return fail(err)
	}
	parent, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	result, err := InspectBrowserEvidence(ctx, o.Project, o.Run, o.LastCheck, o.Report)
	if err != nil {
		return fail(err)
	}
	if err = outputValue(result, FormatBrowserEvidence(result), o.JSON); err != nil {
		return fail(err)
	}
	if result.Integrity == "invalid" {
		return fail(errBrowserEvidence)
	}
	return 0
}
