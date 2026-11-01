package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Use the actual CLI so its argument parsing and receipt are verified together.
func TestSteeringDeliveryCLIHasStableAcknowledgement(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"steer", "--json", "--project", r.Repo, "--run", r.State.RunID, "--source", "ci", "--event", "build-123", "Inspect the failing integration test"}
	var first map[string]any
	for attempt := 0; attempt < 2; attempt++ {
		output, err := exec.Command(nativeTestBinary(t), args...).CombinedOutput()
		if err != nil {
			t.Fatalf("task-event delivery failed: %v\n%s", err, output)
		}
		var receipt map[string]any
		if err := json.Unmarshal(output, &receipt); err != nil {
			t.Fatal(err)
		}
		if receipt["run_id"] != r.State.RunID || receipt["status"] != "waiting" || receipt["duplicate"] != (attempt > 0) {
			t.Fatalf("wrong delivery receipt: %#v", receipt)
		}
		if attempt == 0 {
			first = receipt
		} else if receipt["message"].(map[string]any)["id"] != first["message"].(map[string]any)["id"] {
			t.Fatal("retry created a second steering message")
		}
	}
	inbox, err := readSteering(r.Dir)
	if err != nil || len(inbox.Messages) != 1 {
		t.Fatal("lost or duplicated task update", inbox, err)
	}
	output, err := exec.Command(nativeTestBinary(t), "steering", "--json", "--project", r.Repo, r.State.RunID).CombinedOutput()
	var view SteeringView
	if err != nil || json.Unmarshal(output, &view) != nil || len(view.Messages) != 1 || view.Messages[0].Message.Source != "ci" || view.Messages[0].Message.EventID != "build-123" || view.Messages[0].Status != "waiting" || view.Status == "" {
		t.Fatalf("inspection lost structured receipts: %v %s", err, output)
	}
	for _, invalid := range [][]string{
		{"--source", "ci", "--event", "build-123", "Direction"},
		{"--run", "latest", "--source", "ci", "--event", "build-123", "Direction"},
		{"--run", r.State.RunID, "--source", "", "--event", "", "Direction"},
		{"--run", r.State.RunID, "--source", "ci", "Direction"},
		{"--run", r.State.RunID, "--source", "ci", "--event", "build-123", "Conflicting body"},
	} {
		command := append([]string{"steer", "--project", r.Repo}, invalid...)
		if output, err := exec.Command(nativeTestBinary(t), command...).CombinedOutput(); err == nil {
			t.Fatalf("accepted invalid CLI delivery: %v %s", invalid, output)
		}
	}
	after, err := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("delivery rewrote the controller checkpoint", err)
	}
}
