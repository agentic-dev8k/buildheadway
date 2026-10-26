package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func checkFixtureCommand() string {
	if runtime.GOOS == "windows" {
		return `findstr /x /c:"good" result.txt >nul`
	}
	return `test "$(cat result.txt)" = good`
}
func fixtureRunner(t *testing.T) (*Runner, *int) {
	t.Helper()
	paths := testPaths(t)
	repo := t.TempDir()
	settings := Settings{Version: 2, Planner: Connection{Backend: "codex"}, Worker: Connection{Backend: "opencode", Model: "auto"}}
	r, err := NewRunner(paths, settings, repo, "Create a correct output file", false, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	r.State.RequiredChecks = []string{checkFixtureCommand()}
	r.MaxCycles = 4
	calls := new(int)
	r.Discover = func(context.Context) ([]string, error) { return []string{"opencode/free"}, nil }
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if planner {
			data, _ := os.ReadFile(filepath.Join(repo, "result.txt"))
			if string(data) == "good\n" {
				return decisionJSON(true, nil, nil), nil
			}
			return decisionJSON(false, []Task{{"file", "Write output", "Write the correct result."}}, nil), nil
		}
		*calls++
		value := "bad\n"
		if *calls > 1 {
			value = "good\n"
		}
		if err := os.WriteFile(filepath.Join(repo, "result.txt"), []byte(value), 0600); err != nil {
			return "", err
		}
		return "Work attempted", nil
	}
	return r, calls
}
func TestGoalLoopRepairsRealFailedCheck(t *testing.T) {
	r, calls := fixtureRunner(t)
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 || r.State.Cycle != 2 || r.State.Status != "complete" {
		t.Fatalf("unexpected completion: calls=%d state=%#v", *calls, r.State)
	}
}
func TestPlannerCannotOverrideFailingCheck(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.MaxCycles = 1
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		return decisionJSON(true, nil, nil), nil
	}
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("goal completed with failed checks")
	}
	if r.State.Status == "complete" {
		t.Fatal("false completion")
	}
}
func TestCallLimitResumeSkipsCompletedWorker(t *testing.T) {
	r, calls := fixtureRunner(t)
	*calls = 1
	r.MaxCalls = 1
	err := r.Run(context.Background())
	var limit limitError
	if !errors.As(err, &limit) || r.State.Phase != "reviewing" {
		t.Fatalf("did not pause at review: %v", err)
	}
	if *calls != 2 {
		t.Fatal("initial worker did not execute once")
	}
	resume, err := NewRunner(r.Paths, r.Settings, r.Repo, "", true, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resume.Invoke = r.Invoke
	resume.Discover = r.Discover
	resume.MaxCalls = 3
	if err := resume.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 || resume.State.Status != "complete" {
		t.Fatal("resume repeated a completed worker")
	}
}
func TestPriceChangeBlocksNextDispatch(t *testing.T) {
	r, calls := fixtureRunner(t)
	r.State.Phase = "working"
	r.State.Tasks = []Task{{"one", "One", "Work"}, {"two", "Two", "Work"}}
	discoveries := 0
	r.Discover = func(context.Context) ([]string, error) {
		discoveries++
		if discoveries > 1 {
			return nil, errors.New("no verified zero prices")
		}
		return []string{"opencode/free"}, nil
	}
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("price change did not block")
	}
	if *calls != 1 || r.State.NextTask != 1 || r.State.Status != "blocked" {
		t.Fatal("worker dispatched after pricing became unverifiable")
	}
}
func TestFreeWorkerFallbackAndExplicitSelection(t *testing.T) {
	r, _ := fixtureRunner(t)
	os.MkdirAll(r.Dir, 0700)
	r.Discover = func(context.Context) ([]string, error) { return []string{"opencode/first", "opencode/second"}, nil }
	r.Invoke = func(_ context.Context, _ Connection, _ string, _ string, _ bool, model string) (string, error) {
		if model == "opencode/first" {
			return "", errors.New("429 rate limited")
		}
		return "Done", nil
	}
	result, err := r.work(context.Background(), Task{"one", "Work", "Instructions"})
	if err != nil || result.Model != "opencode/second" {
		t.Fatalf("free fallback failed: %#v %v", result, err)
	}
	r.Settings.Worker.Model = "opencode/paid"
	if _, err := r.work(context.Background(), Task{"one", "Work", "Instructions"}); err == nil {
		t.Fatal("explicit paid model was accepted")
	}
}
func TestCancellationKeepsPendingTask(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.State.Phase = "working"
	r.State.Tasks = []Task{{"one", "Work", "Instructions"}}
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	r.Invoke = func(ctx context.Context, _ Connection, _ string, _ string, _ bool, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected cancellation: %v", err)
	}
	if r.State.Status != "interrupted" || r.State.NextTask != 0 {
		t.Fatal("interrupted task incorrectly marked complete")
	}
}
func TestRepositoryLockPreventsConcurrentGoal(t *testing.T) {
	r, _ := fixtureRunner(t)
	os.MkdirAll(filepath.Join(r.Repo, ".forge"), 0700)
	file, err := os.OpenFile(filepath.Join(r.Repo, ".forge", "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := lockFile(file); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("concurrent controller was allowed")
	}
}

func TestProviderDenialSavesPendingTaskWithoutRepeatedPlannerCalls(t *testing.T) {
	r, calls := fixtureRunner(t)
	r.Discover = func(context.Context) ([]string, error) {
		return []string{"opencode/first", "opencode/second"}, nil
	}
	r.Invoke = func(_ context.Context, _ Connection, _ string, _ string, planner bool, _ string) (string, error) {
		if planner {
			return decisionJSON(false, []Task{{"one", "Work", "Implement the goal"}}, nil), nil
		}
		*calls++
		return "", errors.New("OpenCode's free tier can only be used from within OpenCode")
	}
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("provider refusal did not block the run")
	}
	if *calls != 1 || r.State.PlannerCalls != 1 || r.State.NextTask != 0 || r.State.Status != "blocked" {
		t.Fatalf("provider refusal was retried or task lost: calls=%d state=%#v", *calls, r.State)
	}
	var saved State
	if err := ReadJSON(filepath.Join(r.Dir, "state.json"), &saved); err != nil || saved.NextTask != 0 || saved.Status != "blocked" {
		t.Fatalf("blocked checkpoint was not saved: %#v %v", saved, err)
	}
}

func TestPlannerProviderDenialStopsAfterOneCall(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		return "", errors.New("not available in your region")
	}
	if err := r.Run(context.Background()); err == nil || r.State.PlannerCalls != 1 {
		t.Fatalf("hard planner denial was retried: %v calls=%d", err, r.State.PlannerCalls)
	}
}

func TestAutomaticFreePlannerRecoversFromIncompleteReplyAndKeepsWorkingModel(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.Settings.Planner = Connection{Backend: "opencode", Model: "auto"}
	r.Discover = func(context.Context) ([]string, error) {
		return []string{"opencode/first", "opencode/second"}, nil
	}
	attempts := []string{}
	r.Invoke = func(_ context.Context, c Connection, _ string, _ string, _ bool, _ string) (string, error) {
		attempts = append(attempts, c.Model)
		if c.Model == "opencode/first" {
			return "I will inspect the files.", nil
		}
		return decisionJSON(false, []Task{{"one", "Fix", "Implement the change"}}, nil), nil
	}
	if _, err := r.askPlanner(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || r.State.PlannerModel != "opencode/second" {
		t.Fatalf("free planner fallback failed: %v %s", attempts, r.State.PlannerModel)
	}
	if _, err := r.askPlanner(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 3 || attempts[2] != "opencode/second" || r.Settings.Planner.Model != "auto" {
		t.Fatal("review discarded the working model or changed the user's automatic setting")
	}
}

func TestAutomaticFreePlannerStopsOnReportedCharge(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.Settings.Planner = Connection{Backend: "opencode", Model: "auto"}
	r.Discover = func(context.Context) ([]string, error) {
		return []string{"opencode/first", "opencode/second"}, nil
	}
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		return "", costError{}
	}
	if _, err := r.askPlanner(context.Background(), false); err == nil || r.State.PlannerCalls != 1 {
		t.Fatalf("charged automatic route was retried: %v calls=%d", err, r.State.PlannerCalls)
	}
}
