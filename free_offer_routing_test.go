package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func consoleErrorEvent(status int, kind, message string) string {
	body, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": kind, "message": message}})
	event, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{"name": "APIError", "data": map[string]any{
		"statusCode": status, "message": message, "responseBody": string(body),
	}}})
	return string(event) + "\n"
}

func TestOpenCodePromotionEndAndQuotaUseDifferentRoutes(t *testing.T) {
	retired := consoleErrorEvent(401, "ModelError", "Free promotion has ended for Retired Free. You can continue using the model by subscribing to OpenCode Go - https://opencode.ai/go")
	quota := consoleErrorEvent(429, "FreeUsageLimitError", "Rate limit exceeded. Please try again later.")
	missing := `{"type":"error","error":{"name":"ProviderModelNotFoundError","data":{"providerID":"opencode","modelID":"verified"}}}` + "\n"
	for _, tc := range []struct{ name, payload, want string }{
		{"promotion ended", retired, "unavailable"},
		{"catalog mismatch", missing, "unavailable"},
		{"shared quota", quota, "quota"},
		{"ordinary rate limit", consoleErrorEvent(429, "RateLimitError", "Rate limit exceeded"), "quota"},
		{"authentication", consoleErrorEvent(401, "AuthError", "Unauthorized"), "auth"},
		{"policy", consoleErrorEvent(403, "ModelError", "This model is not available in your country"), "policy"},
		{"later receipt", retired + "{\"type\":\"step_finish\",\"part\":{\"cost\":0.01}}\n", "cost"},
		{"different model", strings.Replace(missing, "verified", "other", 1), ""},
		{"quoted provider error", `{"type":"text","part":{"text":"Free promotion has ended"}}` + "\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := fixtureRunner(t)
			c := costStreamFixture(t, r, tc.payload, tc.want == "quota")
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			out, err := r.invokeCLI(ctx, c, "Inspect", "offer-test", false, "")
			if tc.want == "" {
				if err != nil || out != tc.payload {
					t.Fatalf("unrelated output was used as routing evidence: %v %q", err, out)
				}
				return
			}
			if err == nil || modelFailureKind(err, false) != tc.want || ctx.Err() != nil {
				t.Fatalf("wanted %s before the caller deadline, got %v (context: %v)", tc.want, err, ctx.Err())
			}
		})
	}
}

func TestFreeOfferRoutingRefreshesAndHoldsAcrossRoles(t *testing.T) {
	r, _ := fixtureRunner(t)
	routingClock(r)
	retired, backup := "opencode/verified", "opencode/backup"
	r.Settings.Planner = Connection{Backend: "opencode", Model: "auto"}
	r.Settings.Worker.Model = "auto"
	var attempts []string
	discoveries := 0
	r.Discover = func(context.Context) ([]string, error) {
		discoveries++
		if discoveries == 1 {
			return []string{retired, "opencode/no-longer-free", backup}, nil
		}
		return []string{retired, backup}, nil
	}
	c := costStreamFixture(t, r, consoleErrorEvent(401, "ModelError", "Model verified is not supported"), false)
	r.Invoke = func(ctx context.Context, connection Connection, prompt, label string, planner bool, model string) (string, error) {
		if planner {
			model = connection.Model
		}
		attempts = append(attempts, model)
		if model == retired {
			return r.invokeCLI(ctx, c, prompt, label, planner, model)
		}
		if planner {
			return decisionJSON(true, nil, nil), nil
		}
		return "Done", nil
	}
	ctx := context.Background()
	task := Task{"one", "Work", "Implement"}
	if result, err := r.workOnce(ctx, task, ""); err != nil || !result.OK || result.Model != backup {
		t.Fatalf("worker did not advance to the freshly verified backup: %+v %v", result, err)
	}
	resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resumed.Discover, resumed.Invoke, resumed.Now = r.Discover, r.Invoke, r.Now
	if _, err := resumed.askPlanner(ctx, false); err != nil {
		t.Fatal(err)
	}
	h := resumed.State.modelHealth("worker", retired)
	if strings.Join(attempts, ",") != retired+","+backup+","+backup || discoveries != 3 || h.FailureKind != "unavailable" {
		t.Fatalf("stale offers were dispatched or lost across resume: %v refreshes=%d health=%+v", attempts, discoveries, h)
	}
}

func TestFreeOfferExhaustionPinsAndPartialEdits(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		pinned, partial, hasBackup bool
		catalogBecomesEmpty        bool
	}{
		{name: "empty pool"},
		{name: "catalog drops last offer", catalogBecomesEmpty: true},
		{name: "pinned offer", pinned: true, hasBackup: true},
		{name: "partial edits", partial: true, hasBackup: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := fixtureRunner(t)
			routingClock(r)
			c := costStreamFixture(t, r, consoleErrorEvent(401, "ModelError", "Model verified is not supported"), false)
			r.Settings.Worker.Model = "auto"
			if tc.pinned {
				r.Settings.Worker.Model = c.Model
			}
			calls := 0
			r.Discover = func(context.Context) ([]string, error) {
				if calls != 0 && tc.catalogBecomesEmpty {
					return nil, errNoVerifiedFreeModels
				}
				if tc.hasBackup {
					return []string{c.Model, "opencode/backup"}, nil
				}
				return []string{c.Model}, nil
			}
			r.Wait = func(context.Context, time.Duration) error {
				t.Fatal("retirement spun in a cooldown loop")
				return nil
			}
			r.Invoke = func(ctx context.Context, _ Connection, prompt, label string, planner bool, model string) (string, error) {
				calls++
				if calls > 1 {
					t.Fatalf("unexpected repeated dispatch to %s", model)
				}
				if tc.partial {
					if err := os.WriteFile(filepath.Join(r.Repo, "partial.txt"), []byte("partial"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				return r.invokeCLI(ctx, c, prompt, label, planner, model)
			}
			result, err := r.workOnce(context.Background(), Task{"one", "Work", "Implement"}, "")
			if tc.partial {
				if err != nil || !strings.Contains(result.Report, "Partial edits") {
					t.Fatalf("partial edits bypassed verification: %+v %v", result, err)
				}
			} else {
				var paused limitError
				if !errors.As(err, &paused) || (!strings.Contains(err.Error(), "unavailable") && !strings.Contains(err.Error(), "no verified zero-price")) {
					t.Fatalf("unavailable offer did not pause: %+v %v", result, err)
				}
			}
			if calls != 1 {
				t.Fatalf("expected precisely one attempted call, got %d", calls)
			}
		})
	}
}

func TestProviderQuotaPausesWithoutDispatchingBackup(t *testing.T) {
	r, _ := fixtureRunner(t)
	c := costStreamFixture(t, r, consoleErrorEvent(429, "FreeUsageLimitError", "Rate limit exceeded"), true)
	r.Settings.Worker.Executable = c.Executable
	r.Discover = func(context.Context) ([]string, error) {
		return []string{c.Model, "opencode/backup"}, nil
	}
	plan := r.Invoke
	var calls []string
	r.Invoke = func(ctx context.Context, connection Connection, prompt, label string, planner bool, model string) (string, error) {
		if planner {
			return plan(ctx, connection, prompt, label, planner, model)
		}
		calls = append(calls, model)
		return r.invokeCLI(ctx, c, prompt, label, planner, model)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := r.Run(ctx)
	var quota freeQuotaError
	var auth authError
	saved, _, loadErr := LoadRun(r.Repo, r.State.RunID)
	if !errors.As(err, &quota) || errors.As(err, &auth) || loadErr != nil || saved.Status != "paused" ||
		len(calls) != 1 || calls[0] != c.Model || saved.WorkerCalls != 1 {
		t.Fatalf("quota did not preserve a clean pause: run=%v status=%s load=%v calls=%v", err, saved.Status, loadErr, calls)
	}
	if health := saved.modelHealth("worker", c.Model); health == nil || health.FailureKind != "quota" {
		t.Fatalf("quota was lost from routing history: %+v", health)
	}
}
