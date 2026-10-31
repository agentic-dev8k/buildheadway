package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func screenshotFixture(t *testing.T, dir string) CheckResult {
	t.Helper()
	name := filepath.Join("browser", "flow.png")
	if err := os.MkdirAll(filepath.Join(dir, "browser"), 0700); err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	proof, err := browserPNG(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	proof.Steps, proof.Assertions = 2, 1
	return CheckResult{Command: "@browser:flow", Browser: proof, Output: "executed assertions", Log: "browser.log", ExecutionEvidence: ExecutionEvidence{DurationMillis: 12}}
}

func TestBrowserEvidenceAuditRetainsExecutionAndRejectsChangedArtifacts(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "truncated", "hash", "size", "dimensions", "provider", "traversal", "oversized", "symlink", "windows-separators", "latched"} {
		t.Run(mode, func(t *testing.T) {
			repo := t.TempDir()
			dir := filepath.Join(repo, ".forge", "runs", "evidence")
			check := screenshotFixture(t, dir)
			name := filepath.Join(dir, check.Browser.Screenshot)
			switch mode {
			case "missing":
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
			case "truncated":
				if err := os.WriteFile(name, []byte("not a PNG"), 0600); err != nil {
					t.Fatal(err)
				}
			case "hash":
				check.Browser.SHA256 = strings.Repeat("0", 64)
			case "size":
				check.Browser.Bytes++
			case "dimensions":
				check.Browser.Width++
			case "provider":
				check.Browser.Provider = "unverified"
			case "traversal":
				check.Browser.Screenshot = "../../../../outside.png"
			case "oversized":
				if err := os.Truncate(name, 8<<20+1); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				data, _ := os.ReadFile(name)
				outside := filepath.Join(t.TempDir(), "outside.png")
				if err := os.WriteFile(outside, data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, name); err != nil {
					t.Skip("symlinks unavailable:", err)
				}
			case "windows-separators":
				check.Browser.Screenshot = `browser\flow.png`
			case "latched":
				check.EvidenceError = "previous integrity failure requires an executed check"
			}
			before, _ := json.Marshal(check)
			updated, rows, err := auditBrowserEvidence(context.Background(), repo, dir, []CheckResult{check})
			valid := mode == "valid" || mode == "windows-separators"
			if valid && (err != nil || !updated[0].Passed() || rows[0].Integrity != "valid") || !valid && (!errors.Is(err, errBrowserEvidence) || updated[0].Passed() || rows[0].Problem == "") {
				t.Fatalf("wrong %s integrity: %#v %#v %v", mode, updated, rows, err)
			}
			if mode == "missing" && rows[0].Problem != "screenshot file is missing" || mode == "oversized" && !strings.Contains(rows[0].Problem, "exceeds 8 MiB") {
				t.Fatal("lost actionable image diagnostic", rows[0].Problem)
			}
			after, _ := json.Marshal(check)
			if !bytes.Equal(before, after) {
				t.Fatal("audit mutated recorded execution")
			}
			projected := updated[0]
			projected.EvidenceError = check.EvidenceError
			if !reflect.DeepEqual(projected, check) {
				t.Fatal("lost execution, log or image metadata")
			}
		})
	}
	if _, _, err := auditBrowserEvidence(context.Background(), t.TempDir(), t.TempDir(), []CheckResult{{Command: "@browser:flow"}}); !errors.Is(err, errBrowserEvidence) {
		t.Fatal("accepted passing browser label without proof", err)
	}
	failed := CheckResult{Command: "@browser:flow", ExitCode: 1, ExecutionEvidence: ExecutionEvidence{FailureKind: "execution", Error: "assertion failed"}}
	_, rows, err := auditBrowserEvidence(context.Background(), "missing", "missing", []CheckResult{failed})
	if err != nil || rows[0].Integrity != "not_recorded" || rows[0].CheckPassed {
		t.Fatal("invented integrity success/failure for absent failed evidence", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = auditBrowserEvidence(ctx, "", "", []CheckResult{failed}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}

func savedScreenshotGoal(t *testing.T) (string, string, State) {
	t.Helper()
	repo := t.TempDir()
	state := State{Version: stateVersion, RunID: "evidence-goal", Goal: "Inspect evidence", Status: "complete", Phase: "reviewing", Cycle: 1, Config: DefaultProjectConfig()}
	dir := filepath.Join(repo, ".forge", "runs", state.RunID)
	state.CheckResults = []CheckResult{screenshotFixture(t, dir)}
	state.RequiredChecks = []string{"@browser:flow"}
	state.SuccessCriteria = []string{"Browser flow"}
	state.Acceptance = []AcceptanceCheck{{Criterion: "Browser flow", Checks: state.RequiredChecks}}
	var err error
	state.CheckedSnapshot, err = WorkspaceSnapshot(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := AtomicJSON(filepath.Join(dir, "state.json"), state); err != nil {
		t.Fatal(err)
	}
	if err := AtomicJSON(filepath.Join(repo, ".forge", "latest.json"), map[string]string{"run_id": state.RunID}); err != nil {
		t.Fatal(err)
	}
	return repo, dir, state
}

func TestBrowserEvidenceInspectionSelectorsAndReadOnlyProjections(t *testing.T) {
	repo, dir, state := savedScreenshotGoal(t)
	before, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	for _, id := range []string{"latest", state.RunID} {
		report, err := InspectBrowserEvidence(context.Background(), repo, id, false, "")
		if err != nil || report.Integrity != "valid" || report.ID != state.RunID || !report.Artifacts[0].CheckPassed {
			t.Fatal(report, err)
		}
		if text := FormatBrowserEvidence(report); !strings.Contains(text, state.CheckResults[0].Browser.SHA256) || !strings.Contains(text, "does not rerun checks") {
			t.Fatal(text)
		}
	}
	for _, kind := range []string{"checks", "browser-checks"} {
		reportID := "check-evidence"
		reportDir := filepath.Join(repo, ".forge", kind, reportID)
		record := VerificationReport{Version: 1, ID: reportID, Status: "passed", Results: []CheckResult{screenshotFixture(t, reportDir)}}
		if err := AtomicJSON(filepath.Join(reportDir, "report.json"), record); err != nil {
			t.Fatal(err)
		}
		if err := AtomicJSON(filepath.Join(repo, ".forge", "last-check.json"), record); err != nil {
			t.Fatal(err)
		}
		id := ""
		if kind == "browser-checks" {
			id = reportID
		}
		report, err := InspectBrowserEvidence(context.Background(), repo, "latest", kind == "checks", id)
		if err != nil || report.Integrity != "valid" || report.Kind != kind {
			t.Fatal(report, err)
		}
	}
	if err := os.Remove(filepath.Join(dir, state.CheckResults[0].Browser.Screenshot)); err != nil {
		t.Fatal(err)
	}
	report, err := InspectBrowserEvidence(context.Background(), repo, state.RunID, false, "")
	if err != nil || report.Integrity != "invalid" || report.RecordedStatus != "complete" || report.Artifacts[0].CheckPassed {
		t.Fatal(report, err)
	}
	value, _, err := InspectRun(repo, state.RunID, "status")
	projected := value.(State)
	if err != nil || projected.Status != "complete" || projected.CheckedSnapshot != "" || projected.CheckResults[0].EvidenceError == "" {
		t.Fatal("status retained stale acceptance", err)
	}
	value, _, err = InspectRun(repo, state.RunID, "acceptance")
	if err != nil || value.([]AcceptanceResult)[0].Status == "passed" {
		t.Fatal("stale image passed acceptance", value, err)
	}
	if !strings.Contains(MarkdownReport(projected), "Browser evidence invalidated") {
		t.Fatal("report hid image integrity failure")
	}
	for _, asJSON := range []bool{true, false} {
		name, err := ExportRun(repo, state.RunID, "", asJSON, false)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(name)
		if !bytes.Contains(data, []byte("evidence_error")) && !bytes.Contains(data, []byte("Browser evidence invalidated")) {
			t.Fatal("export lost integrity diagnostics")
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("read-only inspection rewrote historical checkpoint")
	}
}

func TestBrowserEvidenceInspectionRefusesMalformedRecordsAndSelectors(t *testing.T) {
	for _, args := range [][]string{{"extra"}, {"--run", ""}, {"--report", ""}, {"--run", "x", "--last-check"}, {"--report", "x", "--run", "latest"}, {"--bytes", "2"}} {
		if _, err := parseBrowserEvidenceOptions(args, ".", io.Discard); err == nil {
			t.Fatalf("accepted invalid selection: %q", args)
		}
	}
	if usefulCheck("buildheadway browser evidence") {
		t.Fatal("integrity inspection is not application acceptance")
	}
	for _, mode := range []string{"identity", "version", "malformed", "checks-limit", "bytes-limit", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			repo, dir, state := savedScreenshotGoal(t)
			name := filepath.Join(dir, "state.json")
			switch mode {
			case "identity":
				state.RunID = "different"
			case "version":
				state.Version = stateVersion + 1
			case "checks-limit":
				state.CheckResults = make([]CheckResult, 4097)
			case "malformed":
				if err := os.WriteFile(name, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "bytes-limit":
				if err := os.Truncate(name, browserEvidenceRecordLimit+1); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(t.TempDir(), "state.json")
				if err := AtomicJSON(outside, state); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, name); err != nil {
					t.Skip("symlinks unavailable:", err)
				}
			}
			if mode == "identity" || mode == "version" || mode == "checks-limit" {
				if err := AtomicJSON(name, state); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := InspectBrowserEvidence(context.Background(), repo, "evidence-goal", false, ""); err == nil {
				t.Fatal("accepted invalid evidence record", mode)
			}
		})
	}
	if _, err := InspectBrowserEvidence(context.Background(), t.TempDir(), "../outside", false, ""); err == nil {
		t.Fatal("accepted escaped goal")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InspectBrowserEvidence(ctx, "missing", "latest", false, ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestBrowserEvidenceRealCLIAndTerminalInspection(t *testing.T) {
	repo, dir, state := savedScreenshotGoal(t)
	name := filepath.Join(dir, "state.json")
	before, _ := os.ReadFile(name)
	binary := nativeTestBinary(t)
	args := []string{"browser", "evidence", "--project", repo, "--run", state.RunID, "--json"}
	data, err := exec.Command(binary, args...).Output()
	var report BrowserEvidenceReport
	if err != nil || json.Unmarshal(data, &report) != nil || report.Integrity != "valid" || report.ID != state.RunID {
		t.Fatalf("native inspection: %s %v", data, err)
	}
	if err := os.Remove(filepath.Join(dir, state.CheckResults[0].Browser.Screenshot)); err != nil {
		t.Fatal(err)
	}
	data, err = exec.Command(binary, args...).Output()
	if err == nil || json.Unmarshal(data, &report) != nil || report.Integrity != "invalid" {
		t.Fatalf("native inspector masked invalid proof: %s %v", data, err)
	}
	m := appModel{repo: repo, paths: testPaths(t), busy: true}
	cmd, ok := m.projectCommand("/browser evidence --run " + state.RunID)
	if !ok || cmd == nil {
		t.Fatal("terminal command not available")
	}
	message := cmd().(inspectionMsg)
	if message.Err != nil || !strings.Contains(message.Text, "integrity: invalid") || !m.busy {
		t.Fatalf("terminal hid artifact diagnostics: %#v", message)
	}
	after, _ := os.ReadFile(name)
	if !bytes.Equal(before, after) {
		t.Fatal("CLI or terminal inspection rewrote checkpoint")
	}
}
