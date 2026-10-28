package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestQueueArchivePagesSearchStableIDsAndRemainReadOnly(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	queue := GoalQueue{Version: 1, Items: []QueuedGoal{{ID: "q-pending", Goal: "keep work", Status: "pending"}}}
	if err := saveQueue(r.Repo, queue); err != nil {
		t.Fatal(err)
	}
	queuePath := filepath.Join(r.Repo, ".forge/queue.json")
	statePath := filepath.Join(r.Dir, "state.json")
	queueBefore, _ := os.ReadFile(queuePath)
	stateBefore, _ := os.ReadFile(statePath)
	var expected []string
	for i := 162; i >= 0; i-- {
		id := fmt.Sprintf("q-%03d", i)
		goal := "Unrelated goal"
		status := "complete"
		if i%17 == 0 {
			goal = "Needle " + strings.Repeat("界", 3000) + " final diagnosis"
			expected = append([]string{id}, expected...)
		}
		if i%2 != 0 {
			status = "canceled"
		}
		item := QueuedGoal{ID: id, Goal: goal, Status: status, RunID: r.State.RunID, Mode: "figureout", UpdatedAt: "finished"}
		if err := AtomicJSON(filepath.Join(r.Repo, filepath.FromSlash(queueArchiveDirectory), id+".json"), queueArchivedGoal{Version: 1, Item: item}); err != nil {
			t.Fatal(err)
		}
	}
	var found []string
	after := ""
	for {
		page, err := ListQueueArchives(context.Background(), r.Repo, "nEEdLe", after, 3)
		if err != nil || len(page.Items) > 3 {
			t.Fatal("page failed", err)
		}
		for _, item := range page.Items {
			if !item.GoalTruncated || len(item.Goal) > 240 || !utf8.ValidString(item.Goal) || !strings.HasPrefix(item.Goal, "Needle") || !strings.Contains(item.Goal, "final diagnosis") || item.RunID != r.State.RunID || item.Mode != "figureout" {
				t.Fatal("unbounded or incomplete summary")
			}
			found = append(found, item.ID)
		}
		if !page.More {
			if page.NextAfter != "" {
				t.Fatal("terminal page has continuation")
			}
			break
		}
		if page.NextAfter <= after || page.NextAfter != page.Items[len(page.Items)-1].ID {
			t.Fatal("pagination did not advance")
		}
		after = page.NextAfter
	}
	if !reflect.DeepEqual(found, expected) {
		t.Fatalf("missing, duplicated or unordered matches: %v %v", found, expected)
	}
	page, err := ListQueueArchives(context.Background(), r.Repo, "canceled", "", 2)
	if err != nil || !page.More || page.Items[0].ID != "q-001" || page.Items[1].ID != "q-003" {
		t.Fatal("status filter or ordering failed", err)
	}
	if !strings.Contains(FormatQueueArchives(page), "--after q-003") {
		t.Fatal("missing continuation guidance")
	}
	page, err = ListQueueArchives(context.Background(), r.Repo, "q-162", "", 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "q-162" {
		t.Fatal("ID lookup failed", err)
	}
	m := &appModel{repo: r.Repo, busy: true}
	cmd, ok := m.projectCommand("/queue archives needle")
	if !ok || cmd == nil {
		t.Fatal("in-app archive discovery missing")
	}
	msg := cmd().(inspectionMsg)
	if msg.Err != nil || !strings.Contains(msg.Text, "q-000") {
		t.Fatal("in-app search failed", msg.Err)
	}
	queueAfter, _ := os.ReadFile(queuePath)
	stateAfter, _ := os.ReadFile(statePath)
	if string(queueBefore) != string(queueAfter) || string(stateBefore) != string(stateAfter) {
		t.Fatal("archive search rewrote live state")
	}
	full, err := ReadQueueArchive(r.Repo, "q-000")
	if err != nil || len(full.Goal) < 9000 {
		t.Fatal("full archive was truncated", err)
	}
}

func TestQueueArchiveSearchRejectsInvalidInputCorruptionAndCancellation(t *testing.T) {
	repo := t.TempDir()
	page, err := ListQueueArchives(context.Background(), repo, "", "", 20)
	if err != nil || len(page.Items) != 0 || page.More {
		t.Fatal("missing archive directory should be empty", err)
	}
	for _, limit := range []int{0, -1, 101} {
		if _, err := ListQueueArchives(context.Background(), repo, "", "", limit); err == nil {
			t.Fatal("invalid limit accepted")
		}
	}
	if _, err := ListQueueArchives(context.Background(), repo, strings.Repeat("x", 257), "", 20); err == nil {
		t.Fatal("oversized search accepted")
	}
	if _, err := ListQueueArchives(context.Background(), repo, "", "../escape", 20); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ListQueueArchives(ctx, repo, "", "", 20); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
	archive := filepath.Join(repo, filepath.FromSlash(queueArchiveDirectory), "q-bad.json")
	if err := AtomicJSON(archive, queueArchivedGoal{Version: 1, Item: QueuedGoal{ID: "q-bad", Status: "complete"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ListQueueArchives(context.Background(), repo, "", "", 20); err == nil {
		t.Fatal("invalid goal metadata silently listed")
	}
	if err := os.WriteFile(archive, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListQueueArchives(context.Background(), repo, "", "", 20); err == nil {
		t.Fatal("malformed archive silently skipped")
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, archive); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	page, err = ListQueueArchives(context.Background(), repo, "", "", 20)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("listing followed a symlink", err)
	}
}
