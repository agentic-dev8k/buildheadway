package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var errBrowserEvidence = errors.New("browser screenshot evidence is unavailable or changed")

type BrowserArtifact struct {
	Command     string           `json:"command"`
	CheckPassed bool             `json:"check_passed"`
	Integrity   string           `json:"integrity"`
	Evidence    *BrowserEvidence `json:"evidence,omitempty"`
	Problem     string           `json:"problem,omitempty"`
}
type BrowserEvidenceReport struct {
	ID             string            `json:"id"`
	Kind           string            `json:"kind"`
	Directory      string            `json:"directory"`
	RecordedStatus string            `json:"recorded_status"`
	Integrity      string            `json:"integrity"`
	Artifacts      []BrowserArtifact `json:"artifacts"`
}

func browserEvidenceRoot(repo, dir string) (*os.Root, error) {
	repo, err := filepath.Abs(repo)
	if err != nil {
		return nil, err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(repo, dir)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return nil, errors.New("browser evidence directory escapes the project")
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.OpenRoot(relative)
}

// The recorded execution result is retained. Integrity failures are independent
// metadata and remain latched until a new check actually executes.
func auditBrowserEvidence(ctx context.Context, repo, dir string, checks []CheckResult) ([]CheckResult, []BrowserArtifact, error) {
	updated := append([]CheckResult{}, checks...)
	rows := []BrowserArtifact{}
	var root *os.Root
	var rootErr error
	opened := false
	defer func() {
		if root != nil {
			root.Close()
		}
	}()
	invalid := []string{}
	for i, check := range checks {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if check.Browser == nil && !strings.HasPrefix(check.Command, "@browser:") {
			continue
		}
		row := BrowserArtifact{Command: check.Command, Integrity: "not_recorded"}
		problem := check.EvidenceError
		if check.Browser != nil {
			if !check.Browser.valid() {
				problem = "recorded browser screenshot metadata is invalid"
			} else {
				row.Evidence = check.Browser
				if !opened {
					root, rootErr = browserEvidenceRoot(repo, dir)
					opened = true
				}
				if rootErr != nil {
					problem = "saved browser evidence directory is unavailable or escapes the project"
				} else {
					actual, err := browserPNGAt(root, check.Browser.Screenshot)
					if err != nil {
						problem = browserArtifactProblem(err)
					} else if actual.SHA256 != check.Browser.SHA256 || actual.Bytes != check.Browser.Bytes || actual.Width != check.Browser.Width || actual.Height != check.Browser.Height {
						problem = "screenshot does not match its recorded SHA256, size or dimensions"
					} else {
						row.Integrity = "valid"
					}
				}
			}
		} else if check.ExitCode == 0 && check.FailureKind == "" && check.Error == "" {
			problem = "passing browser check has no screenshot evidence"
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if problem != "" {
			updated[i].EvidenceError = problem
			row.Integrity, row.Problem = "invalid", problem
			command, _ := evidenceExcerpt(check.Command, 120)
			invalid = append(invalid, command+": "+problem)
		}
		row.CheckPassed = updated[i].Passed()
		rows = append(rows, row)
	}
	if len(invalid) > 0 {
		return updated, rows, fmt.Errorf("%w: %s", errBrowserEvidence, strings.Join(invalid, "; "))
	}
	return updated, rows, nil
}

func browserArtifactProblem(err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return "screenshot file is missing"
	}
	if errors.Is(err, os.ErrPermission) {
		return "screenshot file cannot be read"
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		// Do not copy arbitrary outside paths into an evidence report.
		return "screenshot path is unavailable or escapes its evidence directory"
	}
	// The decoder exposes only fixed, bounded image-validation diagnostics.
	return err.Error()
}

func (r *Runner) recheckBrowserEvidence(ctx context.Context) (bool, error) {
	updated, _, err := auditBrowserEvidence(ctx, r.Repo, r.Dir, r.State.CheckResults)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, errBrowserEvidence) {
		return false, err
	}
	r.State.CheckResults = updated
	r.State.CheckedSnapshot = ""
	r.State.Phase = "checking"
	r.State.Council = nil
	r.State.RecoveryNote = err.Error() + ". Run checks again and obtain fresh senior approval."
	r.note("browser-evidence-invalidated", r.State.RecoveryNote)
	r.Emit(Event{Kind: "status", Text: "Browser evidence changed; verifying again before review"})
	r.evidenceRechecks++
	if saveErr := r.checkpoint(); saveErr != nil {
		return false, saveErr
	}
	if r.evidenceRechecks >= 3 {
		return false, limitError{"browser evidence keeps changing across three verification rounds; stop the writer or fix the check, then resume"}
	}
	return true, nil
}
func evidenceIntegrityNotice(problem string) string {
	if problem == "" {
		return ""
	}
	return "Browser evidence invalidated: " + problem + "\n"
}

// Inspection projects current artifact validity without rewriting historical
// outcomes or checkpoint bytes. Only an executed new check can refresh evidence.
func loadRunWithEvidence(repo, id string) (State, string, error) {
	state, dir, err := LoadRun(repo, id)
	if err != nil {
		return state, dir, err
	}
	updated, _, err := auditBrowserEvidence(context.Background(), repo, dir, state.CheckResults)
	if err != nil {
		if !errors.Is(err, errBrowserEvidence) {
			return state, dir, err
		}
		state.CheckResults = updated
		state.CheckedSnapshot = ""
	}
	return state, dir, nil
}
