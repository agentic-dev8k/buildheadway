package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func failureHandoffFixture(t *testing.T) (string, string, string) {
	t.Helper()
	repo := t.TempDir()
	path, nonce, err := newBrowserHandoff(repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUILDHEADWAY_BROWSER_CHECK_HANDOFF", path)
	t.Setenv("BUILDHEADWAY_BROWSER_CHECK_NONCE", nonce)
	t.Setenv("BUILDHEADWAY_BROWSER_CHECK_ROOT", repo)
	return repo, path, nonce
}

func TestBrowserFailureHandoffsKeepHardBlockerPrecedenceAndParentOutcome(t *testing.T) {
	for _, hardFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary-first", true: "hard-first"}[hardFirst], func(t *testing.T) {
			repo, path, nonce := failureHandoffFixture(t)
			child := filepath.Join(repo, "packages", "child")
			if err := os.MkdirAll(child, 0700); err != nil {
				t.Fatal(err)
			}
			ordinary := func() {
				saveBrowserFailure(child, errors.New("opening diagnosis "+strings.Repeat("界", 2000)+" final detail"), filepath.Join(child, ".forge", "browser-checks", "browser-report", "report.json"))
			}
			hard := func() { saveBrowserBlocker(child, mcpSetupError{"browser credential variable is unavailable"}) }
			if hardFirst {
				hard()
				ordinary()
			} else {
				ordinary()
				hard()
			}
			evidence, err := browserProcessEvidence(repo, path, nonce, ExecutionEvidence{DurationMillis: 50, FailureKind: "cancelled", Error: context.Canceled.Error()})
			var setup mcpSetupError
			if !errors.As(err, &setup) || evidence.FailureKind != "execution" || evidence.BrowserFailure == nil || len(evidence.BrowserFailure.Error) > 2000 || !utf8.ValidString(evidence.BrowserFailure.Error) || !strings.Contains(evidence.BrowserFailure.Error, "opening diagnosis") || !strings.Contains(evidence.BrowserFailure.Error, "final detail") {
				t.Fatal(evidence, err)
			}
			if evidence.BrowserFailure.Report != "packages/child/.forge/browser-checks/browser-report/report.json" {
				t.Fatal("child project lost root-relative report", evidence)
			}
		})
	}
	repo, path, nonce := failureHandoffFixture(t)
	saveBrowserFailure(repo, errors.New("first failed assertion"), "")
	saveBrowserFailure(repo, errors.New("second failed assertion"), "")
	for _, kind := range []string{"", "cancelled", "timeout", "exit", "execution"} {
		original := ExecutionEvidence{FailureKind: kind, Error: "original diagnostic", DurationMillis: 41}
		evidence, err := browserProcessEvidence(repo, path, nonce, original)
		if err != nil || evidence.FailureKind != kind || evidence.Error != original.Error || evidence.DurationMillis != 41 || evidence.BrowserFailure.Error != "first failed assertion" {
			t.Fatal("child failure replaced parent execution", evidence, err)
		}
	}
	other, otherNonce, err := newBrowserHandoff(repo)
	if err != nil {
		t.Fatal(err)
	}
	if failure, err := readBrowserFailure(repo, other, otherNonce); err != nil || failure != nil {
		t.Fatal("new attempt inherited old failure", failure, err)
	}
}

func TestBrowserFailureHandoffsRejectStaleMalformedAndEscapedEvidence(t *testing.T) {
	for _, mode := range []string{"nonce", "version", "empty", "type", "oversized", "symlink", "directory", "report"} {
		t.Run(mode, func(t *testing.T) {
			repo, path, nonce := failureHandoffFixture(t)
			name := filepath.Join(repo, path+".failure")
			message := browserFailureHandoff{Version: 1, Nonce: nonce, BrowserFailureEvidence: BrowserFailureEvidence{Error: "failed assertion"}}
			switch mode {
			case "nonce":
				message.Nonce = "stale"
			case "version":
				message.Version = 2
			case "empty":
				message.Error = ""
			case "report":
				message.Report = "../outside/report.json"
			}
			if err := AtomicJSON(name, message); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "type":
				if err := os.WriteFile(name, []byte(`{"error":12}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.Truncate(name, 4097); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(name, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(t.TempDir(), "outside.json")
				if err := AtomicJSON(outside, message); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, name); err != nil {
					t.Skip("symlinks unavailable:", err)
				}
			}
			var setup mcpSetupError
			if failure, err := readBrowserFailure(repo, path, nonce); !errors.As(err, &setup) || failure != nil {
				t.Fatal("accepted malformed handoff", failure, err)
			}
		})
	}
}

func TestBrowserFailureEncodedBytesRemainBounded(t *testing.T) {
	repo, path, nonce := failureHandoffFixture(t)
	saveBrowserFailure(repo, errors.New("OPEN "+strings.Repeat("<\x00", 3000)+" END"), "")
	data, err := os.ReadFile(filepath.Join(repo, path+".failure"))
	if err != nil || len(data) > 4096 {
		t.Fatal("unbounded encoded handoff", len(data), err)
	}
	failure, err := readBrowserFailure(repo, path, nonce)
	if err != nil || failure == nil || !strings.HasPrefix(failure.Error, "OPEN ") || !strings.HasSuffix(failure.Error, " END") {
		t.Fatal("escaping caused a false setup blocker or lost diagnosis", failure, err)
	}
}

func TestBrowserFailureChangesStallFingerprintWithoutFreshReportNoise(t *testing.T) {
	r, _ := fixtureRunner(t)
	failure := &BrowserFailureEvidence{Error: "assertion failed", Report: ".forge/browser-checks/first/report.json"}
	r.State.CheckResults = []CheckResult{{Command: "node wrapper.cjs", ExecutionEvidence: ExecutionEvidence{BrowserFailure: failure}}}
	r.recordVerification("same-source")
	first := r.State.VerificationSignature
	failure.Report = ".forge/browser-checks/second/report.json"
	r.recordVerification("same-source")
	if r.State.StalledCycles != 1 || r.State.VerificationSignature != first {
		t.Fatal("new report ID bypassed stalled-cycle accounting")
	}
	r.State.CheckResults[0].BrowserFailure = nil
	r.recordVerification("same-source")
	if r.State.StalledCycles != 0 || r.State.VerificationSignature == first {
		t.Fatal("repair was not recognized as progress")
	}
	r.State.CheckResults = nil
	r.State.HookResults = []HookResult{{Name: "browser", ExecutionEvidence: ExecutionEvidence{BrowserFailure: failure}}}
	r.recordVerification("same-source")
	first = r.State.VerificationSignature
	r.State.HookResults[0].BrowserFailure = nil
	r.recordVerification("same-source")
	if r.State.VerificationSignature == first {
		t.Fatal("hook repair was not recognized")
	}
}

func TestNestedBrowserFailureSurvivesActualParentCancellation(t *testing.T) {
	paths, repo, config, command := browserFailureWrapper(t, "action-error")
	file := filepath.Join(repo, "wrapper.cjs")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "process.exit(0);", `require('fs').writeFileSync('.forge/failure-ready','ready');setTimeout(()=>{},30000);`, 1))
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var checks []CheckResult
	go func() {
		done <- runVerificationConfigured(ctx, paths, repo, filepath.Join(repo, ".forge", "cancel-evidence"), config, []string{command}, 20*time.Second, false, noEmit, nil, func(c CheckResult) error { checks = append(checks, c); return nil })
	}()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("command ended before cancellation: %v", err)
		case <-deadline.C:
			cancel()
			<-done
			t.Fatal("child never recorded failure")
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(repo, ".forge", "failure-ready")); err == nil {
				cancel()
				err = <-done
				if !errors.Is(err, context.Canceled) || len(checks) != 1 || checks[0].FailureKind != "cancelled" || checks[0].ExitCode != 130 || checks[0].BrowserFailure == nil || checks[0].Passed() {
					t.Fatal("lost cancellation or child evidence", checks, err)
				}
				return
			}
		}
	}
}
