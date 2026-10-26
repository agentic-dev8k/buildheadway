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

func TestLargeProcessLogRetainsDiskEvidenceAndBoundsCapture(t *testing.T) {
	binary, _ := os.Executable()
	dir := t.TempDir()
	payload := filepath.Join(dir, "payload")
	data := strings.Repeat("x", int(processOutputLimit)+2048) + "END_OF_LOG\n"
	if err := os.WriteFile(payload, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	env := setEnv(os.Environ(), "BUILDHEADWAY_TEST_ADAPTER_CAPTURE", filepath.Join(dir, "capture"))
	env = setEnv(env, "BUILDHEADWAY_TEST_ADAPTER_OUTPUT_FILE", payload)
	log := filepath.Join(dir, "full.log")
	result, err := RunProcess(context.Background(), []string{binary}, dir, log, "", env, 10*time.Second, nil)
	if err != nil || result.Code != 0 || !result.OutputTruncated || len(result.Output) != int(processOutputLimit) || !strings.HasSuffix(result.Output, "END_OF_LOG\n") {
		t.Fatalf("capture: code=%d truncated=%v bytes=%d err=%v", result.Code, result.OutputTruncated, len(result.Output), err)
	}
	info, err := os.Stat(log)
	if err != nil || info.Size() != int64(len(data)) {
		t.Fatalf("full evidence lost: %v %v", info, err)
	}
}

func TestAdaptersRejectOversizedOutputBeforeCompletionOrCostApproval(t *testing.T) {
	binary, _ := os.Executable()
	for _, planner := range []bool{true, false} {
		r, _ := fixtureRunner(t)
		os.MkdirAll(r.Dir, 0700)
		t.Setenv("BUILDHEADWAY_TEST_ADAPTER_CAPTURE", filepath.Join(t.TempDir(), "capture"))
		payload := filepath.Join(t.TempDir(), "payload")
		// A charge at the start would disappear from an ordinary tail capture.
		data := "{\"type\":\"step_finish\",\"part\":{\"cost\":1}}\n" + strings.Repeat(" ", int(processOutputLimit)) + "\n" + decisionJSON(true, nil, nil)
		os.WriteFile(payload, []byte(data), 0600)
		t.Setenv("BUILDHEADWAY_TEST_ADAPTER_OUTPUT_FILE", payload)
		c := Connection{Backend: "opencode", Model: "opencode/free", Executable: binary, RequireFree: true}
		_, err := r.invokeCLI(context.Background(), c, "Inspect safely", "large-output", planner, "opencode/free")
		var oversized outputLimitError
		if !errors.As(err, &oversized) {
			t.Fatalf("truncated response was accepted: %v", err)
		}
	}
}

func TestOutputLimitBlocksRoutingWithoutFailover(t *testing.T) {
	for _, planner := range []bool{true, false} {
		r, _ := fixtureRunner(t)
		r.Settings.Planner = Connection{Backend: "opencode", Model: "auto"}
		r.Discover = func(context.Context) ([]string, error) { return []string{"opencode/first", "opencode/second"}, nil }
		calls := 0
		r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
			calls++
			return "", outputLimitError{}
		}
		var err error
		if planner {
			_, err = r.askPlanner(context.Background(), false)
		} else {
			_, err = r.workOnce(context.Background(), Task{"one", "Work", "Implement"}, "")
		}
		var oversized outputLimitError
		if !errors.As(err, &oversized) || calls != 1 {
			t.Fatalf("limit bypassed: calls=%d err=%v", calls, err)
		}
	}
}

func TestExactOutputLimitAndLogReadErrors(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(strings.Repeat("x", int(processOutputLimit)))
	data, truncated, err := processOutput(f)
	if err != nil || truncated || len(data) != int(processOutputLimit) {
		t.Fatal("exact limit rejected")
	}
	f.Close()
	if _, _, err := processOutput(f); err == nil {
		t.Fatal("log read failure ignored")
	}
}

func TestOversizedTimedOutAdapterPreservesOutputBlocker(t *testing.T) {
	binary, _ := os.Executable()
	r, _ := fixtureRunner(t)
	os.MkdirAll(r.Dir, 0700)
	r.State.Config.WorkerTimeoutSeconds = 1
	t.Setenv("BUILDHEADWAY_TEST_ADAPTER_CAPTURE", filepath.Join(t.TempDir(), "capture"))
	t.Setenv("BUILDHEADWAY_TEST_ADAPTER_HOLD_OUTPUT", "1")
	payload := filepath.Join(t.TempDir(), "payload")
	os.WriteFile(payload, []byte(strings.Repeat("x", int(processOutputLimit)+4096)), 0600)
	t.Setenv("BUILDHEADWAY_TEST_ADAPTER_OUTPUT_FILE", payload)
	c := Connection{Backend: "opencode", Model: "opencode/free", Executable: binary, RequireFree: true}
	_, err := r.invokeCLI(context.Background(), c, "Inspect", "timeout-output", false, "opencode/free")
	var oversized outputLimitError
	if !errors.As(err, &oversized) {
		t.Fatalf("timeout masked missing evidence: %v", err)
	}
}
