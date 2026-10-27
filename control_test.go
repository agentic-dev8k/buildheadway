package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPendingPauseCannotRaceCompletion(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(r.Dir, "control.json"), `{"version":1,"pause":{"id":"pause-test","run_id":"`+r.State.RunID+`","created_at":"2026-10-27T00:00:00Z"}}`)
	if err := r.finishRun("complete", "approved"); err == nil || r.State.Status == "complete" {
		t.Fatal("completion ignored a durable pause request")
	}
	data, err := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	if err != nil || len(data) == 0 {
		t.Fatal("lost checkpoint")
	}
}

func TestRunControlHelper(t *testing.T) {
	if os.Getenv("BUILDHEADWAY_CONTROL_HELPER") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if mode == "worker" {
		if err := os.WriteFile("result.txt", []byte("good\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Println("partial output before pause")
	if err := os.WriteFile(filepath.Join(".forge", "control-ready"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(".forge", "control-continue")); err == nil {
		return
	}
	time.Sleep(time.Minute)
}

func controlCommand(t *testing.T, mode string) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quoted := ShellQuote(path)
	if runtime.GOOS == "windows" {
		quoted = `"` + path + `"`
	}
	return quoted + " -test.run=^TestRunControlHelper$ -- " + mode
}

func waitForControlHelper(t *testing.T, repo string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(repo, ".forge", "control-ready")); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("control helper did not start")
}

func TestPauseFromSecondTerminalStopsWorkerAndRecoversPartialEdits(t *testing.T) {
	r, _ := fixtureRunner(t)
	t.Setenv("BUILDHEADWAY_CONTROL_HELPER", "1")
	binary := nativeTestBinary(t)
	original := r.Invoke
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if planner {
			return original(ctx, c, prompt, label, planner, model)
		}
		result, err := RunProcess(ctx, ShellCommand(controlCommand(t, "worker")), r.Repo, filepath.Join(r.Dir, "partial-worker.log"), "", ToolEnv(r.Paths), time.Minute, nil)
		return result.Output, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	waitForControlHelper(t, r.Repo)
	command := exec.CommandContext(ctx, binary, "pause", "--project", r.Repo, "--json", r.State.RunID)
	command.Env = testCommandEnv(t.TempDir())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pause CLI failed: %v %s", err, output)
	}
	var request PauseRequest
	if err := json.Unmarshal(output, &request); err != nil || request.RunID != r.State.RunID {
		t.Fatalf("pause reply: %s %v", output, err)
	}
	select {
	case err := <-done:
		var pause runPauseError
		if !errors.As(err, &pause) || r.State.Status != "paused" {
			t.Fatalf("not paused: %s %v", r.State.Status, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pause did not cancel the active worker promptly")
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil || saved.WorkerCalls != 1 || saved.ActiveTask == "" || saved.Status != "paused" {
		t.Fatalf("partial task not checkpointed: %+v %v", saved, err)
	}
	resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	reviews := 0
	resumed.Invoke = func(_ context.Context, _ Connection, prompt, _ string, planner bool, _ string) (string, error) {
		if !planner {
			t.Fatal("replayed an interrupted worker instead of checking its edits")
		}
		reviews++
		if len(resumed.State.CheckResults) != 1 || !resumed.State.CheckResults[0].Passed() || resumed.State.CheckedSnapshot == "" || !strings.Contains(prompt, "partial work") {
			t.Fatal("review did not receive fresh verification and recovery context")
		}
		return decisionJSON(true, nil, nil), nil
	}
	if err := resumed.Run(context.Background()); err != nil || resumed.State.Status != "complete" || reviews != 1 || resumed.State.WorkerCalls != 1 {
		t.Fatalf("resume failed: %v status=%s reviews=%d", err, resumed.State.Status, reviews)
	}
	control, err := readRunControl(r.Dir, r.State.RunID)
	if err != nil || control.Acknowledged != request.ID {
		t.Fatalf("old pause not acknowledged on explicit resume: %+v %v", control, err)
	}
}

func TestPauseDuringVerificationRetainsEvidenceAndRechecksOnResume(t *testing.T) {
	r, _ := fixtureRunner(t)
	t.Setenv("BUILDHEADWAY_CONTROL_HELPER", "1")
	check := controlCommand(t, "check")
	r.State.RequiredChecks, r.State.Phase = []string{check}, "checking"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	waitForControlHelper(t, r.Repo)
	if _, err := RequestPause(r.Repo, r.State.RunID); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || r.State.Status != "paused" {
		t.Fatalf("pause failed: %v", err)
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil || len(saved.CheckResults) != 1 || saved.CheckResults[0].FailureKind != "cancelled" || saved.CheckResults[0].ExitCode != 130 || !strings.Contains(saved.CheckResults[0].Output, "partial output") || saved.CheckedSnapshot != "" {
		t.Fatalf("lost interrupted check evidence: %+v %v", saved.CheckResults, err)
	}
	writeFixture(t, filepath.Join(r.Repo, ".forge", "control-continue"), "continue")
	resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resumed.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		if len(resumed.State.CheckResults) != 1 || !resumed.State.CheckResults[0].Passed() || resumed.State.CheckedSnapshot == "" {
			t.Fatal("stale check reached senior review")
		}
		return decisionJSON(true, nil, []string{check}), nil
	}
	if err := resumed.Run(context.Background()); err != nil || resumed.State.Status != "complete" {
		t.Fatalf("resume: %v %s", err, resumed.State.Status)
	}
}

func TestPauseRequestsAreBoundedIdempotentAndDoNotRewriteCheckpoint(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	first, err := RequestPause(r.Repo, r.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RequestPause(r.Repo, r.State.RunID)
	if err != nil || first != second {
		t.Fatal("duplicate requests were not coalesced")
	}
	after, _ := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	if string(before) != string(after) {
		t.Fatal("pause writer rewrote the controller checkpoint")
	}
	r.State.Status = "complete"
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	if _, err := RequestPause(r.Repo, r.State.RunID); err == nil {
		t.Fatal("accepted a pause for completed work")
	}
	for _, raw := range []string{`null`, `{}`, `{"version":2}`, `{"version":1,"acknowledged":"missing"}`, `{"version":1,"pause":{"id":"pause-one","run_id":"other","created_at":"2026-10-27T00:00:00Z"}}`, strings.Repeat(" ", 4097), `not json`} {
		writeFixture(t, filepath.Join(r.Dir, "control.json"), raw)
		if _, err := readRunControl(r.Dir, r.State.RunID); err == nil {
			t.Fatalf("accepted invalid control: %.100s", raw)
		}
	}
}

func TestPausedQueueHoldsLaterGoalsAndResumesSameRun(t *testing.T) {
	repo, paths := t.TempDir(), testPaths(t)
	settings := Settings{Planner: Connection{Backend: "codex"}, Worker: Connection{Backend: "opencode", Model: "auto"}}
	first, err := QueueAdd(repo, "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := QueueAdd(repo, "second"); err != nil {
		t.Fatal(err)
	}
	var firstRun string
	err = runQueuedWith(context.Background(), paths, settings, repo, true, noEmit, func(r *Runner) {
		firstRun = r.State.RunID
		configureQueueFixture(r)
		r.Invoke = func(ctx context.Context, _ Connection, _, _ string, _ bool, _ string) (string, error) {
			if _, err := RequestPause(r.Repo, r.State.RunID); err != nil {
				return "", err
			}
			<-ctx.Done()
			return "", ctx.Err()
		}
	})
	var pause runPauseError
	if !errors.As(err, &pause) {
		t.Fatalf("queue did not pause: %v", err)
	}
	queue, err := ReadQueue(repo)
	if err != nil || queue.Items[0].ID != first.ID || queue.Items[0].Status != "blocked" || queue.Items[1].Status != "pending" || queue.Items[1].RunID != "" {
		t.Fatalf("queue advanced after pause: %+v %v", queue, err)
	}
	if err := runQueuedWith(context.Background(), paths, settings, repo, true, noEmit, configureQueueFixture); err != nil {
		t.Fatal(err)
	}
	queue, err = ReadQueue(repo)
	if err != nil || queue.Items[0].RunID != firstRun || queue.Items[0].Status != "complete" || queue.Items[1].Status != "complete" {
		t.Fatalf("queue recovery lost identity: %+v %v", queue, err)
	}
}

func TestPausePreservesReportedProviderBlockers(t *testing.T) {
	for _, role := range []string{"planner", "worker"} {
		for _, blocker := range []error{costError{}, authError{"codex", "authentication failed"}, authError{"codex", "renew account access"}, errors.New("access denied by provider"), outputLimitError{}, errors.Join(context.Canceled, costError{})} {
			t.Run(role+"/"+modelFailureKind(blocker, false), func(t *testing.T) {
				r, _ := fixtureRunner(t)
				calls := 0
				r.Invoke = func(ctx context.Context, _ Connection, _, _ string, planner bool, _ string) (string, error) {
					if planner && role == "worker" {
						return decisionJSON(false, []Task{{ID: "work", Title: "work", Instructions: "write the result"}}, nil), nil
					}
					calls++
					if _, err := RequestPause(r.Repo, r.State.RunID); err != nil {
						return "", err
					}
					<-ctx.Done()
					return "", blocker
				}
				err := r.Run(context.Background())
				if err == nil || r.State.Status != "blocked" || !strings.Contains(r.State.Error, blocker.Error()) || calls != 1 {
					t.Fatalf("pause hid a provider blocker: err=%v status=%s calls=%d", err, r.State.Status, calls)
				}
			})
		}
	}
}

func TestInvalidControlBlocksStartupAndStopsAnActiveAgent(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			r, _ := fixtureRunner(t)
			calls := 0
			if !active {
				writeFixture(t, filepath.Join(r.Dir, "control.json"), `{"version":2}`)
			}
			r.Invoke = func(ctx context.Context, _ Connection, _, _ string, _ bool, _ string) (string, error) {
				calls++
				if err := AtomicJSON(filepath.Join(r.Dir, "control.json"), map[string]int{"version": 2}); err != nil {
					return "", err
				}
				<-ctx.Done()
				return "", ctx.Err()
			}
			err := r.Run(context.Background())
			if err == nil || r.State.Status != "blocked" || !strings.Contains(err.Error(), "unsupported run control") || calls != map[bool]int{false: 0, true: 1}[active] {
				t.Fatalf("invalid control was ignored: err=%v state=%s calls=%d", err, r.State.Status, calls)
			}
		})
	}
}

func TestPauseConcurrentWritersCoalesceWithoutChangingCheckpoint(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	type result struct {
		request PauseRequest
		err     error
	}
	results := make(chan result, 8)
	for range cap(results) {
		go func() {
			request, err := RequestPause(r.Repo, r.State.RunID)
			results <- result{request, err}
		}()
	}
	var first PauseRequest
	for range cap(results) {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if first.ID == "" {
			first = r.request
		} else if r.request != first {
			t.Fatal("concurrent requests created different pause identities")
		}
	}
	after, err := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("concurrent pause writers changed the controller checkpoint")
	}
}

func TestPauseForAnotherSavedGoalDoesNotStopActiveGoal(t *testing.T) {
	r, _ := fixtureRunner(t)
	other, err := NewRunner(r.Paths, r.Settings, r.Repo, "another saved goal", false, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.State.Save(other.Dir); err != nil {
		t.Fatal(err)
	}
	invoke := r.Invoke
	requested := false
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if !requested {
			requested = true
			if _, err := RequestPause(r.Repo, other.State.RunID); err != nil {
				return "", err
			}
			// Let the active watcher poll while the other goal has a pending pause.
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(450 * time.Millisecond):
			}
		}
		return invoke(ctx, c, prompt, label, planner, model)
	}
	if err := r.Run(context.Background()); err != nil || r.State.Status != "complete" {
		t.Fatalf("another goal's pause interrupted this goal: %v %s", err, r.State.Status)
	}
	if err := other.pausePending(); err == nil {
		t.Fatal("the targeted goal lost its pause request")
	}
}
