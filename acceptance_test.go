package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcceptanceCannotPassUnmappedFailedOrStaleRequirements(t *testing.T) {
	state := State{Version: stateVersion, CheckedSnapshot: "snapshot", SuccessCriteria: []string{"Works", "Handles errors"}, RequiredChecks: []string{"unit"}, CheckResults: []CheckResult{{Command: "unit", ExitCode: 0, Output: "real unit output"}}}
	answer := Decision{Done: true, Acceptance: []AcceptanceCheck{{"Works", []string{"unit"}}}}
	if CompletionAllowed(state, answer) {
		t.Fatal("approved an unmapped requirement")
	}
	answer.Acceptance = append(answer.Acceptance, AcceptanceCheck{"Handles errors", []string{"error-test"}})
	if CompletionAllowed(state, answer) {
		t.Fatal("approved a check that never ran")
	}
	state.CheckResults = append(state.CheckResults, CheckResult{Command: "error-test", ExitCode: 1, Output: "actual failure"})
	if CompletionAllowed(state, answer) {
		t.Fatal("approved failing coverage")
	}
	state.CheckResults[1].ExitCode = 0
	if !CompletionAllowed(state, answer) {
		t.Fatal("rejected executed complete coverage")
	}
	state.CheckedSnapshot = ""
	if CompletionAllowed(state, answer) {
		t.Fatal("approved stale coverage")
	}
}

func TestAcceptanceMissingCoverageReturnsToTeamAndSurvivesResume(t *testing.T) {
	r, calls := fixtureRunner(t)
	*calls = 1
	base := r.Invoke
	sawGap := false
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		raw, err := base(ctx, c, prompt, label, planner, model)
		if !planner || err != nil {
			return raw, err
		}
		var d Decision
		json.Unmarshal([]byte(raw), &d)
		if d.Done {
			d.SuccessCriteria = append(d.SuccessCriteria, "Preserve the output contract")
			if r.State.Cycle > 1 {
				sawGap = strings.Contains(prompt, "unmapped") && strings.Contains(prompt, "Preserve the output contract")
				d.Acceptance = append(d.Acceptance, AcceptanceCheck{"Preserve the output contract", []string{checkFixtureCommand()}})
			}
		}
		data, _ := json.Marshal(d)
		return string(data), nil
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !sawGap || *calls != 2 || r.State.Cycle != 2 {
		t.Fatal("missing coverage was ignored or implementation unnecessarily repeated")
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil || len(saved.Acceptance) != 2 || !acceptancePassed(saved, Decision{}) {
		t.Fatalf("coverage lost in checkpoint: %v", err)
	}
	writeFixture(t, filepath.Join(r.Repo, "changed.txt"), "external change")
	rows, text, err := InspectRun(r.Repo, r.State.RunID, "acceptance")
	if err != nil || !strings.Contains(text, "unverified") || rows.([]AcceptanceResult)[0].Status == "passed" {
		t.Fatal("inspection trusted stale evidence")
	}
}

func TestAcceptanceConfigIsPinnedAndReplacementRetainsCoverage(t *testing.T) {
	paths, repo := testPaths(t), t.TempDir()
	config := DefaultProjectConfig()
	config.Acceptance = []AcceptanceCheck{{"Pinned behavior", []string{"original-test"}}}
	if err := AtomicJSON(filepath.Join(repo, ".buildheadway.json"), config); err != nil {
		t.Fatal(err)
	}
	r, err := NewRunner(paths, Settings{}, repo, "goal", false, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(r.State.RequiredChecks, "original-test") || len(r.State.Acceptance) != 1 {
		t.Fatal("project acceptance was not pinned")
	}
	r.State.SuggestedChecks = []string{"missing-runner"}
	r.State.Acceptance = append(r.State.Acceptance, AcceptanceCheck{"Extra behavior", []string{"missing-runner"}})
	r.State.CheckResults = []CheckResult{{Command: "missing-runner", ExitCode: 127, Output: "not found"}}
	answer := Decision{Checks: []string{"replacement-runner"}, CheckReplacements: []CheckReplacement{{"missing-runner", "replacement-runner", "Available on this platform"}}}
	if err := r.batch(answer); err != nil {
		t.Fatal(err)
	}
	if r.State.Acceptance[1].Checks[0] != "replacement-runner" || !contains(r.State.RequiredChecks, "original-test") {
		t.Fatal("replacement lost acceptance coverage")
	}
	answer.CheckReplacements = []CheckReplacement{{"original-test", "replacement-runner", "Skip requirement"}}
	if err := r.batch(answer); err == nil {
		t.Fatal("replaced a pinned acceptance test")
	}
}

func TestAcceptanceProtocolRejectsEmptyOrNoOpCoverage(t *testing.T) {
	for _, items := range [][]AcceptanceCheck{
		{{"", []string{"npm test"}}}, {{"Works", nil}}, {{"Works", []string{"echo done"}}}, {{"Works", []string{"exit 0"}}},
	} {
		if err := ValidateDecision(Decision{Acceptance: items}); err == nil {
			t.Fatalf("accepted invalid coverage: %+v", items)
		}
	}
	raw := decisionJSON(true, nil, []string{"npm test"})
	parsed, err := ParseDecision(raw)
	if err != nil || len(parsed.Acceptance) != 1 {
		t.Fatalf("coverage lost through agent protocol: %v", err)
	}
}

func TestChangesDuringVerificationInvalidateEarlierPassingChecks(t *testing.T) {
	r, _ := fixtureRunner(t)
	writeFixture(t, filepath.Join(r.Repo, "result.txt"), "good\n")
	os.MkdirAll(r.Dir, 0700)
	snapshot, err := verificationRound(context.Background(), r.Paths, r.Repo, r.Dir, r.State.Config, HookInput{}, []string{checkFixtureCommand()}, false, noEmit, nil, func(HookResult) error { return nil }, func(c CheckResult) error {
		if c.ExitCode != 0 {
			t.Fatal("fixture check should initially pass")
		}
		return os.WriteFile(filepath.Join(r.Repo, "result.txt"), []byte("broken after the check\n"), 0600)
	})
	if !errors.Is(err, errHookChangedFiles) || snapshot != "" {
		t.Fatal("accepted source modified after an earlier check")
	}
}

func TestAllAgentsReceiveSharedProjectGuidance(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, filepath.Join(repo, "AGENTS.md"), "Shared project conventions")
	writeFixture(t, filepath.Join(repo, "CLAUDE.md"), "Existing repository instructions")
	writeFixture(t, filepath.Join(repo, "BUILDHEADWAY.md"), "Tool-specific instructions")
	config := DefaultProjectConfig()
	config.ContextFiles = []string{"AGENTS.md"}
	text, err := ProjectContext(repo, config)
	if err != nil || strings.Count(text, "Shared project conventions") != 1 || !strings.Contains(text, "Existing repository instructions") || !strings.Contains(text, "Tool-specific instructions") {
		t.Fatalf("guidance missing or duplicated: %v %s", err, text)
	}
}
