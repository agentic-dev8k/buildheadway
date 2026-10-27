package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func councilFixture(t *testing.T) (*Runner, *int) {
	t.Helper()
	r, workers := fixtureRunner(t)
	r.Settings.Seniors = []SeniorAgent{{Name: "critic", Connection: Connection{Backend: "claude", Model: "opus"}}}
	if err := r.EnableFigureOut("full"); err != nil {
		t.Fatal(err)
	}
	r.MaxCalls = 25
	return r, workers
}
func withSummary(raw, summary string) string {
	var d Decision
	json.Unmarshal([]byte(raw), &d)
	d.Summary = summary
	b, _ := json.Marshal(d)
	return string(b)
}
func TestCouncilPassesProposalsCritiquesAssignmentsAndResults(t *testing.T) {
	r, workers := councilFixture(t)
	invoke := r.Invoke
	seen := map[string]int{}
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if planner {
			if strings.Contains(label, "-proposal") {
				seen["proposal"]++
			}
			if strings.Contains(label, "-critique") {
				if c.Backend != "claude" || !strings.Contains(prompt, "LEAD_SIGNAL") {
					t.Fatal("critic did not receive the lead proposal")
				}
				seen["critique"]++
			}
			if strings.Contains(label, "-decision") {
				if !strings.Contains(prompt, "REVIEWER_SIGNAL") {
					t.Fatal("lead did not receive peer critique")
				}
				seen["synthesis"]++
			}
			if strings.Contains(label, "-review-") && !strings.Contains(prompt, "Work attempted") {
				t.Fatal("worker reports were not delivered to seniors")
			}
		} else if !strings.Contains(prompt, "REVIEWER_SIGNAL") || !strings.Contains(prompt, "SENIOR DISCUSSION") {
			t.Fatal("worker did not receive the senior handoff")
		}
		raw, err := invoke(ctx, c, prompt, label, planner, model)
		if planner && err == nil {
			summary := "LEAD_SIGNAL: inspect and implement the correct output"
			if c.Backend == "claude" {
				summary = "REVIEWER_SIGNAL: verify actual output and error handling"
			}
			raw = withSummary(raw, summary)
		}
		return raw, err
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.State.Status != "complete" || *workers != 2 || r.State.PlannerCalls != 9 {
		t.Fatalf("council skipped repair or review: calls=%d workers=%d", r.State.PlannerCalls, *workers)
	}
	for _, phase := range []string{"proposal", "critique", "synthesis"} {
		if seen[phase] != 3 {
			t.Fatalf("missing council stage %s", phase)
		}
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Council.Consensus || !strings.Contains(FormatDiscussion(saved), "verification") || !strings.Contains(MarkdownReport(saved), "REVIEWER_SIGNAL") {
		t.Fatal("council evidence was not saved or exported")
	}
}
func TestSeniorVetoCannotBeOverriddenByLead(t *testing.T) {
	r, workers := councilFixture(t)
	*workers = 1 // First implementation already satisfies the executable check.
	invoke := r.Invoke
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if planner && r.State.Cycle == 1 && strings.Contains(label, "review-critic") {
			return decisionJSON(false, []Task{{"edge-case", "Inspect edge cases", "Inspect the implementation's edge cases and preserve the passing test."}}, nil), nil
		}
		return invoke(ctx, c, prompt, label, planner, model)
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.State.Cycle != 2 || *workers != 3 || len(r.State.TaskHistory) != 2 || r.State.TaskHistory[1].Task.Title != "Inspect edge cases" {
		t.Fatal("lead ignored the critic's veto")
	}
}
func TestCouncilResumesPartialPlanningWithPinnedTeam(t *testing.T) {
	r, workers := councilFixture(t)
	invoke := r.Invoke
	interrupted := false
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if strings.Contains(label, "-critique") && !interrupted {
			interrupted = true
			return "", context.Canceled
		}
		return invoke(ctx, c, prompt, label, planner, model)
	}
	err := r.Run(context.Background())
	if !errors.Is(err, context.Canceled) || r.State.Council.Next != 1 || *workers != 0 {
		t.Fatalf("partial council was lost: %v", err)
	}
	changed := Settings{Planner: Connection{Backend: "pi"}, Worker: Connection{Backend: "invalid"}}
	resumed, err := ResumeRunner(r.Paths, changed, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Settings.Worker.Backend != "opencode" || resumed.MaxStalledCycles != 0 {
		t.Fatal("resume discarded pinned configuration")
	}
	resumed.Discover = r.Discover
	first := true
	resumed.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if first {
			first = false
			if !strings.Contains(label, "critic-critique") || c.Backend != "claude" {
				t.Fatal("resume repeated finished proposal or changed the team")
			}
		}
		return invoke(ctx, c, prompt, label, planner, model)
	}
	if err := resumed.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resumed.State.Status != "complete" || resumed.State.Access != "full" {
		t.Fatal("resume failed to finish saved figureout")
	}
}
func TestCouncilInvalidatesVotesWhenWorkspaceChanges(t *testing.T) {
	r, _ := councilFixture(t)
	if err := os.MkdirAll(r.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	proposals := 0
	r.Invoke = func(_ context.Context, _ Connection, _ string, label string, _ bool, _ string) (string, error) {
		if strings.HasSuffix(label, "-proposal") {
			proposals++
		}
		if proposals == 1 && strings.HasSuffix(label, "-critique") {
			if err := os.WriteFile(filepath.Join(r.Repo, "concurrent.txt"), []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return decisionJSON(false, []Task{{"fix", "Fix", "Implement"}}, nil), nil
	}
	if _, err := r.askPlanner(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if proposals != 2 || r.State.PlannerCalls != 5 {
		t.Fatal("stale council votes were reused")
	}
}
func TestCouncilCompletionRejectsNoOpChecksMissingCriteriaAndBuildHeadwaydConsensus(t *testing.T) {
	r, _ := councilFixture(t)
	r.State.SuccessCriteria = []string{"Output is correct"}
	d := Decision{Done: true}
	r.State.CheckedSnapshot = "current"
	r.State.Council = &CouncilRound{Cycle: 1, Review: true, Complete: true, Consensus: true, Final: d, Decisions: []Decision{d, d, d}}
	for _, command := range []string{"echo success", "true", "exit 0", "Write-Output passed"} {
		r.State.RequiredChecks = []string{command}
		r.State.CheckResults = []CheckResult{{Command: command, ExitCode: 0, Output: "success"}}
		if CompletionAllowed(r.State, d) {
			t.Fatalf("accepted empty verification: %s", command)
		}
	}
	r.State.RequiredChecks = []string{checkFixtureCommand()}
	r.State.CheckResults = []CheckResult{{Command: checkFixtureCommand(), ExitCode: 0, Output: ""}}
	r.State.Acceptance = []AcceptanceCheck{{"Output is correct", []string{checkFixtureCommand()}}}
	if !CompletionAllowed(r.State, d) {
		t.Fatal("rejected actual check and unanimous review")
	}
	r.State.SuccessCriteria = nil
	r.State.Acceptance = nil
	if CompletionAllowed(r.State, d) {
		t.Fatal("accepted missing acceptance criteria")
	}
	r.State.SuccessCriteria = []string{"Correct"}
	r.State.Council.Consensus = false
	if CompletionAllowed(r.State, d) {
		t.Fatal("ignored missing consensus")
	}
}
func TestFigureOutRespectsExplicitBudgetsAndFreePricing(t *testing.T) {
	r, workers := councilFixture(t)
	r.MaxCalls = 2
	if err := r.Run(context.Background()); err == nil || r.State.Status != "paused" || *workers != 0 || r.State.PlannerCalls != 2 {
		t.Fatal("senior calls escaped the call budget")
	}
	r, workers = councilFixture(t)
	r.Discover = func(context.Context) ([]string, error) { return nil, errors.New("no verified free prices") }
	if err := r.Run(context.Background()); err == nil || *workers != 0 || r.State.Status != "blocked" {
		t.Fatal("figureout bypassed the free worker gate")
	}
}
func TestTeamChangesAndQueuePreserveMode(t *testing.T) {
	r, _ := councilFixture(t)
	settings, err := AttachSenior(r.Settings, SeniorAgent{Name: "google", Connection: Connection{Backend: "gemini", Model: "account-model"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveSettings(r.Paths, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSettings(r.Paths)
	if err != nil || len(loaded.Seniors) != 2 {
		t.Fatalf("team not saved: %v", err)
	}
	loaded, err = DetachSenior(loaded, "critic")
	if err != nil || len(loaded.Seniors) != 1 {
		t.Fatal("detach failed")
	}
	if _, err := AttachSenior(loaded, SeniorAgent{Name: "lead", Connection: Connection{Backend: "codex"}}, false); err == nil {
		t.Fatal("duplicate lead was accepted")
	}
	item, err := QueueAddMode(r.Repo, "Finish the app", "figureout")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := claimQueuedRunner(r.Paths, loaded, r.Repo, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.State.Mode != "figureout" || claimed.State.Access != "full" || claimed.State.QueueID != item.ID {
		t.Fatal("queue dropped autonomous mode")
	}
	state, _, err := LoadRun(r.Repo, claimed.State.RunID)
	if err != nil || state.Mode != "figureout" {
		t.Fatalf("queued mode was not checkpointed: %v", err)
	}
}
func TestFigureOutUIQueueAndHelp(t *testing.T) {
	r, _ := councilFixture(t)
	m := newApp(r.Paths, r.Repo, "", false, false)
	m.busy, m.connected, m.settings = true, true, r.Settings
	if _, handled := m.projectCommand("/figureout Build the next app"); !handled {
		t.Fatal("figureout not routed")
	}
	queue, err := ReadQueue(r.Repo)
	if err != nil || len(queue.Items) != 1 || queue.Items[0].Mode != "figureout" || m.initialFigureOut {
		t.Fatalf("UI lost queued mode: %v", err)
	}
}

// Real subprocess probe: exercises argv, stdin, env and adapter response
// parsing on every native CI OS without requiring an account or model call.
func TestMain(m *testing.M) {
	if capture := os.Getenv("BUILDHEADWAY_TEST_ADAPTER_CAPTURE"); capture != "" {
		if contains(os.Args[1:], "--help") {
			os.Stdout.WriteString("--pure --format --model --file --dir --auto --no-mcp --no-approve --output-format --prompt --approval-mode --mcp-config --strict-mcp-config")
			os.Exit(0)
		}
		input, _ := io.ReadAll(os.Stdin)
		data, _ := json.Marshal(map[string]any{"args": os.Args[1:], "stdin": string(input), "permission": os.Getenv("OPENCODE_PERMISSION"), "opencode_config": os.Getenv("OPENCODE_CONFIG_CONTENT")})
		if err := os.WriteFile(capture, data, 0600); err != nil {
			os.Exit(2)
		}
		if path := os.Getenv("BUILDHEADWAY_TEST_ADAPTER_OUTPUT_FILE"); path != "" {
			file, err := os.Open(path)
			if err != nil {
				os.Exit(2)
			}
			io.Copy(os.Stdout, file)
			file.Close()
			if os.Getenv("BUILDHEADWAY_TEST_ADAPTER_HOLD_OUTPUT") != "" {
				select {
				case <-time.After(30 * time.Second):
				}
			}
		} else {
			os.Stdout.WriteString(os.Getenv("BUILDHEADWAY_TEST_ADAPTER_REPLY"))
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestNativeAdapterFullAccessAndNormalPermissions(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, backend := range []string{"codex", "claude", "opencode", "pi", "cline", "gemini"} {
		for _, mode := range []string{"normal", "full", "consultation"} {
			t.Run(backend+"-"+mode, func(t *testing.T) {
				r, _ := fixtureRunner(t)
				full := mode == "full"
				if mode != "normal" {
					if err := r.EnableFigureOut("full"); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.MkdirAll(r.Dir, 0700); err != nil {
					t.Fatal(err)
				}
				capture := filepath.Join(t.TempDir(), "args.json")
				t.Setenv("BUILDHEADWAY_TEST_ADAPTER_CAPTURE", capture)
				t.Setenv("BUILDHEADWAY_TEST_ADAPTER_REPLY", decisionJSON(true, nil, nil))
				c := Connection{Backend: backend, Model: "provider/model", Executable: binary}
				if backend == "codex" {
					c.Reasoning = "max"
				}
				label := "plan"
				if mode == "consultation" {
					label = "dialogue-lead-answer"
				}
				_, err := r.invokeCLI(context.Background(), c, "Inspect the actual project", label, true, "")
				if err != nil {
					t.Fatal(err)
				}
				var got struct {
					Args       []string `json:"args"`
					Stdin      string   `json:"stdin"`
					Permission string   `json:"permission"`
				}
				if err := ReadJSON(capture, &got); err != nil {
					t.Fatal(err)
				}
				if got.Stdin != "Inspect the actual project" {
					t.Fatal("prompt was lost")
				}
				switch backend {
				case "codex":
					if contains(got.Args, "--dangerously-bypass-approvals-and-sandbox") != full || contains(got.Args, "--sandbox") == full || !contains(got.Args, `model_reasoning_effort="max"`) {
						t.Fatalf("wrong Codex permissions: %v", got.Args)
					}
				case "claude":
					if contains(got.Args, "bypassPermissions") != full || contains(got.Args, "--tools") == full || !contains(got.Args, "--json-schema") {
						t.Fatalf("wrong Claude permissions: %v", got.Args)
					}
				case "opencode":
					var p map[string]any
					json.Unmarshal([]byte(got.Permission), &p)
					if (p["external_directory"] == "allow") != full || p["question"] != "deny" || p["task"] != "deny" || contains(got.Args, "--auto") != full {
						t.Fatalf("wrong OpenCode permissions: %v %v", p, got.Args)
					}
					if !full && (p["todowrite"] != "allow" || p["todoread"] != "allow" || p["*"] != "ask") {
						t.Fatal("advice must support native session bookkeeping while preserving file and shell approval gates")
					}
				case "pi":
					if contains(got.Args, "--tools") == full {
						t.Fatalf("wrong Pi tools: %v", got.Args)
					}
				case "cline":
					if contains(got.Args, "--plan") == full {
						t.Fatalf("wrong Cline mode: %v", got.Args)
					}
				case "gemini":
					if contains(got.Args, "yolo") != full || !contains(got.Args, "--output-format") {
						t.Fatalf("wrong Gemini mode: %v", got.Args)
					}
				}
			})
		}
	}
}
func TestGeminiStructuredResponseAndError(t *testing.T) {
	data, _ := json.Marshal(map[string]any{"response": decisionJSON(true, nil, nil), "stats": map[string]any{}})
	if d, err := ParseDecision(string(data)); err != nil || !d.Done {
		t.Fatalf("Gemini JSON rejected: %v", err)
	}
	if _, err := ResponseTexts(`{"response":"done","error":{"message":"not logged in"}}`); err == nil {
		t.Fatal("Gemini error reported as success")
	}
}

func TestAttachedFreeSeniorRejectsAReportedCharge(t *testing.T) {
	r, _ := councilFixture(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r.Settings.Seniors = []SeniorAgent{{Name: "free-reviewer", Connection: Connection{Backend: "opencode", Model: "auto", Executable: binary}}}
	if err := r.EnableFigureOut("full"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUILDHEADWAY_TEST_ADAPTER_CAPTURE", filepath.Join(t.TempDir(), "captured.json"))
	t.Setenv("BUILDHEADWAY_TEST_ADAPTER_REPLY", `{"type":"step_finish","part":{"cost":0.01}}`)
	_, err = r.invokeCLI(context.Background(), Connection{Backend: "opencode", Model: "opencode/claimed-free", Executable: binary}, "Review", "council-review-free-reviewer-critique", true, "")
	var charged costError
	if !errors.As(err, &charged) {
		t.Fatalf("attached free senior accepted a charge: %v", err)
	}
}
