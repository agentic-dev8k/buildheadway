package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"
)

func TestMCPPresetPlansPreserveDirectoryScopeAndExplicitWriteChoice(t *testing.T) {
	repo := t.TempDir()
	sub := filepath.Join(repo, "reference with spaces مفت")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(sub)
	plan, err := planMCPPreset(repo, "filesystem", "reference-files", []string{filepath.Base(sub), sub}, false)
	if err != nil || plan.Name != "reference-files" || !slices.Contains(plan.Server.Args, "--read-only") || !slices.Contains(plan.Server.Args, "--confine-roots") || plan.Server.Args[len(plan.Server.Args)-1] != canonical || len(plan.Server.Args) != 6 {
		t.Fatal("filesystem scope was widened, not resolved relative to the project or duplicated", plan, err)
	}
	writePlan, err := planMCPPreset(repo, "filesystem", "", nil, true)
	if err != nil || slices.Contains(writePlan.Server.Args, "--read-only") || !slices.Contains(writePlan.Server.Args, "--confine-roots") || !strings.Contains(formatMCPPlan(writePlan), "Editing is enabled") {
		t.Fatal("explicit write mode lost scope or was not explained", writePlan, err)
	}
	for _, tc := range []struct {
		preset string
		paths  []string
		write  bool
	}{
		{"missing", nil, false}, {"filesystem", []string{""}, false},
		{"filesystem", []string{"missing directory"}, false},
		{"git", []string{repo, sub}, false}, {"git", nil, true},
		{"fetch", []string{repo}, false}, {"memory", nil, true},
		{"filesystem", make([]string, 17), false},
	} {
		if _, err := planMCPPreset(repo, tc.preset, "test", tc.paths, tc.write); err == nil {
			t.Fatalf("accepted invalid scope/options: %+v", tc)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".forge")); !os.IsNotExist(err) {
		t.Fatal("planning created project state")
	}
}

func TestMCPPresetCredentialsStayIndirectAndPackagesStayPinned(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("GITHUB_MCP_TOKEN", "private-token-fixture-never-save")
	t.Setenv("MEMORY_FILE_PATH", filepath.Join(repo, "private-location.jsonl"))
	seen := map[string]bool{}
	for _, preset := range mcpPresetCatalog() {
		if seen[preset.ID] || !strings.HasPrefix(preset.Documentation, "https://github.com/") {
			t.Fatal("invalid provider catalog provenance", preset)
		}
		seen[preset.ID] = true
		plan, err := planMCPPreset(repo, preset.ID, "", nil, false)
		if err != nil {
			t.Fatal(preset.ID, err)
		}
		if preset.Transport == "stdio" && (strings.Contains(strings.Join(plan.Server.Args, " "), "latest") || !strings.Contains(strings.Join(plan.Server.Args, " "), preset.Version)) {
			t.Fatal("local package version is not reproducible", preset.ID, plan)
		}
		data, _ := json.Marshal(plan)
		if bytes.Contains(data, []byte("private-token-fixture")) || bytes.Contains(data, []byte("private-location.jsonl")) {
			t.Fatal("preset expanded an environment value", preset.ID)
		}
		if preset.ID == "github" && (plan.Server.URL != "https://api.githubcopilot.com/mcp/readonly" || plan.Server.BearerTokenEnv != "GITHUB_MCP_TOKEN") {
			t.Fatal("GitHub preset lost read-only scope or credential indirection")
		}
		if preset.ID == "memory" && !slices.Equal(plan.Server.Env, []string{"MEMORY_FILE_PATH"}) {
			t.Fatal("memory preset lost its explicit storage setting")
		}
	}
	t.Setenv("MEMORY_FILE_PATH", "")
	plan, _ := planMCPPreset(repo, "memory", "", nil, false)
	if err := mcpCredentials(plan.Name, plan.Server); err == nil || !strings.Contains(err.Error(), "MEMORY_FILE_PATH") {
		t.Fatal("memory could start without its selected persistent storage", err)
	}
	unsafe := MCPConfigurationPlan{Name: "safe", Server: MCPServer{Command: "node\x1b[2J\u009b", Args: []string{"argument\r\u009d52"}}}
	for _, r := range formatMCPPlan(unsafe) {
		if unicode.IsControl(r) && r != '\n' {
			t.Fatalf("preview emitted terminal control U+%04X", r)
		}
	}
}

func TestExtensionTemplatesCreateExpandableSkillsAndPromptsWithoutOverwriting(t *testing.T) {
	for _, item := range extensionTemplateCatalog() {
		for _, kind := range []string{"skill", "prompt"} {
			paths, repo := testPaths(t), t.TempDir()
			created, err := InitExtensionTemplate(paths, repo, "custom-workflow", kind, false, item.ID)
			if err != nil {
				t.Fatal(item.ID, kind, err)
			}
			before, _ := os.ReadFile(created.Source)
			goal, err := ExpandWorkflow(paths, repo, created.Name, []string{"preserve $(literal) request"})
			if err != nil || !strings.Contains(goal, "preserve $(literal) request") || strings.Contains(goal, "$ARGUMENTS") || strings.Contains(goal, "${@") {
				t.Fatal("template cannot run through normal expansion", item.ID, kind, goal, err)
			}
			if _, err := InitExtensionTemplate(paths, repo, created.Name, kind, false, "general"); err == nil {
				t.Fatal("template creation overwrote user instructions")
			}
			if after, _ := os.ReadFile(created.Source); !bytes.Equal(before, after) {
				t.Fatal("existing instructions changed")
			}
		}
	}
	root := filepath.Join(t.TempDir(), "not-created")
	if _, err := InitExtensionTemplate(Paths{Config: root}, root, "example", "skill", true, "unknown"); err == nil {
		t.Fatal("accepted an unknown template")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("unknown template created directories")
	}
}

func TestIntegrationCatalogsPreserveAnActiveTerminalGoalAndDraft(t *testing.T) {
	paths, repo := testPaths(t), t.TempDir()
	invalid := []byte("not project configuration")
	if err := os.WriteFile(filepath.Join(repo, ".buildheadway.json"), invalid, 0600); err != nil {
		t.Fatal(err)
	}
	m := newApp(paths, repo, "", false, false)
	m.mode, m.busy, m.stage = "chat", true, "Implementing goal"
	m.editor.SetValue("retain this unsent draft")
	canceled := false
	m.cancel = func() { canceled = true }
	m.progress = Progress{RunID: "active-goal", PlannerCalls: 3, WorkerCalls: 2}
	progress := m.progress
	for _, command := range []string{"/mcp catalog", "/mcp catalog filesystem", "/extensions catalog", "/extensions catalog api-review"} {
		cmd := m.handle(command)
		if cmd == nil {
			t.Fatal("catalog command was not routed", command)
		}
		msg, ok := cmd().(inspectionMsg)
		if !ok || msg.Err != nil || msg.Text == "" {
			t.Fatal("catalog inspection read broken project settings", command, msg)
		}
		updated, _ := m.Update(msg)
		m = updated.(appModel)
		if !m.busy || m.stage != "Implementing goal" || m.editor.Value() != "retain this unsent draft" || canceled || m.progress != progress {
			t.Fatal("catalog inspection changed active goal state", command)
		}
	}
	if after, _ := os.ReadFile(filepath.Join(repo, ".buildheadway.json")); !bytes.Equal(after, invalid) {
		t.Fatal("catalog inspection rewrote configuration")
	}
	for _, tc := range []struct{ words, want []string }{
		{[]string{"mcp", "add", "--preset", "fi"}, []string{"filesystem"}},
		{[]string{"mcp", "catalog", "con"}, []string{"context7"}},
		{[]string{"extensions", "init", "--template", "api"}, []string{"api-review"}},
		{[]string{"extensions", "catalog", "mcp"}, []string{"mcp-integration"}},
	} {
		if got := completionCandidates(tc.words); !slices.Equal(got, tc.want) {
			t.Fatal("catalog completion mismatch", tc.words, got, tc.want)
		}
	}
}
