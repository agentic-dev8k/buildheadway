package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTaskEvidenceRetainsDiagnosisAndFullSavedRecord(t *testing.T) {
	r, _ := fixtureRunner(t)
	large := "EARLY_DIAGNOSIS " + strings.Repeat("界", 8000) + " FINAL_HANDOFF"
	r.State.TaskHistory = []TaskRecord{{Key: "1:0", Cycle: 1, Status: "interrupted", Attempts: 2, Model: "pinned", Task: Task{ID: "fix", Title: "Repair", Instructions: large}, Report: large, Error: large}}
	before, _ := json.Marshal(r.State.TaskHistory)
	projected, _ := json.Marshal(recentTasks(r.State.TaskHistory, 12))
	var rows []map[string]any
	if err := json.Unmarshal(projected, &rows); err != nil {
		t.Fatal(err)
	}
	row := rows[0]
	if row["source"] != "state.json:task_history[0]" || row["report_truncated"] != true || row["error_truncated"] != true || row["instructions_truncated"] != true {
		t.Fatalf("missing truncation/reference: %s", projected)
	}
	for _, s := range []string{row["report"].(string), row["error"].(string), row["task"].(map[string]any)["instructions"].(string)} {
		if !utf8.ValidString(s) || !strings.Contains(s, "EARLY_DIAGNOSIS") || !strings.Contains(s, "FINAL_HANDOFF") {
			t.Fatal("lost diagnosis or invalid UTF-8")
		}
	}
	if len(projected) > 7000 || row["status"] != "interrupted" || row["attempts"] != float64(2) || row["model"] != "pinned" {
		t.Fatal("unbounded or lost task state")
	}
	after, _ := json.Marshal(r.State.TaskHistory)
	if string(before) != string(after) {
		t.Fatal("mutated durable ledger")
	}
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.Dir, "state.json")
	saved, _ := os.ReadFile(path)
	if !strings.Contains(string(saved), large) {
		t.Fatal("saved evidence was truncated")
	}
	prompt := r.plannerPrompt(true)
	if !strings.Contains(prompt, "state.json:task_history[0]") || strings.Contains(prompt, strings.Repeat("界", 1000)) {
		t.Fatal("planner did not use bounded task evidence")
	}
	r.Settings.Worker.Backend = "demo"
	r.Invoke = func(_ context.Context, _ Connection, prompt, _ string, _ bool, _ string) (string, error) {
		if !strings.Contains(prompt, "state.json:task_history[0]") || !strings.Contains(prompt, "EARLY_DIAGNOSIS") || strings.Contains(prompt, strings.Repeat("界", 1000)) {
			t.Fatal("worker did not receive bounded, recoverable task evidence")
		}
		return "Worker inspected prior failure", nil
	}
	if _, err := r.workOnce(context.Background(), Task{ID: "next", Title: "Next", Instructions: "Inspect previous task"}, ""); err != nil {
		t.Fatal(err)
	}
	beforeInspection, _ := os.ReadFile(path)
	item, formatted, err := InspectTask(r.Repo, r.State.RunID, "1:0")
	if err != nil || item.Report != large || item.Task.Instructions != large || item.Error != large || !strings.Contains(formatted, large) {
		t.Fatalf("full task unavailable: %v", err)
	}
	if _, _, err := InspectTask(r.Repo, r.State.RunID, "../state.json"); err == nil {
		t.Fatal("unknown key accepted")
	}
	afterInspection, _ := os.ReadFile(path)
	if string(beforeInspection) != string(afterInspection) {
		t.Fatal("inspection rewrote checkpoint")
	}
}

func TestTaskEvidenceWindowsAndSmallValues(t *testing.T) {
	records := []TaskRecord{{Key: "1:0", Report: "one"}, {Key: "2:0", Report: "two"}}
	for _, count := range []int{-1, 0} {
		if len(recentTasks(records, count)) != 0 {
			t.Fatal("invalid window returned tasks")
		}
	}
	rows := recentTasks(records, 1)
	data, _ := json.Marshal(rows)
	if len(rows) != 1 || rows[0].Report != "two" || !strings.Contains(string(data), "state.json:task_history[1]") || strings.Contains(string(data), "truncated") {
		t.Fatal("small evidence or index changed")
	}
}
