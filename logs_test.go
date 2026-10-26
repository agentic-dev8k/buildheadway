package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerificationLogsKeepFullEvidenceAndMarkExcerpts(t *testing.T) {
	r, _ := fixtureRunner(t)
	payload := filepath.Join(t.TempDir(), "payload")
	data := "EARLY_DIAGNOSTIC\n" + strings.Repeat("x", int(processOutputLimit)+100) + "\nFINAL_DIAGNOSTIC\n"
	if err := os.WriteFile(payload, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUILDHEADWAY_TEST_ADAPTER_CAPTURE", filepath.Join(t.TempDir(), "capture"))
	t.Setenv("BUILDHEADWAY_TEST_ADAPTER_OUTPUT_FILE", payload)
	command := `"` + binary + `"`
	var check CheckResult
	if err := runVerification(context.Background(), r.Paths, r.Repo, r.Dir, []string{command}, 10*time.Second, false, noEmit, nil, func(c CheckResult) error { check = c; return nil }); err != nil {
		t.Fatal(err)
	}
	if check.ExitCode != 0 || !check.OutputTruncated || len(check.Output) != 7000 || !validLogName(check.Log) {
		t.Fatalf("missing check evidence: %+v", check)
	}
	var hook HookResult
	config := DefaultProjectConfig()
	config.Hooks = []ProjectHook{{Name: "audit", Event: "after_check", Command: command}}
	if err := runHooks(context.Background(), r.Paths, r.Repo, r.Dir, config, HookInput{Event: "after_check"}, noEmit, nil, func(h HookResult) error { hook = h; return nil }); err != nil {
		t.Fatal(err)
	}
	if hook.ExitCode != 0 || !hook.OutputTruncated || len(hook.Output) != 7000 || !validLogName(hook.Log) {
		t.Fatalf("missing hook evidence: %+v", hook)
	}
	for _, name := range []string{check.Log, hook.Log} {
		full, err := os.ReadFile(filepath.Join(r.Dir, name))
		if err != nil || string(full) != data {
			t.Fatalf("disk evidence lost: %v", err)
		}
		tail, err := ReadLogs(r.Repo, r.State.RunID, false, name, 64)
		if err != nil || !tail.Truncated || tail.Output != data[len(data)-64:] || tail.Offset+64 != int64(len(data)) {
			t.Fatalf("bad bounded read: %+v %v", tail, err)
		}
	}
	r.State.CheckResults, r.State.HookResults = []CheckResult{check}, []HookResult{hook}
	r.State.Acceptance = []AcceptanceCheck{{Criterion: "Behavior", Checks: []string{command}}}
	r.State.SuccessCriteria = []string{"Behavior"}
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil || saved.CheckResults[0].Log != check.Log || saved.HookResults[0].Log != hook.Log {
		t.Fatalf("references did not survive restart: %v", err)
	}
	evidence := AcceptanceResults(saved, Decision{})[0].Evidence[0]
	if evidence.Log != check.Log || !evidence.OutputTruncated || len(evidence.Output) != 1200 {
		t.Fatal("acceptance dropped the log reference or excerpt marker")
	}
	if !strings.Contains(MarkdownReport(saved), "Full log: "+check.Log) || !strings.Contains(MarkdownReport(saved), "earlier output omitted") {
		t.Fatal("export omitted full evidence location")
	}
	if !strings.Contains(r.plannerPrompt(true), check.Log) || !strings.Contains(r.plannerPrompt(true), "log_directory") {
		t.Fatal("senior cannot find full evidence")
	}
}

func TestLogInspectionIsReadOnlyAndSupportsLastCheck(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := os.MkdirAll(r.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	if err := AtomicJSON(filepath.Join(r.Repo, ".forge", "latest.json"), map[string]string{"run_id": r.State.RunID}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(r.Dir, "one.log"), []byte("complete log"), 0600)
	os.WriteFile(filepath.Join(r.Dir, "prompt.txt"), []byte("not a log"), 0600)
	before, _ := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	report, err := ReadLogs(r.Repo, "latest", false, "", 16)
	if err != nil || len(report.Files) != 1 || report.Files[0].Name != "one.log" {
		t.Fatalf("list: %+v %v", report, err)
	}
	content, err := ReadLogs(r.Repo, "latest", false, "one.log", 16)
	if err != nil || content.Truncated || content.Output != "complete log" {
		t.Fatalf("exact read: %+v %v", content, err)
	}
	after, _ := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	if string(before) != string(after) {
		t.Fatal("inspection rewrote the checkpoint")
	}
	checkDir := filepath.Join(r.Repo, ".forge", "checks", "check-example")
	os.MkdirAll(checkDir, 0700)
	os.WriteFile(filepath.Join(checkDir, "check.log"), []byte("standalone"), 0600)
	AtomicJSON(filepath.Join(r.Repo, ".forge", "last-check.json"), VerificationReport{ID: "check-example"})
	content, err = ReadLogs(r.Repo, "latest", true, "check.log", 10)
	if err != nil || content.ID != "check-example" || content.Output != "standalone" || content.Truncated {
		t.Fatalf("standalone logs: %+v %v", content, err)
	}
}

func TestLogsRejectTraversalLinksAndInvalidBounds(t *testing.T) {
	r, _ := fixtureRunner(t)
	os.MkdirAll(r.Dir, 0700)
	for _, name := range []string{"../state.json", "../secret.log", `..\secret.log`, "/tmp/secret.log", "C:secret.log", ".hidden.log", "plain.txt", "bad\n.log"} {
		if _, err := ReadLogs(r.Repo, r.State.RunID, false, name, 100); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	for _, limit := range []int{-1, 0, maxLogRead + 1} {
		if _, err := ReadLogs(r.Repo, r.State.RunID, false, "", limit); err == nil {
			t.Fatalf("accepted bound %d", limit)
		}
	}
	if _, err := ReadLogs(r.Repo, "../outside", false, "", 100); err == nil {
		t.Fatal("accepted invalid run ID")
	}
	secret := filepath.Join(t.TempDir(), "secret.log")
	os.WriteFile(secret, []byte("secret"), 0600)
	if err := os.Symlink(secret, filepath.Join(r.Dir, "escape.log")); err != nil {
		t.Logf("symlinks unavailable: %v", err)
		return
	}
	if _, err := ReadLogs(r.Repo, r.State.RunID, false, "escape.log", 100); err == nil {
		t.Fatal("read symlink outside run")
	}
	report, err := ReadLogs(r.Repo, r.State.RunID, false, "", 100)
	if err != nil || len(report.Files) != 0 {
		t.Fatalf("listed symlink: %+v %v", report, err)
	}
	os.Symlink(filepath.Dir(secret), filepath.Join(r.Repo, ".forge", "runs", "linked"))
	if _, err := ReadLogs(r.Repo, "linked", false, "secret.log", 100); err == nil {
		t.Fatal("escaped project through run directory")
	}
}

func TestLegacyCheckpointRetainsEvidenceWithoutInventedLogs(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.State.Version = 10
	r.State.CheckResults = []CheckResult{{Command: "go test ./...", ExitCode: 1, Output: "failure"}}
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil || saved.Version != stateVersion || saved.CheckResults[0].Log != "" || saved.CheckResults[0].Output != "failure" {
		t.Fatalf("migration changed evidence: %+v %v", saved, err)
	}
	for _, bad := range []string{"../secret.log", "bad\n.log"} {
		saved.CheckResults[0].Log = bad
		saved.Save(r.Dir)
		if _, _, err := LoadRun(r.Repo, r.State.RunID); err == nil {
			t.Fatal("accepted invalid persisted log reference")
		}
	}
}

func TestBriefEvidenceDoesNotMutateSavedResults(t *testing.T) {
	output := strings.Repeat("a", 3000)
	checks := []CheckResult{{Command: "go test", Output: output, Log: "one.log"}}
	hooks := []HookResult{{Name: "audit", Output: output, Log: "two.log"}}
	if c := briefChecks(checks)[0]; !c.OutputTruncated || len(c.Output) != 2500 || c.Log != "one.log" {
		t.Fatal("check excerpt metadata lost")
	}
	if h := briefHooks(hooks)[0]; !h.OutputTruncated || len(h.Output) != 2500 || h.Log != "two.log" {
		t.Fatal("hook excerpt metadata lost")
	}
	if checks[0].OutputTruncated || hooks[0].OutputTruncated || checks[0].Output != output || hooks[0].Output != output {
		t.Fatal("prompt preparation mutated saved evidence")
	}
	encoded, _ := json.Marshal(checks[0])
	if !strings.Contains(string(encoded), `"log":"one.log"`) {
		t.Fatal("JSON evidence lost reference")
	}
}

func TestProjectEvidenceBoundsLargeGitDiff(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git is not installed")
	}
	repo := t.TempDir()
	for _, args := range [][]string{{"init", repo}, {"-C", repo, "config", "core.autocrlf", "false"}} {
		if output, err := exec.Command(git, args...).CombinedOutput(); err != nil {
			t.Fatalf("git setup: %s %v", output, err)
		}
	}
	path := filepath.Join(repo, "large.txt")
	os.WriteFile(path, []byte("baseline\n"), 0600)
	if output, err := exec.Command(git, "-C", repo, "add", "large.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %s %v", output, err)
	}
	os.WriteFile(path, []byte(strings.Repeat("changed line\n", 100000)), 0600)
	evidence := ProjectEvidence(repo, testPaths(t))
	if len(evidence) > 50000 || !strings.Contains(evidence, "Git evidence truncated") || !strings.Contains(evidence, "large.txt") {
		t.Fatalf("unexpected diff evidence length %d", len(evidence))
	}
}

func TestLogListingLimitStillAllowsSpecificLog(t *testing.T) {
	r, _ := fixtureRunner(t)
	os.MkdirAll(r.Dir, 0700)
	for i := 0; i < 1001; i++ {
		if err := os.WriteFile(filepath.Join(r.Dir, itoa(i)+".log"), []byte("evidence"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ReadLogs(r.Repo, r.State.RunID, false, "", 100); err == nil {
		t.Fatal("unbounded log listing")
	}
	if report, err := ReadLogs(r.Repo, r.State.RunID, false, "1000.log", 100); err != nil || report.Output != "evidence" {
		t.Fatalf("cannot inspect long-run evidence: %+v %v", report, err)
	}
}

func TestVerificationDisplayMarksItsOwnShorterExcerpt(t *testing.T) {
	report := VerificationReport{Results: []CheckResult{{Command: "go test", ExitCode: 1, Output: strings.Repeat("x", 2000), Log: "test.log"}}}
	if !strings.Contains(FormatVerification(report), "earlier output omitted") {
		t.Fatal("display silently truncated stored output")
	}
}
