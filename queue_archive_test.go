package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestQueuePruneRestoresCapacityPreservingUnfinishedAndCheckpoints(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.Dir, "state.json")
	before, _ := os.ReadFile(path)
	queue := GoalQueue{Version: 1, Items: []QueuedGoal{}}
	for i := 0; i < 997; i++ {
		status := "complete"
		if i%2 == 1 {
			status = "canceled"
		}
		queue.Items = append(queue.Items, QueuedGoal{ID: fmt.Sprintf("q-%d", i), Goal: "Preserve this goal", Status: status, RunID: r.State.RunID, Mode: "figureout", Error: "diagnostic", CreatedAt: "created", UpdatedAt: "finished"})
	}
	for _, status := range []string{"pending", "running", "blocked"} {
		queue.Items = append(queue.Items, QueuedGoal{ID: "q-" + status, Goal: "Keep work", Status: status, RunID: r.State.RunID})
	}
	if err := saveQueue(r.Repo, queue); err != nil {
		t.Fatal(err)
	}
	if _, err := QueueAdd(r.Repo, "capacity is full"); err == nil {
		t.Fatal("fixture must reach the existing capacity limit")
	}
	// Archive maintenance must work while a controller owns the repository lock.
	lock, err := acquireProjectLock(r.Repo, "lock", false)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	report, err := PruneQueue(r.Repo)
	if err != nil || report.Pruned != 997 || report.Remaining != 3 || len(report.ArchivedIDs) != 997 {
		t.Fatalf("prune failed: %+v %v", report, err)
	}
	kept, err := ReadQueue(r.Repo)
	if err != nil || !reflect.DeepEqual(kept.Items, queue.Items[997:]) {
		t.Fatal("unfinished queue records changed", err)
	}
	for _, index := range []int{0, 1, 996} {
		item, err := ReadQueueArchive(r.Repo, fmt.Sprintf("q-%d", index))
		if err != nil || !reflect.DeepEqual(item, queue.Items[index]) {
			t.Fatal("archive lost complete metadata", err)
		}
	}
	if _, err := QueueAdd(r.Repo, "new goal"); err != nil {
		t.Fatal("capacity was not restored", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("pruning rewrote a run checkpoint")
	}
	queueBytes, _ := os.ReadFile(filepath.Join(r.Repo, ".forge/queue.json"))
	retry, err := PruneQueue(r.Repo)
	if err != nil || retry.Pruned != 0 {
		t.Fatal("second prune changed unfinished goals", err)
	}
	unchanged, _ := os.ReadFile(filepath.Join(r.Repo, ".forge/queue.json"))
	if string(queueBytes) != string(unchanged) {
		t.Fatal("no-op prune rewrote queue")
	}
}

func TestQueuePruneRetriesArchivedRecordsAndRefusesConflicts(t *testing.T) {
	repo := t.TempDir()
	item := QueuedGoal{ID: "q-done", Goal: "Keep exact content", Status: "complete"}
	queue := GoalQueue{Version: 1, Items: []QueuedGoal{item}}
	if err := saveQueue(repo, queue); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(repo, filepath.FromSlash(queueArchiveDirectory), item.ID+".json")
	// Simulate a crash after an archive is durable but before queue publication.
	if err := AtomicJSON(archive, queueArchivedGoal{Version: 1, Item: item}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(archive)
	if report, err := PruneQueue(repo); err != nil || report.Pruned != 1 {
		t.Fatal("interrupted prune could not resume", err)
	}
	after, _ := os.ReadFile(archive)
	if string(before) != string(after) {
		t.Fatal("existing archive was changed")
	}
	item.Goal = "conflicting metadata"
	queue.Items[0] = item
	if err := saveQueue(repo, queue); err != nil {
		t.Fatal(err)
	}
	queuePath := filepath.Join(repo, ".forge/queue.json")
	queueBefore, _ := os.ReadFile(queuePath)
	if _, err := PruneQueue(repo); err == nil {
		t.Fatal("overwrote a conflicting archive")
	}
	queueAfter, _ := os.ReadFile(queuePath)
	if string(queueBefore) != string(queueAfter) {
		t.Fatal("archive failure removed queue records")
	}
	if _, err := ReadQueueArchive(repo, "../queue"); err == nil {
		t.Fatal("archive traversal accepted")
	}
	var decoded queueArchivedGoal
	if err := json.Unmarshal(before, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded.Item.Status = "pending"
	if err := AtomicJSON(archive, decoded); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadQueueArchive(repo, item.ID); err == nil {
		t.Fatal("unfinished archive accepted")
	}
	decoded.Item.Status = "complete"
	decoded.Version = 99
	if err := AtomicJSON(archive, decoded); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadQueueArchive(repo, item.ID); err == nil {
		t.Fatal("unsupported archive version accepted")
	}
}

func TestQueuePruneInAppCommandsAndDirectoryRefusal(t *testing.T) {
	repo := t.TempDir()
	item := QueuedGoal{ID: "q-ui", Goal: "Archive this finished entry", Status: "canceled"}
	if err := saveQueue(repo, GoalQueue{Version: 1, Items: []QueuedGoal{item}}); err != nil {
		t.Fatal(err)
	}
	m := &appModel{repo: repo, busy: true}
	cmd, handled := m.projectCommand("/queue prune")
	if !handled || cmd == nil {
		t.Fatal("in-app prune unavailable while busy")
	}
	result := cmd().(inspectionMsg)
	if result.Err != nil || !strings.Contains(result.Text, "pruned 1") {
		t.Fatal("in-app prune failed", result.Err)
	}
	cmd, handled = m.projectCommand("/queue archive q-ui")
	if !handled || cmd == nil {
		t.Fatal("in-app archive unavailable")
	}
	result = cmd().(inspectionMsg)
	if result.Err != nil || !strings.Contains(result.Text, item.Goal) {
		t.Fatal("in-app archive failed", result.Err)
	}
	cmd, _ = m.projectCommand("/queue prune unexpected")
	if cmd().(inspectionMsg).Err == nil {
		t.Fatal("in-app prune accepted extra arguments")
	}
	other := t.TempDir()
	outside := t.TempDir()
	if err := saveQueue(other, GoalQueue{Version: 1, Items: []QueuedGoal{item}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(other, ".forge/queue.json"))
	if err := os.Symlink(outside, filepath.Join(other, ".forge/queue-archive")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := PruneQueue(other); err == nil {
		t.Fatal("prune followed archive directory symlink")
	}
	after, _ := os.ReadFile(filepath.Join(other, ".forge/queue.json"))
	entries, _ := os.ReadDir(outside)
	if string(before) != string(after) || len(entries) != 0 {
		t.Fatal("directory refusal changed queue or wrote outside it")
	}
}

func TestQueuePruneArchiveBoundsAndSymlinks(t *testing.T) {
	repo := t.TempDir()
	queue := GoalQueue{Version: 1, Items: []QueuedGoal{{ID: "q-large", Goal: "goal", Status: "canceled", Error: strings.Repeat("x", queueArchiveLimit)}}}
	if err := saveQueue(repo, queue); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".forge/queue.json")
	before, _ := os.ReadFile(path)
	if _, err := PruneQueue(repo); err == nil {
		t.Fatal("oversized archive accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("lost oversized record")
	}
	other := t.TempDir()
	if err := os.Symlink(other, filepath.Join(repo, ".forge/queue-archive/link.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ReadQueueArchive(repo, "link"); err == nil {
		t.Fatal("archive symlink accepted")
	}
}
