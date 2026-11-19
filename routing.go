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

// FreeRoute is a view of the current selection. Offers are checked again
// before a backup is dispatched.
type FreeRoute struct {
	Role, State, Model, Next string
	Backups                  int
	Pinned                   bool
	RetryAt                  time.Time
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
		case "", "transient", "response", "execution", "auth", "policy", "quota", "unavailable", "cost", "output_limit":
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
	var quota freeQuotaError
	if errors.As(err, &quota) {
		return "quota"
	}
	var unavailable freeOfferUnavailableError
	if errors.As(err, &unavailable) {
		return "unavailable"
	}
	var localRequest *localPiRequestError
	if errors.As(err, &localRequest) {
		return localRequest.failureKind()
	}
	var authentication authError
	if errors.As(err, &authentication) || authRE.MatchString(err.Error()) {
		return "auth"
	}
	var mismatch freeRouteMismatchError
	if errors.As(err, &mismatch) {
		return "policy"
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

func hardModelFailure(err error) bool {
	if err == nil {
		return false
	}
	var promptLimit promptLimitError
	if errors.As(err, &promptLimit) {
		return true
	}
	var setup mcpSetupError
	if errors.As(err, &setup) {
		return true
	}
	switch modelFailureKind(err, false) {
	case "auth", "cost", "policy", "quota", "output_limit":
		return true
	}
	return false
}

func modelCooldown(kind string, failures int) time.Duration {
	switch kind {
	case "auth", "policy", "quota", "cost", "output_limit":
		return 0
	case "unavailable":
		// A catalog can briefly advertise an offer after the server drops it.
		return 15 * time.Minute
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
		if kind == "quota" {
			r.Emit(Event{Kind: "free-route", Value: FreeRoute{Role: role, State: "quota", Model: model}})
		}
	}
	return r.checkpoint()
}

func pinnedModel(model string) bool { return model != "" && model != "auto" }

// Never use saved health as evidence of current pricing or availability.
func (r *Runner) freeModels(ctx context.Context, role, requested, preferred string, rotation int, allowOptional bool) ([]string, error) {
	pinned := pinnedModel(requested)
	r.Emit(Event{Kind: "free-route", Value: FreeRoute{Role: role, State: "checking", Pinned: pinned}})
	models, err := r.Discover(ctx)
	if err != nil && !errors.Is(err, errNoVerifiedFreeModels) {
		return nil, err
	}
	models = Unique(models)
	if pinned {
		if !contains(models, requested) {
			r.Emit(Event{Kind: "free-route", Value: FreeRoute{Role: role, State: "empty", Model: requested, Pinned: true}})
			return nil, limitError{fmt.Sprintf("the chosen %s is unavailable or no longer verified as free; inspect /routing", role)}
		}
		models = []string{requested}
	}
	eligible := make([]string, 0, len(models))
	var firstRecheck time.Time
	now := r.routeNow()
	for _, model := range models {
		if optionalFreeRoute(model) && !allowOptional {
			continue
		}
		if _, blocked := r.limitedPlatforms[freePlatformForModel(model)]; blocked {
			continue
		}
		if until := r.offerHold(model); until.After(now) {
			if firstRecheck.IsZero() || until.Before(firstRecheck) {
				firstRecheck = until
			}
			continue
		}
		eligible = append(eligible, model)
	}
	models = eligible
	if len(models) == 0 {
		for _, platform := range append([]string{"opencode"}, r.freePlatforms...) {
			if quota, held := r.limitedPlatforms[platform]; held && (platform == "opencode" || allowOptional) {
				r.Emit(Event{Kind: "free-route", Value: FreeRoute{Role: role, State: "quota"}})
				return nil, quota
			}
		}
		route := FreeRoute{Role: role, State: "empty", Pinned: pinned, RetryAt: firstRecheck}
		if pinned {
			route.Model = requested
		}
		r.Emit(Event{Kind: "free-route", Value: route})
		if !firstRecheck.IsZero() {
			return nil, limitError{fmt.Sprintf("verified free %s offers are unavailable; inspect /routing or use /resume after %s", role, firstRecheck.Local().Format(time.RFC3339))}
		}
		return nil, limitError{fmt.Sprintf("no verified zero-price %s models are available; inspect /models and use /resume when offers return", role)}
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

func (r *Runner) parkFreePlatform(role, requested, model string, allowOptional bool) bool {
	if pinnedModel(requested) || !allowOptional || len(r.freePlatforms) == 0 {
		return false
	}
	platform := freePlatformForModel(model)
	if platform == "" || (platform != "opencode" && !contains(r.freePlatforms, platform)) {
		return false
	}
	if r.limitedPlatforms == nil {
		r.limitedPlatforms = map[string]freeQuotaError{}
	}
	r.limitedPlatforms[platform] = freeQuotaError{platform: platform}
	message := platform + " reached its free limit · checking the other enabled platforms"
	r.note("model-platform-limit", role+" · "+message)
	r.Emit(Event{Kind: "status", Text: message})
	return true
}

func (r *Runner) hasActiveMCP() bool {
	for _, server := range r.State.Config.MCPServers {
		if !server.Disabled {
			return true
		}
	}
	return false
}

func (r *Runner) offerHold(model string) time.Time {
	var latest time.Time
	for _, role := range []string{"planner", "worker"} {
		h := r.State.modelHealth(role, model)
		if h != nil && h.FailureKind == "unavailable" && h.ConsecutiveFailures > 0 {
			if until, _ := time.Parse(time.RFC3339Nano, h.CooldownUntil); until.After(latest) {
				latest = until
			}
		}
	}
	return latest
}

func (r *Runner) readyRoute(role string, models []string, pinned bool) FreeRoute {
	now := r.routeNow()
	route := FreeRoute{Role: role, State: "cooldown", Pinned: pinned}
	for _, model := range models {
		if h := r.State.modelHealth(role, model); h != nil && h.CooldownUntil != "" {
			until, _ := time.Parse(time.RFC3339Nano, h.CooldownUntil)
			if until.After(now) {
				if route.RetryAt.IsZero() || until.Before(route.RetryAt) {
					route.RetryAt = until
				}
				continue
			}
		}
		if route.Model == "" {
			route.Model = model
			continue
		}
		if route.Next == "" {
			route.Next = model
		}
		route.Backups++
	}
	if route.Model != "" {
		route.State, route.RetryAt = "selected", time.Time{}
	}
	r.Emit(Event{Kind: "free-route", Value: route})
	return route
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
			if h.FailureKind == "unavailable" {
				row.Status = "offer_unavailable"
			}
			row.RetryAfterSeconds = int64((until.Sub(now) + time.Second - 1) / time.Second)
		}
		if h.ConsecutiveFailures > 0 && (h.FailureKind == "auth" || h.FailureKind == "policy" || h.FailureKind == "quota" || h.FailureKind == "cost" || h.FailureKind == "output_limit") {
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
		if h.Status == "offer_unavailable" {
			status = fmt.Sprintf("offer unavailable · recheck in %s", (time.Duration(h.RetryAfterSeconds) * time.Second).String())
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
