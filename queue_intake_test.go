package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func deliveryPath(repo, source, event string) string {
	return filepath.Join(repo, filepath.FromSlash(queueDeliveryDirectory), queueDeliveryName(source, event))
}

func TestQueueDeliveryConcurrentRetriesAreReadOnlyAndScoped(t *testing.T) {
	repo := t.TempDir()
	var wg sync.WaitGroup
	receipts := make(chan QueueReceipt, 12)
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := DeliverQueuedGoal(repo, "  Build the café app\n", "figureout", "trusted", "issue:42")
			receipts <- r
			failures <- err
		}()
	}
	wg.Wait()
	close(receipts)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	id, first := "", 0
	for receipt := range receipts {
		if !receipt.Duplicate {
			first++
		}
		if id == "" {
			id = receipt.Item.ID
		}
		if receipt.Item.ID != id || receipt.Item.Goal != "Build the café app" || receipt.Item.Source != "trusted" || receipt.Item.EventID != "issue:42" {
			t.Fatal("retry changed identity", receipt)
		}
	}
	if first != 1 {
		t.Fatalf("created %d copies", first)
	}
	queue, err := ReadQueue(repo)
	if err != nil || len(queue.Items) != 1 {
		t.Fatal("concurrent retry duplicated work", err)
	}
	paths := []string{filepath.Join(repo, ".forge", "queue.json"), deliveryPath(repo, "trusted", "issue:42")}
	for _, path := range paths {
		before, _ := os.ReadFile(path)
		info, _ := os.Stat(path)
		r, err := DeliverQueuedGoal(repo, "Build the café app", "figureout", "trusted", "issue:42")
		if err != nil || !r.Duplicate || r.Item.ID != id {
			t.Fatal(r, err)
		}
		after, _ := os.ReadFile(path)
		later, _ := os.Stat(path)
		if string(before) != string(after) || !info.ModTime().Equal(later.ModTime()) {
			t.Fatal("duplicate rewrote a saved record")
		}
	}
	for _, input := range []struct{ goal, mode string }{{"Changed request", "figureout"}, {"Build the café app", ""}} {
		if _, err := DeliverQueuedGoal(repo, input.goal, input.mode, "trusted", "issue:42"); err == nil {
			t.Fatal("conflicting retry accepted")
		}
	}
	other, err := DeliverQueuedGoal(repo, "Build the café app", "figureout", "another-source", "issue:42")
	if err != nil || other.Item.ID == id {
		t.Fatal("different source collapsed", err)
	}
	separate, err := DeliverQueuedGoal(t.TempDir(), "Build the café app", "figureout", "trusted", "issue:42")
	if err != nil || separate.Item.ID == id {
		t.Fatal("different project collapsed", err)
	}
}

func TestQueueDeliveryRecoversPublicationBeforeReceiptAndPrune(t *testing.T) {
	repo := t.TempDir()
	r, err := DeliverQueuedGoal(repo, "Build once", "", "script", "request/1")
	if err != nil {
		t.Fatal(err)
	}
	path := deliveryPath(repo, "script", "request/1")
	// Replay the on-disk state after queue publication but before its receipt.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	queuePath := filepath.Join(repo, ".forge", "queue.json")
	before, _ := os.ReadFile(queuePath)
	got, err := DeliverQueuedGoal(repo, "Build once", "", "script", "request/1")
	if err != nil || !got.Duplicate || got.Item.ID != r.Item.ID {
		t.Fatal("interrupted publication was duplicated", err)
	}
	if after, _ := os.ReadFile(queuePath); string(before) != string(after) {
		t.Fatal("recovery rewrote the queue")
	}
	// Cancellation must also repair lookup evidence before changing lifecycle.
	os.Remove(path)
	if err := QueueRemove(repo, r.Item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("cancellation forgot the event key", err)
	}
	os.Remove(path)
	if _, err := PruneQueue(repo); err != nil {
		t.Fatal(err)
	}
	archived, err := DeliverQueuedGoal(repo, "Build once", "", "script", "request/1")
	if err != nil || !archived.Duplicate || !archived.Archived || archived.Item.Status != "canceled" || archived.Item.ID != r.Item.ID {
		t.Fatal("pruning forgot the canceled request", archived, err)
	}
	page, err := ListQueueArchives(context.Background(), repo, "request/1", "", 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].Source != "script" || page.Items[0].EventID != "request/1" {
		t.Fatal("archive inspection lost provenance", err)
	}
}

func TestQueueDeliveryMissingOrConflictingHistoryNeverReopens(t *testing.T) {
	repo := t.TempDir()
	r, err := DeliverQueuedGoal(repo, "Build once", "", "script", "request/1")
	if err != nil {
		t.Fatal(err)
	}
	queuePath := filepath.Join(repo, ".forge", "queue.json")
	original, _ := os.ReadFile(queuePath)
	if err := saveQueue(repo, GoalQueue{Version: 1, Items: []QueuedGoal{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := DeliverQueuedGoal(repo, "Build once", "", "script", "request/1"); err == nil || !strings.Contains(err.Error(), "missing its goal record") {
		t.Fatal("missing accepted record was silently recreated", err)
	}
	os.WriteFile(queuePath, original, 0600)
	if err := QueueRemove(repo, r.Item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := PruneQueue(repo); err != nil {
		t.Fatal(err)
	}
	// Restoring an older queue cannot reopen an archived finished request.
	os.WriteFile(queuePath, original, 0600)
	if _, err := DeliverQueuedGoal(repo, "Build once", "", "script", "request/1"); err == nil {
		t.Fatal("conflicting older queue was acknowledged as pending")
	}
	_, err = claimQueuedRunner(testPaths(t), Settings{}, repo, noEmit)
	if err == nil {
		t.Fatal("archived request was dispatched from an older queue")
	}
	if after, _ := os.ReadFile(queuePath); string(after) != string(original) {
		t.Fatal("conflict changed queue")
	}
}

func TestQueueDeliveryPreservesCapacityAndRejectsBadOrigins(t *testing.T) {
	repo := t.TempDir()
	r, err := DeliverQueuedGoal(repo, "Keep", "", "script", "one")
	if err != nil {
		t.Fatal(err)
	}
	q := GoalQueue{Version: 1, Items: []QueuedGoal{r.Item}}
	for i := 1; i < 1000; i++ {
		q.Items = append(q.Items, QueuedGoal{ID: "q-" + itoa(i), Goal: "Keep", Status: "pending"})
	}
	if err := saveQueue(repo, q); err != nil {
		t.Fatal(err)
	}
	if retry, err := DeliverQueuedGoal(repo, "Keep", "", "script", "one"); err != nil || !retry.Duplicate {
		t.Fatal("full queue rejected a known retry", err)
	}
	if _, err := DeliverQueuedGoal(repo, "New", "", "script", "two"); err == nil {
		t.Fatal("event bypassed capacity")
	}
	for _, pair := range [][2]string{{"script", ""}, {"", "event"}, {"../escape", "event"}, {"s", "\x00"}, {"s", strings.Repeat("e", 129)}} {
		if _, err := DeliverQueuedGoal(repo, "Valid goal", "", pair[0], pair[1]); err == nil {
			t.Fatal("bad origin accepted", pair)
		}
	}
	for _, goal := range []string{"", " \n", "contains\x00nul", string([]byte{0xff}), strings.Repeat("x", 32001)} {
		if _, err := QueueAdd(repo, goal); err == nil {
			t.Fatal("invalid goal text accepted")
		}
	}
	q.Items[1] = r.Item
	q.Items[1].ID = "q-copy"
	if err := saveQueue(repo, q); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadQueue(repo); err == nil {
		t.Fatal("duplicate saved source/event accepted")
	}
}

func TestQueueDeliveryCorruptMetadataStopsBeforeExecution(t *testing.T) {
	for _, content := range []string{`{`, `{"version":2}`, strings.Repeat("x", queueDeliveryLimit+1)} {
		t.Run(itoa(len(content)), func(t *testing.T) {
			repo := t.TempDir()
			r, err := DeliverQueuedGoal(repo, "Keep", "", "s", "e")
			if err != nil {
				t.Fatal(err)
			}
			queuePath := filepath.Join(repo, ".forge", "queue.json")
			before, _ := os.ReadFile(queuePath)
			os.WriteFile(deliveryPath(repo, "s", "e"), []byte(content), 0600)
			if _, err := DeliverQueuedGoal(repo, "Keep", "", "s", "e"); err == nil {
				t.Fatal("corrupt receipt accepted")
			}
			if _, err := claimQueuedRunner(testPaths(t), Settings{}, repo, noEmit); err == nil {
				t.Fatal("corrupt receipt reached execution")
			}
			if err := QueueRemove(repo, r.Item.ID); err == nil {
				t.Fatal("corrupt receipt was silently replaced")
			}
			if after, _ := os.ReadFile(queuePath); string(after) != string(before) {
				t.Fatal("corruption changed saved work")
			}
		})
	}
}

func TestKeyedQueuedGoalsKeepOneRunAndRetainBlockers(t *testing.T) {
	paths, repo := testPaths(t), t.TempDir()
	settings := Settings{Planner: Connection{Backend: "codex"}, Worker: Connection{Backend: "opencode", Model: "auto"}}
	first, err := DeliverQueuedGoal(repo, "First goal", "", "script", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := DeliverQueuedGoal(repo, "Second goal", "", "script", "second")
	if err != nil {
		t.Fatal(err)
	}
	workers, plans := 0, 0
	configure := func(r *Runner) {
		configureQueueFixture(r)
		invoke := r.Invoke
		r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
			if planner {
				passing := len(r.State.CheckResults) != 0
				for _, check := range r.State.CheckResults {
					passing = passing && check.ExitCode == 0
				}
				if passing {
					return decisionJSON(true, nil, nil), nil
				}
				return decisionJSON(false, []Task{{"work", "Write result", "Write the correct result"}}, nil), nil
			}
			return invoke(ctx, c, prompt, label, planner, model)
		}
	}
	err = runQueuedWith(context.Background(), paths, settings, repo, true, noEmit, func(r *Runner) {
		configure(r)
		invoke := r.Invoke
		r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
			if planner {
				plans++
				return invoke(ctx, c, prompt, label, planner, model)
			}
			workers++
			retry, err := DeliverQueuedGoal(repo, "First goal", "", "script", "first")
			if err != nil || !retry.Duplicate || retry.Item.RunID != r.State.RunID {
				t.Fatal("active retry lost its run", err)
			}
			return "", authError{"opencode", "sign in required"}
		}
	})
	if err == nil || !hardModelFailure(err) || workers != 1 {
		t.Fatal("provider blocker did not hold the queue", err)
	}
	retry, err := DeliverQueuedGoal(repo, "First goal", "", "script", "first")
	if err != nil || !retry.Duplicate || retry.Item.Status != "blocked" || retry.Item.ID != first.Item.ID {
		t.Fatal("blocked retry reopened work", err)
	}
	q, _ := ReadQueue(repo)
	if q.Items[1].ID != second.Item.ID || q.Items[1].RunID != "" || q.Items[1].Status != "pending" {
		t.Fatal("later work bypassed blocker")
	}
	if plans == 0 {
		t.Fatal("test skipped planning")
	}
	if err := runQueuedWith(context.Background(), paths, settings, repo, true, noEmit, configure); err != nil {
		t.Fatal(err)
	}
	q, err = ReadQueue(repo)
	if err != nil || q.Items[0].Status != "complete" || q.Items[1].Status != "complete" || q.Items[0].RunID != retry.Item.RunID {
		t.Fatal("resume duplicated the goal or skipped acceptance", err)
	}
	if _, err := PruneQueue(repo); err != nil {
		t.Fatal(err)
	}
	final, err := DeliverQueuedGoal(repo, "First goal", "", "script", "first")
	if err != nil || !final.Archived || !final.Duplicate || final.Item.Status != "complete" {
		t.Fatal("completed retry scheduled work", err)
	}
	history, err := RunHistory(repo)
	if err != nil || len(history) != 2 {
		t.Fatal("duplicate run was created", err)
	}
}

func TestQueueDeliveryRejectsDirectoryAndFileSymlinks(t *testing.T) {
	for _, target := range []string{"directory", "file"} {
		t.Run(target, func(t *testing.T) {
			repo, outside := t.TempDir(), t.TempDir()
			if target == "directory" {
				os.MkdirAll(filepath.Join(repo, ".forge"), 0700)
				if err := os.Symlink(outside, filepath.Join(repo, filepath.FromSlash(queueDeliveryDirectory))); err != nil {
					t.Skip("symlinks unavailable", err)
				}
			} else {
				r, err := DeliverQueuedGoal(repo, "Keep", "", "s", "e")
				if err != nil {
					t.Fatal(err)
				}
				_ = r
				path := deliveryPath(repo, "s", "e")
				data, _ := os.ReadFile(path)
				other := filepath.Join(outside, "receipt.json")
				os.WriteFile(other, data, 0600)
				os.Remove(path)
				if err := os.Symlink(other, path); err != nil {
					t.Skip("symlinks unavailable", err)
				}
			}
			_, err := DeliverQueuedGoal(repo, "Keep", "", "s", "e")
			if err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatal("unsafe metadata path was accepted", err)
			}
			if target == "directory" {
				entries, _ := os.ReadDir(outside)
				if len(entries) != 0 {
					t.Fatal("wrote outside project")
				}
			}
		})
	}
}
