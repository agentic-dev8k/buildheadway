package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const stateVersion = 12

var validRunID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)

// LoadRun is read-only. Migration is committed only after Runner owns the lock.
func LoadRun(repo, id string) (State, string, error) {
	var state State
	if id == "" || id == "latest" {
		var latest struct {
			RunID string `json:"run_id"`
		}
		if err := ReadJSON(filepath.Join(repo, ".forge", "latest.json"), &latest); err != nil {
			return state, "", fmt.Errorf("no saved goal in this folder: %w", err)
		}
		id = latest.RunID
	}
	if !validRunID.MatchString(id) {
		return state, "", errors.New("invalid saved run ID")
	}
	dir := filepath.Join(repo, ".forge", "runs", id)
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return state, dir, err
	}
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(data, &legacy); err != nil {
		return state, dir, fmt.Errorf("invalid checkpoint: %w", err)
	}
	var oldHistory []any
	if json.Unmarshal(legacy["history"], &oldHistory) == nil {
		history := []string{}
		for _, value := range oldHistory {
			switch item := value.(type) {
			case string:
				history = append(history, item)
			case map[string]any:
				if summary, ok := item["summary"].(string); ok {
					history = append(history, summary)
				}
			}
		}
		legacy["history"], _ = json.Marshal(history)
		data, _ = json.Marshal(legacy)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, dir, fmt.Errorf("invalid checkpoint: %w", err)
	}
	if state.Version < 1 || state.Version > stateVersion {
		return state, dir, errors.New("unsupported saved-session format")
	}
	if state.RunID != id {
		return state, dir, errors.New("checkpoint run ID does not match its folder")
	}
	if state.Cycle < 1 || state.NextTask < 0 || state.NextTask > len(state.Tasks) || state.PlannerCalls < 0 || state.WorkerCalls < 0 || state.ActiveMillis < 0 {
		return state, dir, errors.New("invalid checkpoint counters")
	}
	switch state.Phase {
	case "planning", "working", "checking", "reviewing":
	default:
		return state, dir, errors.New("invalid checkpoint phase")
	}
	switch state.Status {
	case "running", "paused", "blocked", "interrupted", "complete", "planned":
	default:
		return state, dir, errors.New("invalid checkpoint status")
	}
	if state.Version < 3 {
		// Older checkpoints cannot say whether a pending worker already changed files.
		if state.Phase == "working" && state.NextTask < len(state.Tasks) {
			state.RecoveryNote = "An older checkpoint has pending work. Inspect the current files and reconcile the remaining tasks before assigning more work."
			state.Phase = "checking"
		}
		state.CheckedSnapshot = ""
	}
	if state.Version < 4 {
		state.Config = DefaultProjectConfig()
	}
	if err := ValidateProjectConfig(state.Config); err != nil {
		return state, dir, fmt.Errorf("invalid saved project configuration: %w", err)
	}
	if state.QueueID != "" && !validRunID.MatchString(state.QueueID) {
		return state, dir, errors.New("invalid checkpoint queue ID")
	}
	if err := ValidateCouncilState(state); err != nil {
		return state, dir, err
	}
	if err := validateSteering(state.Steering); err != nil {
		return state, dir, err
	}
	if err := validateAcceptance(state.Acceptance); err != nil {
		return state, dir, fmt.Errorf("invalid saved acceptance requirements: %w", err)
	}
	if state.HookRechecks < 0 {
		return state, dir, errors.New("invalid hook recheck counter")
	}
	if err := validateRouting(state); err != nil {
		return state, dir, fmt.Errorf("invalid saved model routing: %w", err)
	}
	for _, check := range state.CheckResults {
		if check.Log != "" && !validLogName(check.Log) {
			return state, dir, errors.New("invalid saved check log")
		}
	}
	for _, hook := range state.HookResults {
		if hook.Log != "" && !validLogName(hook.Log) {
			return state, dir, errors.New("invalid saved hook log")
		}
	}
	state.Version = stateVersion
	keys := map[string]bool{}
	foundActive := state.ActiveTask == ""
	for _, task := range state.TaskHistory {
		if task.Key == "" || keys[task.Key] || task.Attempts < 0 {
			return state, dir, errors.New("invalid task ledger")
		}
		keys[task.Key] = true
		if task.Key == state.ActiveTask {
			foundActive = true
		}
	}
	if !foundActive {
		return state, dir, errors.New("checkpoint refers to a missing active task")
	}
	return state, dir, nil
}

type Progress struct {
	RunID                                             string
	Cycle, Finished, Total, PlannerCalls, WorkerCalls int
	ActiveMillis                                      int64
}

func (r *Runner) checkpoint() error {
	if !r.lastCheckpoint.IsZero() {
		now := time.Now()
		r.State.ActiveMillis += now.Sub(r.lastCheckpoint).Milliseconds()
		r.lastCheckpoint = now
	}
	if err := r.State.Save(r.Dir); err != nil {
		return err
	}
	r.Emit(Event{Kind: "progress", Value: Progress{r.State.RunID, r.State.Cycle, r.State.NextTask, len(r.State.Tasks), r.State.PlannerCalls, r.State.WorkerCalls, r.State.ActiveMillis}})
	return nil
}
func (r *Runner) note(kind, text string) {
	r.State.Events = append(r.State.Events, RunEvent{time.Now().UTC().Format(time.RFC3339), kind, text})
	if len(r.State.Events) > 200 {
		r.State.Events = r.State.Events[len(r.State.Events)-200:]
	}
}
func (r *Runner) activeRecord() *TaskRecord {
	for i := range r.State.TaskHistory {
		if r.State.TaskHistory[i].Key == r.State.ActiveTask {
			return &r.State.TaskHistory[i]
		}
	}
	return nil
}
func (r *Runner) beginTask(task Task) error {
	key := fmt.Sprintf("%d:%d", r.State.Cycle, r.State.NextTask)
	r.State.ActiveTask = key
	record := r.activeRecord()
	if record == nil {
		r.State.TaskHistory = append(r.State.TaskHistory, TaskRecord{Key: key, Cycle: r.State.Cycle, Task: task, Status: "pending"})
		record = r.activeRecord()
	}
	record.Status = "running"
	record.StartedAt = time.Now().UTC().Format(time.RFC3339)
	record.Error = ""
	r.State.CheckedSnapshot = ""
	r.note("task-start", task.Title)
	return r.checkpoint()
}
func (r *Runner) workerAttempt(model string) error {
	if r.MaxWorkerCalls > 0 && r.State.WorkerCalls >= r.MaxWorkerCalls {
		return limitError{"worker-call budget reached; resume with a higher --max-worker-calls"}
	}
	r.State.WorkerCalls++
	if r.Settings.Worker.Backend == "opencode" {
		r.modelAttempt("worker", model)
	}
	if record := r.activeRecord(); record != nil {
		record.Attempts++
		record.Model = model
		r.note("worker-attempt", record.Task.Title+" · "+model)
	}
	return r.checkpoint()
}
func (r *Runner) finishTask(result WorkerResult, err error) {
	record := r.activeRecord()
	if record == nil {
		return
	}
	record.Report, record.Error = result.Report, result.Error
	record.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		record.Status, record.Error = "blocked", err.Error()
		if errors.Is(err, context.Canceled) && !hardModelFailure(err) {
			record.Status = "interrupted"
		}
	} else {
		record.Status = "reported"
		if !result.OK {
			record.Status = "failed"
		}
		r.State.ActiveTask = ""
		r.State.WorkerSession = nil
		r.State.Dialogue = nil
	}
	r.note("task-"+record.Status, record.Task.Title)
}
func (r *Runner) recoverCheckpoint() {
	r.State.RoutingWait = nil
	if r.State.ActiveHook != "" {
		r.State.RecoveryNote = "Verification hook " + r.State.ActiveHook + " was interrupted. Repeatable hooks and all checks will run again before review."
		r.note("hook-recovery", r.State.RecoveryNote)
		r.State.ActiveHook = ""
		r.State.Phase = "checking"
	}
	r.State.HookRechecks = 0
	if record := r.activeRecord(); record != nil {
		cooperative := r.State.WorkerSession != nil && (r.State.WorkerSession.Status == "consulting" || r.State.WorkerSession.Status == "ready")
		if cooperative {
			r.State.RecoveryNote = "The worker returned control for a team consultation. Finish the saved replies and continue the same task, inspecting current files before any further edits."
			r.State.Phase = "working"
			r.note("recovery", r.State.RecoveryNote)
		} else {
			record.Status = "interrupted"
			record.FinishedAt = time.Now().UTC().Format(time.RFC3339)
			r.State.RecoveryNote = "The worker for " + record.Task.Title + " may have changed files before stopping. Inspect its partial work, current check results, and remaining tasks. Do not blindly repeat it."
			r.note("recovery", r.State.RecoveryNote)
			r.State.ActiveTask = ""
			r.State.Phase = "checking"
			r.State.WorkerSession = nil
			r.State.Dialogue = nil
		}
	}
	if r.State.Phase == "reviewing" || r.State.Status == "complete" {
		r.State.Phase = "checking"
	}
	r.State.CheckedSnapshot = ""
	r.State.StalledCycles = 0
	if r.State.RecoveryNote != "" {
		r.Emit(Event{Kind: "status", Text: "Inspecting saved work before continuing"})
	}
}

// Content hashes detect ordinary concurrent edits and changes made between runs.
// Generated/dependency directories are outside this source snapshot; checks still
// execute in the actual project. Symlink targets are recorded, never traversed.
var snapshotIgnored = map[string]bool{
	".forge": true, ".git": true, "node_modules": true, "vendor": true,
	".venv": true, "venv": true, "__pycache__": true, ".pytest_cache": true,
	".mypy_cache": true, ".ruff_cache": true, ".next": true, ".nuxt": true,
	".turbo": true, "dist": true, "build": true, "target": true, "coverage": true,
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func WorkspaceSnapshot(ctx context.Context, repo string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(repo, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == repo {
			return nil
		}
		if entry.IsDir() && snapshotIgnored[entry.Name()] {
			return filepath.SkipDir
		}
		relative, err := filepath.Rel(repo, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%s\x00%s\x00", filepath.ToSlash(relative), info.Mode())
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintln(hash, target)
		} else if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			content := sha256.New()
			_, err = io.Copy(content, contextReader{ctx, file})
			file.Close()
			if err != nil {
				return err
			}
			hash.Write(content.Sum(nil))
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("could not fingerprint project files: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func (r *Runner) recordVerification(snapshot string) {
	r.State.CheckedSnapshot = snapshot
	r.State.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	hash := sha256.New()
	fmt.Fprintln(hash, snapshot)
	for _, check := range r.State.CheckResults {
		fmt.Fprintf(hash, "%s\x00%d\n", check.Command, check.ExitCode)
	}
	for _, hook := range r.State.HookResults {
		fmt.Fprintf(hash, "%s\x00%d\n", hook.Name, hook.ExitCode)
	}
	signature := hex.EncodeToString(hash.Sum(nil))
	if signature == r.State.VerificationSignature {
		r.State.StalledCycles++
	} else {
		r.State.StalledCycles = 0
	}
	r.State.VerificationSignature = signature
	r.note("verification", fmt.Sprintf("%d checks executed; %d unchanged rounds", len(r.State.CheckResults), r.State.StalledCycles))
}
func recentTasks(records []TaskRecord, count int) []TaskRecord {
	if len(records) > count {
		records = records[len(records)-count:]
	}
	copy := append([]TaskRecord{}, records...)
	for i := range copy {
		copy[i].Report = Tail(copy[i].Report, 1500)
		copy[i].Error = Tail(copy[i].Error, 1000)
		copy[i].Task.Instructions = Tail(copy[i].Task.Instructions, 2000)
	}
	return copy
}
func briefChecks(checks []CheckResult) []CheckResult {
	copy := append([]CheckResult{}, checks...)
	for i := range copy {
		copy[i].OutputTruncated = copy[i].OutputTruncated || len(copy[i].Output) > 2500
		copy[i].Output = Tail(copy[i].Output, 2500)
	}
	return copy
}

func (r *Runner) validateCheckReplacements(answer Decision) error {
	for _, replacement := range answer.CheckReplacements {
		if contains(r.State.RequiredChecks, replacement.Old) {
			return errors.New("the planner tried to replace a required check")
		}
		if !contains(r.State.SuggestedChecks, replacement.Old) || !contains(answer.Checks, replacement.New) {
			return errors.New("a check replacement must name an existing suggested check and a new check to execute")
		}
		unavailable := false
		for _, result := range r.State.CheckResults {
			if result.Command == replacement.Old && (result.ExitCode == 126 || result.ExitCode == 127 || result.ExitCode == 9009) {
				unavailable = true
			}
		}
		if !unavailable {
			return errors.New("the planner tried to drop a runnable verification check; repair the failure instead")
		}
	}
	return nil
}
func (r *Runner) replaceUnavailableChecks(answer Decision) error {
	if err := r.validateCheckReplacements(answer); err != nil {
		return err
	}
	for _, replacement := range answer.CheckReplacements {
		remaining := []string{}
		for _, check := range r.State.SuggestedChecks {
			if check != replacement.Old {
				remaining = append(remaining, check)
			}
		}
		r.State.SuggestedChecks = remaining
		r.State.RetiredChecks = append(r.State.RetiredChecks, replacement)
		r.note("check-replacement", replacement.Old+" → "+replacement.New+": "+replacement.Reason)
	}
	return nil
}

type RunSummary struct {
	RunID     string `json:"run_id"`
	Goal      string `json:"goal"`
	Status    string `json:"status"`
	Phase     string `json:"phase"`
	Cycle     int    `json:"cycle"`
	UpdatedAt string `json:"updated_at"`
	Error     string `json:"error,omitempty"`
}

func RunHistory(repo string) ([]RunSummary, error) {
	entries, err := os.ReadDir(filepath.Join(repo, ".forge", "runs"))
	if errors.Is(err, os.ErrNotExist) {
		return []RunSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	rows := []RunSummary{}
	for _, entry := range entries {
		if !entry.IsDir() || !validRunID.MatchString(entry.Name()) {
			continue
		}
		state, _, err := LoadRun(repo, entry.Name())
		if err != nil {
			rows = append(rows, RunSummary{RunID: entry.Name(), Status: "unreadable", Error: err.Error()})
			continue
		}
		rows = append(rows, RunSummary{state.RunID, state.Goal, state.Status, state.Phase, state.Cycle, state.UpdatedAt, state.Error})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UpdatedAt == rows[j].UpdatedAt {
			return rows[i].RunID > rows[j].RunID
		}
		return rows[i].UpdatedAt > rows[j].UpdatedAt
	})
	return rows, nil
}
func FormatStatus(state State) string {
	var out strings.Builder
	if state.Mode == "figureout" {
		fmt.Fprintf(&out, "Figureout · %s access · %d seniors\n", state.Access, len(state.Team))
	}
	fmt.Fprintf(&out, "%s\n%s\n%s · %s · cycle %d\nBatch: %d/%d tasks reported · planner calls: %d · worker attempts: %d\n", state.RunID, state.Goal, state.Status, state.Phase, state.Cycle, state.NextTask, len(state.Tasks), state.PlannerCalls, state.WorkerCalls)
	fmt.Fprintf(&out, "Active time: %s\n", (time.Duration(state.ActiveMillis) * time.Millisecond).Round(time.Second))
	if state.RoutingWait != nil && state.Status == "running" {
		fmt.Fprintf(&out, "Waiting for a free %s model until %s; buildheadway routing shows health and cooldowns\n", state.RoutingWait.Role, state.RoutingWait.Until)
	}
	if state.CheckedAt != "" {
		fmt.Fprintf(&out, "Last checks: %s\n", state.CheckedAt)
	}
	for _, check := range state.CheckResults {
		fmt.Fprintf(&out, "  exit %d · %s\n", check.ExitCode, check.Command)
		fmt.Fprint(&out, sourceChangeNotice(check.SourceChanged))
		fmt.Fprint(&out, executionNotice(check.ExecutionEvidence))
	}
	if len(state.Steering) > 0 {
		fmt.Fprintf(&out, "User updates applied: %d (buildheadway steering shows the inbox)\n", len(state.Steering))
	}
	for _, hook := range state.HookResults {
		fmt.Fprintf(&out, "  hook %s · exit %d\n", hook.Name, hook.ExitCode)
		fmt.Fprint(&out, sourceChangeNotice(hook.SourceChanged))
		fmt.Fprint(&out, executionNotice(hook.ExecutionEvidence))
	}
	if state.Error != "" {
		fmt.Fprintln(&out, state.Error)
	}
	if state.Summary != "" {
		fmt.Fprintln(&out, state.Summary)
	}
	return strings.TrimSpace(out.String())
}
func FormatTasks(state State) string {
	if len(state.TaskHistory) == 0 {
		return "No task history yet."
	}
	var out strings.Builder
	for _, task := range state.TaskHistory {
		fmt.Fprintf(&out, "[%s] %s · cycle %d · %d attempts\n", task.Status, task.Task.Title, task.Cycle, task.Attempts)
		if task.Model != "" {
			fmt.Fprintf(&out, "  %s\n", task.Model)
		}
		if task.Error != "" {
			fmt.Fprintf(&out, "  %s\n", Tail(task.Error, 500))
		}
	}
	return strings.TrimSpace(out.String())
}
func FormatHistory(rows []RunSummary) string {
	if len(rows) == 0 {
		return "No saved goals in this folder."
	}
	var out strings.Builder
	for _, row := range rows {
		fmt.Fprintf(&out, "%s · %s · cycle %d\n  %s\n", row.RunID, row.Status, row.Cycle, strings.ReplaceAll(row.Goal, "\n", " "))
	}
	return strings.TrimSpace(out.String())
}
func InspectRun(repo, id, kind string) (any, string, error) {
	if kind == "history" {
		rows, err := RunHistory(repo)
		return rows, FormatHistory(rows), err
	}
	state, _, err := LoadRun(repo, id)
	if err != nil {
		return nil, "", err
	}
	switch kind {
	case "status":
		return state, FormatStatus(state), nil
	case "routing":
		report := routingReport(state, time.Now())
		return report, FormatRouting(report), nil
	case "acceptance":
		snapshot, err := WorkspaceSnapshot(context.Background(), repo)
		if err != nil {
			return nil, "", err
		}
		if snapshot != state.CheckedSnapshot {
			state.CheckedSnapshot = ""
		}
		return AcceptanceResults(state, Decision{}), FormatAcceptance(state), nil
	case "tasks":
		return state.TaskHistory, FormatTasks(state), nil
	case "discussion":
		return state.Discussion, FormatDiscussion(state), nil
	case "events":
		var out strings.Builder
		for _, event := range state.Events {
			fmt.Fprintf(&out, "%s · %s · %s\n", event.At, event.Kind, event.Text)
		}
		if len(state.Events) == 0 {
			out.WriteString("No harness events yet.")
		}
		return state.Events, strings.TrimSpace(out.String()), nil
	}
	return nil, "", errors.New("unknown inspection command")
}

func briefHooks(hooks []HookResult) []HookResult {
	copy := append([]HookResult{}, hooks...)
	for i := range copy {
		copy[i].OutputTruncated = copy[i].OutputTruncated || len(copy[i].Output) > 2500
		copy[i].Output = Tail(copy[i].Output, 2500)
	}
	return copy
}
