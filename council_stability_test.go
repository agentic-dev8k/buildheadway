package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestCouncilPausesRepeatedPlanningEvidenceChanges(t *testing.T) {
	for _, timing := range []string{"during-call", "between-calls", "after-synthesis"} {
		t.Run(timing, func(t *testing.T) {
			r, workers := councilFixture(t)
			r.MaxCalls = 12 // Bound the regression even without the instability guard.
			check, criterion := "node retained.cjs", "Retain requests while evidence changes"
			edits := 0
			change := func() {
				edits++
				writeFixture(t, filepath.Join(r.Repo, "changing.txt"), fmt.Sprint(edits))
			}
			r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
				if timing == "during-call" {
					change()
				}
				return councilRequestJSON(t, check, criterion), nil
			}
			r.Emit = func(event Event) {
				message, ok := event.Value.(AgentMessage)
				if ok && (timing == "between-calls" && message.Kind == "plan-proposal" || timing == "after-synthesis" && message.Kind == "plan-decision") {
					change()
				}
			}
			err := r.Run(context.Background())
			var limit limitError
			wantCalls := 3
			if timing == "after-synthesis" {
				wantCalls = 9
			}
			if !errors.As(err, &limit) || !strings.Contains(err.Error(), "council evidence") || r.State.Status != "paused" || r.State.PlannerCalls != wantCalls || *workers != 0 {
				t.Fatalf("unstable council did not pause after three invalidated rounds: calls=%d edits=%d workers=%d status=%s err=%v", r.State.PlannerCalls, edits, *workers, r.State.Status, err)
			}
			assertCouncilRequest(t, r.State, check, criterion)
			saved, _, err := LoadRun(r.Repo, r.State.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if saved.Council != nil || saved.Dialogue != nil || saved.CheckedSnapshot != "" || len(saved.CheckResults) != 0 || CompletionAllowed(saved, Decision{Done: true}) {
				t.Fatal("unstable planning retained votes or invented verification")
			}
			if !strings.Contains(FormatDiscussion(saved), "Votes discarded") || !strings.Contains(saved.RecoveryNote, "resume") {
				t.Fatal("the checkpoint omitted instability diagnosis and recovery guidance")
			}
		})
	}
}

func TestCouncilReviewInstabilityPausesAndResumesWithFreshChecks(t *testing.T) {
	r, workers := councilFixture(t)
	*workers = 1
	r.MaxCycles = 0
	r.MaxCalls = 18
	base := r.Invoke
	reviews, checks := 0, 0
	r.Emit = func(event Event) {
		if event.Kind == "check" {
			checks++
		}
	}
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if strings.Contains(label, "-review-") {
			reviews++
			writeFixture(t, filepath.Join(r.Repo, "review-change.txt"), fmt.Sprint(reviews))
		}
		return base(ctx, c, prompt, label, planner, model)
	}
	err := r.Run(context.Background())
	var limit limitError
	if !errors.As(err, &limit) || !strings.Contains(err.Error(), "council evidence") || r.State.Status != "paused" || reviews != 3 || r.State.WorkerCalls != 1 || checks != 3 {
		t.Fatalf("review instability bypassed the bound or repeated implementation: err=%v reviews=%d workers=%d checks=%d", err, reviews, r.State.WorkerCalls, checks)
	}
	if r.State.Phase != "checking" || r.State.CheckedSnapshot != "" || r.State.Council != nil || len(r.State.CheckResults) != 1 || !r.State.CheckResults[0].Passed() {
		t.Fatal("paused review discarded actual checks or kept stale approval")
	}
	resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resumed.MaxCalls = 12
	resumed.MaxCycles = 0
	resumed.Discover = r.Discover
	freshChecks, freshReviews := 0, 0
	resumed.Emit = func(event Event) {
		if event.Kind == "check" {
			freshChecks++
		}
	}
	resumed.Invoke = func(_ context.Context, _ Connection, _ string, label string, planner bool, _ string) (string, error) {
		if !planner || !strings.Contains(label, "-review-") || freshChecks == 0 {
			t.Fatal("resume skipped verification or repeated the worker")
		}
		freshReviews++
		return decisionJSON(true, nil, nil), nil
	}
	if err := resumed.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if freshChecks != 1 || freshReviews != 3 || resumed.State.Status != "complete" || resumed.State.WorkerCalls != 1 || resumed.State.PlannerCalls != 9 {
		t.Fatal("stable resume did not get fresh checks and unanimous approval", resumed.State.Status, freshChecks, freshReviews)
	}
}

func TestCouncilBetweenReviewCallsRechecksBeforeAnotherSenior(t *testing.T) {
	r, workers := councilFixture(t)
	*workers = 1
	base := r.Invoke
	checks, reviews := 0, 0
	changed := false
	r.Emit = func(event Event) {
		if event.Kind == "check" {
			checks++
		}
		if message, ok := event.Value.(AgentMessage); ok && message.Kind == "review-proposal" && !changed {
			changed = true
			writeFixture(t, filepath.Join(r.Repo, "between-seniors.txt"), "new evidence")
		}
	}
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if strings.Contains(label, "-review-") {
			reviews++
			if reviews > 1 && checks < 2 {
				t.Fatal("another senior reviewed changed files before fresh executable checks")
			}
		}
		return base(ctx, c, prompt, label, planner, model)
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !changed || checks != 2 || reviews != 4 || r.State.WorkerCalls != 1 || r.State.Status != "complete" {
		t.Fatal("the stale review was reused or the completed worker repeated", checks, reviews, r.State.Status)
	}
}

func TestCouncilStabilityGuardPreservesBudgetsAndHardBlockers(t *testing.T) {
	for _, blocker := range []error{nil, authError{"codex", "sign-in required"}, costError{}, outputLimitError{}, mcpSetupError{"credentials unavailable"}, context.Canceled} {
		name := "smaller call budget"
		if blocker != nil {
			name = blocker.Error()
		}
		t.Run(name, func(t *testing.T) {
			r, workers := councilFixture(t)
			r.MaxCalls = 2
			if blocker != nil {
				r.MaxCalls = 6
			}
			r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
				if r.State.PlannerCalls == 3 {
					return "", blocker
				}
				writeFixture(t, filepath.Join(r.Repo, "changing.txt"), fmt.Sprint(r.State.PlannerCalls))
				return decisionJSON(false, nil, nil), nil
			}
			err := r.Run(context.Background())
			if err == nil || *workers != 0 || strings.Contains(err.Error(), "council evidence") {
				t.Fatalf("instability guard hid an earlier limit/blocker: %v", err)
			}
			if blocker == nil {
				if r.State.PlannerCalls != 2 || r.State.Status != "paused" || !strings.Contains(err.Error(), "planner call limit") {
					t.Fatal("a council retry exceeded the configured call budget", err)
				}
			} else if !errors.Is(err, blocker) || r.State.PlannerCalls != 3 || (hardModelFailure(blocker) && r.State.Status != "blocked") || (errors.Is(blocker, context.Canceled) && r.State.Status != "interrupted") {
				t.Fatal("the independent provider/cancellation blocker was retried or reclassified", err, r.State.Status)
			}
		})
	}
}

func TestStableCouncilRoundResetsConsecutiveInstability(t *testing.T) {
	r, workers := councilFixture(t)
	*workers = 1
	base := r.Invoke
	planEdits, reviewEdits := 0, 0
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if planner {
			edits := &planEdits
			if strings.Contains(label, "-review-") {
				edits = &reviewEdits
			}
			if *edits < 2 {
				*edits++
				writeFixture(t, filepath.Join(r.Repo, "changing.txt"), label+fmt.Sprint(*edits))
			}
		}
		return base(ctx, c, prompt, label, planner, model)
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal("independent stable planning/review rounds accumulated an old instability count", err)
	}
	if r.State.Status != "complete" || r.State.PlannerCalls != 10 || r.State.WorkerCalls != 1 {
		t.Fatal("stability recovery skipped checks or complete council rounds", r.State.Status, r.State.PlannerCalls)
	}
}

func TestCouncilInstabilityKeepsCallerCancellationAtPauseBoundary(t *testing.T) {
	r, _ := councilFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		writeFixture(t, filepath.Join(r.Repo, "changing.txt"), fmt.Sprint(r.State.PlannerCalls))
		return decisionJSON(false, nil, nil), nil
	}
	r.Emit = func(event Event) {
		if event.Kind == "status" && strings.Contains(event.Text, "(3/3)") {
			cancel() // The caller cancels as the new pause diagnosis is emitted.
		}
	}
	if err := r.Run(ctx); !errors.Is(err, context.Canceled) || r.State.Status != "interrupted" || r.State.PlannerCalls != 3 || r.State.Council != nil {
		t.Fatal("the stability pause hid caller cancellation", err, r.State.Status)
	}
}
