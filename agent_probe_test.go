package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBoundedAgentProbeOutputAcceptsWritesWithoutGrowing(t *testing.T) {
	var output boundedAgentProbeOutput
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			data := strings.Repeat("x", agentVersionProbeOutputLimit)
			if n, err := output.Write([]byte(data)); err != nil || n != len(data) {
				t.Errorf("write: n=%d err=%v", n, err)
			}
		}()
	}
	workers.Wait()
	data, truncated := output.result()
	if !truncated || len(data) != agentVersionProbeOutputLimit {
		t.Fatalf("capture grew or lost truncation: bytes=%d truncated=%t", len(data), truncated)
	}
}

func versionProbeFixture(t *testing.T, mode string, ctx context.Context) DetectedAgent {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	name := ExecutableName("probe-agent", runtime.GOOS)
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, name), data, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUILDHEADWAY_TEST_VERSION_PROBE", mode)
	t.Setenv("BUILDHEADWAY_AGENT_PATH", tools)
	inventory := scanAgents(ctx, testPaths(t), []AgentDefinition{{ID: "probe-agent", Name: "Probe Agent", Command: "probe-agent", Adapter: true}})
	if len(inventory.Agents) != 1 || !inventory.Agents[0].Installed {
		t.Fatalf("fixture was not discovered: %+v", inventory)
	}
	return inventory.Agents[0]
}

func TestAgentVersionProbeBoundsOutputAndExplainsFailures(t *testing.T) {
	large := versionProbeFixture(t, "large", context.Background())
	if large.Version != "" || !strings.Contains(large.ProbeError, "16 KiB") {
		t.Fatalf("oversized version output was accepted: %+v", large)
	}

	failed := versionProbeFixture(t, "exit", context.Background())
	if failed.Version != "" || !strings.Contains(failed.ProbeError, "status 7") || !strings.Contains(failed.ProbeError, "credential stale") || strings.Contains(failed.ProbeError, "\x1b") || strings.Contains(failed.ProbeError, "second line") {
		t.Fatalf("version failure was not actionable and inert: %+v", failed)
	}

	empty := versionProbeFixture(t, "empty", context.Background())
	if empty.Version != "" || !strings.Contains(empty.ProbeError, "no version text") {
		t.Fatalf("empty version output was accepted: %+v", empty)
	}
}

func TestAgentVersionProbeHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	started := time.Now()
	agent := versionProbeFixture(t, "hold", ctx)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("cancellation was not prompt: %s", elapsed)
	}
	if agent.Version != "" || !strings.Contains(agent.ProbeError, "canceled") {
		t.Fatalf("cancellation was hidden: %+v", agent)
	}
}
