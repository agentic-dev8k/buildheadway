package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxLogRead = 1 << 20

type LogEntry struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}
type LogReport struct {
	ID        string     `json:"id"`
	Directory string     `json:"directory"`
	Files     []LogEntry `json:"files,omitempty"`
	Name      string     `json:"name,omitempty"`
	Bytes     int64      `json:"bytes,omitempty"`
	Offset    int64      `json:"offset,omitempty"`
	Truncated bool       `json:"truncated"`
	Output    string     `json:"output,omitempty"`
}

func validLogName(name string) bool {
	return name != "" && len(name) <= 240 && filepath.Base(name) == name && !strings.ContainsAny(name, `/\\:`) && !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".log") && strings.IndexFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-')
	}) == -1
}

// ReadLogs never starts an agent, reruns a check, or changes saved evidence.
// Root-relative opens prevent traversal through renamed paths or symlinks.
func ReadLogs(repo, id string, lastCheck bool, name string, limit int) (LogReport, error) {
	report := LogReport{}
	if limit < 1 || limit > maxLogRead {
		return report, errors.New("log bytes must be between 1 and 1048576")
	}
	if name != "" && !validLogName(name) {
		return report, errors.New("choose a .log filename from buildheadway logs; paths are not accepted")
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return report, err
	}
	defer root.Close()
	readID := func(path, field string) (string, error) {
		file, err := root.Open(path)
		if err != nil {
			return "", err
		}
		defer file.Close()
		data, err := ReadLimited(file, 1<<20)
		if err != nil {
			return "", err
		}
		return logRunID(data, field)
	}
	kind := "runs"
	if lastCheck {
		kind = "checks"
		id, err = readID(".forge/last-check.json", "id")
	} else if id == "" || id == "latest" {
		id, err = readID(".forge/latest.json", "run_id")
	}
	if err != nil {
		return report, fmt.Errorf("cannot find saved logs: %w", err)
	}
	if !validRunID.MatchString(id) {
		return report, errors.New("invalid saved run ID")
	}
	relative := filepath.Join(".forge", kind, id)
	logs, err := root.OpenRoot(relative)
	if err != nil {
		return report, err
	}
	defer logs.Close()
	report.ID, report.Directory = id, filepath.ToSlash(relative)
	if name == "" {
		directory, err := logs.Open(".")
		if err != nil {
			return report, err
		}
		defer directory.Close()
		// Stream directory entries to bound working memory on very long runs.
		for {
			entries, err := directory.ReadDir(128)
			for _, entry := range entries {
				if !validLogName(entry.Name()) || !entry.Type().IsRegular() {
					continue
				}
				info, statErr := entry.Info()
				if statErr != nil {
					return report, statErr
				}
				if !info.Mode().IsRegular() {
					continue
				}
				report.Files = append(report.Files, LogEntry{Name: entry.Name(), Bytes: info.Size()})
				if len(report.Files) > 1000 {
					return LogReport{}, errors.New("more than 1000 logs; select a filename from the saved report instead")
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return report, err
			}
		}
		sort.Slice(report.Files, func(i, j int) bool { return report.Files[i].Name < report.Files[j].Name })
		return report, nil
	}
	info, err := logs.Lstat(name)
	if err != nil {
		return report, err
	}
	if !info.Mode().IsRegular() {
		return report, errors.New("log must be a regular file; symlinks are not accepted")
	}
	file, err := logs.Open(name)
	if err != nil {
		return report, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return report, err
	}
	if !info.Mode().IsRegular() {
		return report, errors.New("log must be a regular file")
	}
	report.Name, report.Bytes = name, info.Size()
	report.Offset = max(int64(0), info.Size()-int64(limit))
	report.Truncated = report.Offset > 0
	data := make([]byte, info.Size()-report.Offset)
	n, err := file.ReadAt(data, report.Offset)
	if err != nil && err != io.EOF {
		return report, err
	}
	report.Output = string(data[:n])
	return report, nil
}

func FormatLogs(report LogReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Logs · %s\n%s\n", report.ID, report.Directory)
	if report.Name != "" {
		fmt.Fprintf(&out, "%s · %d bytes · offset %d\n", report.Name, report.Bytes, report.Offset)
		if report.Truncated {
			out.WriteString("[Earlier output omitted; increase --bytes up to 1048576 or open the file directly.]\n")
		}
		out.WriteString(report.Output)
	} else {
		for _, file := range report.Files {
			fmt.Fprintf(&out, "%s · %d bytes\n", file.Name, file.Bytes)
		}
		if len(report.Files) == 0 {
			out.WriteString("No process logs saved.\n")
		}
		out.WriteString("Read one with buildheadway logs [--run ID | --last-check] LOG_NAME.\n")
	}
	return strings.TrimSpace(out.String())
}
func logEvidence(name string, truncated bool) string {
	var out string
	if name != "" {
		out = "Full log: " + name + "\n"
	}
	if truncated {
		out += "[Output excerpt; earlier output omitted.]\n"
	}
	return out
}
func runLogsCommand(args []string) int {
	flags := flag.NewFlagSet("buildheadway logs", flag.ContinueOnError)
	project := flags.String("project", ".", "Project folder")
	id := flags.String("run", "latest", "Saved goal ID")
	last := flags.Bool("last-check", false, "Logs from the last standalone buildheadway check")
	limit := flags.Int("bytes", 16000, "Maximum tail bytes, up to 1048576")
	asJSON := flags.Bool("json", false, "Print machine-readable output")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 1 {
		return fail(errors.New("usage: buildheadway logs [--run ID | --last-check] [LOG_NAME]"))
	}
	if *last && *id != "latest" {
		return fail(errors.New("choose --run or --last-check, not both"))
	}
	name := ""
	if flags.NArg() == 1 {
		name = flags.Arg(0)
	}
	report, err := ReadLogs(*project, *id, *last, name, *limit)
	if err != nil {
		return fail(err)
	}
	if err := outputValue(report, FormatLogs(report), *asJSON); err != nil {
		return fail(err)
	}
	return 0
}

func logRunID(data []byte, field string) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return "", err
	}
	var id string
	err := json.Unmarshal(fields[field], &id)
	return id, err
}
