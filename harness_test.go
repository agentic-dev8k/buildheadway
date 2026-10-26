package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHarnessCrashSubprocess(t *testing.T) {
	repo := os.Getenv("BUILDHEADWAY_HARNESS_CRASH_REPO")
	if repo == "" {
		return
	}
	r, err := NewRunner(Paths{}, Settings{Planner: Connection{Backend: "codex"}, Worker: Connection{Backend: "opencode", Model: "auto"}}, repo, "Write a correct output file", false, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	r.State.RequiredChecks = []string{checkFixtureCommand()}
	r.Discover = func(context.Context) ([]string, error) { return []string{"opencode/free"}, nil }
	r.Invoke = func(_ context.Context, _ Connection, _ string, _ string, planner bool, _ string) (string, error) {
		if planner {
			return decisionJSON(false, []Task{{"output", "Write output", "Write the correct result"}}, nil), nil
		}
		if err := os.WriteFile(filepath.Join(repo, "result.txt"), []byte("good\n"), 0600); err != nil {
			t.Fatal(err)
		}
		os.Exit(73) // Crash after an edit, before returning or saving the worker result.
		return "", nil
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash helper unexpectedly returned")
}

func TestAbruptProcessExitPreservesWorkForReview(t *testing.T) {
	repo := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable, "-test.run=^TestHarnessCrashSubprocess$")
	child.Env = setEnv(os.Environ(), "BUILDHEADWAY_HARNESS_CRASH_REPO", repo)
	output, err := child.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 73 {
		t.Fatalf("expected crash exit: %s %v", output, err)
	}
	saved, _, err := LoadRun(repo, "latest")
	if err != nil || saved.Status != "running" || saved.ActiveTask == "" {
		t.Fatalf("missing crash checkpoint: %+v %v", saved, err)
	}
	settings := Settings{Planner: Connection{Backend: "codex"}, Worker: Connection{Backend: "opencode", Model: "auto"}}
	resume, err := ResumeRunner(testPaths(t), settings, repo, "latest", noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resume.Invoke = func(_ context.Context, _ Connection, prompt, _ string, planner bool, _ string) (string, error) {
		if !planner {
			t.Fatal("crashed task was replayed")
		}
		if len(resume.State.CheckResults) != 1 || resume.State.CheckResults[0].ExitCode != 0 || !strings.Contains(prompt, "partial work") {
			t.Fatal("crash recovery skipped current evidence")
		}
		return decisionJSON(true, nil, nil), nil
	}
	if err := resume.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resume.State.Status != "complete" || resume.State.WorkerCalls != 1 {
		t.Fatal("crash recovery repeated implementation")
	}
}

func TestInterruptedWorkerIsReviewedBeforeReassignment(t *testing.T) {
	r, calls := fixtureRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Invoke = func(_ context.Context, _ Connection, _ string, _ string, planner bool, _ string) (string, error) {
		if planner {
			return decisionJSON(false, []Task{{"file", "Write output", "Write correct output"}}, nil), nil
		}
		*calls++
		if err := os.WriteFile(filepath.Join(r.Repo, "result.txt"), []byte("good\n"), 0600); err != nil {
			t.Fatal(err)
		}
		cancel()
		return "", context.Canceled
	}
	if err := r.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected interruption: %v", err)
	}
	saved, _, err := LoadRun(r.Repo, "latest")
	if err != nil || saved.ActiveTask == "" || saved.TaskHistory[0].Attempts != 1 || saved.TaskHistory[0].Status != "interrupted" {
		t.Fatalf("partial work not checkpointed: %+v %v", saved, err)
	}
	resume, err := ResumeRunner(r.Paths, r.Settings, r.Repo, saved.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resume.Invoke = func(_ context.Context, _ Connection, prompt, _ string, planner bool, _ string) (string, error) {
		if !planner {
			t.Fatal("replayed interrupted worker without reviewing its changes")
		}
		if !strings.Contains(prompt, "may have changed files") || len(resume.State.CheckResults) != 1 || resume.State.CheckResults[0].ExitCode != 0 {
			t.Fatal("review did not receive partial-work context and fresh evidence")
		}
		return decisionJSON(true, nil, nil), nil
	}
	if err := resume.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 || resume.State.Status != "complete" || resume.State.ActiveTask != "" {
		t.Fatal("resume lost the partial work")
	}
}

func TestCrashCheckpointIsDurableBeforeWorkerStarts(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.State.Phase = "working"
	r.State.Tasks = []Task{{"file", "Write output", "Write output"}}
	r.Invoke = func(_ context.Context, _ Connection, _ string, _ string, planner bool, _ string) (string, error) {
		if planner {
			return decisionJSON(true, nil, nil), nil
		}
		saved, _, err := LoadRun(r.Repo, "latest")
		if err != nil || saved.ActiveTask == "" || saved.TaskHistory[0].Status != "running" || saved.TaskHistory[0].Attempts != 1 || saved.WorkerCalls != 1 {
			t.Fatalf("worker started before durable checkpoint: %+v %v", saved, err)
		}
		return "", errors.New("not available in your region")
	}
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("expected provider block")
	}
}

func TestStaleReviewCannotApproveChangedFiles(t *testing.T) {
	r, calls := fixtureRunner(t)
	*calls = 1 // First actual worker writes the correct content.
	base := r.Invoke
	changed := false
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if planner && r.State.Phase == "reviewing" && !changed {
			changed = true
			if err := os.WriteFile(filepath.Join(r.Repo, "result.txt"), []byte("bad\n"), 0600); err != nil {
				t.Fatal(err)
			}
			return decisionJSON(true, nil, nil), nil
		}
		return base(ctx, c, prompt, label, planner, model)
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 3 || r.State.Status != "complete" {
		t.Fatalf("stale evidence approved: %d %+v", *calls, r.State)
	}
	found := false
	for _, event := range r.State.Events {
		if event.Kind == "stale-verification" {
			found = true
		}
	}
	if !found {
		t.Fatal("stale evidence was not recorded")
	}
}

func TestCompletedRunIsReverifiedOnResume(t *testing.T) {
	r, calls := fixtureRunner(t)
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Repo, "result.txt"), []byte("regression\n"), 0600); err != nil {
		t.Fatal(err)
	}
	resume, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resume.Invoke, resume.Discover = r.Invoke, r.Discover
	if err := resume.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 3 || resume.State.CheckResults[0].ExitCode != 0 {
		t.Fatal("resume trusted old completion evidence")
	}
}

func TestCheckAndCriteriaMemorySurvivesReplanning(t *testing.T) {
	r, calls := fixtureRunner(t)
	r.State.RequiredChecks = nil
	base := r.Invoke
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if planner && r.State.Phase == "planning" {
			answer := Decision{Summary: "Implement", Tasks: []Task{{"file", "Write output", "Write output"}}, Checks: []string{checkFixtureCommand()}, SuccessCriteria: []string{"Keep the original promise"}, Acceptance: []AcceptanceCheck{{"Keep the original promise", []string{checkFixtureCommand()}}}}
			data, _ := json.Marshal(answer)
			return string(data), nil
		}
		if planner && *calls == 1 {
			return decisionJSON(false, []Task{{"file", "Repair output", "Fix the actual failure"}}, nil), nil
		}
		return base(ctx, c, prompt, label, planner, model)
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.State.SuggestedChecks) != 1 || !contains(r.State.SuccessCriteria, "Keep the original promise") || len(r.State.TaskHistory) != 2 {
		t.Fatalf("planning forgot requirements or task history: %+v", r.State)
	}
	for _, task := range r.State.TaskHistory {
		if task.Attempts != 1 || task.Status != "reported" {
			t.Fatalf("bad task record: %+v", task)
		}
	}
	if r.State.WorkerCalls != 2 {
		t.Fatal("worker attempts not counted")
	}
}

func TestGateRequiresPreviouslySuggestedChecks(t *testing.T) {
	state := State{RequiredChecks: []string{"unit"}, SuggestedChecks: []string{"build"}, CheckResults: []CheckResult{{Command: "unit", ExitCode: 0, Output: "ok"}}}
	if CompletionAllowed(state, Decision{Done: true}) {
		t.Fatal("forgotten suggested check bypassed gate")
	}
	state.CheckResults = append(state.CheckResults, CheckResult{Command: "build", ExitCode: 0, Output: "ok"})
	if !CompletionAllowed(state, Decision{Done: true}) {
		t.Fatal("passing retained checks rejected")
	}
	state.Version = stateVersion
	if CompletionAllowed(state, Decision{Done: true}) {
		t.Fatal("missing current snapshot bypassed gate")
	}
}

func TestOnlyUnavailableSuggestedChecksCanBeReplaced(t *testing.T) {
	for _, scenario := range []struct {
		name              string
		code              int
		required, allowed bool
	}{
		{"missing command", 127, false, true}, {"Windows missing command", 9009, false, true},
		{"failing test", 1, false, false}, {"test collection failure", 2, false, false},
		{"required check", 127, true, false}, {"passing check", 0, false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			r, _ := fixtureRunner(t)
			r.State.SuggestedChecks = []string{"old"}
			r.State.CheckResults = []CheckResult{{Command: "old", ExitCode: scenario.code, Output: "output"}}
			if scenario.required {
				r.State.RequiredChecks = []string{"old"}
			}
			answer := Decision{Checks: []string{"new"}, CheckReplacements: []CheckReplacement{{"old", "new", "Use the available runner"}}}
			err := r.batch(answer)
			if (err == nil) != scenario.allowed {
				t.Fatalf("replacement policy: %v", err)
			}
			if scenario.allowed && (contains(r.State.SuggestedChecks, "old") || !contains(r.State.SuggestedChecks, "new") || len(r.State.RetiredChecks) != 1) {
				t.Fatal("replacement not durable")
			}
			if !scenario.allowed && !contains(r.State.SuggestedChecks, "old") {
				t.Fatal("rejected replacement changed checks")
			}
		})
	}
}

func TestNoProgressPausesAndResumeCanRecover(t *testing.T) {
	r, calls := fixtureRunner(t)
	r.MaxCycles = 0
	r.MaxStalledCycles = 2
	r.Invoke = func(_ context.Context, _ Connection, _ string, _ string, planner bool, _ string) (string, error) {
		if planner {
			return decisionJSON(false, []Task{{"file", "Repeat an ineffective repair", "Attempt repair"}}, nil), nil
		}
		*calls++
		return "I tried, but nothing changed", nil
	}
	err := r.Run(context.Background())
	var limit limitError
	if !errors.As(err, &limit) || !strings.Contains(err.Error(), "no progress") || *calls != 3 || r.State.Status != "paused" {
		t.Fatalf("stalled loop was not bounded: %v calls=%d", err, *calls)
	}
	resume, err := ResumeRunner(r.Paths, r.Settings, r.Repo, "latest", noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resume.Discover = r.Discover
	resume.Invoke = func(_ context.Context, _ Connection, _ string, _ string, planner bool, _ string) (string, error) {
		if planner {
			if data, _ := os.ReadFile(filepath.Join(r.Repo, "result.txt")); string(data) == "good\n" {
				return decisionJSON(true, nil, nil), nil
			}
			return decisionJSON(false, []Task{{"fix", "Use a new approach", "Write the correct output"}}, nil), nil
		}
		return "Fixed", os.WriteFile(filepath.Join(r.Repo, "result.txt"), []byte("good\n"), 0600)
	}
	if err := resume.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resume.State.Status != "complete" {
		t.Fatal("stalled session could not recover")
	}
}

func TestInvalidCheckpointsFailBeforeExecution(t *testing.T) {
	for _, mutate := range []func(*State){
		func(s *State) { s.NextTask = -1 }, func(s *State) { s.NextTask = 5 },
		func(s *State) { s.RunID = "other" }, func(s *State) { s.Cycle = 0 },
		func(s *State) { s.Phase = "unknown" }, func(s *State) { s.Status = "unknown" },
		func(s *State) { s.Version = 100 }, func(s *State) { s.ActiveTask = "missing" },
	} {
		r, _ := fixtureRunner(t)
		id := r.State.RunID
		mutate(&r.State)
		if err := r.State.Save(r.Dir); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadRun(r.Repo, id); err == nil {
			t.Fatal("accepted corrupt checkpoint")
		}
	}
	if _, _, err := LoadRun(t.TempDir(), "../../outside"); err == nil {
		t.Fatal("accepted run path traversal")
	}
}

func TestLegacyCheckpointMigrationPreservesProgress(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.State.Version = 2
	r.State.Phase = "working"
	r.State.Tasks = []Task{{"file", "Write output", "Finish the file"}}
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	state, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil || state.Version != stateVersion || state.Phase != "checking" || state.RecoveryNote == "" || len(state.Tasks) != 1 {
		t.Fatalf("bad migration: %+v %v", state, err)
	}
}

func TestHistoryListsMultipleGoalsAndIsolatesCorruption(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	other, err := NewRunner(r.Paths, r.Settings, r.Repo, "A later goal", false, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.State.Save(other.Dir); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(r.Repo, ".forge", "runs", "bad-run")
	if err := os.MkdirAll(bad, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "state.json"), []byte("not JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := RunHistory(r.Repo)
	if err != nil || len(rows) != 3 {
		t.Fatalf("history failed: %+v %v", rows, err)
	}
	for _, kind := range []string{"status", "tasks", "events"} {
		value, text, err := InspectRun(r.Repo, r.State.RunID, kind)
		if err != nil || value == nil || text == "" {
			t.Fatalf("%s failed: %v", kind, err)
		}
	}
}

func TestWorkspaceSnapshotUsesContentsAndHonorsCancellation(t *testing.T) {
	repo := t.TempDir()
	file := filepath.Join(repo, "source.txt")
	if err := os.WriteFile(file, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(file)
	before, err := WorkspaceSnapshot(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := WorkspaceSnapshot(context.Background(), repo)
	if err != nil || before == after {
		t.Fatal("same-size, same-time content edit was missed")
	}
	if err := os.MkdirAll(filepath.Join(repo, ".forge"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".forge", "runtime.log"), []byte("noise"), 0600); err != nil {
		t.Fatal(err)
	}
	stable, err := WorkspaceSnapshot(context.Background(), repo)
	if err != nil || stable != after {
		t.Fatal("runtime state polluted the source fingerprint")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WorkspaceSnapshot(ctx, repo); !errors.Is(err, context.Canceled) {
		t.Fatal("snapshot ignored pause")
	}
}

func TestWorkerReceivesPriorFailureEvidence(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.State.CheckResults = []CheckResult{{Command: "test", ExitCode: 1, Output: "expected five, got minus one"}}
	r.State.TaskHistory = []TaskRecord{{Task: Task{Title: "First attempt"}, Report: "Attempted subtraction", Status: "reported"}}
	r.Invoke = func(_ context.Context, _ Connection, prompt, _ string, _ bool, _ string) (string, error) {
		if !strings.Contains(prompt, "expected five, got minus one") || !strings.Contains(prompt, "Attempted subtraction") {
			t.Fatal("worker lacks repair context")
		}
		return "Attempted", nil
	}
	if _, err := r.work(context.Background(), Task{"repair", "Repair", "Correct the behavior"}); err != nil {
		t.Fatal(err)
	}
}

func TestPausedGoalDoesNotLaunchQueuedGoal(t *testing.T) {
	for _, err := range []error{context.Canceled, errors.New("connection blocked"), limitError{"no progress"}} {
		m := newApp(testPaths(t), t.TempDir(), "", false, false)
		m.busy = true
		if _, e := QueueAdd(m.repo, "Another goal"); e != nil {
			t.Fatal(e)
		}
		updated, _ := m.Update(Event{Kind: "finished", Value: finishedMsg{Err: err}})
		next := updated.(appModel)
		queue, e := ReadQueue(next.repo)
		if e != nil || next.busy || len(queue.Items) != 1 || queue.Items[0].Status != "pending" {
			t.Fatal("paused/blocked goal launched or lost the queued goal")
		}
	}
}

func TestNewScaffoldGetsAutomaticChecks(t *testing.T) {
	r, _ := fixtureRunner(t)
	if len(SuggestedChecks(r.Repo, r.Paths)) != 0 {
		t.Fatal("unexpected initial checks")
	}
	if err := os.WriteFile(filepath.Join(r.Repo, "package.json"), []byte(`{"scripts":{"test":"node --test","build":"node build.cjs"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	// Cancel before spawning commands; detection and durable setup still occur.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.check(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
	if !contains(r.State.RequiredChecks, "npm test") || !contains(r.State.RequiredChecks, "npm run build") {
		t.Fatal("new scaffold checks were missed")
	}
}

func TestFailedWorkerReturnsToReviewBeforeDependentTasks(t *testing.T) {
	r, calls := fixtureRunner(t)
	r.Invoke = func(_ context.Context, _ Connection, prompt, _ string, planner bool, _ string) (string, error) {
		if planner {
			if r.State.Phase == "planning" {
				return decisionJSON(false, []Task{{"first", "Prerequisite", "Implement"}, {"dependent", "Dependent task", "Requires prerequisite"}}, nil), nil
			}
			if *calls == 1 {
				if !strings.Contains(prompt, "Dependent task") || r.State.NextTask != 1 {
					t.Fatal("pending dependency missing from review")
				}
				return decisionJSON(false, []Task{{"repair", "Repair the prerequisite and finish", "Complete the goal"}}, nil), nil
			}
			return decisionJSON(true, nil, nil), nil
		}
		*calls++
		if *calls == 1 {
			return "", errors.New("worker could not complete the task")
		}
		if r.State.Tasks[r.State.NextTask].ID != "repair" {
			t.Fatal("dependent task ran despite failed prerequisite")
		}
		return "Repaired", os.WriteFile(filepath.Join(r.Repo, "result.txt"), []byte("good\n"), 0600)
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 || r.State.TaskHistory[1].Status != "superseded" {
		t.Fatal("failed batch did not replan")
	}
}

func TestPlannerCanCorrectRejectedCheckReplacementAutomatically(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.State.SuggestedChecks = []string{"keep-test"}
	r.State.CheckResults = []CheckResult{{Command: "keep-test", ExitCode: 1, Output: "test failed"}}
	calls := 0
	r.Invoke = func(_ context.Context, _ Connection, prompt, _ string, _ bool, _ string) (string, error) {
		calls++
		if calls == 1 {
			answer := Decision{Tasks: []Task{}, Checks: []string{"weaker-test"}, SuccessCriteria: []string{}, CheckReplacements: []CheckReplacement{{"keep-test", "weaker-test", "Attempted shortcut"}}}
			data, _ := json.Marshal(answer)
			return string(data), nil
		}
		if !strings.Contains(prompt, "repair the failure") {
			t.Fatal("planner did not receive the rejected replacement reason")
		}
		return decisionJSON(false, []Task{{"repair", "Repair original test failure", "Fix the implementation"}}, nil), nil
	}
	answer, err := r.askPlanner(context.Background(), true)
	if err != nil || calls != 2 || len(answer.Tasks) != 1 || !contains(r.State.SuggestedChecks, "keep-test") {
		t.Fatalf("replacement recovery failed: %+v %v", answer, err)
	}
}
