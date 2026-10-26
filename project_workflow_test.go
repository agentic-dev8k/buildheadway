package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeFixture(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectSettingsStrictAndInitializationPreservesWork(t *testing.T) {
	repo := t.TempDir()
	for _, invalid := range []string{`null`, `[]`, `{} {}`, `{"unknown":1}`, `{"max_cycles":-1}`, `{"check_timeout_seconds":0}`, `{"context_files":["../outside"]}`, `{"platform_checks":{"other":["test"]}}`} {
		writeFixture(t, filepath.Join(repo, ".buildheadway.json"), invalid)
		if _, err := LoadProjectConfig(repo); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
	writeFixture(t, filepath.Join(repo, ".buildheadway.json"), `{"checks":["echo generic"],"max_worker_calls":5,"platform_checks":{"`+runtime.GOOS+`":["echo native"]}}`)
	writeFixture(t, filepath.Join(repo, "BUILDHEADWAY.md"), "Preserve this guidance")
	created, err := InitProject(repo)
	if err != nil || len(created) != 0 {
		t.Fatalf("initialization: %v %v", created, err)
	}
	config, err := LoadProjectConfig(repo)
	if err != nil || config.MaxWorkerCalls != 5 || len(config.RequiredChecks()) != 2 || config.WorkerTimeoutSeconds != 1800 {
		t.Fatalf("settings: %+v %v", config, err)
	}
	text, _ := os.ReadFile(filepath.Join(repo, "BUILDHEADWAY.md"))
	if string(text) != "Preserve this guidance" {
		t.Fatal("initialization overwrote guidance")
	}
}

func TestProjectContextAndMemoryPersistAcrossResumes(t *testing.T) {
	r, calls := fixtureRunner(t)
	writeFixture(t, filepath.Join(r.Repo, "BUILDHEADWAY.md"), "Keep the public API stable")
	writeFixture(t, filepath.Join(r.Repo, "docs", "design.txt"), "Use the existing parser")
	r.State.Config.ContextFiles = []string{`docs\design.txt`}
	r.State.Config.MaxWorkerCalls = 1
	if _, err := Remember(r.Repo, "Use small focused changes"); err != nil {
		t.Fatal(err)
	}
	r.PlanOnly = true
	invoke := r.Invoke
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		for _, want := range []string{"Keep the public API stable", "Use the existing parser", "Use small focused changes"} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("missing project context %q", want)
			}
		}
		return invoke(ctx, c, prompt, label, planner, model)
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 0 || r.State.Status != "planned" || len(r.State.TaskHistory) != 1 {
		t.Fatal("planning executed implementation or lost its tasks")
	}
	if _, err := os.Stat(filepath.Join(r.Repo, "result.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("planning modified the fixture")
	}
	writeFixture(t, filepath.Join(r.Repo, ".buildheadway.json"), `{"max_worker_calls":99}`)
	resume, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil || resume.MaxWorkerCalls != 1 {
		t.Fatalf("saved settings were not pinned: %v", err)
	}
	*calls = 1
	resume.Invoke, resume.Discover = r.Invoke, r.Discover
	if err := resume.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 || resume.State.PlannerCalls != 2 || len(resume.State.TaskHistory) != 1 || resume.State.Status != "complete" {
		t.Fatalf("resuming a plan duplicated work: %+v", resume.State)
	}
}

func TestProjectContextRejectsOutsideSymlinksAndBinaryData(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	writeFixture(t, filepath.Join(outside, "secret"), "outside")
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(repo, "BUILDHEADWAY.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ProjectContext(repo, DefaultProjectConfig()); err == nil {
		t.Fatal("read outside-project context")
	}
	if err := os.Remove(filepath.Join(repo, "BUILDHEADWAY.md")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(repo, "BUILDHEADWAY.md"), "binary\x00text")
	if _, err := ProjectContext(repo, DefaultProjectConfig()); err == nil {
		t.Fatal("accepted binary guidance")
	}
}

func TestMemoryConcurrentNotesDeduplicateAndCanBeRemoved(t *testing.T) {
	repo := t.TempDir()
	var group sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func(i int) { defer group.Done(); _, err := Remember(repo, "note "+itoa(i%4)); failures <- err }(i)
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	memory, err := ReadMemory(repo)
	if err != nil || len(memory.Entries) != 4 {
		t.Fatalf("lost notes: %+v %v", memory, err)
	}
	if err := BuildHeadwayt(repo, memory.Entries[0].ID); err != nil {
		t.Fatal(err)
	}
	memory, err = ReadMemory(repo)
	if err != nil || len(memory.Entries) != 3 {
		t.Fatal("buildheadwayt did not persist")
	}
}

func TestWorkerBudgetPausesBeforeDispatchAndCanResume(t *testing.T) {
	r, calls := fixtureRunner(t)
	r.MaxWorkerCalls = 1
	var limit limitError
	if err := r.Run(context.Background()); !errors.As(err, &limit) {
		t.Fatalf("expected worker cap: %v", err)
	}
	if *calls != 1 || r.State.WorkerCalls != 1 || r.State.Status != "paused" {
		t.Fatal("worker budget exceeded")
	}
	resume, err := ResumeRunner(r.Paths, r.Settings, r.Repo, "latest", noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resume.Invoke, resume.Discover, resume.MaxWorkerCalls = r.Invoke, r.Discover, 2
	if err := resume.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 || resume.State.Status != "complete" {
		t.Fatal("budget resume did not finish")
	}
}

func TestRuntimeBudgetStopsActiveCallAndIncludesPreviousUsage(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.MaxRuntime = 120 * time.Millisecond
	r.Invoke = func(ctx context.Context, _ Connection, _, _ string, _ bool, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	var limit limitError
	if err := r.Run(context.Background()); !errors.As(err, &limit) {
		t.Fatalf("runtime cap: %v", err)
	}
	if r.State.Status != "paused" || r.State.ActiveMillis < 100 {
		t.Fatalf("time not saved: %+v", r.State)
	}
	resume, err := ResumeRunner(r.Paths, r.Settings, r.Repo, "latest", noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resume.MaxRuntime = 50 * time.Millisecond
	resume.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		t.Fatal("exhausted budget made a model call")
		return "", nil
	}
	if err := resume.Run(context.Background()); !errors.As(err, &limit) {
		t.Fatalf("prior usage not counted: %v", err)
	}
}

func configureQueueFixture(r *Runner) {
	r.State.RequiredChecks = []string{checkFixtureCommand()}
	r.Discover = func(context.Context) ([]string, error) { return []string{"opencode/free"}, nil }
	r.Invoke = func(_ context.Context, _ Connection, _, _ string, planner bool, _ string) (string, error) {
		if planner {
			if len(r.State.CheckResults) > 0 {
				return decisionJSON(true, nil, nil), nil
			}
			return decisionJSON(false, []Task{{"work", "Write result", "Write the result"}}, nil), nil
		}
		return "written", os.WriteFile(filepath.Join(r.Repo, "result.txt"), []byte("good\n"), 0600)
	}
}

func TestQueueCrashClaimReusesCheckpointAndFailureHoldsLaterGoals(t *testing.T) {
	repo, paths := t.TempDir(), testPaths(t)
	writeFixture(t, filepath.Join(repo, ".buildheadway.json"), `{"checks":["echo verified"]}`)
	settings := Settings{Planner: Connection{Backend: "codex"}, Worker: Connection{Backend: "opencode", Model: "auto"}}
	first, err := QueueAdd(repo, "first goal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := QueueAdd(repo, "second goal"); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireProjectLock(repo, "lock", false)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := claimQueuedRunner(paths, settings, repo, noEmit)
	if err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if _, _, err := LoadRun(repo, claimed.State.RunID); err != nil {
		lock.Close()
		t.Fatal("claim missing durable checkpoint")
	}
	if err := QueueRemove(repo, first.ID); err == nil {
		lock.Close()
		t.Fatal("removed active claim")
	}
	lock.Close() // Simulate a process exiting after its claim but before dispatch.
	attempts := 0
	err = runQueuedWith(context.Background(), paths, settings, repo, true, noEmit, func(r *Runner) {
		attempts++
		if r.State.RunID != claimed.State.RunID {
			t.Fatal("claim created a duplicate run")
		}
		configureQueueFixture(r)
		r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
			return "", authError{"codex", "sign in required"}
		}
	})
	if err == nil || attempts != 1 {
		t.Fatal("queue did not hold later goals after failure")
	}
	queue, err := ReadQueue(repo)
	if err != nil || queue.Items[0].Status != "blocked" || queue.Items[1].Status != "pending" || queue.Items[1].RunID != "" {
		t.Fatalf("invalid queue: %+v %v", queue, err)
	}
	if err := runQueuedWith(context.Background(), paths, settings, repo, true, noEmit, configureQueueFixture); err != nil {
		t.Fatal(err)
	}
	queue, err = ReadQueue(repo)
	if err != nil || queue.Items[0].RunID != claimed.State.RunID || queue.Items[0].Status != "complete" || queue.Items[1].Status != "complete" {
		t.Fatalf("queue did not recover: %+v %v", queue, err)
	}
	rows, err := RunHistory(repo)
	if err != nil || len(rows) != 2 {
		t.Fatal("duplicate queued runs")
	}
}

func TestQueueConcurrentAddsAndReordering(t *testing.T) {
	repo := t.TempDir()
	var group sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func(i int) { defer group.Done(); _, err := QueueAdd(repo, "goal "+itoa(i)); failures <- err }(i)
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	queue, err := ReadQueue(repo)
	if err != nil || len(queue.Items) != 12 {
		t.Fatal("concurrent add lost a goal")
	}
	last := queue.Items[11].ID
	if err := QueueMove(repo, last, 1); err != nil {
		t.Fatal(err)
	}
	queue, err = ReadQueue(repo)
	if err != nil || queue.Items[0].ID != last {
		t.Fatal("move not saved")
	}
	if err := QueueRemove(repo, last); err != nil {
		t.Fatal(err)
	}
	queue, _ = ReadQueue(repo)
	if queue.Items[0].Status != "canceled" || !QueueReady(repo) {
		t.Fatal("cancellation lost remaining queue")
	}
	lock, err := acquireProjectLock(repo, "lock", false)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := RunQueued(context.Background(), testPaths(t), Settings{}, repo, true, noEmit); err == nil {
		t.Fatal("second consumer acquired active project")
	}
	if _, err := QueueAdd(repo, "add while running"); err != nil {
		t.Fatal(err)
	}
}

func TestStandaloneVerificationRecordsFailuresWithoutChangingGoal(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.PlanOnly = true
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(r.Repo, "result.txt"), "bad\n")
	report, err := CheckProject(context.Background(), r.Paths, r.Repo, noEmit)
	if err == nil || report.Status != "failed" || len(report.Results) != 1 || report.Results[0].ExitCode == 0 {
		t.Fatalf("false check success: %+v %v", report, err)
	}
	writeFixture(t, filepath.Join(r.Repo, "result.txt"), "good\n")
	report, err = CheckProject(context.Background(), r.Paths, r.Repo, noEmit)
	if err != nil || report.Status != "passed" {
		t.Fatalf("check: %+v %v", report, err)
	}
	after, _ := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	if string(after) != string(before) {
		t.Fatal("standalone verification mutated the goal")
	}
	var saved VerificationReport
	if err := ReadJSON(filepath.Join(r.Repo, ".forge", "last-check.json"), &saved); err != nil || saved.ID != report.ID {
		t.Fatal("check results not saved")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err = CheckProject(ctx, r.Paths, r.Repo, noEmit)
	if !errors.Is(err, context.Canceled) || report.Status != "interrupted" {
		t.Fatalf("cancellation: %+v %v", report, err)
	}
}

func TestExportPreservesExistingFilesAndRoundTripsJSON(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.PlanOnly = true
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.State.Summary = "````\n## Untrusted heading\n"
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(r.Repo, "report.json")
	writeFixture(t, destination, "keep")
	if _, err := ExportRun(r.Repo, "latest", destination, true, false); !errors.Is(err, os.ErrExist) {
		t.Fatal("export overwrote existing file")
	}
	if _, err := ExportRun(r.Repo, "latest", destination, true, true); err != nil {
		t.Fatal(err)
	}
	var state State
	data, _ := os.ReadFile(destination)
	if err := json.Unmarshal(data, &state); err != nil || state.RunID != r.State.RunID {
		t.Fatal("export mismatch")
	}
	md, err := ExportRun(r.Repo, "latest", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(md)
	if !strings.Contains(string(data), "`````text") {
		t.Fatal("report fence allowed content breakout")
	}
	rows, err := RunHistory(r.Repo)
	if err != nil || len(FilterHistory(rows, "CORRECT", 20)) != 1 || len(FilterHistory(rows, "absent", 20)) != 0 {
		t.Fatal("history search incorrect")
	}
}

func TestGitInspectionIncludesStagedUnstagedAndUntrackedWithoutMutation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	git("init")
	writeFixture(t, filepath.Join(repo, "file.txt"), "base\n")
	git("add", "file.txt")
	git("-c", "user.name=BuildHeadway Test", "-c", "user.email=buildheadway@example.invalid", "commit", "-m", "base")
	writeFixture(t, filepath.Join(repo, "file.txt"), "staged-line\n")
	git("add", "file.txt")
	writeFixture(t, filepath.Join(repo, "file.txt"), "working-line\n")
	writeFixture(t, filepath.Join(repo, "untracked.txt"), "new")
	indexBefore, _ := os.ReadFile(filepath.Join(repo, ".git", "index"))
	output, err := GitChanges(context.Background(), testPaths(t), repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"staged-line", "working-line", "untracked.txt"} {
		if !strings.Contains(output, want) {
			t.Fatalf("missing diff %s", want)
		}
	}
	indexAfter, _ := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if string(indexBefore) != string(indexAfter) {
		t.Fatal("inspection changed git index")
	}
}

func TestUIPlanAndChecksDoNotStartQueuedGoals(t *testing.T) {
	for _, event := range []Event{{Kind: "finished", Value: finishedMsg{Status: "planned"}}, {Kind: "check-finished", Value: inspectionMsg{Kind: "check", Text: "passed"}}} {
		m := newApp(testPaths(t), t.TempDir(), "", false, false)
		m.busy = true
		if _, err := QueueAdd(m.repo, "later"); err != nil {
			t.Fatal(err)
		}
		model, _ := m.Update(event)
		next := model.(appModel)
		queue, err := ReadQueue(next.repo)
		if err != nil || next.busy || queue.Items[0].Status != "pending" {
			t.Fatal("plan/check unexpectedly ran queued goal")
		}
	}
}
