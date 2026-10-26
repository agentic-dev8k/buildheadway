package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeExtension(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestWorkflowOverridesLiteralArgumentsAndPinnedResume(t *testing.T) {
	paths, repo := testPaths(t), t.TempDir()
	writeExtension(t, filepath.Join(paths.Config, "prompts", "review.md"), "Personal $ARGUMENTS")
	project := filepath.Join(repo, ".buildheadway-prompts", "review.md")
	writeExtension(t, project, "---\ndescription: Review API\n---\nProject $1 / ${2:-default} / $ARGUMENTS")
	args, err := workflowArguments(`"C:\My Project\api.go" '$(touch should-never-exist)'`)
	if err != nil || len(args) != 2 {
		t.Fatalf("arguments: %q %v", args, err)
	}
	goal, err := ExpandWorkflow(paths, repo, "review", args)
	if err != nil || !strings.Contains(goal, "Project C:\\My Project\\api.go") || !strings.Contains(goal, "$(touch should-never-exist)") || strings.Contains(goal, "Personal") {
		t.Fatalf("unexpected expansion: %s %v", goal, err)
	}
	r, err := NewRunner(paths, Settings{}, repo, goal, false, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	writeExtension(t, project, "Changed template")
	resumed, err := ResumeRunner(paths, Settings{}, repo, r.State.RunID, noEmit)
	if err != nil || resumed.State.Goal != goal {
		t.Fatal("resume changed the selected workflow", err)
	}
	writeExtension(t, filepath.Join(repo, ".buildheadway-skills", "audit", "SKILL.md"), "---\nname: audit\ndescription: Inspect API contracts\n---\nRead references/contracts.md, then audit $ARGUMENTS.")
	goal, err = ExpandWorkflow(paths, repo, "audit", []string{"public API"})
	if err != nil || !strings.Contains(goal, "Supporting files are in:") || !strings.Contains(goal, "public API") {
		t.Fatal("skill instructions/support directory missing", err)
	}
	if _, err := workflowArguments(`"unterminated`); err == nil {
		t.Fatal("accepted unmatched quote")
	}
}
func TestWorkflowRejectsAmbiguityAndEscapingSymlinks(t *testing.T) {
	paths, repo := testPaths(t), t.TempDir()
	writeExtension(t, filepath.Join(repo, ".buildheadway-skills", "audit", "SKILL.md"), "Inspect")
	prompt := filepath.Join(repo, ".buildheadway-prompts", "audit.md")
	writeExtension(t, prompt, "Conflicting instructions")
	if _, err := DiscoverWorkflows(paths, repo); err == nil {
		t.Fatal("silently selected ambiguous workflow")
	}
	if err := os.Remove(prompt); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "private.md")
	writeExtension(t, out, "Outside configuration")
	if err := os.Symlink(out, prompt); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := DiscoverWorkflows(paths, repo); err == nil {
		t.Fatal("followed a workflow outside the project")
	}
}
func TestSteeringDuringWorkerReplansBeforeDependentTask(t *testing.T) {
	r, _ := councilFixture(t)
	workerCalls, sawNewPlan := 0, false
	r.Invoke = func(_ context.Context, _ Connection, prompt, label string, planner bool, _ string) (string, error) {
		if planner {
			if len(r.State.Steering) > 0 {
				if !strings.Contains(prompt, "ALSO_WRITE_DOCUMENTATION") {
					t.Fatal("senior missed updated direction")
				}
				sawNewPlan = true
				if _, err := os.Stat(filepath.Join(r.Repo, "README.md")); err == nil {
					return decisionJSON(true, nil, nil), nil
				}
				return decisionJSON(false, []Task{{"docs", "Document", "ALSO_WRITE_DOCUMENTATION"}}, nil), nil
			}
			return decisionJSON(false, []Task{{"first", "Write output", "Write good"}, {"stale", "Old dependent task", "STALE_MUST_NOT_EXECUTE"}}, nil), nil
		}
		workerCalls++
		if r.State.Tasks[r.State.NextTask].ID == "stale" {
			t.Fatal("dispatched a stale dependent task")
		}
		if workerCalls == 1 {
			writeExtension(t, filepath.Join(r.Repo, "result.txt"), "good\n")
			if _, err := SendSteering(r.Repo, r.State.RunID, "ALSO_WRITE_DOCUMENTATION"); err != nil {
				t.Fatal(err)
			}
			return workerCommunication("consult", "Partial result", TeamRequest{"lead", "question", "A stale question"}), nil
		}
		if !sawNewPlan || !strings.Contains(prompt, "ALSO_WRITE_DOCUMENTATION") {
			t.Fatal("worker continued before replanning")
		}
		writeExtension(t, filepath.Join(r.Repo, "README.md"), "Usage verified.\n")
		return workerCommunication("complete", "Updated documentation"), nil
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if workerCalls != 2 || len(r.State.Steering) != 1 || r.State.Status != "complete" || !r.State.Council.Consensus {
		t.Fatalf("steering failed: calls=%d state=%s", workerCalls, r.State.Status)
	}
	if r.State.Dialogue != nil {
		t.Fatal("stale consultation survived direction change")
	}
	if _, err := SendSteering(r.Repo, r.State.RunID, "Too late"); err == nil {
		t.Fatal("accepted direction after completion")
	}
	if !strings.Contains(MarkdownReport(r.State), "ALSO_WRITE_DOCUMENTATION") {
		t.Fatal("report lost user direction")
	}
}
func TestSteeringDiscardsLastReviewApproval(t *testing.T) {
	r, workers := councilFixture(t)
	*workers = 1
	invoke, sent, reviewedNew := r.Invoke, false, false
	r.Invoke = func(ctx context.Context, c Connection, prompt, label string, planner bool, model string) (string, error) {
		if strings.Contains(label, "review-lead-decision") && !sent {
			sent = true
			if _, err := SendSteering(r.Repo, r.State.RunID, "Recheck compatibility before final approval"); err != nil {
				t.Fatal(err)
			}
		} else if planner && len(r.State.Steering) > 0 {
			if !strings.Contains(prompt, "Recheck compatibility") {
				t.Fatal("review missed user update")
			}
			reviewedNew = true
		}
		return invoke(ctx, c, prompt, label, planner, model)
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !sent || !reviewedNew || r.State.Cycle != 2 || *workers != 2 {
		t.Fatal("stale approval completed the run or repeated the worker")
	}
}
func TestSteeringConcurrentWritersAndCompletionHaveNoLostMessages(t *testing.T) {
	r, _ := fixtureRunner(t)
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := SendSteering(r.Repo, r.State.RunID, fmt.Sprintf("Update %d", i))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := r.finishRun("complete", "Cannot finish yet"); !errors.Is(err, errSteering) {
		t.Fatal("completed with a pending direction", err)
	}
	if err := r.applySteering(); err != nil {
		t.Fatal(err)
	}
	if err := r.applySteering(); err != nil {
		t.Fatal(err)
	}
	if len(r.State.Steering) != 12 {
		t.Fatal("lost or duplicated user messages")
	}
	if err := r.finishRun("complete", "Done"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadRun(r.Repo, r.State.RunID); err != nil {
		t.Fatal(err)
	}
}

// Invoked by the real verification shell on each supported CI operating system.
func TestVerificationHookSubprocess(t *testing.T) {
	if os.Getenv("BUILDHEADWAY_HOOK_EVENT") == "" {
		return
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	var input HookInput
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	if input.Version != 1 || input.Event != os.Getenv("BUILDHEADWAY_HOOK_EVENT") || input.Project != os.Getenv("BUILDHEADWAY_PROJECT") || input.RunID == "" {
		t.Fatal("hook input/environment mismatch")
	}
	file, err := os.OpenFile(filepath.Join(input.Project, ".forge", "hook-inputs.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	file.Write(data)
	file.Close()
	switch input.Name {
	case "format":
		writeExtension(t, filepath.Join(input.Project, "result.txt"), "good\n")
	case "gate":
		if len(input.Checks) == 0 || input.Checks[0].ExitCode != 0 {
			t.Fatal("gate missing actual passing check evidence")
		}
		if _, err := os.Stat(filepath.Join(input.Project, "approved.txt")); err != nil {
			fmt.Fprintln(os.Stderr, "missing approval artifact")
			os.Exit(7)
		}
	case "mutate-once":
		path := filepath.Join(input.Project, "hook-created.txt")
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			writeExtension(t, path, "source changed after tests\n")
		}
	case "mutate-always":
		writeExtension(t, filepath.Join(input.Project, "hook-changing.txt"), time.Now().String())
	case "timeout":
		time.Sleep(20 * time.Second)
	}
}
func hookFixtureCommand(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return `"` + path + `" -test.run=^TestVerificationHookSubprocess$`
	}
	return ShellQuote(path) + " -test.run=^TestVerificationHookSubprocess$"
}
func TestHooksRunRealCommandsAndBlockUntilFailureIsRepaired(t *testing.T) {
	r, _ := councilFixture(t)
	cmd := hookFixtureCommand(t)
	r.State.Config.Hooks = []ProjectHook{{Name: "format", Event: "before_check", Command: cmd}, {Name: "gate", Event: "after_check", Command: cmd}}
	r.State.Phase = "checking"
	workers := 0
	r.Invoke = func(_ context.Context, _ Connection, prompt, _ string, planner bool, _ string) (string, error) {
		if planner {
			if workers == 0 {
				if !strings.Contains(prompt, "missing approval artifact") || !strings.Contains(prompt, "hook_results") {
					t.Fatal("failed gate not routed to senior reviewers")
				}
				return decisionJSON(false, []Task{{"repair", "Repair gate", "Create approved.txt after inspecting the gate"}}, nil), nil
			}
			return decisionJSON(true, nil, nil), nil
		}
		workers++
		if !strings.Contains(prompt, "missing approval artifact") {
			t.Fatal("worker did not receive hook failure")
		}
		writeExtension(t, filepath.Join(r.Repo, "approved.txt"), "approved\n")
		return workerCommunication("complete", "Repaired required gate"), nil
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if workers != 1 || r.State.Status != "complete" || len(r.State.HookResults) != 2 || !hooksPassed(r.State.Config, r.State.HookResults) {
		t.Fatal("hook failure was not repaired")
	}
	r.State.HookResults[1].ExitCode = 7
	if CompletionAllowed(r.State, Decision{Done: true}) {
		t.Fatal("passing tests bypassed failed hook")
	}
}
func TestAfterCheckMutationReverifiesAndRepeatedMutationPauses(t *testing.T) {
	for _, name := range []string{"mutate-once", "mutate-always"} {
		t.Run(name, func(t *testing.T) {
			r, _ := fixtureRunner(t)
			writeExtension(t, filepath.Join(r.Repo, "result.txt"), "good\n")
			r.State.Config.Hooks = []ProjectHook{{Name: name, Event: "after_check", Command: hookFixtureCommand(t)}}
			r.State.Phase = "checking"
			err := r.Run(context.Background())
			if name == "mutate-always" {
				var limit limitError
				if !errors.As(err, &limit) || r.State.Status != "paused" || r.State.CheckedSnapshot != "" {
					t.Fatal("unstable hook falsely completed", err)
				}
			} else {
				if err != nil || r.State.Status != "complete" {
					t.Fatal("did not reverify", err)
				}
				data, _ := os.ReadFile(filepath.Join(r.Repo, ".forge", "hook-inputs.jsonl"))
				if strings.Count(string(data), "\"event\":") != 2 {
					t.Fatal("test suite did not rerun after hook edit")
				}
			}
		})
	}
}
func TestHookConfigurationPinnedOnResumeAndTimeoutRecorded(t *testing.T) {
	r, _ := fixtureRunner(t)
	cmd := hookFixtureCommand(t)
	r.State.Config.Hooks = []ProjectHook{{Name: "timeout", Event: "before_check", Command: cmd, TimeoutSeconds: 1}}
	r.State.ActiveHook, r.State.Phase = "timeout", "checking"
	if err := r.State.Save(r.Dir); err != nil {
		t.Fatal(err)
	}
	changed := DefaultProjectConfig()
	changed.Hooks = []ProjectHook{{Name: "new-command", Event: "after_check", Command: "unexpected-command"}}
	if err := AtomicJSON(filepath.Join(r.Repo, ".buildheadway.json"), changed); err != nil {
		t.Fatal(err)
	}
	resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resumed.MaxCalls = 1
	resumed.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		return decisionJSON(true, nil, nil), nil
	}
	err = resumed.Run(context.Background())
	if err == nil || len(resumed.State.HookResults) == 0 || resumed.State.HookResults[0].Name != "timeout" || resumed.State.HookResults[0].ExitCode != 124 {
		t.Fatal("timeout or pinned configuration lost", err)
	}
	if resumed.State.Status == "complete" {
		t.Fatal("timed out hook approved")
	}
}
func TestStandaloneCheckRunsHookGates(t *testing.T) {
	r, _ := fixtureRunner(t)
	config := DefaultProjectConfig()
	config.Checks = r.State.RequiredChecks
	cmd := hookFixtureCommand(t)
	config.Hooks = []ProjectHook{{Name: "format", Event: "before_check", Command: cmd}, {Name: "gate", Event: "after_check", Command: cmd}}
	if err := AtomicJSON(filepath.Join(r.Repo, ".buildheadway.json"), config); err != nil {
		t.Fatal(err)
	}
	report, err := CheckProject(context.Background(), r.Paths, r.Repo, nil)
	if err == nil || report.Status != "failed" || len(report.Hooks) != 2 || report.Results[0].ExitCode != 0 {
		t.Fatal("buildheadway check ignored hook gates", err)
	}

	config.Hooks[1].Name = "mutate-once"
	if err := AtomicJSON(filepath.Join(r.Repo, ".buildheadway.json"), config); err != nil {
		t.Fatal(err)
	}
	report, err = CheckProject(context.Background(), r.Paths, r.Repo, nil)
	if err != nil || report.Status != "passed" {
		t.Fatal("standalone hook recheck failed", err)
	}
}
