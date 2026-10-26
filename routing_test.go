package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func routingClock(r *Runner) *time.Time {
	now := time.Now().UTC()
	r.Now = func() time.Time { return now }
	r.Wait = func(ctx context.Context, delay time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		now = now.Add(delay)
		return nil
	}
	return &now
}

func TestRoutingRemembersFailureAcrossAssignmentsAndResume(t *testing.T) {
	r, _ := fixtureRunner(t)
	now := routingClock(r)
	r.Discover = func(context.Context) ([]string, error) {
		return []string{"opencode/first", "opencode/second"}, nil
	}
	attempts := []string{}
	r.Invoke = func(_ context.Context, _ Connection, prompt, _ string, _ bool, model string) (string, error) {
		attempts = append(attempts, model)
		if model == "opencode/first" {
			return "", errors.New("429 rate limited")
		}
		return "Usable report", nil
	}
	task := Task{"one", "First assignment", "Inspect and implement"}
	for i := 0; i < 2; i++ {
		if result, err := r.workOnce(context.Background(), task, ""); err != nil || !result.OK {
			t.Fatalf("assignment failed: %+v %v", result, err)
		}
	}
	resume, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resume.Discover, resume.Invoke, resume.Now, resume.Wait = r.Discover, r.Invoke, r.Now, r.Wait
	resume.recoverCheckpoint()
	if _, err := resume.workOnce(context.Background(), task, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Join(attempts, ",") != "opencode/first,opencode/second,opencode/second,opencode/second" {
		t.Fatalf("failure was forgotten: %v", attempts)
	}
	failed := resume.State.modelHealth("worker", "opencode/first")
	working := resume.State.modelHealth("worker", "opencode/second")
	if failed.Attempts != 1 || failed.Failures != 1 || failed.CooldownUntil == "" || working.Successes != 3 {
		t.Fatalf("health did not survive recovery: %+v %+v", failed, working)
	}
	*now = now.Add(16 * time.Second)
	if model, _ := resume.readyModel("worker", []string{"opencode/first"}); model == "" {
		t.Fatal("a recovered model could not be probed again")
	}
	if r.State.modelHealth("planner", "opencode/first") != nil {
		t.Fatal("worker failures polluted planner formatting health")
	}
}

func TestRoutingExplicitSelectionWaitsAndRechecksPricing(t *testing.T) {
	r, _ := fixtureRunner(t)
	now := routingClock(r)
	start := *now
	r.Settings.Worker.Model = "opencode/first"
	discoveries, attempts := 0, 0
	r.Discover = func(context.Context) ([]string, error) {
		discoveries++
		return []string{"opencode/first", "opencode/second"}, nil
	}
	r.Invoke = func(_ context.Context, _ Connection, prompt, _ string, _ bool, model string) (string, error) {
		if model != "opencode/first" {
			t.Fatal("explicit model selection was overridden")
		}
		attempts++
		if attempts == 1 {
			return "", errors.New("503 overloaded")
		}
		if !strings.Contains(prompt, "PREVIOUS ATTEMPT") || !strings.Contains(prompt, "Inspect the current files") {
			t.Fatal("retry did not receive the partial-work warning")
		}
		return "Done", nil
	}
	result, err := r.workOnce(context.Background(), Task{"one", "Work", "Implement"}, "")
	if err != nil || !result.OK || attempts != 2 || discoveries < 3 || now.Sub(start) != 15*time.Second {
		t.Fatalf("cooldown or catalog refresh failed: %+v %v attempts=%d discoveries=%d delay=%s", result, err, attempts, discoveries, now.Sub(start))
	}
	h := r.State.modelHealth("worker", "opencode/first")
	if h.ConsecutiveFailures != 0 || h.CooldownUntil != "" || h.Successes != 1 || r.State.RoutingWait != nil {
		t.Fatal("a usable response did not reset the cooldown")
	}
}

func TestRoutingRefreshBlocksUnverifiedFallback(t *testing.T) {
	for _, role := range []string{"worker", "planner"} {
		t.Run(role, func(t *testing.T) {
			r, _ := fixtureRunner(t)
			r.Settings.Planner = Connection{Backend: "opencode", Model: "auto"}
			discoveries, calls := 0, 0
			r.Discover = func(context.Context) ([]string, error) {
				discoveries++
				if discoveries > 1 {
					return nil, errors.New("current prices cannot be verified")
				}
				return []string{"opencode/first", "opencode/second"}, nil
			}
			r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
				calls++
				return "", errors.New("429 too many requests")
			}
			var err error
			if role == "worker" {
				_, err = r.workOnce(context.Background(), Task{"one", "Work", "Implement"}, "")
			} else {
				_, err = r.askPlanner(context.Background(), false)
			}
			if err == nil || calls != 1 || !strings.Contains(err.Error(), "prices cannot be verified") {
				t.Fatalf("cached free status authorized a call: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestRoutingWaitRespondsToCancellationAndSteering(t *testing.T) {
	for _, direction := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "steer"}[direction], func(t *testing.T) {
			r, _ := fixtureRunner(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
				calls++
				return "", errors.New("429 too many requests")
			}
			r.Wait = func(ctx context.Context, delay time.Duration) error {
				saved, _, err := LoadRun(r.Repo, r.State.RunID)
				if err != nil || saved.RoutingWait == nil || saved.RoutingWait.Role != "worker" {
					t.Fatalf("active cooldown was not persisted: %+v %v", saved.RoutingWait, err)
				}
				if direction {
					if _, err := SendSteering(r.Repo, r.State.RunID, "Use the new requirements"); err != nil {
						t.Fatal(err)
					}
				} else {
					cancel()
				}
				return waitContext(ctx, delay)
			}
			started := time.Now()
			_, err := r.workOnce(ctx, Task{"one", "Work", "Implement"}, "")
			want := context.Canceled
			if direction {
				want = errSteering
			}
			if !errors.Is(err, want) || calls != 1 || time.Since(started) > 2*time.Second || r.State.RoutingWait != nil {
				t.Fatalf("wait did not yield: err=%v calls=%d", err, calls)
			}
			saved, _, loadErr := LoadRun(r.Repo, r.State.RunID)
			if loadErr != nil || saved.RoutingWait != nil {
				t.Fatalf("finished wait remained active on disk: %+v %v", saved.RoutingWait, loadErr)
			}
		})
	}
}

func TestRoutingRuntimeBudgetCancelsCooldownAndSavesPause(t *testing.T) {
	r, _ := fixtureRunner(t)
	invoke := r.Invoke
	r.MaxRuntime = 500 * time.Millisecond
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if !planner {
			return "", errors.New("429 temporarily unavailable")
		}
		return invoke(ctx, c, prompt, label, planner, model)
	}
	err := r.Run(context.Background())
	var limit limitError
	if !errors.As(err, &limit) || !strings.Contains(err.Error(), "runtime budget") || r.State.WorkerCalls != 1 || r.State.Status != "paused" {
		t.Fatalf("runtime did not interrupt a cooldown: %v calls=%d status=%s", err, r.State.WorkerCalls, r.State.Status)
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil || saved.RoutingWait != nil || len(saved.ModelHealth) != 1 {
		t.Fatalf("pause lost routing history or retained a stale wait: %+v %v", saved, err)
	}
}

func TestRoutingBudgetStopsBeforeCooldownWait(t *testing.T) {
	for _, role := range []string{"worker", "planner"} {
		t.Run(role, func(t *testing.T) {
			r, _ := fixtureRunner(t)
			r.MaxCalls, r.MaxWorkerCalls = 1, 1
			r.Settings.Planner = Connection{Backend: "opencode", Model: "auto"}
			r.Wait = func(context.Context, time.Duration) error {
				t.Fatal("exhausted call budget entered a cooldown wait")
				return nil
			}
			r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
				return "", errors.New("429 rate limited")
			}
			var err error
			if role == "worker" {
				_, err = r.workOnce(context.Background(), Task{"one", "Work", "Implement"}, "")
			} else {
				_, err = r.askPlanner(context.Background(), false)
			}
			var limit limitError
			if !errors.As(err, &limit) {
				t.Fatalf("budget did not pause: %v", err)
			}
		})
	}
}

func TestRoutingHardBlockersNeverFailOver(t *testing.T) {
	for _, failure := range []error{costError{}, errors.New("429 insufficient quota"), errors.New("401 authentication failed"), errors.New("not available in your region")} {
		t.Run(failure.Error(), func(t *testing.T) {
			r, _ := fixtureRunner(t)
			r.Discover = func(context.Context) ([]string, error) { return []string{"opencode/first", "opencode/second"}, nil }
			calls := 0
			r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
				calls++
				return "", failure
			}
			if _, err := r.workOnce(context.Background(), Task{"one", "Work", "Implement"}, ""); err == nil || calls != 1 {
				t.Fatalf("blocker was retried: %v calls=%d", err, calls)
			}
			h := r.State.modelHealth("worker", "opencode/first")
			if h.Failures != 1 || h.CooldownUntil != "" {
				t.Fatal("hard blocker became a temporary cooldown")
			}
		})
	}
}

func TestRoutingPartialEditsReturnToTeamBeforeFallback(t *testing.T) {
	r, _ := councilFixture(t)
	invoke := r.Invoke
	r.Discover = func(context.Context) ([]string, error) { return []string{"opencode/first", "opencode/second"}, nil }
	workers, reviews := 0, 0
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if planner {
			if strings.Contains(label, "-review-") {
				reviews++
				if !strings.Contains(prompt, "Partial edits are present") {
					t.Fatal("seniors did not receive the partial-work report")
				}
			}
			return invoke(ctx, c, prompt, label, planner, model)
		}
		workers++
		if err := os.WriteFile(filepath.Join(r.Repo, "result.txt"), []byte("good\n"), 0600); err != nil {
			return "", err
		}
		return "", errors.New("503 connection dropped after writing")
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if workers != 1 || reviews != 3 || r.State.Status != "complete" || r.State.TaskHistory[0].Status != "failed" || r.State.CheckResults[0].ExitCode != 0 {
		t.Fatalf("partial work was replayed or not reviewed: workers=%d reviews=%d state=%+v", workers, reviews, r.State)
	}
}

func TestRoutingUnusablePlannerRepliesAreBounded(t *testing.T) {
	r, _ := fixtureRunner(t)
	routingClock(r)
	r.Settings.Planner = Connection{Backend: "opencode", Model: "auto"}
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		return "I will plan later", nil
	}
	if _, err := r.askPlanner(context.Background(), false); err == nil || r.State.PlannerCalls != 2 {
		t.Fatalf("invalid replies did not stop after repair attempts: %v calls=%d", err, r.State.PlannerCalls)
	}
	if h := r.State.modelHealth("planner", "opencode/free"); h.FailureKind != "response" || h.Failures != 2 {
		t.Fatalf("bad reply was not recorded: %+v", h)
	}
}

func TestRoutingMigrationValidationAndReadOnlyInspection(t *testing.T) {
	r, _ := fixtureRunner(t)
	r.State.Version = 8
	r.State.ModelHealth = []ModelHealth{{Role: "worker", Model: "opencode/free", Attempts: 1, Failures: 1, ConsecutiveFailures: 1, FailureKind: "transient", CooldownUntil: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)}}
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(r.Dir, "state.json")
	before, _ := os.ReadFile(file)
	value, text, err := InspectRun(r.Repo, r.State.RunID, "routing")
	if err != nil || len(value.(RoutingReport).Models) != 1 || !strings.Contains(text, "cooldown") {
		t.Fatalf("inspection failed: %v %s", err, text)
	}
	after, _ := os.ReadFile(file)
	if string(before) != string(after) {
		t.Fatal("inspection rewrote the old checkpoint")
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil || saved.Version != stateVersion {
		t.Fatalf("migration failed: %+v %v", saved, err)
	}
	for _, mutate := range []func(*State){
		func(s *State) { s.ModelHealth[0].Role = "unknown" },
		func(s *State) { s.ModelHealth[0].Failures = 2 },
		func(s *State) { s.ModelHealth[0].CooldownUntil = "not a time" },
		func(s *State) { s.ModelHealth = append(s.ModelHealth, s.ModelHealth[0]) },
		func(s *State) { s.RoutingWait = &RoutingWait{Role: "worker", Until: "invalid"} },
	} {
		copy := saved
		copy.ModelHealth = append([]ModelHealth{}, saved.ModelHealth...)
		mutate(&copy)
		if err := copy.Save(r.Dir); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadRun(r.Repo, r.State.RunID); err == nil {
			t.Fatal("corrupt health checkpoint was accepted")
		}
	}
}

func TestRoutingBackoffIsCappedAndExcludesBlockedWaits(t *testing.T) {
	for _, kind := range []string{"transient", "response", "execution"} {
		if delay := modelCooldown(kind, 1000000); delay != 5*time.Minute {
			t.Fatalf("unbounded %s retry delay: %s", kind, delay)
		}
	}
	for _, kind := range []string{"auth", "policy", "cost"} {
		if modelCooldown(kind, 4) != 0 {
			t.Fatalf("blocker %s was scheduled for retry", kind)
		}
	}
}
