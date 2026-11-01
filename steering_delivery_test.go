package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSteeringDeliveryConcurrentRetriesAndConflicts(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	const text = "Inspect the failed job; retain all acceptance checks."
	var wg sync.WaitGroup
	receipts, failures := make(chan SteeringReceipt, 16), make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			receipt, err := DeliverSteering(r.Repo, r.State.RunID, text, "ci", "workflow/42:failed")
			receipts <- receipt
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
	ids, originals := map[string]bool{}, 0
	for receipt := range receipts {
		ids[receipt.Message.ID] = true
		if !receipt.Duplicate {
			originals++
		}
		if receipt.Status != "waiting" || receipt.RunID != r.State.RunID || receipt.Message.Text != text {
			t.Fatal("wrong receipt", receipt)
		}
	}
	if len(ids) != 1 || originals != 1 {
		t.Fatal("concurrent retries were not coalesced", ids, originals)
	}
	path := filepath.Join(r.Dir, "steering.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	if _, err := DeliverSteering(r.Repo, r.State.RunID, "Changed payload", "ci", "workflow/42:failed"); err == nil {
		t.Fatal("same event key replaced accepted text")
	}
	if _, err := DeliverSteering(r.Repo, r.State.RunID, text, "ci", "workflow/42:failed"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	info, err := os.Stat(path)
	if err != nil || string(before) != string(after) || !info.ModTime().Equal(past) {
		t.Fatal("duplicate/conflict mutated the durable inbox", err)
	}
	if err := r.finishRun("complete", "Cannot finish"); !errors.Is(err, errSteering) {
		t.Fatal("pending delivery allowed completion", err)
	}
}

func TestSteeringDeliveryAcknowledgesAppliedEventsAfterRecoveryAndCompletion(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	first, err := DeliverSteering(r.Repo, r.State.RunID, "Inspect compatibility", "review", "comment-17")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.applySteering(); err != nil {
		t.Fatal(err)
	}
	if err := AtomicJSON(filepath.Join(r.Repo, ".forge", "latest.json"), map[string]string{"run_id": r.State.RunID}); err != nil {
		t.Fatal(err)
	}
	// An older inbox backup may lack the event retained in the checkpoint.
	if err := AtomicJSON(filepath.Join(r.Dir, "steering.json"), SteeringInbox{Version: 1, Messages: []SteeringMessage{}}); err != nil {
		t.Fatal(err)
	}
	resume, err := NewRunner(r.Paths, r.Settings, r.Repo, "", true, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	receipt, err := DeliverSteering(r.Repo, r.State.RunID, "Inspect compatibility", "review", "comment-17")
	if err != nil || !receipt.Duplicate || receipt.Status != "applied" || receipt.Message != first.Message {
		t.Fatal("restored checkpoint lost applied receipt", receipt, err)
	}
	view, err := InspectSteering(r.Repo, r.State.RunID)
	if err != nil || len(view.Messages) != 1 || view.Messages[0].Status != "applied" || !strings.Contains(view.Status, "source: review · event: comment-17") {
		t.Fatal("inspection lost delivery provenance", view, err)
	}
	after, _ := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	if string(before) != string(after) {
		t.Fatal("acknowledgement or inspection rewrote the checkpoint")
	}
	if err := resume.finishRun("complete", "Done"); err != nil {
		t.Fatal(err)
	}
	before, _ = os.ReadFile(filepath.Join(r.Dir, "state.json"))
	receipt, err = DeliverSteering(r.Repo, r.State.RunID, "Inspect compatibility", "review", "comment-17")
	if err != nil || !receipt.Duplicate || receipt.Status != "applied" {
		t.Fatal("late retry did not acknowledge the original event", receipt, err)
	}
	if _, err := DeliverSteering(r.Repo, r.State.RunID, "New request", "review", "comment-18"); err == nil {
		t.Fatal("new event reopened a completed goal")
	}
	after, _ = os.ReadFile(filepath.Join(r.Dir, "state.json"))
	if string(before) != string(after) {
		t.Fatal("completed-run retry changed the checkpoint")
	}
}

func TestSteeringDeliveryRequiresStableTaskAndSourceKeys(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][4]string{
		{"latest", "ci", "42", "Direction"}, {"", "ci", "42", "Direction"},
		{r.State.RunID, "ci", "", "Direction"}, {r.State.RunID, "", "42", "Direction"},
		{r.State.RunID, "ci\nrole", "42", "Direction"}, {r.State.RunID, "ci", "../42", "Direction"},
		{r.State.RunID, strings.Repeat("a", 65), "42", "Direction"}, {r.State.RunID, "ci", strings.Repeat("a", 129), "Direction"},
		{r.State.RunID, "ci", "42", "\x00"}, {r.State.RunID, "ci", "42", string([]byte{0xff})},
	} {
		if _, err := DeliverSteering(r.Repo, args[0], args[3], args[1], args[2]); err == nil {
			t.Fatalf("accepted invalid delivery %#v", args)
		}
	}
	for _, source := range []string{"ci", "review"} {
		if _, err := DeliverSteering(r.Repo, r.State.RunID, "Direction", source, "42"); err != nil {
			t.Fatal(err)
		}
	}
	other, err := NewRunner(r.Paths, r.Settings, r.Repo, "Separate task", false, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.State.Save(other.Dir); err != nil {
		t.Fatal(err)
	}
	if receipt, err := DeliverSteering(r.Repo, other.State.RunID, "Direction", "ci", "42"); err != nil || receipt.Duplicate {
		t.Fatal("event scope leaked into a different run", receipt, err)
	}
	inbox, err := readSteering(r.Dir)
	if err != nil || len(inbox.Messages) != 2 {
		t.Fatal("invalid deliveries changed the first inbox", inbox, err)
	}
}

func TestSteeringDeliveryRetainsBoundsAndRejectsConflictingSavedCopies(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	inbox := SteeringInbox{Version: 1}
	for i := 0; i < 64; i++ {
		inbox.Messages = append(inbox.Messages, SteeringMessage{ID: fmt.Sprintf("steer-%d", i), Text: "Direction", Source: "ci", EventID: fmt.Sprint(i)})
	}
	if err := AtomicJSON(filepath.Join(r.Dir, "steering.json"), inbox); err != nil {
		t.Fatal(err)
	}
	if receipt, err := DeliverSteering(r.Repo, r.State.RunID, "Direction", "ci", "63"); err != nil || !receipt.Duplicate {
		t.Fatal("full history rejected a duplicate receipt", receipt, err)
	}
	if _, err := DeliverSteering(r.Repo, r.State.RunID, "Direction", "ci", "64"); err == nil {
		t.Fatal("new delivery exceeded history limit")
	}
	r.State.Steering = append([]SteeringMessage{}, inbox.Messages...)
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	if err := AtomicJSON(filepath.Join(r.Dir, "steering.json"), SteeringInbox{Version: 1, Messages: []SteeringMessage{}}); err != nil {
		t.Fatal(err)
	}
	if receipt, err := DeliverSteering(r.Repo, r.State.RunID, "Direction", "ci", "63"); err != nil || !receipt.Duplicate || receipt.Status != "applied" {
		t.Fatal("restored inbox lost full applied history", receipt, err)
	}
	if _, err := DeliverSteering(r.Repo, r.State.RunID, "Direction", "ci", "64"); err == nil {
		t.Fatal("restored inbox bypassed the history limit")
	}
	inbox.Messages[0].Text = "Conflicting saved copy"
	if err := AtomicJSON(filepath.Join(r.Dir, "steering.json"), inbox); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectSteering(r.Repo, r.State.RunID); err == nil {
		t.Fatal("inspection hid a conflicting inbox")
	}
	if _, err := DeliverSteering(r.Repo, r.State.RunID, "Direction", "ci", "63"); err == nil {
		t.Fatal("duplicate receipt ignored inconsistent saved evidence")
	}
	if err := r.steeringPending(); err == nil {
		t.Fatal("runner ignored inconsistent saved evidence")
	}
	// Duplicate source/event keys with distinct message IDs must also block.
	inbox.Messages[0] = inbox.Messages[1]
	inbox.Messages[0].ID = "different-id"
	if err := AtomicJSON(filepath.Join(r.Dir, "steering.json"), inbox); err != nil {
		t.Fatal(err)
	}
	if _, err := readSteering(r.Dir); err == nil {
		t.Fatal("accepted duplicate durable event keys")
	}
}

func TestSteeringDeliveryRetainsTextBudgetAcrossInboxRecovery(t *testing.T) {
	r, _ := fixtureRunner(t)
	for i := 0; i < 16; i++ {
		r.State.Steering = append(r.State.Steering, SteeringMessage{ID: fmt.Sprintf("steer-%d", i), Text: strings.Repeat("a", 8000), Source: "ci", EventID: fmt.Sprint(i)})
	}
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	if receipt, err := DeliverSteering(r.Repo, r.State.RunID, strings.Repeat("a", 8000), "ci", "0"); err != nil || !receipt.Duplicate {
		t.Fatal("applied receipt consumed additional text budget", receipt, err)
	}
	if _, err := DeliverSteering(r.Repo, r.State.RunID, strings.Repeat("b", 4000), "ci", "new"); err == nil {
		t.Fatal("missing inbox allowed total text budget to be exceeded")
	}
}

func TestSteeringDeliveryRetriesDoNotRepeatGoalWork(t *testing.T) {
	r, _ := councilFixture(t)
	workers := 0
	r.Invoke = func(_ context.Context, _ Connection, prompt, label string, planner bool, model string) (string, error) {
		if planner {
			if len(r.State.Steering) > 0 {
				receipt, err := DeliverSteering(r.Repo, r.State.RunID, "ALSO_WRITE_DOCUMENTATION", "review", "comment-1")
				if err != nil || !receipt.Duplicate || receipt.Status != "applied" || !strings.Contains(prompt, "source=review event=comment-1") {
					t.Fatal("senior retry lost the accepted event", receipt, err)
				}
				if _, err := os.Stat(filepath.Join(r.Repo, "README.md")); err == nil {
					return decisionJSON(true, nil, nil), nil
				}
				return decisionJSON(false, []Task{{"docs", "Document", "ALSO_WRITE_DOCUMENTATION"}}, nil), nil
			}
			return decisionJSON(false, []Task{{"first", "Write output", "Write good"}, {"stale", "Old task", "MUST_NOT_RUN"}}, nil), nil
		}
		workers++
		if model != "opencode/free" || r.State.Tasks[r.State.NextTask].ID == "stale" {
			t.Fatal("stale task or unverified worker dispatched")
		}
		if workers == 1 {
			writeExtension(t, filepath.Join(r.Repo, "result.txt"), "good\n")
			for i := 0; i < 4; i++ {
				if _, err := DeliverSteering(r.Repo, r.State.RunID, "ALSO_WRITE_DOCUMENTATION", "review", "comment-1"); err != nil {
					t.Fatal(err)
				}
			}
		} else {
			if !strings.Contains(prompt, "ALSO_WRITE_DOCUMENTATION") || !strings.Contains(prompt, "source=review event=comment-1") {
				t.Fatal("worker lost event provenance")
			}
			writeExtension(t, filepath.Join(r.Repo, "README.md"), "Verified usage.\n")
		}
		return workerCommunication("complete", "Implemented assigned work"), nil
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if workers != 2 || len(r.State.Steering) != 1 || r.State.Status != "complete" || !r.State.Council.Consensus || len(r.State.CheckResults) == 0 {
		b, _ := json.Marshal(r.State)
		t.Fatalf("duplicate event disrupted the goal: workers=%d state=%s", workers, b)
	}
	for _, check := range r.State.CheckResults {
		if !check.Passed() {
			t.Fatal("completed with a failing check", check)
		}
	}
	if !strings.Contains(MarkdownReport(r.State), "source=review event=comment-1") {
		t.Fatal("report lost event provenance")
	}
}
