package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const scanHelp = "--pure --format --model --file --dir --json --plan --yolo"
const scanCatalog = "opencode/verified\n{\"cost\":{\"input\":0,\"output\":0,\"cache\":{\"read\":0,\"write\":0}},\"capabilities\":{\"toolcall\":true}}\nopencode/paid-free\n{\"cost\":{\"input\":1,\"output\":0},\"capabilities\":{\"toolcall\":true}}\n"

func scanFixture(t *testing.T) (DetectedAgent, string) {
	t.Helper()
	helpCache.Clear()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("BUILDHEADWAY_TEST_SCAN_FIXTURE", "1")
	t.Setenv("BUILDHEADWAY_TEST_SCAN_CAPTURE", capture)
	t.Setenv("BUILDHEADWAY_TEST_SCAN_HELP", scanHelp)
	t.Setenv("BUILDHEADWAY_TEST_SCAN_CATALOG", scanCatalog)
	return DetectedAgent{AgentDefinition: Registry[2], Installed: true, Path: binary}, capture
}

func TestAgentChecksSeparateCompatibilityPricingAndInference(t *testing.T) {
	a, capture := scanFixture(t)
	paths := testPaths(t)
	for i := 0; i < 2; i++ {
		checkDetectedAgent(context.Background(), &a, paths, true)
		if a.AdapterCheck.Status != "compatible" || a.FreeModels.Status != "verified" || len(a.FreeModels.Models) != 1 || a.FreeModels.Models[0] != "opencode/verified" || a.FreeModels.CheckedAt == "" {
			t.Fatalf("incorrect catalog proof: %+v %+v", a.AdapterCheck, a.FreeModels)
		}
	}
	data, _ := os.ReadFile(capture)
	refreshes := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var args []string
		if err := json.Unmarshal([]byte(line), &args); err != nil {
			t.Fatal(err)
		}
		if contains(args, "models") {
			refreshes++
			if !contains(args, "--pure") || !contains(args, "--refresh") || !contains(args, "--verbose") {
				t.Fatal("catalog was not freshly verified")
			}
		} else if !contains(args, "--help") {
			t.Fatalf("unexpected call: %v", args)
		}
	}
	if refreshes != 2 {
		t.Fatalf("scan reused stale prices: %s", data)
	}
	text := FormatInventory(Inventory{Agents: []DetectedAgent{a}})
	if !strings.Contains(text, "inference are not checked") || !strings.Contains(text, "opencode/verified") {
		t.Fatal(text)
	}
}

func TestAgentChecksBlockUnsupportedUnknownPricesAndCancellation(t *testing.T) {
	a, _ := scanFixture(t)
	paths := testPaths(t)
	t.Setenv("BUILDHEADWAY_TEST_SCAN_CATALOG", "opencode/free-by-name\n{\"capabilities\":{\"toolcall\":true}}\n")
	checkDetectedAgent(context.Background(), &a, paths, true)
	if a.FreeModels.Status != "error" || len(a.FreeModels.Models) != 0 {
		t.Fatal("unknown prices were accepted")
	}
	// Different backend avoids the intentionally cached help for a stable binary.
	a.ID = "cline"
	checkDetectedAgent(context.Background(), &a, paths, true)
	if a.AdapterCheck.Status != "compatible" || a.FreeModels.Status != "unsupported" {
		t.Fatal("Cline was advertised as a verified free worker")
	}
	a.Installed = false
	checkDetectedAgent(context.Background(), &a, paths, true)
	if a.AdapterCheck.Status != "not_installed" {
		t.Fatal("missing executable was hidden")
	}
	a.ID, a.Installed = "opencode", true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	checkDetectedAgent(ctx, &a, paths, true)
	if a.FreeModels.Status != "error" {
		t.Fatal("cancelled discovery was accepted")
	}
}

func TestOpenCode2CompatibilityReportsSupportedPackage(t *testing.T) {
	a, _ := scanFixture(t)
	t.Setenv("BUILDHEADWAY_TEST_SCAN_HELP", "--standalone --format --model --file")
	checkDetectedAgent(context.Background(), &a, testPaths(t), true)
	if a.AdapterCheck.Status != "incompatible" || a.FreeModels.Status != "blocked" || !strings.Contains(a.AdapterCheck.Detail, "opencode-ai") || !strings.Contains(a.AdapterCheck.Detail, "@opencode/cli") {
		t.Fatalf("unsupported generation not identified: %+v", a.AdapterCheck)
	}
}

func TestNativeScanExplicitChecksAndNoProjectMutation(t *testing.T) {
	a, _ := scanFixture(t)
	tools := t.TempDir()
	fixture := filepath.Join(tools, ExecutableName("opencode", runtime.GOOS))
	data, err := os.ReadFile(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture, data, 0755); err != nil {
		t.Fatal(err)
	}
	project, home := t.TempDir(), t.TempDir()
	checkpoint := filepath.Join(project, "checkpoint.txt")
	if err := os.WriteFile(checkpoint, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	binary := nativeTestBinary(t)
	invoke := func(args ...string) ([]byte, error) {
		c := exec.Command(binary, append([]string{"scan"}, args...)...)
		c.Dir = project
		c.Env = setEnv(testCommandEnv(home), "BUILDHEADWAY_AGENT_PATH", tools)
		return c.CombinedOutput()
	}
	for _, checked := range []bool{false, true} {
		args := []string{"--json", "--agent", "opencode"}
		if checked {
			args = append(args, "--models")
		}
		out, err := invoke(args...)
		if err != nil {
			t.Fatalf("scan: %v %s", err, out)
		}
		var got Inventory
		if err := json.Unmarshal(out, &got); err != nil || len(got.Agents) != 1 {
			t.Fatalf("bad JSON: %s", out)
		}
		g := got.Agents[0]
		if contains(g.Capabilities, "verified-free-worker") || (g.FreeModels != nil) != checked || (g.AdapterCheck != nil) != checked {
			t.Fatalf("installation misrepresented as verification: %s", out)
		}
	}
	for _, args := range [][]string{{"--agent", ""}, {"--agent", "unknown"}, {"extra"}, {"--typo"}} {
		if out, err := invoke(args...); err == nil {
			t.Fatalf("accepted invalid selection: %v %s", args, out)
		}
	}
	if got, _ := os.ReadFile(checkpoint); string(got) != "unchanged" {
		t.Fatal("scan rewrote project files")
	}
	entries, _ := os.ReadDir(project)
	if len(entries) != 1 {
		t.Fatal("scan created project/run state")
	}
}

func TestNativeTargetedScanDoesNotProbeUnselectedAgents(t *testing.T) {
	a, capture := scanFixture(t)
	tools := t.TempDir()
	data, err := os.ReadFile(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codex", "opencode", "goose"} {
		if err := os.WriteFile(filepath.Join(tools, ExecutableName(name, runtime.GOOS)), data, 0755); err != nil {
			t.Fatal(err)
		}
	}
	binary := nativeTestBinary(t)
	home, project := t.TempDir(), t.TempDir()
	marker := filepath.Join(project, "unchanged.txt")
	if err := os.WriteFile(marker, []byte("preserve project"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func(args ...string) ([]byte, error) {
		cmd := exec.Command(binary, append([]string{"scan"}, args...)...)
		cmd.Dir = project
		cmd.Env = setEnv(testCommandEnv(home), "BUILDHEADWAY_AGENT_PATH", tools)
		return cmd.CombinedOutput()
	}
	for _, args := range [][]string{{"--agent", "unknown"}, {"--agent", ""}, {"--typo"}, {"unexpected"}} {
		if out, err := invoke(args...); err == nil {
			t.Fatalf("accepted invalid selection %v: %s", args, out)
		}
		if _, err := os.Stat(capture); !os.IsNotExist(err) {
			t.Fatalf("invalid selection %v executed an agent: %v", args, err)
		}
	}
	readCalls := func() [][]string {
		t.Helper()
		raw, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		var calls [][]string
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			var call []string
			if err := json.Unmarshal([]byte(line), &call); err != nil {
				t.Fatal(err, string(raw))
			}
			calls = append(calls, call)
		}
		return calls
	}
	out, err := invoke("--agent", "opencode", "--models", "--json")
	if err != nil {
		t.Fatal(err, string(out))
	}
	var checked Inventory
	if err := json.Unmarshal(out, &checked); err != nil || len(checked.Agents) != 1 || checked.Agents[0].ID != "opencode" || checked.Agents[0].FreeModels == nil || checked.Agents[0].FreeModels.Status != "verified" {
		t.Fatal("selected catalog lost verification", err, string(out))
	}
	calls := readCalls()
	versions, refreshes, globalHelp, runHelp := 0, 0, 0, 0
	for _, c := range calls {
		if len(c) == 1 && c[0] == "--help" {
			globalHelp++
		}
		if len(c) == 2 && c[0] == "run" && c[1] == "--help" {
			runHelp++
		}
		if contains(c, "--version") {
			versions++
		}
		if contains(c, "models") {
			refreshes++
			if !contains(c, "--refresh") {
				t.Fatal("stale catalog", c)
			}
		}
	}
	if versions != 1 || refreshes != 1 || globalHelp != 1 || runHelp != 1 || len(calls) != 4 {
		t.Fatalf("targeted scan executed unrelated probes or lost checks: %v", calls)
	}
	if err := os.Remove(capture); err != nil {
		t.Fatal(err)
	}
	out, err = invoke("--agent", "goose", "--check", "--json")
	var detected Inventory
	if err == nil || json.Unmarshal(out, &detected) != nil || len(detected.Agents) != 1 || detected.Agents[0].AdapterCheck.Status != "unsupported" {
		t.Fatal("detection-only selection bypassed checks", err, string(out))
	}
	calls = readCalls()
	if len(calls) != 1 || !contains(calls[0], "--version") {
		t.Fatal("detection-only selection executed other agents", calls)
	}
	if err := os.Remove(capture); err != nil {
		t.Fatal(err)
	}
	out, err = invoke("--json")
	var all Inventory
	if err != nil || json.Unmarshal(out, &all) != nil || len(all.Agents) != len(Registry) {
		t.Fatal("ordinary inventory lost agents", err, string(out))
	}
	for _, id := range []string{"codex", "opencode", "goose"} {
		g, ok := all.Find(id)
		if !ok || !g.Installed || g.Version != "scan-fixture-1" {
			t.Fatal("ordinary inventory skipped installed agent", id, g)
		}
	}
	if len(readCalls()) != 3 {
		t.Fatal("ordinary inventory did not probe all fixtures")
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "preserve project" {
		t.Fatal("scan changed project", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("scan created persistent state", err, entries)
	}
}
