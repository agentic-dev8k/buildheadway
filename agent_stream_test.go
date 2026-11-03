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

const quotaDiagnostic = `timestamp=2026-11-03T17:58:46Z level=ERROR run=fixture message="stream error" providerID=opencode modelID=verified error.error="AI_APICallError: Free usage limit reached. Free models work in OpenCode; subscribe to keep going"`

func TestAgentStreamOnlyAuthoritativeProviderFailuresInterrupt(t *testing.T) {
	for _, tc := range []struct{ backend, model, line, kind string }{
		{"opencode", "opencode/verified", quotaDiagnostic, "auth"},
		{"opencode", "opencode/verified", `{"type":"error","error":{"name":"APIError","data":{"message":"Free usage limit reached","statusCode":429}}}`, "auth"},
		{"cline", "model", `{"type":"error","message":"Unauthorized: re-authenticate"}`, "auth"},
		{"cline", "model", `{"type":"agent_event","event":{"type":"error","error":{"message":"Unauthorized: re-authenticate"}}}`, "auth"},
		{"cline", "model", `{"type":"run_result","finishReason":"error","text":"insufficient quota"}`, "auth"},
		{"opencode", "opencode/verified", strings.Replace(quotaDiagnostic, "Free usage limit reached", "not available in your region", 1), "policy"},
		{"opencode", "opencode/another", quotaDiagnostic, ""},
		{"opencode", "opencode/verified", strings.Replace(quotaDiagnostic, "level=ERROR", "level=INFO", 1), ""},
		{"opencode", "opencode/verified", `{"type":"text","part":{"text":"Free usage limit reached"}}`, ""},
		{"opencode", "opencode/verified", `{"type":"tool_use","part":{"output":"Unauthorized: example error"}}`, ""},
		{"cline", "model", `{"type":"run_result","finishReason":"completed","text":"Example: Unauthorized"}`, ""},
		{"opencode", "opencode/verified", `A diagnostic might say Free usage limit reached`, ""},
		{"opencode", "opencode/verified", strings.Replace(quotaDiagnostic, "Free usage limit reached", "503 overloaded", 1), ""},
	} {
		err := agentStreamBlocker(tc.backend, tc.model, tc.line)
		if tc.kind == "" {
			if err != nil {
				t.Fatalf("non-provider text became a blocker: %s: %v", tc.line, err)
			}
		} else if err == nil || modelFailureKind(err, false) != tc.kind || !hardModelFailure(err) {
			t.Fatalf("lost hard blocker (%s): %v", tc.kind, err)
		}
	}
}

func TestHangingAgentStopsOnProviderBlockerAndKeepsFullLog(t *testing.T) {
	for _, backend := range []string{"opencode", "cline"} {
		t.Run(backend, func(t *testing.T) {
			r, _ := fixtureRunner(t)
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(r.Dir, 0700); err != nil {
				t.Fatal(err)
			}
			payload := quotaDiagnostic + "\n"
			if backend == "cline" {
				payload = `{"type":"run_result","finishReason":"error","text":"Unauthorized: re-authenticate your Cline account"}` + "\n"
			}
			output := filepath.Join(t.TempDir(), "stream.log")
			os.WriteFile(output, []byte(payload), 0600)
			capture := filepath.Join(t.TempDir(), "args.json")
			t.Setenv("BUILDHEADWAY_TEST_ADAPTER_CAPTURE", capture)
			t.Setenv("BUILDHEADWAY_TEST_ADAPTER_OUTPUT_FILE", output)
			t.Setenv("BUILDHEADWAY_TEST_ADAPTER_HOLD_OUTPUT", "1")
			start := time.Now()
			_, err = r.invokeCLI(context.Background(), Connection{Backend: backend, Executable: binary, Model: "opencode/verified"}, "Inspect the project", "plan", true, "")
			if err == nil || !hardModelFailure(err) || modelFailureKind(err, false) != "auth" || errors.Is(err, context.Canceled) || time.Since(start) > 8*time.Second {
				t.Fatalf("provider failure waited for deadline or lost priority: %v (%s)", err, time.Since(start))
			}
			logs, _ := filepath.Glob(filepath.Join(r.Dir, "*plan*.log"))
			if len(logs) != 1 {
				t.Fatal(logs)
			}
			if got, _ := os.ReadFile(logs[0]); string(got) != payload {
				t.Fatal("provider evidence was not retained")
			}
			if backend == "opencode" {
				var got struct {
					Args []string `json:"args"`
				}
				if err := ReadJSON(capture, &got); err != nil {
					t.Fatal(err)
				}
				if !contains(got.Args, "--print-logs") || !contains(got.Args, "ERROR") {
					t.Fatal("missing error diagnostics")
				}
			}
		})
	}
}

func TestUsageQuotaNeverRotatesToAnotherFreeWorker(t *testing.T) {
	r, _ := fixtureRunner(t)
	discoveries, calls := 0, 0
	r.Discover = func(context.Context) ([]string, error) {
		discoveries++
		return []string{"opencode/first", "opencode/second"}, nil
	}
	r.Invoke = func(context.Context, Connection, string, string, bool, string) (string, error) {
		calls++
		return "", errors.New("Free usage limit reached")
	}
	_, err := r.workOnce(context.Background(), Task{"one", "Repair", "Implement the fix"}, "")
	if err == nil || !hardModelFailure(err) || calls != 1 || discoveries == 0 || r.State.Status == "complete" {
		t.Fatalf("quota was bypassed: %v calls=%d refreshes=%d", err, calls, discoveries)
	}
}
