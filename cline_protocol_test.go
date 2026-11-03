package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Cline 3 emits a terminal run_result rather than the legacy agent_event.text.
func TestClineRunResultProtocol(t *testing.T) {
	payload := decisionJSON(true, nil, []string{"npm test"})
	event, _ := json.Marshal(map[string]any{"type": "run_result", "finishReason": "completed", "text": payload})
	for _, prefix := range []string{"", "{\"type\":\"agent_event\",\"event\":{\"type\":\"iteration_start\"}}\n"} {
		d, err := ParseDecision(prefix + string(event))
		if err != nil || !d.Done {
			t.Fatalf("current Cline result was lost: %#v %v", d, err)
		}
	}
	// A failed terminal event must override an earlier plausible completion.
	for _, failure := range []string{
		`{"type":"run_result","finishReason":"error","text":"Unauthorized: re-authenticate your Cline account"}`,
		`{"type":"error","message":"Unauthorized: re-authenticate your Cline account"}`,
		`{"type":"error","error":{"name":"APIError","data":{"message":"Unauthorized: re-authenticate your Cline account"}}}`,
	} {
		_, err := ParseDecision(string(event) + "\n" + failure)
		if err == nil || modelFailureKind(err, true) != "auth" || !strings.Contains(err.Error(), "re-authenticate") {
			t.Fatalf("terminal authentication blocker was lost: %v", err)
		}
	}
	for _, reason := range []string{"aborted", "error", "unknown", ""} {
		failed, _ := json.Marshal(map[string]any{"type": "run_result", "finishReason": reason, "text": payload})
		if _, err := ParseDecision(string(failed)); err == nil {
			t.Fatalf("accepted unfinished Cline result: %s", reason)
		}
	}
}

func TestClineNestedErrorRetainsProviderMessage(t *testing.T) {
	_, err := ResponseTexts(`{"type":"agent_event","event":{"type":"error","error":{"name":"Error","message":"Unauthorized: re-authenticate your Cline account","stack":"private diagnostic stack"},"errorClass":"auth","recoverable":false}}`)
	if err == nil || modelFailureKind(err, true) != "auth" || strings.Contains(err.Error(), "private diagnostic stack") {
		t.Fatalf("provider message must remain actionable without dumping the stack: %v", err)
	}
}

func TestFreeUsageLimitIsHardBlocker(t *testing.T) {
	err := fmt.Errorf("Free usage limit reached. Free models work in OpenCode; subscribe to keep going")
	if !hardModelFailure(err) || modelFailureKind(err, true) != "auth" {
		t.Fatalf("account-wide usage limit could trigger fallback: %v", err)
	}
}

func TestClineFinalResultsParticipateInRepairAndAcceptance(t *testing.T) {
	r, workers := fixtureRunner(t)
	r.Settings.Planner = Connection{Backend: "cline", Model: "explicit-model", Provider: "explicit-provider"}
	invoke := r.Invoke
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		reply, err := invoke(ctx, c, prompt, label, planner, model)
		if !planner || err != nil {
			return reply, err
		}
		if c.Backend != "cline" || c.Model != "explicit-model" || c.Provider != "explicit-provider" {
			t.Fatal("explicit senior connection was changed")
		}
		data, _ := json.Marshal(map[string]any{"type": "run_result", "finishReason": "completed", "text": reply})
		return string(data), nil
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.State.Status != "complete" || *workers != 2 || r.State.PlannerCalls < 3 || len(r.State.CheckResults) == 0 {
		t.Fatal("Cline result bypassed repair/check/review")
	}
	for _, check := range r.State.CheckResults {
		if check.ExitCode != 0 {
			t.Fatal("senior approved a failed check")
		}
	}
}
