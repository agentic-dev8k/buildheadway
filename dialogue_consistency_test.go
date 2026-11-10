package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDialogueDiscardsEvidenceChangedDuringAdvice(t *testing.T) {
	r, _ := councilFixture(t)
	if err := r.beginDialogue(context.Background(), "changing-advice", "lead", []TeamRequest{{"critic", "question", "Inspect the current implementation"}}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		calls++
		if calls == 1 {
			writeFixture(t, filepath.Join(r.Repo, "source.txt"), "new evidence")
			return discussionDecision("OBSOLETE_REPLY"), nil
		}
		return discussionDecision("CURRENT_REPLY"), nil
	}
	if err := r.routeDialogue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || r.State.Dialogue.Turns != 2 || len(r.State.Dialogue.Replies) != 1 || !strings.Contains(r.dialogueContext(), "CURRENT_REPLY") || strings.Contains(r.dialogueContext(), "OBSOLETE_REPLY") {
		t.Fatalf("changed evidence reached the active handoff or reset its calls: calls=%d dialogue=%+v", calls, r.State.Dialogue)
	}
	if err := ValidateDialogueState(r.State); err != nil {
		t.Fatal(err)
	}
}

func TestDialogueRechecksBetweenRecipientsAndBeforeDelivery(t *testing.T) {
	for _, recipients := range []int{1, 2} {
		t.Run(fmt.Sprint(recipients), func(t *testing.T) {
			r, _ := councilFixture(t)
			requests := []TeamRequest{{"lead", "question", "Inspect the current contract"}}
			if recipients == 2 {
				requests = append(requests, TeamRequest{"critic", "question", "Inspect the current edge cases"})
			}
			if err := r.beginDialogue(context.Background(), "delivery-boundary", "@worker", requests); err != nil {
				t.Fatal(err)
			}
			changed, calls := false, 0
			r.Emit = func(event Event) {
				if message, ok := event.Value.(AgentMessage); ok && message.Kind == "answer" && !changed {
					changed = true
					writeFixture(t, filepath.Join(r.Repo, "source.txt"), "changed after the first reply was recorded")
				}
			}
			r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
				calls++
				if !changed {
					return discussionDecision("OBSOLETE_REPLY"), nil
				}
				return discussionDecision("CURRENT_REPLY"), nil
			}
			if err := r.routeDialogue(context.Background()); err != nil {
				t.Fatal(err)
			}
			if calls != recipients+1 || r.State.Dialogue.Turns != calls || len(r.State.Dialogue.Replies) != recipients || strings.Contains(r.dialogueContext(), "OBSOLETE_REPLY") {
				t.Fatal("active answers mixed different source snapshots", calls, r.State.Dialogue)
			}
			if !strings.Contains(FormatDiscussion(r.State), "OBSOLETE_REPLY") {
				t.Fatal("discarding active advice erased the historical discussion")
			}
		})
	}
}

func TestDialogueSourceChangesCannotResetSpentAdviceCalls(t *testing.T) {
	r, _ := councilFixture(t)
	request := []TeamRequest{{"critic", "question", "Resolve the current design question"}}
	if err := r.beginDialogue(context.Background(), "same-decision", "lead", request); err != nil {
		t.Fatal(err)
	}
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		return discussionDecision("A concrete answer"), nil
	}
	for call := 0; call < maxDialogueTurns; call++ {
		if call > 0 {
			writeFixture(t, filepath.Join(r.Repo, "source.txt"), fmt.Sprint(call))
			if err := r.beginDialogue(context.Background(), "same-decision", "lead", request); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.routeDialogue(context.Background()); err != nil {
			t.Fatal(err)
		}
		if r.State.Dialogue.Turns != call+1 {
			t.Fatalf("a changed snapshot reset the advice allowance: want=%d got=%d", call+1, r.State.Dialogue.Turns)
		}
	}
	writeFixture(t, filepath.Join(r.Repo, "source.txt"), "another edit cannot authorize a ninth call")
	if err := r.beginDialogue(context.Background(), "same-decision", "lead", request); err == nil || r.State.Dialogue.Turns != maxDialogueTurns || r.State.PlannerCalls != maxDialogueTurns {
		t.Fatal("a changed workspace bypassed the eight-call discussion budget", err)
	}
}

func changingWorkerConsultation(t *testing.T) (*Runner, *int) {
	t.Helper()
	r, _ := councilFixture(t)
	workers := new(int)
	r.Invoke = func(_ context.Context, _ Connection, _ string, label string, planner bool, _ string) (string, error) {
		if strings.HasPrefix(label, "dialogue-") {
			writeFixture(t, filepath.Join(r.Repo, "source.txt"), fmt.Sprint(r.State.PlannerCalls))
			return discussionDecision("ADVICE_FOR_OLD_SOURCE"), nil
		}
		if planner {
			return decisionJSON(false, []Task{{"fix", "Write output", "Write good output after consulting the lead"}}, nil), nil
		}
		*workers++
		if *workers != 1 {
			t.Fatal("the worker continued with stale consultation answers")
		}
		writeFixture(t, filepath.Join(r.Repo, "result.txt"), "partial\n")
		return workerCommunication("consult", "PARTIAL_WORK_RETAINED", TeamRequest{"lead", "question", "Confirm the final output against the current files"}), nil
	}
	return r, workers
}

func TestDialogueInstabilityPausesWorkerAndResumesSavedHandoff(t *testing.T) {
	r, workers := changingWorkerConsultation(t)
	r.MaxCalls = 12 // The red run is also bounded if stale advice is delivered.
	err := r.Run(context.Background())
	var limit limitError
	if !errors.As(err, &limit) || !strings.Contains(err.Error(), "consultation evidence") || r.State.Status != "paused" || r.State.PlannerCalls != 6 || *workers != 1 {
		t.Fatalf("unstable consultation did not pause before implementation: %v calls=%d workers=%d", err, r.State.PlannerCalls, *workers)
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.WorkerSession == nil || saved.WorkerSession.Status != "consulting" || saved.Dialogue == nil || saved.Dialogue.Turns != 3 || len(saved.Dialogue.Replies) != 0 || saved.ActiveTask == "" || saved.CheckedSnapshot != "" {
		t.Fatal("the saved handoff lost its usage, identity or stale-answer invalidation")
	}
	resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, saved.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resumed.Discover = r.Discover
	resumed.MaxCalls = 16
	advice, reviews, continued := 0, 0, 0
	resumed.Invoke = func(_ context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if strings.HasPrefix(label, "dialogue-") {
			advice++
			return discussionDecision("FRESH_ADVICE: write good followed by a newline"), nil
		}
		if planner {
			if !strings.Contains(label, "-review-") || len(resumed.State.CheckResults) != 1 || !resumed.State.CheckResults[0].Passed() {
				t.Fatal("recovery replanned the returned handoff or skipped executed checks")
			}
			reviews++
			return decisionJSON(true, nil, nil), nil
		}
		continued++
		if c.Backend != "opencode" || model != "opencode/free" || !strings.Contains(prompt, "FRESH_ADVICE") || !strings.Contains(prompt, "PARTIAL_WORK_RETAINED") || strings.Contains(prompt, "ADVICE_FOR_OLD_SOURCE") {
			t.Fatal("worker continuation lost its pinned free model, partial report or fresh answer")
		}
		writeFixture(t, filepath.Join(r.Repo, "result.txt"), "good\n")
		return workerCommunication("complete", "Applied current advice"), nil
	}
	if err := resumed.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resumed.State.Status != "complete" || advice != 1 || continued != 1 || reviews != 3 || resumed.State.WorkerCalls != 2 || resumed.State.PlannerCalls != 10 {
		t.Fatal("saved consultation repeated work or skipped fresh unanimous approval", advice, continued, reviews)
	}
}

func TestDialogueInstabilityPreservesHardBlockersAndCallerCancellation(t *testing.T) {
	for _, blocker := range []error{authError{"codex", "renew quota"}, costError{}, outputLimitError{}, mcpSetupError{"credentials unavailable"}, context.Canceled} {
		t.Run(blocker.Error(), func(t *testing.T) {
			r, workers := changingWorkerConsultation(t)
			invoke := r.Invoke
			r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
				if strings.HasPrefix(label, "dialogue-") && r.State.PlannerCalls == 6 {
					return "", blocker
				}
				return invoke(ctx, c, prompt, label, planner, model)
			}
			err := r.Run(context.Background())
			if !errors.Is(err, blocker) || r.State.PlannerCalls != 6 || *workers != 1 || (hardModelFailure(blocker) && r.State.Status != "blocked") || (errors.Is(blocker, context.Canceled) && r.State.Status != "interrupted") {
				t.Fatal("evidence recovery hid or retried a provider/cancellation blocker", err)
			}
		})
	}
}

func TestDialogueChangedSavedSnapshotKeepsUsedTurns(t *testing.T) {
	r, _ := councilFixture(t)
	if err := r.beginDialogue(context.Background(), "saved-advice", "lead", []TeamRequest{{"critic", "question", "Inspect current source"}}); err != nil {
		t.Fatal(err)
	}
	r.State.Dialogue.Turns = maxDialogueTurns - 1
	if err := r.checkpoint(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(r.Dir, "state.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("read-only discussion recovery changed the checkpoint")
	}
	r.State = saved
	writeFixture(t, filepath.Join(r.Repo, "source.txt"), "changed while paused")
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		return discussionDecision("LAST_PERMITTED_REPLY"), nil
	}
	if err := r.routeDialogue(context.Background()); err != nil || r.State.Dialogue.Turns != maxDialogueTurns || r.State.PlannerCalls != 1 {
		t.Fatal("changed saved files reset the used advice budget", err, r.State.Dialogue.Turns)
	}
}

func TestDialogueInstabilityKeepsSmallerCallBudgetAndPauseBoundaryCancellation(t *testing.T) {
	for _, cancelAtBoundary := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelAtBoundary), func(t *testing.T) {
			r, workers := changingWorkerConsultation(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r.MaxCalls = 5 // Three planning calls leave room for only two advice calls.
			if cancelAtBoundary {
				r.MaxCalls = 12
				r.Emit = func(event Event) {
					if event.Kind == "status" && strings.Contains(event.Text, "(3/3)") {
						cancel()
					}
				}
			}
			err := r.Run(ctx)
			if *workers != 1 || r.State.WorkerCalls != 1 || r.State.Dialogue == nil || len(r.State.Dialogue.Replies) != 0 {
				t.Fatal("a limit/cancellation delivered stale advice or repeated implementation")
			}
			if cancelAtBoundary {
				if !errors.Is(err, context.Canceled) || r.State.Status != "interrupted" || r.State.PlannerCalls != 6 || r.State.Dialogue.Turns != 3 {
					t.Fatal("consultation pause hid caller cancellation", err, r.State.Status)
				}
			} else if err == nil || !strings.Contains(err.Error(), "planner call limit") || r.State.Status != "paused" || r.State.PlannerCalls != 5 || r.State.Dialogue.Turns != 2 {
				t.Fatal("consultation recovery exceeded the smaller configured budget", err)
			}
		})
	}
}

func TestChangedReviewConsultationClearsCheckedSnapshotBeforePausing(t *testing.T) {
	r, workers := councilFixture(t)
	*workers = 1 // The first worker invocation writes the passing fixture output.
	base := r.Invoke
	advice := 0
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if strings.HasPrefix(label, "dialogue-") {
			advice++
			writeFixture(t, filepath.Join(r.Repo, "changed-review.txt"), fmt.Sprint(advice))
			return discussionDecision("Advice based on files that just changed"), nil
		}
		if strings.Contains(label, "-review-") {
			return discussionDecision("Consult before approving", TeamRequest{"critic", "question", "Inspect the current verification evidence"}), nil
		}
		return base(ctx, c, prompt, label, planner, model)
	}
	err := r.Run(context.Background())
	var limit limitError
	if !errors.As(err, &limit) || !strings.Contains(err.Error(), "consultation evidence") || r.State.Status != "paused" || advice != 3 {
		t.Fatal("expected a bounded paused review consultation", err)
	}
	if r.State.CheckedSnapshot != "" {
		t.Fatal("changed review consultation retained an obsolete checked-source snapshot")
	}
	if len(r.State.CheckResults) != 1 || !r.State.CheckResults[0].Passed() || r.State.WorkerCalls != 1 || CompletionAllowed(r.State, Decision{Done: true}) {
		t.Fatal("invalidation replaced real check outcomes, repeated work or allowed completion")
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil || saved.CheckedSnapshot != "" || len(saved.CheckResults) != 1 || !saved.CheckResults[0].Passed() {
		t.Fatal("paused checkpoint lost the stale-snapshot invalidation or actual checks", err)
	}
	resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	checks, reviews := 0, 0
	resumed.Emit = func(event Event) {
		if event.Kind == "check" {
			checks++
		}
	}
	resumed.Invoke = func(_ context.Context, _ Connection, _ string, label string, planner bool, _ string) (string, error) {
		if !planner || !strings.Contains(label, "-review-") || checks != 1 || resumed.State.CheckedSnapshot == "" {
			t.Fatal("resume skipped fresh verification or repeated implementation")
		}
		reviews++
		return decisionJSON(true, nil, nil), nil
	}
	if err := resumed.Run(context.Background()); err != nil || resumed.State.Status != "complete" || reviews != 3 || resumed.State.WorkerCalls != 1 {
		t.Fatal("stable review recovery did not require current checks and fresh unanimous approval", err)
	}
}
