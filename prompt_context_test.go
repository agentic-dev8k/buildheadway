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
	"testing"
	"time"
)

func TestPromptAdmissionPrecedesDiscoveryAndCounters(t *testing.T) {
	for _, backend := range []string{"codex", "claude", "opencode", "pi", "cline", "gemini", "demo"} {
		t.Run(backend, func(t *testing.T) {
			r, _ := fixtureRunner(t)
			r.MaxPromptBytes = minMaxPromptBytes
			r.Discover = func(context.Context) ([]string, error) {
				t.Fatal("discovered models for rejected context")
				return nil, nil
			}
			r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
				t.Fatal("called model for rejected context")
				return "", nil
			}
			prompt := strings.Repeat("界", minMaxPromptBytes)
			_, err := r.askDecision(context.Background(), false, Connection{Backend: backend, Model: "auto"}, prompt, "plan", "")
			var oversized promptLimitError
			var limit limitError
			if !errors.As(err, &oversized) || !errors.As(err, &limit) || !hardModelFailure(err) {
				t.Fatalf("not a hard pausing limit: %v", err)
			}
			if r.State.PlannerCalls != 0 || len(r.State.ModelHealth) != 0 || len(r.State.PromptUsage) != 1 || r.State.PromptUsage[0].Bytes != len(prompt) {
				t.Fatal("accounted rejected prompt as a model attempt")
			}
		})
	}
}

func TestPromptWorkerAndPlannerRetryDoNotDiscoverForRejectedAttempt(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.MaxPromptBytes = minMaxPromptBytes
	discoveries, calls := 0, 0
	r.Discover = func(context.Context) ([]string, error) { discoveries++; return []string{"opencode/free"}, nil }
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		calls++
		return "", errors.New("unusable response " + strings.Repeat("x", 1200))
	}
	_, err := r.askDecision(context.Background(), false, Connection{Backend: "opencode", Model: "auto"}, strings.Repeat("x", minMaxPromptBytes-50), "plan", "")
	var oversized promptLimitError
	if !errors.As(err, &oversized) || calls != 1 || discoveries != 1 || r.State.PlannerCalls != 1 {
		t.Fatalf("retry escaped admission: %d %d %v", calls, discoveries, err)
	}
	for _, backend := range []string{"opencode", "demo"} {
		r, _ := fixtureRunner(t)
		r.MaxPromptBytes = minMaxPromptBytes
		r.Settings.Worker.Backend = backend
		r.Discover = func(context.Context) ([]string, error) {
			t.Fatal("worker discovery for rejected assignment")
			return nil, nil
		}
		r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
			t.Fatal("worker for rejected assignment")
			return "", nil
		}
		_, err := r.workOnce(context.Background(), Task{ID: "fix", Title: "Fix", Instructions: strings.Repeat("a", minMaxPromptBytes)}, "")
		if !errors.As(err, &oversized) || r.State.WorkerCalls != 0 || len(r.State.ModelHealth) != 0 {
			t.Fatal("worker admission did not precede accounting", err)
		}
	}
}

func TestPromptByteBoundariesHistoryAndReadOnlyInspection(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.MaxPromptBytes = minMaxPromptBytes
	if err := r.admitPrompt(strings.Repeat("界", 5461)+"x", "plan"); err != nil {
		t.Fatal("exact UTF-8 byte boundary refused", err)
	}
	if err := r.admitPrompt(strings.Repeat("界", 5461)+"xx", "plan"); err == nil {
		t.Fatal("one extra byte allowed")
	}
	for i := 0; i < 25; i++ {
		if err := r.admitPrompt("small", fmt.Sprintf("plan-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.State.PromptUsage) != 20 || r.State.PromptUsage[0].Label != "plan-5" {
		t.Fatal("unbounded context history")
	}
	file := filepath.Join(r.Dir, "state.json")
	before, _ := os.ReadFile(file)
	value, text, err := InspectRun(r.Repo, r.State.RunID, "context")
	if err != nil || !strings.Contains(text, "not prove") {
		t.Fatal(err, text)
	}
	data, _ := json.Marshal(value)
	if strings.Contains(string(data), "small") || !strings.Contains(string(data), "configured_limit_bytes") {
		t.Fatal("unexpected text or schema", string(data))
	}
	after, _ := os.ReadFile(file)
	if string(before) != string(after) {
		t.Fatal("inspection rewrote checkpoint")
	}
	r.State.PromptUsage[0].Fits = false
	if validatePromptUsage(r.State.PromptUsage) == nil {
		t.Fatal("corrupt fits metadata accepted")
	}
	if validatePromptUsage([]PromptUsage{{At: time.Now().UTC().Format(time.RFC3339Nano), Label: "plan", Bytes: 1, LimitBytes: defaultMaxPromptBytes, Fits: true}}) != nil {
		t.Fatal("existing 0.28 measurement schema rejected")
	}
}

func TestPromptPauseKeepsGoalAndResumesThroughFreshChecks(t *testing.T) {
	r, calls := fixtureRunner(t)
	r.State.Goal = strings.Repeat("goal ", 5000)
	r.MaxPromptBytes = minMaxPromptBytes
	if err := r.Run(context.Background()); err == nil || r.State.Status != "paused" {
		t.Fatal("large prompt did not pause", err, r.State.Status)
	}
	goal := r.State.Goal
	if *calls != 0 || len(r.State.CheckResults) != 0 {
		t.Fatal("oversized goal executed work")
	}
	resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resumed.Invoke = r.Invoke
	resumed.Discover = r.Discover
	resumed.MaxPromptBytes = 1 << 20
	if err := resumed.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resumed.State.Goal != goal || resumed.State.Status != "complete" || len(resumed.State.CheckResults) == 0 || !resumed.State.CheckResults[0].Passed() || *calls == 0 {
		t.Fatal("resume lost goal or bypassed executed verification/review")
	}
}

func TestSharedAcceptanceEvidenceDoesNotMultiplyDiagnostics(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.State.CheckedSnapshot = strings.Repeat("a", 64)
	diagnostic := "unique early diagnosis " + strings.Repeat("界", 900) + " final diagnosis"
	for i := 0; i < 20; i++ {
		r.State.CheckResults = append(r.State.CheckResults, CheckResult{Command: fmt.Sprintf("go test ./pkg%d", i), Output: diagnostic})
	}
	for i := 0; i < 60; i++ {
		r.State.Acceptance = append(r.State.Acceptance, AcceptanceCheck{Criterion: fmt.Sprintf("criterion %d", i), Checks: []string{"go test ./pkg0"}})
	}
	before, _ := json.Marshal(r.State)
	prompt := r.plannerPrompt(true)
	if n := strings.Count(prompt, "unique early diagnosis"); n != 20 {
		t.Fatalf("shared diagnostics copied %d times", n)
	}
	if !strings.Contains(prompt, "state.json:check_results[0]") || !strings.Contains(prompt, "criterion 59") {
		t.Fatal("omitted requirement/reference")
	}
	after, _ := json.Marshal(r.State)
	if string(before) != string(after) {
		t.Fatal("prompt preparation changed evidence")
	}
}

func TestNativePromptLimitInspectionAndExplicitResume(t *testing.T) {
	binary := nativeTestBinary(t)
	paths := testPaths(t)
	repo := t.TempDir()
	settings := Settings{Planner: Connection{Backend: "demo"}, Worker: Connection{Backend: "demo"}}
	if err := SaveSettings(paths, settings); err != nil {
		t.Fatal(err)
	}
	config := DefaultProjectConfig()
	config.Checks = []string{"@buildheadway-demo-check"}
	config.MaxPromptBytes = minMaxPromptBytes
	if err := AtomicJSON(filepath.Join(repo, ".buildheadway.json"), config); err != nil {
		t.Fatal(err)
	}
	env := setEnv(os.Environ(), "BUILDHEADWAY_HOME", filepath.Dir(paths.Config))
	goal := strings.Repeat("retain full goal ", 1200)
	cmd := exec.Command(binary, "plan", "--project", repo, goal)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "prompt has") {
		t.Fatalf("native goal did not pause %v %s", err, out)
	}
	state, dir, err := LoadRun(repo, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if state.Goal != goal || state.Status != "paused" || state.PlannerCalls != 0 || len(state.ModelHealth) != 0 {
		t.Fatal("rejected native goal lost data or invoked model")
	}
	before, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	cmd = exec.Command(binary, "context", "--project", repo, "--json")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	var report ContextReport
	if err := json.Unmarshal(out, &report); err != nil || len(report.Recent) != 1 || report.Recent[0].Fits {
		t.Fatal("lost rejected prompt metadata", err, string(out))
	}
	after, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if string(before) != string(after) {
		t.Fatal("native context rewrote evidence")
	}
	cmd = exec.Command(binary, "resume", "--project", repo, "--max-prompt-bytes", "1048576", state.RunID)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native explicit resume failed %v %s", err, out)
	}
	state, _, err = LoadRun(repo, state.RunID)
	if err != nil || state.Status != "complete" || len(state.CheckResults) != 1 || !state.CheckResults[0].Passed() || state.PlannerCalls == 0 || state.Config.MaxPromptBytes != minMaxPromptBytes {
		t.Fatalf("native resume bypassed pinned config/verification %v %+v", err, state)
	}
}
