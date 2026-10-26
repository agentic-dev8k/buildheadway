package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// Deliberately opt-in: these tests use the connected service, not a mock agent.
// The chosen model must still pass the refreshed zero-price catalog check.
func liveFreeRunner(t *testing.T, repo, goal string) *Runner {
	t.Helper()
	if os.Getenv("BUILDHEADWAY_LIVE_FREE_TESTS") != "1" {
		t.Skip("set BUILDHEADWAY_LIVE_FREE_TESTS=1 to run real zero-price inference")
	}
	model := os.Getenv("BUILDHEADWAY_LIVE_MODEL")
	if model == "" {
		model = "opencode/glm-5.3-flash-free"
	}
	connection := Connection{Backend: "opencode", Model: model}
	runner, err := NewRunner(testPaths(t), Settings{Version: 2, Planner: connection, Worker: connection}, repo, goal, false, func(event Event) {
		if event.Kind != "tool" {
			t.Logf("%s: %s", event.Kind, event.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	models, err := runner.discoverModels(ctx)
	if err != nil || !contains(models, model) {
		t.Fatalf("live model has no refreshed zero-price entry: %v %v", models, err)
	}
	return runner
}

func TestLiveFreeFigureOutCouncil(t *testing.T) {
	liveFigureOut(t, false)
}

func TestLiveFreeWorkerConsultationContinuesTask(t *testing.T) {
	liveFigureOut(t, true)
}

func liveFigureOut(t *testing.T, consult bool) {
	t.Helper()
	if os.Getenv("BUILDHEADWAY_LIVE_FREE_TESTS") != "1" {
		t.Skip("opt-in real inference test")
	}
	repo := t.TempDir()
	if output := os.Getenv("BUILDHEADWAY_LIVE_OUTPUT"); output != "" {
		if err := os.MkdirAll(output, 0700); err != nil {
			t.Fatal(err)
		}
		var err error
		repo, err = os.MkdirTemp(output, "conversation-")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("Retained live project and checkpoints: %s", repo)
	}
	tests := `const test = require('node:test');
const assert = require('node:assert/strict');
const {add} = require('./math.cjs');
test('numeric addition', () => {
  assert.equal(add(4, 3), 7);
  assert.equal(add(-4, 3), -1);
  assert.equal(add(1.5, 2.5), 4);
});
`
	for name, value := range map[string]string{
		"package.json": `{"private":true,"scripts":{"test":"node --test math.test.cjs"}}`,
		"math.cjs":     "exports.add = (a, b) => a - b;\n", "math.test.cjs": tests,
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	goal := "Fix numeric addition in math.cjs so the existing npm test passes. Preserve math.test.cjs byte-for-byte. No new dependencies or unrelated features. Only implementation workers may edit files. This is a focused arithmetic repair, not a new application. Use npm test as the acceptance check."
	if consult {
		goal += " Exercise the internal team workflow: the assigned implementation worker must first inspect the code and return a buildheadway_worker status=consult question to lead asking whether decimal arithmetic must be preserved. It must receive the lead's actual answer before editing, then continue the same assignment and implement the repair. Seniors should assign this workflow, answer the worker's question when routed, and review the completed work. Do not ask the human."
	}
	r := liveFreeRunner(t, repo, goal)
	preferred := r.Settings.Planner.Model
	r.Settings.Planner.Model = "auto"
	if err := r.EnableFigureOut("full"); err != nil {
		t.Fatal(err)
	}
	r.State.SeniorModels = map[string]string{"lead": preferred, "reviewer": preferred}
	r.MaxCalls, r.MaxWorkerCalls, r.MaxCycles = 20, 6, 4
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	baseline, err := RunProcess(ctx, []string{FindTool("node", r.Paths), "--test", "math.test.cjs"}, repo, filepath.Join(t.TempDir(), "baseline.log"), "", ToolEnv(r.Paths), 20*time.Second, nil)
	if err != nil || baseline.Code == 0 {
		t.Fatalf("fixture must begin with an actual failed check: %v", err)
	}
	if err := r.Run(ctx); err != nil {
		logs, _ := filepath.Glob(filepath.Join(r.Dir, "*council*.log"))
		sort.Strings(logs)
		if len(logs) > 0 {
			data, _ := os.ReadFile(logs[len(logs)-1])
			t.Logf("Last council log: %s", Tail(string(data), 12000))
		}
		t.Fatalf("figureout: %v\n%s", err, FormatDiscussion(r.State))
	}
	if r.State.Status != "complete" || r.State.PlannerCalls < 6 || r.State.WorkerCalls < 1 || !r.State.Council.Consensus {
		t.Fatal("live council skipped implementation or unanimous review")
	}
	if consult {
		question, answer := 0, false
		for _, message := range r.State.Discussion {
			if message.From == "@worker" && message.To == "lead" && message.Kind == "question" {
				question = message.Sequence
			}
			if message.From == "lead" && message.To == "@worker" && message.Kind == "answer" && question != 0 && message.ReplyTo == question {
				answer = true
			}
		}
		if question == 0 || !answer || r.State.WorkerCalls < 2 {
			t.Fatalf("live worker did not receive an actual routed answer: %s", FormatDiscussion(r.State))
		}
		t.Logf("Live worker question and matched senior reply verified: %s", FormatDiscussion(r.State))
	}
	data, _ := os.ReadFile(filepath.Join(repo, "math.test.cjs"))
	if string(data) != tests {
		t.Fatal("live workers or reviewers weakened the tests")
	}
	for _, check := range r.State.CheckResults {
		if check.ExitCode != 0 {
			t.Fatal("live council approved failed checks")
		}
	}
	t.Logf("Live figureout completed: %d senior calls, %d worker calls, %d discussion messages", r.State.PlannerCalls, r.State.WorkerCalls, len(r.State.Discussion))
}

func TestLiveFreeGoalPlansImplementsAndReviewsActualFiles(t *testing.T) {
	if os.Getenv("BUILDHEADWAY_LIVE_FREE_TESTS") != "1" {
		t.Skip("opt-in real inference test")
	}
	repo := t.TempDir()
	tests := `const test = require('node:test');
const assert = require('node:assert/strict');
const {add} = require('./math.cjs');
test('addition handles positive, negative and decimal inputs', () => {
  assert.equal(add(4, 3), 7);
  assert.equal(add(-4, 3), -1);
  assert.equal(add(1.5, 2.5), 4);
});
`
	for name, content := range map[string]string{
		"package.json":  `{"private":true,"scripts":{"test":"node --test math.test.cjs"}}`,
		"math.cjs":      "exports.add = (a, b) => a - b;\n",
		"math.test.cjs": tests,
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	runner := liveFreeRunner(t, repo, "Fix add(a,b) in math.cjs so the existing tests pass. Preserve math.test.cjs exactly. Use numeric addition, no new dependencies. Inspect files and verify npm test. Only implementation workers may edit project files.")
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	baseline, err := RunProcess(ctx, []string{FindTool("node", runner.Paths), "--test", "math.test.cjs"}, repo, filepath.Join(t.TempDir(), "baseline.log"), "", ToolEnv(runner.Paths), 20*time.Second, nil)
	if err != nil || baseline.Code == 0 {
		t.Fatalf("fixture did not start with a real failing test: %v %s", err, baseline.Output)
	}
	runner.MaxCalls = 5
	runner.MaxCycles = 3
	runner.PlanOnly = true
	err = runner.Run(ctx)
	if err == nil {
		if runner.State.Status != "planned" || runner.State.WorkerCalls != 0 || len(runner.State.CheckResults) != 0 {
			t.Fatal("live planning dispatched a worker or harness check")
		}
		before, readErr := os.ReadFile(filepath.Join(repo, "math.cjs"))
		if readErr != nil || string(before) != "exports.add = (a, b) => a - b;\n" {
			t.Fatal("planning changed implementation")
		}
		resumed, resumeErr := ResumeRunner(runner.Paths, runner.Settings, repo, runner.State.RunID, runner.Emit)
		if resumeErr != nil {
			t.Fatal(resumeErr)
		}
		runner = resumed
		runner.MaxCalls, runner.MaxCycles = 5, 3
		err = runner.Run(ctx)
	}
	if err != nil {
		logs, _ := filepath.Glob(filepath.Join(runner.Dir, "*plan*.log"))
		reviews, _ := filepath.Glob(filepath.Join(runner.Dir, "*review*.log"))
		logs = append(logs, reviews...)
		sort.Strings(logs)
		for _, log := range logs {
			data, _ := os.ReadFile(log)
			texts, parseErr := ResponseTexts(string(data))
			if parseErr != nil {
				t.Logf("Planner service error: %v", parseErr)
			} else {
				for _, text := range texts {
					t.Logf("Planner reply: %s", Tail(text, 2500))
				}
			}
		}
		if len(logs) > 0 {
			data, _ := os.ReadFile(logs[len(logs)-1])
			t.Logf("Last planner protocol log: %s", Tail(string(data), 9000))
		}
		t.Fatal(err)
	}
	if runner.State.Status != "complete" || runner.State.PlannerCalls < 2 || len(runner.State.WorkerResults) == 0 {
		t.Fatalf("live goal skipped planning, implementation or review: %#v", runner.State)
	}
	data, _ := os.ReadFile(filepath.Join(repo, "math.test.cjs"))
	if string(data) != tests {
		t.Fatal("agent weakened the existing tests")
	}
	for _, result := range runner.State.CheckResults {
		if result.ExitCode != 0 {
			t.Fatalf("live verification failed: %#v", result)
		}
	}
	t.Logf("Real goal complete: %d planner calls, %d implementation batches; original failing tests now pass.", runner.State.PlannerCalls, runner.State.Cycle)
}
