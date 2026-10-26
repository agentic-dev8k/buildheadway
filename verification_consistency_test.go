package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The child performs real source edits and records which commands actually ran.
func TestVerificationBoundarySubprocess(t *testing.T) {
	if os.Getenv("BUILDHEADWAY_BOUNDARY_HELPER") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if err := os.MkdirAll(".forge", 0700); err != nil {
		t.Fatal(err)
	}
	trace, err := os.OpenFile(filepath.Join(".forge", "boundary.trace"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(trace, mode)
	trace.Close()
	path := "boundary-source.txt"
	switch mode {
	case "mutate", "mutate-fail":
		if err := os.WriteFile(path, []byte("changed\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if mode == "mutate-fail" {
			os.Exit(7)
		}
	case "restore":
		if err := os.WriteFile(path, []byte("original\n"), 0600); err != nil {
			t.Fatal(err)
		}
	case "observe":
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "changed\n" {
			t.Fatalf("formatter output missing: %q %v", data, err)
		}
	case "toggle":
		if _, err := os.Stat(filepath.Join(".forge", "allow-stable")); err == nil {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		next := "original\n"
		if string(data) == next {
			next = "changed\n"
		}
		if err := os.WriteFile(path, []byte(next), 0600); err != nil {
			t.Fatal(err)
		}
	case "read":
		if _, err := os.ReadFile(path); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown boundary helper %q", mode)
	}
}
func boundaryCommand(t *testing.T, mode string) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quoted := ShellQuote(path)
	if runtime.GOOS == "windows" {
		quoted = `"` + path + `"`
	}
	return quoted + " -test.run=^TestVerificationBoundarySubprocess$ -- " + mode
}
func boundaryFixture(t *testing.T) *Runner {
	t.Helper()
	t.Setenv("BUILDHEADWAY_BOUNDARY_HELPER", "1")
	r, _ := fixtureRunner(t)
	writeFixture(t, filepath.Join(r.Repo, "boundary-source.txt"), "original\n")
	return r
}
func TestOneCheckCannotHideSourceChangesBehindALaterRestore(t *testing.T) {
	r := boundaryFixture(t)
	var records []CheckResult
	snapshot, err := verificationRound(context.Background(), r.Paths, r.Repo, r.Dir, r.State.Config, HookInput{}, []string{boundaryCommand(t, "mutate"), boundaryCommand(t, "restore")}, false, noEmit, nil, func(HookResult) error { return nil }, func(c CheckResult) error { records = append(records, c); return nil })
	if !errors.Is(err, errHookChangedFiles) || snapshot != "" {
		t.Fatalf("accepted mixed source versions: snapshot=%q err=%v", snapshot, err)
	}
	trace, _ := os.ReadFile(filepath.Join(r.Repo, ".forge", "boundary.trace"))
	if strings.Contains(string(trace), "restore") || len(records) != 1 || records[0].ExitCode != 0 || !records[0].SourceChanged {
		t.Fatalf("did not stop after source edit: trace=%s records=%+v", trace, records)
	}
}
func TestAfterHookCannotHideChangesBehindALaterRestore(t *testing.T) {
	r := boundaryFixture(t)
	config := r.State.Config
	config.Hooks = []ProjectHook{{Name: "edit", Event: "after_check", Command: boundaryCommand(t, "mutate")}, {Name: "restore", Event: "after_check", Command: boundaryCommand(t, "restore")}}
	var hooks []HookResult
	snapshot, err := verificationRound(context.Background(), r.Paths, r.Repo, r.Dir, config, HookInput{}, []string{boundaryCommand(t, "read")}, false, noEmit, nil, func(h HookResult) error { hooks = append(hooks, h); return nil }, func(CheckResult) error { return nil })
	if !errors.Is(err, errHookChangedFiles) || snapshot != "" {
		t.Fatalf("accepted hook-restored stale evidence: snapshot=%q err=%v", snapshot, err)
	}
	if len(hooks) != 1 || hooks[0].Name != "edit" || !hooks[0].SourceChanged {
		t.Fatalf("later hook ran after invalidation: %+v", hooks)
	}
}

func TestConcurrentEditBetweenChecksStopsBeforeNextCommand(t *testing.T) {
	r := boundaryFixture(t)
	records := 0
	snapshot, err := verificationRound(context.Background(), r.Paths, r.Repo, r.Dir, r.State.Config, HookInput{}, []string{boundaryCommand(t, "read"), boundaryCommand(t, "restore")}, false, noEmit, nil, func(HookResult) error { return nil }, func(c CheckResult) error {
		records++
		return os.WriteFile(filepath.Join(r.Repo, "boundary-source.txt"), []byte("concurrent edit\n"), 0600)
	})
	if !errors.Is(err, errHookChangedFiles) || snapshot != "" || records != 1 {
		t.Fatalf("dispatched against changed source: %q %v %d", snapshot, err, records)
	}
	trace, _ := os.ReadFile(filepath.Join(r.Repo, ".forge", "boundary.trace"))
	if strings.Contains(string(trace), "restore") {
		t.Fatal("a later check restored source and hid an intervening edit")
	}
}

func TestBeforeCheckFormattersEstablishTheVerificationBaseline(t *testing.T) {
	r := boundaryFixture(t)
	config := r.State.Config
	config.Hooks = []ProjectHook{{Name: "format", Event: "before_check", Command: boundaryCommand(t, "mutate")}, {Name: "audit", Event: "after_check", Command: boundaryCommand(t, "observe")}}
	var checks []CheckResult
	var hooks []HookResult
	snapshot, err := verificationRound(context.Background(), r.Paths, r.Repo, r.Dir, config, HookInput{}, []string{boundaryCommand(t, "observe")}, false, noEmit, nil, func(h HookResult) error { hooks = append(hooks, h); return nil }, func(c CheckResult) error { checks = append(checks, c); return nil })
	actual, snapErr := WorkspaceSnapshot(context.Background(), r.Repo)
	if err != nil || snapErr != nil || snapshot != actual || len(checks) != 1 || len(hooks) != 2 {
		t.Fatalf("rejected intended formatter: %q %v %v", snapshot, err, snapErr)
	}
	if checks[0].SourceChanged || hooks[0].SourceChanged || hooks[1].SourceChanged || !hooksPassed(config, hooks) {
		t.Fatal("formatter was classified as stale verification")
	}
}

func TestChangingChecksPauseBeforeSeniorReviewAndResumeAfterStabilizing(t *testing.T) {
	r := boundaryFixture(t)
	r.State.RequiredChecks = []string{boundaryCommand(t, "toggle")}
	r.State.Phase = "checking"
	calls := 0
	r.State.SuccessCriteria = []string{"Source stays stable"}
	r.State.Acceptance = []AcceptanceCheck{{Criterion: "Source stays stable", Checks: r.State.RequiredChecks}}
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		calls++
		data, err := json.Marshal(Decision{Done: true, Summary: "Reviewed unchanged source and executed checks", Tasks: []Task{}, Checks: r.State.RequiredChecks, SuccessCriteria: r.State.SuccessCriteria, Acceptance: r.State.Acceptance})
		return string(data), err
	}
	err := r.Run(context.Background())
	var limit limitError
	if !errors.As(err, &limit) || r.State.Status != "paused" || calls != 0 || r.State.CheckedSnapshot != "" {
		t.Fatalf("unstable checks reached review: %v %s %d", err, r.State.Status, calls)
	}
	if len(r.State.CheckResults) != 1 || r.State.CheckResults[0].ExitCode != 0 || !r.State.CheckResults[0].SourceChanged || !validLogName(r.State.CheckResults[0].Log) {
		t.Fatal("invalidated command lost its real result or log")
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil || !saved.CheckResults[0].SourceChanged {
		t.Fatalf("invalidation lost on restart: %v", err)
	}
	if !strings.Contains(FormatStatus(saved), "verification evidence is invalid") || !strings.Contains(MarkdownReport(saved), "verification evidence is invalid") {
		t.Fatal("inspection hid the invalidated check")
	}
	trace, _ := os.ReadFile(filepath.Join(r.Repo, ".forge", "boundary.trace"))
	if strings.Count(string(trace), "toggle") != 3 {
		t.Fatalf("unstable round limit lost: %s", trace)
	}
	if err := os.WriteFile(filepath.Join(r.Repo, ".forge", "allow-stable"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resumed.Invoke = r.Invoke
	if err := resumed.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resumed.State.Status != "complete" || calls != 1 || resumed.State.CheckResults[0].SourceChanged || resumed.State.CheckedSnapshot == "" {
		t.Fatalf("did not obtain fresh checks and review: %s %d", resumed.State.Status, calls)
	}
}

func TestSourceChangingEvidenceCannotSatisfyAcceptanceOrHookGates(t *testing.T) {
	state := State{Version: stateVersion, CheckedSnapshot: "snapshot", RequiredChecks: []string{"go test ./..."}, SuccessCriteria: []string{"Works"}, Acceptance: []AcceptanceCheck{{Criterion: "Works", Checks: []string{"go test ./..."}}}, CheckResults: []CheckResult{{Command: "go test ./...", ExitCode: 0, SourceChanged: true, Log: "check.log"}}}
	if CompletionAllowed(state, Decision{Done: true}) || AcceptanceResults(state, Decision{})[0].Status != "unverified" {
		t.Fatal("invalidated passing exit code approved completion")
	}
	evidence := AcceptanceResults(state, Decision{})[0].Evidence[0]
	if !evidence.SourceChanged || evidence.Log != "check.log" {
		t.Fatal("acceptance stripped invalidation evidence")
	}
	config := DefaultProjectConfig()
	config.Hooks = []ProjectHook{{Name: "audit", Event: "after_check", Command: "go vet ./..."}}
	if hooksPassed(config, []HookResult{{Name: "audit", Event: "after_check", Command: "go vet ./...", ExitCode: 0, SourceChanged: true}}) {
		t.Fatal("source-changing hook passed its gate")
	}
}

func TestFailedSourceChangingCheckKeepsActualExitCode(t *testing.T) {
	r := boundaryFixture(t)
	var records []CheckResult
	_, err := verificationRound(context.Background(), r.Paths, r.Repo, r.Dir, r.State.Config, HookInput{}, []string{boundaryCommand(t, "mutate-fail")}, false, noEmit, nil, func(HookResult) error { return nil }, func(c CheckResult) error { records = append(records, c); return nil })
	if !errors.Is(err, errHookChangedFiles) || len(records) != 1 || records[0].ExitCode != 7 || !records[0].SourceChanged {
		t.Fatalf("source invalidation hid command failure: %+v %v", records, err)
	}
}

func TestLegacyVerificationCheckpointMigratesWithoutInventedInvalidation(t *testing.T) {
	r := boundaryFixture(t)
	r.State.Version = 11
	r.State.CheckResults = []CheckResult{{Command: "go test ./...", ExitCode: 0, Output: "historical output", Log: "check.log"}}
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil || saved.Version != stateVersion || saved.CheckResults[0].SourceChanged || saved.CheckResults[0].Output != "historical output" {
		t.Fatalf("legacy evidence changed: %+v %v", saved, err)
	}
}
