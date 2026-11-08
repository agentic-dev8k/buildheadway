package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func councilRequestJSON(t *testing.T, check, criterion string) string {
	t.Helper()
	var answer Decision
	if err := json.Unmarshal([]byte(decisionJSON(false, []Task{{"save", "Save state", "Preserve state and verify it."}}, []string{check})), &answer); err != nil {
		t.Fatal(err)
	}
	answer.SuccessCriteria = []string{}
	answer.Checks = []string{}
	answer.Acceptance = []AcceptanceCheck{{criterion, []string{check}}}
	data, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertCouncilRequest(t *testing.T, state State, check, criterion string) {
	t.Helper()
	if !contains(state.SuggestedChecks, check) || !contains(state.SuccessCriteria, criterion) {
		t.Fatalf("senior verification request was lost: checks=%v criteria=%v", state.SuggestedChecks, state.SuccessCriteria)
	}
	for _, item := range state.Acceptance {
		if item.Criterion == criterion && contains(item.Checks, check) {
			return
		}
	}
	t.Fatal("senior acceptance mapping was lost", state.Acceptance)
}

func TestCouncilRetainsRequestsWhenPlanningEvidenceChanges(t *testing.T) {
	r, _ := councilFixture(t)
	check, criterion := "node retained-check.cjs", "Saved state survives restart"
	proposals := 0
	r.Invoke = func(_ context.Context, _ Connection, _ string, label string, _ bool, _ string) (string, error) {
		if strings.HasSuffix(label, "-proposal") {
			proposals++
			if proposals == 1 {
				return councilRequestJSON(t, check, criterion), nil
			}
		}
		if proposals == 1 && strings.HasSuffix(label, "-critique") {
			writeFixture(t, filepath.Join(r.Repo, "concurrent.txt"), "changed")
		}
		return decisionJSON(false, []Task{{"fix", "Implement", "Finish the requested goal."}}, nil), nil
	}
	answer, err := r.askPlanner(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if proposals != 2 || r.State.PlannerCalls != 5 {
		t.Fatalf("retaining requests restarted an unchanged council: %d proposals, %d calls", proposals, r.State.PlannerCalls)
	}
	if err := r.batch(answer); err != nil {
		t.Fatal(err)
	}
	assertCouncilRequest(t, r.State, check, criterion)
}

func TestCouncilRetainsRequestsThroughBudgetPauseAndFreshResume(t *testing.T) {
	r, workers := councilFixture(t)
	check, criterion := "node retained-check.cjs", "Persisted records remain correct"
	writeFixture(t, filepath.Join(r.Repo, "retained-check.cjs"), `const assert = require('node:assert/strict'); const fs = require('node:fs'); assert.deepEqual(JSON.parse(fs.readFileSync('saved.json', 'utf8')), {persisted:true});`)
	r.MaxCalls = 1
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		return councilRequestJSON(t, check, criterion), nil
	}
	var limit limitError
	if err := r.Run(context.Background()); !errors.As(err, &limit) || r.State.Status != "paused" || *workers != 0 || r.State.PlannerCalls != 1 {
		t.Fatalf("call budget did not stop before the next senior: %v", err)
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	assertCouncilRequest(t, saved, check, criterion)
	if saved.Council == nil || saved.Council.Next != 1 || len(saved.CheckResults) != 0 || CompletionAllowed(saved, Decision{Done: true}) {
		t.Fatal("request retention invented verification or approval")
	}
	writeFixture(t, filepath.Join(r.Repo, "changed-while-paused.txt"), "invalidate the old votes")
	settings := Settings{Planner: Connection{Backend: "pi"}, Worker: Connection{Backend: "invalid"}}
	resumed, err := ResumeRunner(r.Paths, settings, r.Repo, saved.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resumed.MaxCalls = 25
	resumed.Discover = r.Discover
	// Use this goal's files; replies are simulated, but checks execute normally.
	attempts, reviews := 0, 0
	resumed.Invoke = func(_ context.Context, c Connection, prompt, label string, planner bool, _ string) (string, error) {
		if planner {
			if c.Backend != "codex" && c.Backend != "claude" {
				t.Fatal("resume changed the pinned senior team")
			}
			if !strings.Contains(prompt, criterion) || !strings.Contains(prompt, check) {
				t.Fatal("the new council did not receive retained requirements")
			}
			if strings.Contains(label, "-review-") {
				reviews++
				return decisionJSON(true, nil, nil), nil
			}
			return decisionJSON(false, []Task{{"save", "Save state", "Finish state persistence."}}, nil), nil
		}
		attempts++
		writeFixture(t, filepath.Join(r.Repo, "result.txt"), "good\n")
		writeFixture(t, filepath.Join(r.Repo, "saved.json"), `{"persisted":true}`)
		return "Saved the requested state. Verification remains for the harness.", nil
	}
	if err := resumed.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resumed.State.Status != "complete" || attempts != 1 || reviews != 3 || resumed.State.WorkerCalls != 1 {
		t.Fatalf("resume repeated work or skipped fresh approval: attempts=%d reviews=%d status=%s", attempts, reviews, resumed.State.Status)
	}
	assertCouncilRequest(t, resumed.State, check, criterion)
	for _, result := range resumed.State.CheckResults {
		if result.Command == check && result.Passed() {
			return
		}
	}
	t.Fatal("completion omitted the retained executable acceptance check")
}

func TestCouncilRequirementInspectionLeavesCheckpointUnchanged(t *testing.T) {
	r, _ := councilFixture(t)
	r.MaxCalls = 1
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		return councilRequestJSON(t, "node retained.cjs", "Inspect retained requests"), nil
	}
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("expected the configured budget pause")
	}
	file := filepath.Join(r.Dir, "state.json")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	_, text, err := InspectRun(r.Repo, r.State.RunID, "acceptance")
	if err != nil || !strings.Contains(text, "Inspect retained requests") || !strings.Contains(text, "unverified") {
		t.Fatalf("saved request is not visible as unverified: %v %s", err, text)
	}
	after, err := os.ReadFile(file)
	if err != nil || string(before) != string(after) {
		t.Fatal("acceptance inspection rewrote a paused checkpoint")
	}
}

func TestCouncilRetainsRequirementsFromAChangedReview(t *testing.T) {
	r, workers := councilFixture(t)
	*workers = 1 // The first actual worker produces passing output.
	check, criterion := "node review-check.cjs", "Review changes receive executable coverage"
	writeFixture(t, filepath.Join(r.Repo, "review-check.cjs"), `const assert = require('node:assert/strict'); const fs = require('node:fs'); assert.equal(fs.readFileSync('review-note.txt', 'utf8'), 'verify this change');`)
	base := r.Invoke
	changed := false
	reviews := 0
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if strings.Contains(label, "-review-") {
			reviews++
			if !changed {
				changed = true
				writeFixture(t, filepath.Join(r.Repo, "review-note.txt"), "verify this change")
				return councilRequestJSON(t, check, criterion), nil
			}
		}
		return base(ctx, c, prompt, label, planner, model)
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.State.Status != "complete" || r.State.WorkerCalls != 1 || reviews != 4 || r.State.Cycle != 2 {
		t.Fatalf("review recovery repeated implementation or skipped fresh votes: workers=%d reviews=%d cycle=%d", r.State.WorkerCalls, reviews, r.State.Cycle)
	}
	assertCouncilRequest(t, r.State, check, criterion)
	if !acceptancePassed(r.State, Decision{}) || !strings.Contains(FormatDiscussion(r.State), "Votes discarded") {
		t.Fatal("changed review reused old approval or lost acceptance evidence")
	}
	found := false
	for _, result := range r.State.CheckResults {
		found = found || result.Command == check && result.Passed()
	}
	if !found {
		t.Fatal("review requirement completed without executing its real Node assertion")
	}
}

func TestLegacyCouncilRequestsSurviveInvalidationBeforeAnotherCall(t *testing.T) {
	for _, phase := range []string{"planning", "reviewing"} {
		t.Run(phase, func(t *testing.T) {
			r, _ := councilFixture(t)
			check, criterion := "node legacy-request.cjs", "Retain earlier saved council requests"
			writeFixture(t, filepath.Join(r.Repo, "legacy-request.cjs"), `const assert = require('node:assert/strict'); const fs = require('node:fs'); assert.equal(fs.readFileSync('result.txt','utf8'), 'good\n');`)
			writeFixture(t, filepath.Join(r.Repo, "result.txt"), "good\n")
			answer, err := ParseDecision(councilRequestJSON(t, check, criterion))
			if err != nil {
				t.Fatal(err)
			}
			// Older partial rounds have not copied accepted requests into State.
			r.State.Status, r.State.PlannerCalls, r.State.Phase = "paused", 1, phase
			r.State.Council = &CouncilRound{Cycle: 1, Review: phase == "reviewing", Fingerprint: strings.Repeat("0", 64), Next: 1, Decisions: []Decision{answer}}
			if err := r.State.Save(r.Dir); err != nil {
				t.Fatal(err)
			}
			resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
			if err != nil {
				t.Fatal(err)
			}
			resumed.MaxCalls = 1
			resumed.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
				t.Fatal("exhausted call budget dispatched a model")
				return "", nil
			}
			var limit limitError
			if err := resumed.Run(context.Background()); !errors.As(err, &limit) || resumed.State.PlannerCalls != 1 || resumed.State.WorkerCalls != 0 {
				t.Fatalf("legacy recovery bypassed call budgets: %v", err)
			}
			saved, _, err := LoadRun(r.Repo, r.State.RunID)
			if err != nil {
				t.Fatal(err)
			}
			assertCouncilRequest(t, saved, check, criterion)
			if saved.Version != stateVersion || saved.Council == nil || saved.Council.Next != 0 || CompletionAllowed(saved, Decision{Done: true}) {
				t.Fatal("legacy request recovery invented votes, approval or a new saved schema")
			}
			if phase == "planning" && len(saved.CheckResults) != 0 || phase == "reviewing" && (len(saved.CheckResults) != 2 || !acceptancePassed(saved, Decision{})) {
				t.Fatal("resumed verification did not retain its actual execution evidence")
			}
		})
	}
}

func TestLegacyUnchangedCouncilImportsRequestsWithoutRepeatingTheProposal(t *testing.T) {
	r, _ := councilFixture(t)
	check, criterion := "node legacy-request.cjs", "Preserve accepted legacy proposals"
	answer, err := ParseDecision(councilRequestJSON(t, check, criterion))
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureIgnore(r.Repo); err != nil {
		t.Fatal(err)
	}
	if err := r.refreshProjectContext(); err != nil {
		t.Fatal(err)
	}
	fingerprint, _, err := r.councilFingerprint(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	r.State.Status, r.State.PlannerCalls = "paused", 1
	r.State.Council = &CouncilRound{Cycle: 1, Fingerprint: fingerprint, Next: 1, Decisions: []Decision{answer}}
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resumed.MaxCalls = 2
	resumed.Invoke = func(_ context.Context, c Connection, prompt, label string, _ bool, _ string) (string, error) {
		if !strings.Contains(label, "critic-critique") || c.Backend != "claude" || !strings.Contains(prompt, criterion) {
			t.Fatal("importing retained requests repeated an unchanged accepted proposal")
		}
		return "", authError{"claude", "sign-in required"}
	}
	if err := resumed.Run(context.Background()); err == nil || !hardModelFailure(err) || resumed.State.PlannerCalls != 2 {
		t.Fatalf("legacy council recovery bypassed the expected blocker: %v", err)
	}
	assertCouncilRequest(t, resumed.State, check, criterion)
}

func TestRetainedCouncilRequestsKeepProviderBlockersAndReplacementGates(t *testing.T) {
	for _, blocker := range []error{authError{"claude", "sign-in required"}, costError{}, outputLimitError{}, mcpSetupError{"missing tool credentials"}} {
		t.Run(blocker.Error(), func(t *testing.T) {
			r, workers := councilFixture(t)
			check, criterion := "node account-independent.cjs", "Keep requests across a provider blocker"
			r.Invoke = func(_ context.Context, _ Connection, _ string, label string, _ bool, _ string) (string, error) {
				if strings.HasSuffix(label, "-proposal") {
					return councilRequestJSON(t, check, criterion), nil
				}
				return "", blocker
			}
			err := r.Run(context.Background())
			if err == nil || !hardModelFailure(err) || r.State.Status != "blocked" || r.State.PlannerCalls != 2 || *workers != 0 {
				t.Fatalf("provider blocker was retried or bypassed: %v", err)
			}
			assertCouncilRequest(t, r.State, check, criterion)
		})
	}
	r, _ := councilFixture(t)
	old, next, criterion := "missing-check-runner", "node replacement.cjs", "Keep existing verification until explicit replacement"
	r.State.SuggestedChecks = []string{old}
	r.State.Acceptance = []AcceptanceCheck{{criterion, []string{old}}}
	r.State.CheckResults = []CheckResult{{Command: old, ExitCode: 127}}
	r.MaxCalls = 1
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		var answer Decision
		json.Unmarshal([]byte(councilRequestJSON(t, next, criterion)), &answer)
		answer.Checks = []string{next}
		answer.CheckReplacements = []CheckReplacement{{Old: old, New: next, Reason: "The older command was unavailable."}}
		data, _ := json.Marshal(answer)
		return string(data), nil
	}
	if err := r.Run(context.Background()); err == nil || r.State.Status != "paused" {
		t.Fatal("expected the explicit planner budget to pause the round")
	}
	assertCouncilRequest(t, r.State, old, criterion)
	assertCouncilRequest(t, r.State, next, criterion)
	if r.State.CheckResults[0].ExitCode != 127 || len(r.State.Tasks) != 0 || r.State.WorkerCalls != 0 || CompletionAllowed(r.State, Decision{Done: true}) {
		t.Fatal("request intake applied a replacement, assignment or passing result before a final decision")
	}
}

func TestCouncilRequirementLimitBlocksWithoutDroppingOrPoisoningSavedCoverage(t *testing.T) {
	r, _ := councilFixture(t)
	for i := 0; i < 100; i++ {
		r.State.Acceptance = append(r.State.Acceptance, AcceptanceCheck{Criterion: fmt.Sprintf("Retained requirement %d", i), Checks: []string{checkFixtureCommand()}})
	}
	before, err := json.Marshal(r.State.Acceptance)
	if err != nil {
		t.Fatal(err)
	}
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		return councilRequestJSON(t, "node extra.cjs", "A new requirement beyond the aggregate limit"), nil
	}
	if err := r.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "at most 100") || r.State.Status != "blocked" || r.State.PlannerCalls != 1 || r.State.WorkerCalls != 0 {
		t.Fatalf("oversized coverage was dropped, retried or accepted: %v", err)
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil {
		t.Fatalf("coverage intake poisoned the checkpoint: %v", err)
	}
	after, err := json.Marshal(saved.Acceptance)
	if err != nil || string(before) != string(after) || contains(saved.SuggestedChecks, "node extra.cjs") {
		t.Fatal("failed request intake partially changed retained obligations")
	}
}
