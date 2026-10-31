package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// A real SDK subprocess produces the PNG; the simulated reviewer corrupts only
// saved evidence, leaving the tested project source untouched.
func TestBrowserScreenshotChangedDuringReviewRequiresFreshVerification(t *testing.T) {
	paths, repo, config := fixtureBrowser(t, "")
	config.Checks = []string{checkFixtureCommand()}
	if err := AtomicJSON(filepath.Join(repo, ".buildheadway.json"), config); err != nil {
		t.Fatal(err)
	}
	settings := Settings{Version: 2, Planner: Connection{Backend: "codex"}, Worker: Connection{Backend: "opencode", Model: "auto"}}
	r, err := NewRunner(paths, settings, repo, "Create a correct output file and exercise the browser", false, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	r.MaxCalls = 10
	r.Discover = func(context.Context) ([]string, error) { return []string{"opencode/free"}, nil }
	workers, reviews := 0, 0
	r.Invoke = func(_ context.Context, _ Connection, _ string, _ string, planner bool, _ string) (string, error) {
		if !planner {
			workers++
			return "Implemented output", os.WriteFile(filepath.Join(repo, "result.txt"), []byte("good\n"), 0600)
		}
		if workers == 0 {
			return decisionJSON(false, []Task{{ID: "output", Title: "Write output", Instructions: "Write good and preserve checks"}}, nil), nil
		}
		reviews++
		if reviews == 1 {
			found := false
			for _, c := range r.State.CheckResults {
				if c.Browser != nil {
					found = true
					if err := os.WriteFile(filepath.Join(r.Dir, c.Browser.Screenshot), []byte("corrupted after verification"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !found {
				t.Fatal("fixture never executed the browser check")
			}
		}
		return decisionJSON(true, nil, nil), nil
	}
	if err = r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.State.Status != "complete" || reviews != 2 || workers != 1 {
		t.Fatalf("accepted stale screenshot: status=%s reviews=%d workers=%d", r.State.Status, reviews, workers)
	}
}

func evidenceGoal(t *testing.T, hook bool) (*Runner, *int, *int) {
	t.Helper()
	paths, repo, config := fixtureBrowser(t, "")
	config.Checks = []string{checkFixtureCommand()}
	if hook {
		config.Hooks = []ProjectHook{screenshotRemovalHook(t, paths, repo)}
	}
	if err := AtomicJSON(filepath.Join(repo, ".buildheadway.json"), config); err != nil {
		t.Fatal(err)
	}
	settings := Settings{Version: 2, Planner: Connection{Backend: "codex"}, Worker: Connection{Backend: "opencode", Model: "opencode/free"}}
	r, err := NewRunner(paths, settings, repo, "Create a correct output file", false, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	r.MaxCalls = 10
	r.Discover = func(context.Context) ([]string, error) { return []string{"opencode/free"}, nil }
	workers, reviews := new(int), new(int)
	r.Invoke = func(_ context.Context, _ Connection, _ string, _ string, planner bool, _ string) (string, error) {
		if !planner {
			*workers++
			return "Created output", os.WriteFile(filepath.Join(repo, "result.txt"), []byte("good\n"), 0600)
		}
		if *workers == 0 {
			return decisionJSON(false, []Task{{ID: "output", Title: "Output", Instructions: "Write good and preserve all checks"}}, nil), nil
		}
		*reviews++
		return decisionJSON(true, nil, nil), nil
	}
	return r, workers, reviews
}

// A real Node hook consumes the real harness input and removes only saved PNGs.
// It can be stopped independently without editing the pinned hook command.
func screenshotRemovalHook(t *testing.T, paths Paths, repo string) ProjectHook {
	t.Helper()
	node := FindTool("node", paths)
	if node == "" {
		t.Skip("Node.js is required for the actual after_check fixture")
	}
	name := filepath.Join(repo, ".forge", "remove-screenshot.cjs")
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	script := `const fs=require('fs'),path=require('path');
const input=JSON.parse(fs.readFileSync(0,'utf8'));
fs.appendFileSync('.forge/evidence-hook-rounds','ran\n');
if(!fs.existsSync('.forge/evidence-hook-stopped')) {
 for(const kind of ['runs','checks']) for(const c of input.checks) if(c.browser) {
  const name=path.join('.forge',kind,input.run_id,c.browser.screenshot);
  if(fs.existsSync(name)) fs.unlinkSync(name);
 }
}`
	if err := os.WriteFile(name, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	command := strconv.Quote(node) + " " + strconv.Quote(name)
	if runtime.GOOS == "windows" {
		// cmd.exe does not decode JSON's doubled path separators.
		command = `"` + node + `" "` + name + `"`
	}
	return ProjectHook{Name: "remove-proof", Event: "after_check", Command: command}
}

func TestBrowserScreenshotInvalidBeforeReviewPausesBoundedlyAndResumes(t *testing.T) {
	r, workers, reviews := evidenceGoal(t, true)
	err := r.Run(context.Background())
	var limit limitError
	if !errors.As(err, &limit) || !strings.Contains(err.Error(), "three verification rounds") || r.State.Status != "paused" || r.State.Phase != "checking" || *workers != 1 || *reviews != 0 || r.State.CheckedSnapshot != "" {
		t.Fatalf("unbounded or stale review: %v status=%s phase=%s workers=%d reviews=%d", err, r.State.Status, r.State.Phase, *workers, *reviews)
	}
	data, _ := os.ReadFile(filepath.Join(r.Repo, ".forge", "evidence-hook-rounds"))
	if strings.Count(string(data), "ran\n") != 3 {
		t.Fatalf("expected three executed rounds, got %q", data)
	}
	if err := os.WriteFile(filepath.Join(r.Repo, ".forge", "evidence-hook-stopped"), []byte("stop"), 0600); err != nil {
		t.Fatal(err)
	}
	resumed, err := ResumeRunner(r.Paths, r.Settings, r.Repo, r.State.RunID, noEmit)
	if err != nil {
		t.Fatal(err)
	}
	resumed.MaxCalls, resumed.Invoke, resumed.Discover = 10, r.Invoke, r.Discover
	if err = resumed.Run(context.Background()); err != nil || resumed.State.Status != "complete" || *workers != 1 || *reviews != 1 {
		t.Fatal("resume did not rerun checks before fresh approval", err)
	}
	for _, result := range resumed.State.CheckResults {
		if !result.Passed() || result.EvidenceError != "" {
			t.Fatal("resume retained invalid proof", result)
		}
	}
}

func TestBrowserScreenshotReviewPreservesHardBlockersAndCallBudgets(t *testing.T) {
	for _, blocker := range []error{authError{"codex", "authentication failed"}, errors.New("429 insufficient quota"), errors.New("access denied by provider"), costError{}, outputLimitError{}, mcpSetupError{"runtime unavailable"}} {
		t.Run(blocker.Error(), func(t *testing.T) {
			r, workers, reviews := evidenceGoal(t, false)
			invoke := r.Invoke
			r.Invoke = func(ctx context.Context, con Connection, prompt, label string, planner bool, model string) (string, error) {
				if planner && *workers > 0 {
					*reviews++
					for _, check := range r.State.CheckResults {
						if check.Browser != nil {
							if err := os.Remove(filepath.Join(r.Dir, check.Browser.Screenshot)); err != nil {
								t.Fatal(err)
							}
						}
					}
					return "", blocker
				}
				return invoke(ctx, con, prompt, label, planner, model)
			}
			if err := r.Run(context.Background()); err == nil || !strings.Contains(err.Error(), blocker.Error()) || errors.Is(err, errBrowserEvidence) || r.State.Status == "complete" || *workers != 1 || *reviews != 1 || r.evidenceRechecks != 0 {
				t.Fatal("artifact retry hid a hard blocker", err, r.State.Status)
			}
		})
	}
	r, workers, reviews := evidenceGoal(t, false)
	r.MaxCalls = 2
	invoke := r.Invoke
	r.Invoke = func(ctx context.Context, con Connection, prompt, label string, planner bool, model string) (string, error) {
		text, err := invoke(ctx, con, prompt, label, planner, model)
		if planner && *workers > 0 {
			for _, c := range r.State.CheckResults {
				if c.Browser != nil {
					if err := os.Remove(filepath.Join(r.Dir, c.Browser.Screenshot)); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		return text, err
	}
	var limit limitError
	if err := r.Run(context.Background()); !errors.As(err, &limit) || r.State.Status != "paused" || *reviews != 1 || *workers != 1 || r.State.PlannerCalls != 2 {
		t.Fatal("recheck bypassed configured model budget", err)
	}
}

func TestStandaloneBrowserChecksRejectRemovedEvidence(t *testing.T) {
	paths, repo, config := fixtureBrowser(t, "")
	config.Hooks = []ProjectHook{screenshotRemovalHook(t, paths, repo)}
	if err := AtomicJSON(filepath.Join(repo, ".buildheadway.json"), config); err != nil {
		t.Fatal(err)
	}
	report, err := CheckProject(context.Background(), paths, repo, noEmit)
	if !errors.Is(err, errBrowserEvidence) || report.Status != "failed" || len(report.Results) != 1 || report.Results[0].EvidenceError == "" || report.Results[0].ExitCode != 0 || report.Results[0].Passed() {
		t.Fatalf("lost execution/integrity distinction: %#v %v", report, err)
	}
	inspected, err := InspectBrowserEvidence(context.Background(), repo, "latest", true, "")
	if err != nil || inspected.Integrity != "invalid" || inspected.RecordedStatus != "failed" {
		t.Fatal(inspected, err)
	}
}
