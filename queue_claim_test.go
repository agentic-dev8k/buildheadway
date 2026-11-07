package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestQueuedClaimRestoresPinnedStateBeforeQueuePublication(t *testing.T) {
	for _, keyed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unkeyed", true: "keyed"}[keyed], func(t *testing.T) {
			repo, paths := t.TempDir(), testPaths(t)
			writeFixture(t, filepath.Join(repo, ".buildheadway.json"), `{"checks":["echo initial"],"max_worker_calls":3,"max_planner_calls":7}`)
			settings := Settings{Planner: Connection{Backend: "codex", Model: "saved-senior"}, Worker: Connection{Backend: "opencode", Model: "saved-free"}}
			source, event := "", ""
			if keyed {
				source, event = "trusted", "task/42"
			}
			receipt, err := DeliverQueuedGoal(repo, "Preserve the café goal", "figureout", source, event)
			if err != nil {
				t.Fatal(err)
			}
			queuePath := filepath.Join(repo, ".forge", "queue.json")
			pending, _ := os.ReadFile(queuePath)
			first, err := claimQueuedRunner(paths, settings, repo, noEmit)
			if err != nil {
				t.Fatal(err)
			}
			first.State.PlannerCalls, first.State.WorkerCalls, first.State.ActiveMillis = 2, 1, 1234
			first.State.Status, first.State.Phase, first.State.Error = "blocked", "checking", "account access must be restored"
			if err := first.State.Save(first.Dir); err != nil {
				t.Fatal(err)
			}
			checkpointPath := filepath.Join(first.Dir, "state.json")
			before, _ := os.ReadFile(checkpointPath)
			info, _ := os.Stat(checkpointPath)
			if err := os.WriteFile(queuePath, pending, 0600); err != nil {
				t.Fatal(err)
			}
			// New settings must not replace a recovered goal's pinned team,
			// configuration, explicit worker, counters or provider error.
			writeFixture(t, filepath.Join(repo, ".buildheadway.json"), `{"checks":["echo changed"],"max_worker_calls":99}`)
			settings.Planner.Model, settings.Worker.Model = "changed-senior", "changed-free"
			restored, err := claimQueuedRunner(paths, settings, repo, noEmit)
			if err != nil {
				t.Fatal(err)
			}
			if restored.State.RunID != first.State.RunID || restored.Dir != first.Dir || restored.State.QueueID != receipt.Item.ID || restored.resumeID != first.State.RunID {
				t.Fatal("lost the original claim identity")
			}
			if restored.State.PlannerCalls != 2 || restored.State.WorkerCalls != 1 || restored.State.ActiveMillis != 1234 || restored.State.Error != first.State.Error || restored.State.Phase != "checking" || restored.State.Status != "blocked" {
				t.Fatal("recovery reset saved work, budgets or blocker evidence")
			}
			if !reflect.DeepEqual(restored.State.Config, first.State.Config) || !reflect.DeepEqual(restored.State.Team, first.State.Team) || !reflect.DeepEqual(restored.State.WorkerConnection, first.State.WorkerConnection) || restored.MaxWorkerCalls != 3 || restored.MaxCalls != 7 || restored.Settings.Worker.Model != "saved-free" {
				t.Fatal("recovery replaced pinned configuration")
			}
			after, _ := os.ReadFile(checkpointPath)
			later, _ := os.Stat(checkpointPath)
			if string(after) != string(before) || !later.ModTime().Equal(info.ModTime()) {
				t.Fatal("claim recovery rewrote the checkpoint")
			}
			queue, err := ReadQueue(repo)
			if err != nil || queue.Items[0].RunID != first.State.RunID {
				t.Fatal("recovered run reference was not published", err)
			}
			if keyed {
				retry, err := DeliverQueuedGoal(repo, receipt.Item.Goal, "figureout", source, event)
				if err != nil || !retry.Duplicate || retry.Item.RunID != first.State.RunID {
					t.Fatal("intake retry lost recovered identity", err)
				}
			}
			history, err := RunHistory(repo)
			if err != nil || len(history) != 1 {
				t.Fatal("claim recovery created another run", err)
			}
		})
	}
}

func TestQueuedClaimConflictingCheckpointBlocksBeforeMutation(t *testing.T) {
	for _, field := range []string{"goal", "mode", "queue-id", "invalid-json", "missing-checkpoint", "missing-run"} {
		t.Run(field, func(t *testing.T) {
			repo, paths := t.TempDir(), testPaths(t)
			_, err := QueueAdd(repo, "Keep original goal")
			if err != nil {
				t.Fatal(err)
			}
			first, err := claimQueuedRunner(paths, Settings{}, repo, noEmit)
			if err != nil {
				t.Fatal(err)
			}
			queue, _ := ReadQueue(repo)
			switch field {
			case "goal":
				queue.Items[0].Goal = "A different task"
			case "mode":
				queue.Items[0].Mode = "figureout"
			case "queue-id":
				first.State.QueueID = "q-another"
				if err := first.State.Save(first.Dir); err != nil {
					t.Fatal(err)
				}
			case "invalid-json":
				os.WriteFile(filepath.Join(first.Dir, "state.json"), []byte("not a checkpoint"), 0600)
			case "missing-checkpoint":
				os.Remove(filepath.Join(first.Dir, "state.json"))
			case "missing-run":
				queue.Items[0].RunID = "explicit-missing-run"
			}
			if err := saveQueue(repo, queue); err != nil {
				t.Fatal(err)
			}
			queuePath, statePath := filepath.Join(repo, ".forge", "queue.json"), filepath.Join(first.Dir, "state.json")
			beforeQueue, _ := os.ReadFile(queuePath)
			beforeState, stateErr := os.ReadFile(statePath)
			if _, err := claimQueuedRunner(paths, Settings{}, repo, noEmit); err == nil {
				t.Fatal("inconsistent checkpoint was dispatched")
			}
			if after, _ := os.ReadFile(queuePath); string(after) != string(beforeQueue) {
				t.Fatal("refusal rewrote queue")
			}
			after, afterErr := os.ReadFile(statePath)
			if string(after) != string(beforeState) || errors.Is(afterErr, os.ErrNotExist) != errors.Is(stateErr, os.ErrNotExist) {
				t.Fatal("refusal replaced checkpoint evidence")
			}
			if field == "goal" || field == "mode" {
				if err := first.syncQueue(); err == nil {
					t.Fatal("progress synchronization hid a changed queue goal")
				}
			}
		})
	}
}

func TestQueuedClaimRefusesAmbiguousReservationAndSymlinks(t *testing.T) {
	for _, kind := range []string{"empty", "file", "directory-link", "checkpoint-link", "runs-link"} {
		t.Run(kind, func(t *testing.T) {
			repo, paths := t.TempDir(), testPaths(t)
			item, err := QueueAdd(repo, "Do not overwrite saved work")
			if err != nil {
				t.Fatal(err)
			}
			runsPath := filepath.Join(repo, ".forge", "runs")
			os.MkdirAll(runsPath, 0700)
			candidate := filepath.Join(runsPath, queuedRunID(item.ID))
			outside := t.TempDir()
			switch kind {
			case "empty":
				os.Mkdir(candidate, 0700)
			case "file":
				os.WriteFile(candidate, []byte("preserve"), 0600)
			case "directory-link":
				if err := os.Symlink(outside, candidate); err != nil {
					t.Skip("symlinks unavailable", err)
				}
			case "checkpoint-link":
				os.Mkdir(candidate, 0700)
				state := filepath.Join(outside, "state.json")
				os.WriteFile(state, []byte("preserve"), 0600)
				if err := os.Symlink(state, filepath.Join(candidate, "state.json")); err != nil {
					t.Skip("symlinks unavailable", err)
				}
			case "runs-link":
				os.Remove(runsPath)
				if err := os.Symlink(outside, runsPath); err != nil {
					t.Skip("symlinks unavailable", err)
				}
			}
			before, _ := os.ReadFile(filepath.Join(repo, ".forge", "queue.json"))
			if _, err := claimQueuedRunner(paths, Settings{}, repo, noEmit); err == nil {
				t.Fatal("ambiguous reservation was replaced")
			}
			if after, _ := os.ReadFile(filepath.Join(repo, ".forge", "queue.json")); string(after) != string(before) {
				t.Fatal("unsafe claim rewrote queue")
			}
			if kind != "checkpoint-link" {
				entries, _ := os.ReadDir(outside)
				if len(entries) != 0 {
					t.Fatal("claim wrote outside project")
				}
			}
			if kind == "empty" {
				entries, _ := os.ReadDir(candidate)
				if len(entries) != 0 {
					t.Fatal("empty reservation was silently initialized")
				}
			}
		})
	}
}

func TestQueuedClaimRetainsLegacyExplicitRunReference(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.State.QueueID = "q-legacy"
	if strings.HasPrefix(r.State.RunID, "queued-") {
		t.Fatal("fixture must use an older random run ID")
	}
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	item := QueuedGoal{ID: r.State.QueueID, Goal: r.State.Goal, RunID: r.State.RunID, Status: "blocked"}
	if err := saveQueue(r.Repo, GoalQueue{Version: 1, Items: []QueuedGoal{item}}); err != nil {
		t.Fatal(err)
	}
	restored, err := claimQueuedRunner(r.Paths, r.Settings, r.Repo, noEmit)
	if err != nil || restored.State.RunID != r.State.RunID || restored.Dir != r.Dir {
		t.Fatal("legacy explicit claim was not resumed", err)
	}
}
