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
	"time"
)

func browserFailureWrapper(t *testing.T, mode string) (Paths, string, ProjectConfig, string) {
	t.Helper()
	paths, repo, config := fixtureBrowser(t, mode)
	node := FindTool("node", paths)
	if node == "" {
		t.Skip("Node.js is required for the real zero-exit wrapper")
	}
	plan := config.BrowserChecks[0]
	config.BrowserChecks = nil
	if err := AtomicJSON(filepath.Join(repo, ".buildheadway.json"), config); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(repo, "browser-plan.json")
	if err := AtomicJSON(file, plan); err != nil {
		t.Fatal(err)
	}
	argv, _ := json.Marshal([]string{nativeTestBinary(t), "browser", "verify", "--project", repo, "--file", file})
	script := filepath.Join(repo, "wrapper.cjs")
	content := "const a=" + string(argv) + ";const r=require('child_process').spawnSync(a[0],a.slice(1),{stdio:'pipe'});if(r.error)throw r.error;process.exit(0);"
	if err := os.WriteFile(script, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUILDHEADWAY_HOME", filepath.Dir(paths.Data))
	return paths, repo, config, `"` + node + `" "` + script + `"`
}

func TestNestedBrowserFailureCannotPassThroughSuccessfulWrapper(t *testing.T) {
	paths, repo, config, command := browserFailureWrapper(t, "action-error")
	var results []CheckResult
	err := runVerificationConfigured(context.Background(), paths, repo, filepath.Join(repo, ".forge", "evidence"), config, []string{command}, 20*time.Second, false, noEmit, nil, func(c CheckResult) error { results = append(results, c); return nil })
	if err != nil || len(results) != 1 || results[0].ExitCode != 0 || results[0].Passed() {
		t.Fatalf("zero-exit wrapper hid a browser assertion failure: results=%#v error=%v", results, err)
	}
	check := results[0]
	if check.BrowserFailure == nil || check.BrowserFailure.Report == "" || check.FailureKind != "" || check.Error != "" || check.DurationMillis <= 0 || !validLogName(check.Log) {
		t.Fatal("lost wrapper outcome or child report", check)
	}
	if strings.Contains(check.BrowserFailure.Error, "private-error-token") {
		t.Fatal("provider output leaked")
	}
	state := State{Version: stateVersion, CheckedSnapshot: "verified-source", RequiredChecks: []string{command}, Acceptance: []AcceptanceCheck{{Criterion: "Browser flow", Checks: []string{command}}}, CheckResults: results}
	if AcceptanceResults(state, Decision{})[0].Status != "failed" || CompletionAllowed(state, Decision{Done: true}) {
		t.Fatal("failed child check established acceptance")
	}
	if !strings.Contains(FormatVerification(VerificationReport{Results: results}), "Nested browser check failed") || !strings.Contains(MarkdownReport(state), check.BrowserFailure.Report) {
		t.Fatal("report hid nested failure")
	}
	state.CheckResults = append([]CheckResult(nil), results...)
	state.CheckResults[0].BrowserFailure = nil
	if !CompletionAllowed(state, Decision{Done: true}) {
		t.Fatal("positive control must pass when only the nested failure is removed")
	}
}

func TestNestedBrowserHooksRetainRepairableFailuresAndHardBlockers(t *testing.T) {
	for _, mode := range []string{"action-error", "initial-error", ""} {
		t.Run(mode, func(t *testing.T) {
			paths, repo, config, command := browserFailureWrapper(t, mode)
			config.Hooks = []ProjectHook{{Name: "browser", Event: "after_check", Command: command}}
			var results []HookResult
			err := runHooks(context.Background(), paths, repo, filepath.Join(repo, ".forge", "hook-evidence"), config, HookInput{Event: "after_check"}, noEmit, nil, func(h HookResult) error { results = append(results, h); return nil })
			var setup mcpSetupError
			if len(results) != 1 || results[0].ExitCode != 0 || results[0].Passed() != (mode == "") || errors.As(err, &setup) != (mode == "initial-error") {
				t.Fatalf("incorrect hook outcome: %#v %v", results, err)
			}
			if mode == "action-error" && (err != nil || results[0].BrowserFailure == nil || hooksPassed(config, results)) {
				t.Fatal("ordinary browser failure became a blocker or passing hook", err)
			}
			if mode == "initial-error" && results[0].FailureKind != "execution" {
				t.Fatal("MCP setup blocker lost execution evidence")
			}
		})
	}
}

func TestNestedBrowserFailureRepairsAfterBudgetPauseAndFreshResume(t *testing.T) {
	paths, repo, config, command := browserFailureWrapper(t, "project-output")
	config.Checks = []string{command}
	if err := AtomicJSON(filepath.Join(repo, ".buildheadway.json"), config); err != nil {
		t.Fatal(err)
	}
	settings := Settings{Version: 3, Planner: Connection{Backend: "codex"}, Worker: Connection{Backend: "opencode", Model: "opencode/free"}}
	r, err := NewRunner(paths, settings, repo, "Create a working app and verify its browser flow", false, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	r.MaxCalls = 1
	discoveries, workers, reviews := 0, 0, 0
	r.Discover = func(context.Context) ([]string, error) { discoveries++; return []string{"opencode/free"}, nil }
	active := r
	r.Invoke = func(_ context.Context, _ Connection, _ string, _ string, planner bool, _ string) (string, error) {
		if !planner {
			workers++
			text := "bad\n"
			if workers == 2 {
				text = "good\n"
			}
			return "Changed app output", os.WriteFile(filepath.Join(repo, "result.txt"), []byte(text), 0600)
		}
		if workers == 0 {
			return decisionJSON(false, []Task{{ID: "implement", Title: "Implement", Instructions: "Create a working app"}}, []string{command}), nil
		}
		reviews++
		if active.State.CheckResults[0].Passed() {
			return decisionJSON(true, nil, []string{command}), nil
		}
		if active.State.CheckResults[0].BrowserFailure == nil {
			t.Fatal("senior did not receive nested failure")
		}
		return decisionJSON(false, []Task{{ID: "repair", Title: "Repair", Instructions: "Fix the failed browser assertion without changing the checks"}}, []string{command}), nil
	}
	var limit limitError
	if err = r.Run(context.Background()); !errors.As(err, &limit) || r.State.Status != "paused" || workers != 1 || reviews != 0 || r.State.CheckResults[0].BrowserFailure == nil {
		t.Fatal("budget pause lost child evidence", err)
	}
	oldCalls, _ := os.ReadFile(filepath.Join(repo, ".forge", "browser-calls"))
	resumed, err := ResumeRunner(paths, settings, repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	active = resumed
	resumed.MaxCalls = 10
	resumed.Discover = r.Discover
	invoke := r.Invoke
	resumed.Invoke = func(ctx context.Context, c Connection, p, l string, senior bool, m string) (string, error) {
		if senior {
			now, _ := os.ReadFile(filepath.Join(repo, ".forge", "browser-calls"))
			if len(now) <= len(oldCalls) {
				t.Fatal("resume reused old verification")
			}
		}
		return invoke(ctx, c, p, l, senior, m)
	}
	if err = resumed.Run(context.Background()); err != nil || resumed.State.Status != "complete" || workers != 2 || reviews != 2 || discoveries < 2 || resumed.Settings.Worker.Model != "opencode/free" {
		t.Fatalf("repair/resume failed: %v workers=%d reviews=%d prices=%d", err, workers, reviews, discoveries)
	}
	for _, c := range resumed.State.CheckResults {
		if !c.Passed() || c.BrowserFailure != nil {
			t.Fatal("failed child was not freshly verified", c)
		}
	}
}

func TestBrowserVerifyCLIUsageFailuresReachTheParentWithoutModelAccess(t *testing.T) {
	for _, args := range [][]string{{"browser", "verify", "--unknown-option"}, {"browser", "verify"}, {"browser", "verify", "--help"}, {"browser", "status", "--unknown-option"}} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			repo, path, nonce := failureHandoffFixture(t)
			cmd := exec.Command(nativeTestBinary(t), args...)
			cmd.Dir = repo
			cmd.Env = testCommandEnv(t.TempDir())
			err := cmd.Run()
			failure, readErr := readBrowserFailure(repo, path, nonce)
			wantFailure := args[1] == "verify" && args[len(args)-1] != "--help"
			if readErr != nil || (failure != nil) != wantFailure || wantFailure && err == nil || args[len(args)-1] == "--help" && err != nil {
				t.Fatalf("incorrect CLI failure handoff: %#v failure=%#v run=%v read=%v", args, failure, err, readErr)
			}
		})
	}
}

func TestNestedBrowserFailureFromAChildProjectReachesTheRoot(t *testing.T) {
	paths, repo, config, command := browserFailureWrapper(t, "action-error")
	child := filepath.Join(repo, "packages", "child")
	if err := AtomicJSON(filepath.Join(child, ".buildheadway.json"), config); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(repo, "wrapper.cjs")
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(repo)
	after, _ := json.Marshal(child)
	text := strings.Replace(string(data), `"--project",`+string(before), `"--project",`+string(after), 1)
	if text == string(data) {
		t.Fatal("fixture did not change child project")
	}
	if err := os.WriteFile(script, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	var results []CheckResult
	err = runVerificationConfigured(context.Background(), paths, repo, filepath.Join(repo, ".forge", "evidence"), config, []string{command}, 20*time.Second, false, noEmit, nil, func(c CheckResult) error { results = append(results, c); return nil })
	if err != nil || len(results) != 1 || results[0].BrowserFailure == nil || results[0].Passed() || !strings.HasPrefix(results[0].BrowserFailure.Report, "packages/child/.forge/browser-checks/") {
		t.Fatal("child project failure was lost", results, err)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(results[0].BrowserFailure.Report))); err != nil {
		t.Fatal("missing real child report", err)
	}
}
