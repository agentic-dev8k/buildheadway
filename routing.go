package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Health describes usable responses, not whether an implementation is correct.
// Planner formatting failures do not penalize the same model as a worker.
type ModelHealth struct {
	Role                string `json:"role"`
	Model               string `json:"model"`
	Attempts            int    `json:"attempts"`
	Successes           int    `json:"successes"`
	Failures            int    `json:"failures"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	LastAttemptAt       string `json:"last_attempt_at,omitempty"`
	LastSuccessAt       string `json:"last_success_at,omitempty"`
	LastFailureAt       string `json:"last_failure_at,omitempty"`
	FailureKind         string `json:"failure_kind,omitempty"`
	LastError           string `json:"last_error,omitempty"`
	CooldownUntil       string `json:"cooldown_until,omitempty"`
}

type RoutingWait struct {
	Role  string `json:"role"`
	Until string `json:"until"`
}

func validRoutingRole(role string) bool { return role == "planner" || role == "worker" }

func validateRouting(s State) error {
	seen := map[string]bool{}
	for _, h := range s.ModelHealth {
		key := h.Role + "\x00" + h.Model
		if !validRoutingRole(h.Role) || strings.TrimSpace(h.Model) == "" || len(h.Model) > 2000 || seen[key] {
			return errors.New("invalid model routing identity")
		}
		seen[key] = true
		if h.Attempts < 0 || h.Successes < 0 || h.Failures < 0 || h.ConsecutiveFailures < 0 || h.Successes > h.Attempts || h.Failures > h.Attempts-h.Successes || h.ConsecutiveFailures > h.Failures {
			return errors.New("invalid model routing counters")
		}
		switch h.FailureKind {
		case "", "transient", "response", "execution", "auth", "policy", "cost", "output_limit":
		default:
			return errors.New("invalid model failure kind")
		}
		for _, value := range []string{h.LastAttemptAt, h.LastSuccessAt, h.LastFailureAt, h.CooldownUntil} {
			if value != "" {
				if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
					return errors.New("invalid model routing time")
				}
			}
		}
	}
	if w := s.RoutingWait; w != nil {
		if !validRoutingRole(w.Role) {
			return errors.New("invalid routing wait role")
		}
		if _, err := time.Parse(time.RFC3339Nano, w.Until); err != nil {
			return errors.New("invalid routing wait time")
		}
	}
	return nil
}

func (r *Runner) routeNow() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (s *State) modelHealth(role, model string) *ModelHealth {
	for i := range s.ModelHealth {
		if s.ModelHealth[i].Role == role && s.ModelHealth[i].Model == model {
			return &s.ModelHealth[i]
		}
	}
	return nil
}

func (r *Runner) modelAttempt(role, model string) {
	h := r.State.modelHealth(role, model)
	if h == nil {
		r.State.ModelHealth = append(r.State.ModelHealth, ModelHealth{Role: role, Model: model})
		h = &r.State.ModelHealth[len(r.State.ModelHealth)-1]
	}
	h.Attempts++
	h.LastAttemptAt = r.routeNow().UTC().Format(time.RFC3339Nano)
	r.State.RoutingWait = nil
	r.note("model-selected", fmt.Sprintf("%s · %s · %d consecutive failures; eligible in the refreshed zero-price catalog", role, model, h.ConsecutiveFailures))
}

func modelFailureKind(err error, response bool) string {
	var oversized outputLimitError
	if errors.As(err, &oversized) {
		return "output_limit"
	}
	var charge costError
	if errors.As(err, &charge) || strings.Contains(err.Error(), "nonzero worker cost") {
		return "cost"
	}
	if authRE.MatchString(err.Error()) {
		return "auth"
	}
	if policyRE.MatchString(err.Error()) {
		return "policy"
	}
	if errors.Is(err, context.DeadlineExceeded) || transientRE.MatchString(err.Error()) {
		return "transient"
	}
	if response {
		return "response"
	}
	return "execution"
}

func modelCooldown(kind string, failures int) time.Duration {
	if kind == "auth" || kind == "policy" || kind == "cost" || kind == "output_limit" {
		return 0 // These stop the run; waiting cannot repair authorization or billing.
	}
	delay := 30 * time.Second
	if kind == "transient" {
		delay = 15 * time.Second
	}
	for i := 1; i < failures && delay < 5*time.Minute; i++ {
		delay = min(delay*2, 5*time.Minute)
	}
	return delay
}

func (r *Runner) modelOutcome(role, model, kind string, callErr error) error {
	h := r.State.modelHealth(role, model)
	if h == nil {
		return errors.New("model result has no recorded attempt")
	}
	now := r.routeNow().UTC()
	if callErr == nil {
		h.Successes++
		h.ConsecutiveFailures = 0
		h.CooldownUntil = ""
		h.LastSuccessAt = now.Format(time.RFC3339Nano)
		r.note("model-response", role+" · "+model+" returned a usable response; project checks still decide correctness")
	} else {
		h.Failures++
		h.ConsecutiveFailures++
		h.LastFailureAt = now.Format(time.RFC3339Nano)
		h.FailureKind, h.LastError = kind, Tail(Clean(callErr.Error()), 700)
		h.CooldownUntil = ""
		if delay := modelCooldown(kind, h.ConsecutiveFailures); delay > 0 {
			h.CooldownUntil = now.Add(delay).Format(time.RFC3339Nano)
		}
		r.note("model-failure", role+" · "+model+" · "+kind+" · "+h.LastError)
	}
	return r.checkpoint()
}

// Never use saved health as evidence of current pricing or availability.
func (r *Runner) freeModels(ctx context.Context, role, requested, preferred string, rotation int) ([]string, error) {
	models, err := r.Discover(ctx)
	if err != nil {
		return nil, err
	}
	models = Unique(models)
	if len(models) == 0 {
		return nil, fmt.Errorf("no verified zero-price %s models are available", role)
	}
	if requested != "" && requested != "auto" {
		if !contains(models, requested) {
			return nil, fmt.Errorf("the chosen %s is unavailable or no longer verified as free; choose /models", role)
		}
		return []string{requested}, nil
	}
	if rotation > 0 && len(models) > 1 {
		offset := rotation % len(models)
		models = append(append([]string{}, models[offset:]...), models[:offset]...)
	}
	if contains(models, preferred) {
		models = Unique(append([]string{preferred}, models...))
	}
	// Stable ordering preserves catalog/user preference among equally healthy models.
	sort.SliceStable(models, func(i, j int) bool {
		failures := func(model string) int {
			if h := r.State.modelHealth(role, model); h != nil {
				return h.ConsecutiveFailures
			}
			return 0
		}
		return failures(models[i]) < failures(models[j])
	})
	return models, nil
}

func (r *Runner) readyModel(role string, models []string) (string, time.Time) {
	now := r.routeNow()
	var firstRetry time.Time
	for _, model := range models {
		if h := r.State.modelHealth(role, model); h != nil && h.CooldownUntil != "" {
			until, _ := time.Parse(time.RFC3339Nano, h.CooldownUntil)
			if until.After(now) {
				if firstRetry.IsZero() || until.Before(firstRetry) {
					firstRetry = until
				}
				continue
			}
		}
		return model, time.Time{}
	}
	return "", firstRetry
}

func (r *Runner) waitForModels(ctx context.Context, role string, until time.Time) (waitErr error) {
	r.State.RoutingWait = &RoutingWait{Role: role, Until: until.UTC().Format(time.RFC3339Nano)}
	text := fmt.Sprintf("All verified free %s models are cooling down; retrying in %s", role, until.Sub(r.routeNow()).Round(time.Second))
	r.note("model-wait", text)
	r.Emit(Event{Kind: "status", Text: text})
	if err := r.checkpoint(); err != nil {
		r.State.RoutingWait = nil
		return err
	}
	defer func() {
		r.State.RoutingWait = nil
		if err := r.checkpoint(); err != nil {
			waitErr = errors.Join(waitErr, fmt.Errorf("saving the end of a routing wait: %w", err))
		}
	}()
	return r.waitUntil(ctx, until)
}

func (r *Runner) waitUntil(ctx context.Context, until time.Time) error {
	wait := r.Wait
	if wait == nil {
		wait = waitContext
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.steeringPending(); err != nil {
			return err
		}
		remaining := until.Sub(r.routeNow())
		if remaining <= 0 {
			return nil
		}
		if err := wait(ctx, min(remaining, 250*time.Millisecond)); err != nil {
			return err
		}
	}
}

type ModelRouteStatus struct {
	ModelHealth
	Status            string `json:"status"`
	RetryAfterSeconds int64  `json:"retry_after_seconds"`
}

type RoutingReport struct {
	Models  []ModelRouteStatus `json:"models"`
	Waiting *RoutingWait       `json:"waiting,omitempty"`
	Events  []RunEvent         `json:"events"`
}

func routingReport(state State, now time.Time) RoutingReport {
	report := RoutingReport{Models: []ModelRouteStatus{}, Events: []RunEvent{}}
	for _, h := range state.ModelHealth {
		row := ModelRouteStatus{ModelHealth: h, Status: "needs_catalog_check"}
		if until, err := time.Parse(time.RFC3339Nano, h.CooldownUntil); err == nil && until.After(now) {
			row.Status = "cooldown"
			row.RetryAfterSeconds = int64((until.Sub(now) + time.Second - 1) / time.Second)
		}
		if h.ConsecutiveFailures > 0 && (h.FailureKind == "auth" || h.FailureKind == "policy" || h.FailureKind == "cost" || h.FailureKind == "output_limit") {
			row.Status = "last_call_blocked"
		}
		report.Models = append(report.Models, row)
	}
	sort.Slice(report.Models, func(i, j int) bool {
		a, b := report.Models[i], report.Models[j]
		if a.Role == b.Role {
			return a.Model < b.Model
		}
		return a.Role < b.Role
	})
	if state.Status == "running" && state.RoutingWait != nil {
		copy := *state.RoutingWait
		report.Waiting = &copy
	}
	for _, event := range state.Events {
		if strings.HasPrefix(event.Kind, "model-") {
			report.Events = append(report.Events, event)
		}
	}
	if len(report.Events) > 12 {
		report.Events = report.Events[len(report.Events)-12:]
	}
	return report
}

func FormatRouting(report RoutingReport) string {
	if len(report.Models) == 0 {
		return "No free-model routing history yet."
	}
	var out strings.Builder
	out.WriteString("Free-model routing · saved observations; pricing is rechecked before each call\nUsable responses do not establish project correctness.\n")
	if report.Waiting != nil {
		fmt.Fprintf(&out, "Waiting for a %s model until %s\n", report.Waiting.Role, report.Waiting.Until)
	}
	for _, h := range report.Models {
		status := "ready for a catalog check"
		if h.RetryAfterSeconds > 0 {
			status = fmt.Sprintf("cooldown · retry in %ds", h.RetryAfterSeconds)
		}
		if h.Status == "last_call_blocked" {
			status = "last call blocked; resolve the recorded blocker before resuming"
		}
		fmt.Fprintf(&out, "%s · %s · %s\n  %d attempts · %d usable responses · %d failures\n", h.Role, h.Model, status, h.Attempts, h.Successes, h.Failures)
		if h.LastError != "" {
			fmt.Fprintf(&out, "  last failure (%s): %s\n", h.FailureKind, h.LastError)
		}
	}
	if len(report.Events) > 0 {
		out.WriteString("Recent routing:\n")
		for _, event := range report.Events {
			fmt.Fprintf(&out, "  %s · %s\n", event.At, event.Text)
		}
	}
	return strings.TrimSpace(out.String())
}
