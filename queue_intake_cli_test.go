package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativeKeyedQueueDeliverySurvivesRetryAndArchive(t *testing.T) {
	binary := nativeTestBinary(t)
	repo, home := t.TempDir(), t.TempDir()
	invoke := func(args ...string) ([]byte, error) {
		c := exec.Command(binary, append([]string{"queue"}, args...)...)
		c.Dir, c.Env = repo, testCommandEnv(home)
		return c.CombinedOutput()
	}
	args := []string{"--json", "--source", "trusted-script", "--event", "issue:42", "add", "Repair the existing tests"}
	first, err := invoke(args...)
	if err != nil {
		t.Fatalf("keyed queue intake unavailable: %v %s", err, first)
	}
	var receipt struct {
		Duplicate bool       `json:"duplicate"`
		Archived  bool       `json:"archived"`
		Item      QueuedGoal `json:"item"`
	}
	if err := json.Unmarshal(first, &receipt); err != nil || receipt.Duplicate || receipt.Item.ID == "" {
		t.Fatalf("invalid receipt: %s", first)
	}
	if receipt.Item.Source != "trusted-script" || receipt.Item.EventID != "issue:42" {
		t.Fatal("CLI lost origin metadata")
	}
	path := filepath.Join(repo, ".forge", "queue.json")
	before, _ := os.ReadFile(path)
	retry, err := invoke(args...)
	if err != nil {
		t.Fatalf("retry: %v %s", err, retry)
	}
	var duplicate struct {
		Duplicate bool       `json:"duplicate"`
		Item      QueuedGoal `json:"item"`
	}
	if err := json.Unmarshal(retry, &duplicate); err != nil || !duplicate.Duplicate || duplicate.Item != receipt.Item {
		t.Fatalf("retry changed the original goal: %s", retry)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("duplicate rewrote queue")
	}
	if out, err := invoke("remove", receipt.Item.ID); err != nil {
		t.Fatalf("cancel: %v %s", err, out)
	}
	if out, err := invoke("prune"); err != nil {
		t.Fatalf("prune: %v %s", err, out)
	}
	archived, err := invoke(args...)
	if err != nil {
		t.Fatalf("archived retry: %v %s", err, archived)
	}
	if err := json.Unmarshal(archived, &receipt); err != nil || !receipt.Duplicate || !receipt.Archived || receipt.Item.Status != "canceled" {
		t.Fatalf("archived event was recreated: %s", archived)
	}
	queue, err := ReadQueue(repo)
	if err != nil || len(queue.Items) != 0 {
		t.Fatal("archived retry re-enqueued work", err)
	}
	bad := append(append([]string{}, args[:len(args)-1]...), "Different goal")
	if out, err := invoke(bad...); err == nil {
		t.Fatalf("changed event text accepted: %s", out)
	}
	queueBefore, _ := os.ReadFile(path)
	for _, bad := range [][]string{
		{"--source", "s", "add", "Goal"},
		{"--source", "", "--event", "", "add", "Goal"},
		{"--source", "s", "--event", "e", "list"},
		{"--source", "s", "--event", "e", "run"},
		{"add", "--source", "s", "--event", "e", "Goal"},
		{"--source", "../unsafe", "--event", "e", "add", "Goal"},
	} {
		if out, err := invoke(bad...); err == nil {
			t.Fatalf("invalid event arguments accepted: %v %s", bad, out)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(queueBefore) {
		t.Fatal("invalid arguments changed queue")
	}
	c := exec.Command(binary, "remember", "--source", "s", "--event", "e", "Not a queue goal")
	c.Dir, c.Env = repo, testCommandEnv(home)
	if out, err := c.CombinedOutput(); err == nil {
		t.Fatalf("non-queue command accepted event flags: %s", out)
	}
}
