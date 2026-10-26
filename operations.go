package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type VerificationReport struct {
	Hooks      []HookResult  `json:"hooks,omitempty"`
	Version    int           `json:"version"`
	ID         string        `json:"id"`
	Status     string        `json:"status"`
	StartedAt  string        `json:"started_at"`
	FinishedAt string        `json:"finished_at,omitempty"`
	Results    []CheckResult `json:"results"`
	Error      string        `json:"error,omitempty"`
}

func runVerification(ctx context.Context, paths Paths, repo, dir string, commands []string, timeout time.Duration, demo bool, emit Emit, before func(string) error, record func(CheckResult) error) error {
	if emit == nil {
		emit = noEmit
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	for index, command := range Unique(commands) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if before != nil {
			if err := before(command); err != nil {
				return err
			}
		}
		emit(Event{Kind: "status", Text: "Running verification"})
		emit(Event{Kind: "check", Text: command})
		argv := ShellCommand(command)
		if command == "@buildheadway-demo-check" && demo {
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			argv = []string{executable, "--internal-demo-check", repo}
		}
		logPath := filepath.Join(dir, fmt.Sprintf("%s-check-%02d.log", time.Now().UTC().Format("150405.000000"), index+1))
		result, err := RunProcess(ctx, argv, repo, logPath, "", setEnv(ToolEnv(paths), "CI", "1"), timeout, nil)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			result.Code = 124
			result.Output += "\n" + err.Error()
		}
		check := CheckResult{Command: command, ExitCode: result.Code, Output: Tail(result.Output, 7000), Log: filepath.Base(logPath), OutputTruncated: result.OutputTruncated || len(result.Output) > 7000}
		if err := record(check); err != nil {
			return err
		}
		text := "Passed"
		if result.Code != 0 {
			text = "Failed (exit " + itoa(result.Code) + ")"
		}
		emit(Event{Kind: "check-result", Text: text})
	}
	return nil
}
func CheckProject(ctx context.Context, paths Paths, repo string, emit Emit) (VerificationReport, error) {
	if emit == nil {
		emit = noEmit
	}
	report := VerificationReport{Version: 1, Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339), Results: []CheckResult{}}
	repo, err := filepath.Abs(repo)
	if err != nil {
		return report, err
	}
	if info, err := os.Stat(repo); err != nil || !info.IsDir() {
		return report, errors.New("choose an existing project folder")
	}
	lock, err := acquireProjectLock(repo, "lock", false)
	if err != nil {
		return report, errors.New("pause the active goal before running separate checks")
	}
	defer lock.Close()
	config, err := LoadProjectConfig(repo)
	if err != nil {
		return report, err
	}
	commands := append(SuggestedChecks(repo, paths), config.RequiredChecks()...)
	if state, _, err := LoadRun(repo, "latest"); err == nil {
		commands = append(commands, state.RequiredChecks...)
		commands = append(commands, state.SuggestedChecks...)
	} else if !errors.Is(err, os.ErrNotExist) {
		return report, err
	}
	commands = Unique(commands)
	if len(commands) == 0 && len(activeHooks(config, "before_check")) == 0 {
		return report, errors.New("no verification commands found; use buildheadway init and add checks to .buildheadway.json")
	}
	if err := ensureIgnore(repo); err != nil {
		return report, err
	}
	report.ID, err = newID("check-")
	if err != nil {
		return report, err
	}
	dir := filepath.Join(repo, ".forge", "checks", report.ID)
	save := func() error {
		if err := AtomicJSON(filepath.Join(dir, "report.json"), report); err != nil {
			return err
		}
		return AtomicJSON(filepath.Join(repo, ".forge", "last-check.json"), report)
	}
	if err := save(); err != nil {
		return report, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		report.Results, report.Hooks = nil, nil
		_, err = verificationRound(ctx, paths, repo, dir, config, HookInput{RunID: report.ID}, commands, true, emit, nil,
			func(result HookResult) error { report.Hooks = append(report.Hooks, result); return save() },
			func(result CheckResult) error { report.Results = append(report.Results, result); return save() })
		if !errors.Is(err, errHookChangedFiles) {
			break
		}
		emit(Event{Kind: "status", Text: "Files changed during verification; running all checks again"})
	}
	report.Status = "passed"
	if len(report.Results) == 0 || !hooksPassed(config, report.Hooks) {
		report.Status = "failed"
	}
	for _, result := range report.Results {
		if result.ExitCode != 0 || result.SourceChanged {
			report.Status = "failed"
		}
	}
	if err != nil {
		report.Status, report.Error = "interrupted", err.Error()
	}
	report.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	if saveErr := save(); saveErr != nil {
		err = errors.Join(err, saveErr)
	}
	if err == nil && report.Status != "passed" {
		err = errors.New("verification failed; see the check output or .forge/last-check.json")
	}
	return report, err
}
func FormatVerification(report VerificationReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Verification %s · %d checks\n", report.Status, len(report.Results))
	for _, result := range report.Results {
		fmt.Fprintf(&out, "exit %d · %s\n", result.ExitCode, result.Command)
		fmt.Fprint(&out, sourceChangeNotice(result.SourceChanged))
		fmt.Fprint(&out, logEvidence(result.Log, result.OutputTruncated || result.ExitCode != 0 && len(result.Output) > 1600))
		if result.ExitCode != 0 {
			fmt.Fprintln(&out, Tail(result.Output, 1600))
		}
	}
	for _, hook := range report.Hooks {
		fmt.Fprintf(&out, "hook %s · %s · exit %d\n", hook.Name, hook.Event, hook.ExitCode)
		fmt.Fprint(&out, sourceChangeNotice(hook.SourceChanged))
		fmt.Fprint(&out, logEvidence(hook.Log, hook.OutputTruncated || hook.ExitCode != 0 && len(hook.Output) > 1600))
		if hook.ExitCode != 0 {
			fmt.Fprintln(&out, Tail(hook.Output, 1600))
		}
	}
	if report.Error != "" {
		fmt.Fprintln(&out, report.Error)
	}
	return strings.TrimSpace(out.String())
}
func fenced(text string) string {
	text = Clean(text)
	longest, run := 0, 0
	for _, character := range text {
		if character == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	return fence + "text\n" + text + "\n" + fence + "\n"
}
func MarkdownReport(state State) string {
	var out strings.Builder
	out.WriteString("# BuildHeadway run report\n\n")
	out.WriteString(fenced(FormatStatus(state)))
	out.WriteString("\n## Free-model routing\n\n" + fenced(FormatRouting(routingReport(state, time.Now()))))
	if state.Mode == "figureout" {
		out.WriteString("\n## Senior team\n\n" + fenced(FormatTeam(state.Team)))
		out.WriteString("\n## Agent discussion (most recent 400 messages)\n\n" + fenced(FormatDiscussion(state)))
	}
	if len(state.Steering) > 0 {
		out.WriteString("\n## User direction\n\n" + fenced(state.goalContext()))
	}
	out.WriteString("\n## Success criteria\n\n")
	if len(state.SuccessCriteria) == 0 {
		out.WriteString("No criteria recorded.\n")
	} else {
		out.WriteString(fenced(strings.Join(state.SuccessCriteria, "\n")))
	}
	out.WriteString("\n## Acceptance evidence\n\n" + fenced(FormatAcceptance(state)))
	out.WriteString("\n## Task history\n\n")
	out.WriteString(fenced(FormatTasks(state)))
	for _, task := range state.TaskHistory {
		if task.Report != "" {
			out.WriteString("\n" + fenced(task.Task.Title+"\n"+task.Report))
		}
	}
	out.WriteString("\n## Verification\n\n")
	for _, check := range state.CheckResults {
		out.WriteString(fenced(fmt.Sprintf("%s\nExit code: %d\n%s%s%s", check.Command, check.ExitCode, sourceChangeNotice(check.SourceChanged), logEvidence(check.Log, check.OutputTruncated), check.Output)) + "\n")
	}
	if len(state.HookResults) > 0 {
		out.WriteString("\n## Verification hooks\n\n")
		for _, hook := range state.HookResults {
			out.WriteString(fenced(fmt.Sprintf("%s · %s\n%s\nExit code: %d\n%s%s%s", hook.Name, hook.Event, hook.Command, hook.ExitCode, sourceChangeNotice(hook.SourceChanged), logEvidence(hook.Log, hook.OutputTruncated), hook.Output)))
		}
	}
	out.WriteString("\n## Recent activity\n\n")
	events := state.Events
	if len(events) > 20 {
		events = events[len(events)-20:]
	}
	for _, event := range events {
		out.WriteString(fenced(event.At + " · " + event.Kind + " · " + event.Text))
	}
	out.WriteString("\nThis report records executed checks and agent reports. Worker reports do not independently prove that the goal is complete.\n")
	return out.String()
}
func ExportRun(repo, id, destination string, asJSON, overwrite bool) (string, error) {
	state, _, err := LoadRun(repo, id)
	if err != nil {
		return "", err
	}
	var data []byte
	if asJSON {
		data, err = json.MarshalIndent(state, "", "  ")
		data = append(data, '\n')
	} else {
		data = []byte(MarkdownReport(state))
	}
	if err != nil {
		return "", err
	}
	if destination == "" {
		extension := ".md"
		if asJSON {
			extension = ".json"
		}
		destination = filepath.Join(repo, ".forge", "exports", state.RunID+"-"+time.Now().UTC().Format("150405.000000000")+extension)
	} else if !filepath.IsAbs(destination) {
		destination = filepath.Join(repo, destination)
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return "", err
	}
	if overwrite {
		// Do not follow an existing symlink when replacing an export.
		file, err := os.CreateTemp(filepath.Dir(destination), ".buildheadway-export-*")
		if err != nil {
			return "", err
		}
		name := file.Name()
		defer os.Remove(name)
		_, err = file.Write(data)
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		err = replaceFile(name, destination)
		return destination, err
	}
	return destination, WriteNewFile(destination, data)
}
func GitChanges(ctx context.Context, paths Paths, repo string) (string, error) {
	git := FindTool("git", paths)
	if git == "" {
		return "", errors.New("Git is not installed; use /agents to inspect tools")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var out strings.Builder
	out.WriteString("Current Git working tree (including changes made before BuildHeadway):\n")
	for _, args := range [][]string{
		{"--no-pager", "status", "--short", "--untracked-files=all", "--", ".", ":!.buildheadway"},
		{"--no-pager", "diff", "--no-ext-diff", "--no-textconv", "--", ".", ":!.buildheadway"},
		{"--no-pager", "diff", "--cached", "--no-ext-diff", "--no-textconv", "--", ".", ":!.buildheadway"},
	} {
		cmd := exec.CommandContext(ctx, git, args...)
		cmd.Dir = repo
		cmd.Env = setEnv(setEnv(ToolEnv(paths), "GIT_TERMINAL_PROMPT", "0"), "GIT_OPTIONAL_LOCKS", "0")
		writer := &limitedBuffer{Limit: 1 << 20}
		cmd.Stdout, cmd.Stderr = writer, writer
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("cannot inspect Git changes: %s", Tail(Clean(writer.Text.String()), 2000))
		}
		out.WriteString(Clean(writer.Text.String()))
		if writer.Truncated {
			out.WriteString("\n[Output truncated at 1 MiB; inspect the full diff with Git.]\n")
		}
	}
	if out.String() == "Current Git working tree (including changes made before BuildHeadway):\n" {
		out.WriteString("Working tree is clean.\n")
	}
	return out.String(), nil
}

type limitedBuffer struct {
	Text      strings.Builder
	Limit     int
	Truncated bool
}

func (w *limitedBuffer) Write(data []byte) (int, error) {
	n := len(data)
	available := max(0, w.Limit-w.Text.Len())
	if n > available {
		w.Truncated = true
		data = data[:available]
	}
	w.Text.Write(data)
	return n, nil
}
func FilterHistory(rows []RunSummary, search string, limit int) []RunSummary {
	filtered := []RunSummary{}
	search = strings.ToLower(strings.TrimSpace(search))
	for _, row := range rows {
		if search != "" && !strings.Contains(strings.ToLower(row.Goal+" "+row.RunID+" "+row.Status), search) {
			continue
		}
		filtered = append(filtered, row)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}
	return filtered
}
