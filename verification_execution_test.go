package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestVerificationExecutionHelper(t *testing.T) {
	if os.Getenv("BUILDHEADWAY_EXECUTION_HELPER") != "1" {
		return
	}
	fmt.Println("partial verification diagnostics")
	if err := os.WriteFile(filepath.Join(".forge", "execution-ready"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	if os.Args[len(os.Args)-1] == "fail" {
		os.Exit(7)
	}
	if _, err := os.Stat(filepath.Join(".forge", "allow-execution")); err == nil {
		return
	}
	time.Sleep(time.Minute)
}

func executionCommand(t *testing.T, mode string) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quoted := ShellQuote(path)
	if runtime.GOOS == "windows" {
		quoted = `"` + path + `"`
	}
	return quoted + " -test.run=^TestVerificationExecutionHelper$ -- " + mode
}

func TestInterruptedCheckRetainsExecutedEvidence(t *testing.T) {
	r, _ := fixtureRunner(t)
	t.Setenv("BUILDHEADWAY_EXECUTION_HELPER", "1")
	config := DefaultProjectConfig()
	config.Checks = []string{executionCommand(t, "wait")}
	if err := AtomicJSON(filepath.Join(r.Repo, ".buildheadway.json"), config); err != nil {
		t.Fatal(err)
	}
	ctx, ready := cancelWhenExecutionStarts(t, r.Repo)
	report, err := CheckProject(ctx, r.Paths, r.Repo, noEmit)
	if readyErr := <-ready; readyErr != nil {
		t.Fatal(readyErr)
	}
	if !errors.Is(err, context.Canceled) || report.Status != "interrupted" || len(report.Results) != 1 {
		t.Fatalf("lost interrupted command evidence: err=%v report=%+v", err, report)
	}
	check := report.Results[0]
	if check.ExitCode != 130 || check.FailureKind != "cancelled" || check.DurationMillis <= 0 || !strings.Contains(check.Output, "partial verification diagnostics") || !validLogName(check.Log) {
		t.Fatalf("lost partial output or recorded success: %+v", check)
	}
	var saved VerificationReport
	if err := ReadJSON(filepath.Join(r.Repo, ".forge", "last-check.json"), &saved); err != nil || len(saved.Results) != 1 || saved.Results[0].FailureKind != "cancelled" {
		t.Fatalf("interrupted evidence not durable: %+v %v", saved, err)
	}
	if !strings.Contains(FormatVerification(saved), "cancelled") {
		t.Fatal("terminal report lost failure kind")
	}
}

func cancelWhenExecutionStarts(t *testing.T, repo string) (context.Context, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ready := make(chan error, 1)
	go func() {
		deadline := time.NewTimer(15 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-deadline.C:
				cancel()
				ready <- errors.New("verification helper did not start")
				return
			case <-ctx.Done():
				ready <- ctx.Err()
				return
			case <-tick.C:
				if _, err := os.Stat(filepath.Join(repo, ".forge", "execution-ready")); err == nil {
					cancel()
					ready <- nil
					return
				}
			}
		}
	}()
	return ctx, ready
}

func TestVerificationExecutionDistinguishesTimeoutExitAndStartupFailure(t *testing.T) {
	t.Setenv("BUILDHEADWAY_EXECUTION_HELPER", "1")
	for _, scenario := range []struct {
		name, mode, kind string
		code             int
		timeout          time.Duration
	}{
		{"timeout", "wait", "timeout", 124, 3 * time.Second},
		{"real-exit", "fail", "exit", 7, 10 * time.Second},
		{"log-setup-error", "fail", "execution", -1, 10 * time.Second},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			r, _ := fixtureRunner(t)
			dir := r.Dir
			if scenario.kind == "execution" {
				dir = filepath.Join(r.Repo, "not-a-directory")
				writeFixture(t, dir, "regular file")
			}
			var checks []CheckResult
			err := runVerification(context.Background(), r.Paths, r.Repo, dir, []string{executionCommand(t, scenario.mode)}, scenario.timeout, false, noEmit, nil, func(c CheckResult) error { checks = append(checks, c); return nil })
			if err != nil || len(checks) != 1 {
				t.Fatalf("missing result: %+v %v", checks, err)
			}
			c := checks[0]
			if c.FailureKind != scenario.kind || c.ExitCode != scenario.code || c.DurationMillis <= 0 || c.Passed() {
				t.Fatalf("incorrect outcome: %+v", c)
			}
			if scenario.kind != "exit" && c.Error == "" {
				t.Fatal("execution error missing")
			}
			if scenario.kind != "execution" && !strings.Contains(c.Output, "partial verification diagnostics") {
				t.Fatal("lost real output")
			}
		})
	}
}

func TestInterruptedGoalRechecksBeforeSeniorApproval(t *testing.T) {
	r, _ := fixtureRunner(t)
	t.Setenv("BUILDHEADWAY_EXECUTION_HELPER", "1")
	command := executionCommand(t, "wait")
	r.State.RequiredChecks, r.State.Phase = []string{command}, "checking"
	ctx, ready := cancelWhenExecutionStarts(t, r.Repo)
	if err := r.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil || len(saved.CheckResults) != 1 || saved.CheckResults[0].FailureKind != "cancelled" || saved.CheckedSnapshot != "" {
		t.Fatalf("missing saved interruption: %+v %v", saved.CheckResults, err)
	}
	writeFixture(t, filepath.Join(r.Repo, ".forge", "allow-execution"), "ready")
	resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	reviews := 0
	resumed.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		reviews++
		if len(resumed.State.CheckResults) != 1 || !resumed.State.CheckResults[0].Passed() || resumed.State.CheckedSnapshot == "" {
			t.Fatal("senior received stale interrupted evidence")
		}
		return decisionJSON(true, nil, []string{command}), nil
	}
	if err := resumed.Run(context.Background()); err != nil || resumed.State.Status != "complete" || reviews != 1 {
		t.Fatalf("fresh verification/review failed: %v reviews=%d state=%s", err, reviews, resumed.State.Status)
	}
}

func TestExecutionFailureCannotPassAcceptanceOrCompletion(t *testing.T) {
	for _, kind := range []string{"cancelled", "timeout", "execution", "exit"} {
		check := CheckResult{Command: "go test ./...", ExecutionEvidence: ExecutionEvidence{FailureKind: kind}}
		state := State{Version: stateVersion, CheckedSnapshot: "snapshot", RequiredChecks: []string{check.Command}, SuccessCriteria: []string{"Works"}, Acceptance: []AcceptanceCheck{{Criterion: "Works", Checks: []string{check.Command}}}, CheckResults: []CheckResult{check}}
		if check.Passed() || CompletionAllowed(state, Decision{Done: true}) || acceptancePassed(state, Decision{}) {
			t.Fatalf("failure metadata allowed success: %s", kind)
		}
		config := DefaultProjectConfig()
		config.Hooks = []ProjectHook{{Name: "audit", Event: "after_check", Command: check.Command}}
		if hooksPassed(config, []HookResult{{Name: "audit", Event: "after_check", Command: check.Command, ExecutionEvidence: check.ExecutionEvidence}}) {
			t.Fatal("failed hook passed")
		}
	}
}

func TestInterruptedHookRetainsExecutedEvidence(t *testing.T) {
	r, _ := fixtureRunner(t)
	t.Setenv("BUILDHEADWAY_EXECUTION_HELPER", "1")
	config := DefaultProjectConfig()
	config.Checks = []string{executionCommand(t, "fail")}
	config.Hooks = []ProjectHook{{Name: "audit", Event: "before_check", Command: executionCommand(t, "wait")}}
	if err := AtomicJSON(filepath.Join(r.Repo, ".buildheadway.json"), config); err != nil {
		t.Fatal(err)
	}
	ctx, ready := cancelWhenExecutionStarts(t, r.Repo)
	report, err := CheckProject(ctx, r.Paths, r.Repo, noEmit)
	if readyErr := <-ready; readyErr != nil {
		t.Fatal(readyErr)
	}
	if !errors.Is(err, context.Canceled) || len(report.Hooks) != 1 || len(report.Results) != 0 {
		t.Fatalf("interrupted hook lost or check ran after cancellation: %+v %v", report, err)
	}
	hook := report.Hooks[0]
	if hook.Passed() || hook.ExitCode != 130 || hook.FailureKind != "cancelled" || hook.DurationMillis <= 0 || !strings.Contains(hook.Output, "partial verification diagnostics") {
		t.Fatalf("bad hook evidence: %+v", hook)
	}
	var saved VerificationReport
	if err := ReadJSON(filepath.Join(r.Repo, ".forge", "last-check.json"), &saved); err != nil || len(saved.Hooks) != 1 || saved.Hooks[0].FailureKind != "cancelled" {
		t.Fatalf("hook metadata not saved: %+v %v", saved, err)
	}
}

func TestCommandTimeoutDoesNotImpersonateRunDeadline(t *testing.T) {
	r, _ := fixtureRunner(t)
	t.Setenv("BUILDHEADWAY_EXECUTION_HELPER", "1")
	result, err := RunProcess(context.Background(), ShellCommand(executionCommand(t, "wait")), r.Repo, filepath.Join(r.Dir, "timeout.log"), "", ToolEnv(r.Paths), 50*time.Millisecond, nil)
	var timedOut commandTimeoutError
	if !errors.As(err, &timedOut) || errors.Is(err, context.DeadlineExceeded) || result.Code != 124 {
		t.Fatalf("per-command timeout is indistinguishable from a run deadline: result=%+v error=%v", result, err)
	}
	result, evidence := verificationEvidence(context.Background(), result, err, time.Now())
	if evidence.FailureKind != "timeout" || result.Code != 124 {
		t.Fatalf("lost command timeout classification: %+v %+v", result, evidence)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	result, err = RunProcess(ctx, []string{"never-dispatched"}, r.Repo, filepath.Join(r.Dir, "not-started.log"), "", nil, time.Minute, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("upstream deadline identity lost")
	}
}
