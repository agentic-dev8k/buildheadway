package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestVerificationExcerptsKeepRootCauseAndMetadata(t *testing.T) {
	text := "EARLY_ROOT_CAUSE " + strings.Repeat("界", 10000) + " FINAL_FAILURE"
	checks := []CheckResult{{Command: "node test.cjs", Output: text, ExitCode: 7, Log: "check.log", ExecutionEvidence: ExecutionEvidence{FailureKind: "exit", DurationMillis: 123}, SourceChanged: true}}
	hooks := []HookResult{{Name: "after", Output: text, ExitCode: 7, Log: "hook.log", ExecutionEvidence: ExecutionEvidence{FailureKind: "exit", DurationMillis: 124}, SourceChanged: true}}
	beforeChecks, _ := json.Marshal(checks)
	beforeHooks, _ := json.Marshal(hooks)
	a := briefChecks(checks)[0]
	b := briefHooks(hooks)[0]
	for _, output := range []string{a.Output, b.Output} {
		if len(output) > 2500 || !utf8.ValidString(output) || !strings.Contains(output, "EARLY_ROOT_CAUSE") || !strings.Contains(output, "FINAL_FAILURE") {
			t.Fatal("lost root cause or broken excerpt")
		}
	}
	if !a.OutputTruncated || !b.OutputTruncated || a.ExitCode != 7 || b.ExitCode != 7 || a.Log != "check.log" || b.Log != "hook.log" || a.FailureKind != "exit" || b.FailureKind != "exit" || a.DurationMillis != 123 || b.DurationMillis != 124 || !a.SourceChanged || !b.SourceChanged {
		t.Fatal("lost execution evidence")
	}
	afterChecks, _ := json.Marshal(checks)
	afterHooks, _ := json.Marshal(hooks)
	if string(beforeChecks) != string(afterChecks) || string(beforeHooks) != string(afterHooks) {
		t.Fatal("rewrote saved evidence")
	}
}

func TestVerificationExcerptsReachAgentPrompts(t *testing.T) {
	r, _ := fixtureRunner(t)
	text := "EARLY_CHECK_DIAGNOSIS " + strings.Repeat("界", 10000) + " FINAL_CHECK_FAILURE"
	r.State.CheckResults = []CheckResult{{Command: "node test.cjs", ExitCode: 1, Output: text, Log: "check.log"}}
	r.State.HookResults = []HookResult{{Name: "audit", ExitCode: 1, Output: strings.ReplaceAll(text, "CHECK", "HOOK"), Log: "hook.log"}}
	verify := func(prompt string) {
		for _, value := range []string{"EARLY_CHECK_DIAGNOSIS", "FINAL_CHECK_FAILURE", "EARLY_HOOK_DIAGNOSIS", "FINAL_HOOK_FAILURE", "check.log", "hook.log"} {
			if !strings.Contains(prompt, value) {
				t.Fatalf("missing agent evidence %s", value)
			}
		}
		if strings.Contains(prompt, strings.Repeat("界", 1000)) {
			t.Fatal("unbounded verification evidence")
		}
	}
	verify(r.plannerPrompt(true))
	r.Settings.Worker.Backend = "demo"
	r.Invoke = func(_ context.Context, _ Connection, prompt, _ string, _ bool, _ string) (string, error) {
		verify(prompt)
		return "Inspected evidence", nil
	}
	if _, err := r.workOnce(context.Background(), Task{ID: "repair", Title: "Repair", Instructions: "Inspect failures"}, ""); err != nil {
		t.Fatal(err)
	}
	for _, original := range []string{r.State.CheckResults[0].Output, r.State.HookResults[0].Output} {
		if len(original) < 30000 {
			t.Fatal("durable output changed")
		}
	}
}
