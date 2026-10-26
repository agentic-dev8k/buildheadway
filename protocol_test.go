package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func decisionJSON(done bool, tasks []Task, checks []string) string {
	if tasks == nil {
		tasks = []Task{}
	}
	if checks == nil {
		checks = []string{}
	}
	coverage := checks
	if len(coverage) == 0 {
		coverage = []string{checkFixtureCommand()}
	}
	data, _ := json.Marshal(Decision{Acceptance: []AcceptanceCheck{{Criterion: "Goal works", Checks: coverage}}, Done: done, Summary: "Review result", Tasks: tasks, Checks: checks, SuccessCriteria: []string{"Goal works"}})
	return string(data)
}
func TestPlannerProtocols(t *testing.T) {
	payload := decisionJSON(true, nil, []string{"npm test"})
	var object any
	json.Unmarshal([]byte(payload), &object)
	events := map[string]any{
		"claude":   map[string]any{"type": "result", "is_error": false, "structured_output": object},
		"opencode": map[string]any{"type": "text", "part": map[string]any{"text": payload}},
		"pi":       map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": payload}}}},
		"cline":    map[string]any{"type": "agent_event", "event": map[string]any{"text": payload}},
		"codex":    map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": payload}},
	}
	for name, event := range events {
		t.Run(name, func(t *testing.T) {
			data, _ := json.Marshal(event)
			answer, err := ParseDecision("progress on stderr\n" + string(data))
			if err != nil || !answer.Done {
				t.Fatalf("protocol failed: %#v %v", answer, err)
			}
		})
	}
}
func TestDecisionRejectsMalformedCompletion(t *testing.T) {
	valid := decisionJSON(true, nil, []string{"npm test"})
	for _, bad := range []string{
		strings.Replace(valid, `"done":true`, `"done":null`, 1),
		strings.Replace(valid, `"tasks":[]`, `"tasks":null`, 1),
		strings.Replace(valid, `"done":true`, `"done":"true"`, 1),
		strings.Replace(valid, `"summary":"Review result"`, `"summary":null`, 1),
		strings.Replace(valid, `"checks":["npm test"]`, `"checks":[1]`, 1),
	} {
		if _, err := ParseDecision(bad); err == nil {
			t.Fatalf("accepted invalid decision %s", bad)
		}
	}
	if _, err := ParseDecision(decisionJSON(true, []Task{{"one", "Work", "Do work"}}, nil)); err == nil {
		t.Fatal("accepted completion with pending work")
	}
}

func TestPlannerDecisionInsideExplanationAndFences(t *testing.T) {
	task := Task{"one", "Fix arithmetic", `Use the expression a + b, keeping braces like {a,b} intact.`}
	payload := decisionJSON(false, []Task{task}, []string{"npm test"})
	fence := strings.Repeat("`", 3)
	response := "I inspected the actual files. Here is the decision:\n" + fence + "json\n" + payload + "\n" + fence + "\nThe worker can implement it now."
	answer, err := ParseDecision(response)
	if err != nil || len(answer.Tasks) != 1 || answer.Tasks[0] != task {
		t.Fatalf("wrapped decision was lost: %#v %v", answer, err)
	}
	// Prefer the final decision if an agent explains how it revised a draft.
	final := decisionJSON(true, nil, []string{"npm test"})
	answer, err = ParseDecision(payload + "\nRevised after review:\n" + final)
	if err != nil || !answer.Done {
		t.Fatalf("accepted stale draft instead of final decision: %#v %v", answer, err)
	}
}
func TestProtocolErrorsNeverBecomeSuccessfulReports(t *testing.T) {
	for _, raw := range []string{
		`{"type":"error","error":{"message":"429 rate limit"}}`,
		`{"type":"result","is_error":true,"result":"401 unauthorized"}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"model unavailable"}}`,
	} {
		if _, err := ResponseTexts(raw); err == nil {
			t.Fatalf("accepted error %s", raw)
		}
	}
}
func TestFreePricingRequiresExplicitAllZeroCharges(t *testing.T) {
	catalog := ""
	add := func(id string, cost map[string]any, tools bool) {
		meta, _ := json.Marshal(map[string]any{"cost": cost, "capabilities": map[string]any{"toolcall": tools}})
		catalog += id + "\n" + string(meta) + "\n"
	}
	add("opencode/free", map[string]any{"input": 0, "output": 0, "cache": map[string]any{"read": 0, "write": 0}}, true)
	add("opencode/paid", map[string]any{"input": 0.1, "output": 0}, true)
	add("opencode/unknown", map[string]any{"output": 0}, true)
	add("opencode/cache", map[string]any{"input": 0, "output": 0, "cache": map[string]any{"read": 1}}, true)
	add("opencode/long", map[string]any{"input": 0, "output": 0, "experimentalOver200K": map[string]any{"input": 2}}, true)
	add("opencode/no-tools", map[string]any{"input": 0, "output": 0}, false)
	models := ParseFreeModels(catalog)
	if len(models) != 1 || models[0] != "opencode/free" {
		t.Fatalf("bad price selection: %v", models)
	}
}
func TestCompletionGateRequiresExecutedPassingChecks(t *testing.T) {
	answer := Decision{Done: true, Checks: []string{"verify"}, Tasks: []Task{}}
	for _, state := range []State{
		{}, {RequiredChecks: []string{"verify"}},
		{RequiredChecks: []string{"verify"}, CheckResults: []CheckResult{{Command: "verify", ExitCode: 1, Output: "failed"}}},
		{RequiredChecks: []string{"verify"}, CheckResults: []CheckResult{{Command: "different", ExitCode: 0, Output: "passed"}}},
	} {
		if CompletionAllowed(state, answer) {
			t.Fatalf("incorrectly completed %#v", state)
		}
	}
	if !CompletionAllowed(State{RequiredChecks: []string{"verify"}, CheckResults: []CheckResult{{Command: "verify", ExitCode: 0, Output: "ok"}}}, answer) {
		t.Fatal("rejected verified completion")
	}
}
