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

func costStreamFixture(t *testing.T, r *Runner, payload string, hold bool) Connection {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "receipt.jsonl")
	if err := os.WriteFile(output, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUILDHEADWAY_TEST_ADAPTER_CAPTURE", filepath.Join(t.TempDir(), "args.json"))
	t.Setenv("BUILDHEADWAY_TEST_ADAPTER_OUTPUT_FILE", output)
	if hold {
		t.Setenv("BUILDHEADWAY_TEST_ADAPTER_HOLD_OUTPUT", "1")
	}
	return Connection{Backend: "opencode", Executable: binary, Model: "opencode/verified"}
}

func TestReportedFreeCostStopsHangingAgentBeforeDeadline(t *testing.T) {
	for _, role := range []string{"worker", "auto-planner", "required-free-senior", "council-free-senior"} {
		t.Run(role, func(t *testing.T) {
			r, _ := fixtureRunner(t)
			payload := "{\"type\":\"step_finish\",\"part\":{\"cost\":0.125}}\n"
			c := costStreamFixture(t, r, payload, true)
			plan, label := role != "worker", "work"
			switch role {
			case "auto-planner":
				r.Settings.Planner = Connection{Backend: "opencode", Model: "auto"}
				label = "plan"
			case "required-free-senior":
				c.RequireFree = true
				label = "dialogue-free"
			case "council-free-senior":
				r.Settings.Planner = Connection{Backend: "opencode", Model: "auto"}
				if err := r.EnableFigureOut("workspace"); err != nil {
					t.Fatal(err)
				}
				label = "council-lead-plan"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := r.invokeCLI(ctx, c, "Inspect actual files", label, plan, "")
			var charge costError
			if !errors.As(err, &charge) || ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || !hardModelFailure(err) || modelFailureKind(err, false) != "cost" {
				t.Fatalf("charge did not interrupt before deadline: %v parent=%v", err, ctx.Err())
			}
			logs, _ := filepath.Glob(filepath.Join(r.Dir, "*"+label+"*.log"))
			if len(logs) != 1 {
				t.Fatal(logs)
			}
			if got, _ := os.ReadFile(logs[0]); string(got) != payload {
				t.Fatalf("receipt evidence changed: %s", got)
			}
		})
	}
}

func TestTinyReportedFreeCostCannotUnderflowToZero(t *testing.T) {
	r, _ := fixtureRunner(t)
	c := costStreamFixture(t, r, "{\"type\":\"step_finish\",\"part\":{\"cost\":1e-999}}\nCompleted assignment\n", false)
	_, err := r.invokeCLI(context.Background(), c, "Repair", "tiny-cost", false, "")
	var charge costError
	if !errors.As(err, &charge) {
		t.Fatalf("positive charge rounded to zero: %v", err)
	}
}

func TestPaidSeniorAndNonCostTextRemainUsable(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		plan          bool
	}{
		{"paid-senior", "{\"type\":\"step_finish\",\"part\":{\"cost\":0.125}}\nSenior report\n", true},
		{"zero-worker", "{\"type\":\"step_finish\",\"part\":{\"cost\":0}}\nWorker report\n", false},
		{"quoted-charge", "{\"type\":\"text\",\"part\":{\"text\":\"Example cost 9\",\"cost\":9}}\n", false},
		{"tool-output", "{\"type\":\"tool_use\",\"part\":{\"cost\":9}}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := fixtureRunner(t)
			c := costStreamFixture(t, r, tc.payload, false)
			r.Settings.Planner = c
			out, err := r.invokeCLI(context.Background(), c, "Inspect", "ordinary", tc.plan, "")
			if err != nil || !strings.Contains(out, strings.TrimSpace(tc.payload)) {
				t.Fatalf("valid output blocked: %v %s", err, out)
			}
		})
	}
}

func TestReportedChargeUsesExactReceiptNumbers(t *testing.T) {
	for _, cost := range []string{"0.01", "1e-999", "1e999", "0.00000000000000000001", "10E-1000"} {
		if !openCodeReportedCharge(`{"type":"step_finish","part":{"cost":` + cost + `}}`) {
			t.Fatalf("lost positive receipt %s", cost)
		}
	}
	for _, cost := range []string{"0", "0.000", "0e999", "-0", "-1", "null", `"1"`, "true", "{}", "[]", "1e", "NaN"} {
		if openCodeReportedCharge(`{"type":"step_finish","part":{"cost":` + cost + `}}`) {
			t.Fatalf("invented charge from %s", cost)
		}
	}
	for _, line := range []string{`{"type":"step_finish"}`, `{"type":"tool_use","part":{"cost":1}}`, `{"type":"text","part":{"text":"{\"type\":\"step_finish\",\"part\":{\"cost\":1}}"}}`, "step_finish cost=1"} {
		if openCodeReportedCharge(line) {
			t.Fatalf("nonreceipt became charge: %s", line)
		}
	}
}

func TestStreamChargeKeepsPriorityOverConcurrentCancellation(t *testing.T) {
	r, _ := fixtureRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Emit = func(e Event) {
		if e.Kind == "tool" {
			cancel()
		}
	}
	// These small records are copied in one stdout write. Cancel at the first
	// received event must not erase the charge already delivered with it.
	c := costStreamFixture(t, r, "{\"type\":\"tool_use\",\"part\":{\"tool\":\"read\"}}\n{\"type\":\"step_finish\",\"part\":{\"cost\":0.01}}\n", true)
	_, err := r.invokeCLI(ctx, c, "Repair", "cancel-cost", false, "")
	var charge costError
	if !errors.As(err, &charge) || ctx.Err() != context.Canceled || !hardModelFailure(err) {
		t.Fatalf("cancel hid charge: %v parent=%v", err, ctx.Err())
	}
}

func TestStreamCostBlocksFallbackAndRecoversPartialWork(t *testing.T) {
	r, _ := fixtureRunner(t)
	c := costStreamFixture(t, r, "{\"type\":\"step_finish\",\"part\":{\"cost\":0.01}}\n", true)
	r.Settings.Worker = c
	discoveries, calls := 0, 0
	r.Discover = func(context.Context) ([]string, error) {
		discoveries++
		return []string{"opencode/verified", "opencode/another"}, nil
	}
	planner := r.Invoke
	r.Invoke = func(ctx context.Context, _ Connection, prompt, label string, plan bool, model string) (string, error) {
		if plan {
			return planner(ctx, c, prompt, label, true, model)
		}
		calls++
		if err := os.WriteFile(filepath.Join(r.Repo, "result.txt"), []byte("good\n"), 0600); err != nil {
			return "", err
		}
		return r.invokeCLI(ctx, c, prompt, label, false, model)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := r.Run(ctx)
	var charge costError
	if !errors.As(err, &charge) || r.State.Status != "blocked" || calls != 1 || discoveries != 1 || r.State.WorkerCalls != 1 || len(r.State.CheckResults) != 0 || r.State.CheckedSnapshot != "" {
		t.Fatalf("cost allowed fallback/completion: %v status=%s calls=%d discoveries=%d", err, r.State.Status, calls, discoveries)
	}
	saved, _, err := LoadRun(r.Repo, r.State.RunID)
	if err != nil || len(saved.ModelHealth) != 1 || saved.ModelHealth[0].FailureKind != "cost" || saved.ModelHealth[0].Attempts != 1 {
		t.Fatalf("lost cost accounting: %+v %v", saved.ModelHealth, err)
	}
	resume, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resume.Discover = r.Discover
	resume.Invoke = func(ctx context.Context, c Connection, prompt, label string, plan bool, model string) (string, error) {
		if !plan {
			t.Fatal("repeated sufficient partial repair before inspection")
		}
		if len(resume.State.CheckResults) == 0 || resume.State.CheckResults[0].ExitCode != 0 || resume.State.CheckedSnapshot == "" {
			t.Fatal("reviewed partial work before executed verification")
		}
		return decisionJSON(true, nil, nil), nil
	}
	if err := resume.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resume.State.Status != "complete" || resume.State.WorkerCalls != 1 || calls != 1 {
		t.Fatal("lost partial repair/call identity")
	}
}
