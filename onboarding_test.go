package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOnboardingReviewsMissingWorkerBeforeInstallation(t *testing.T) {
	m := newApp(testPaths(t), t.TempDir(), "Preserve this goal", false, false)
	m.inventory = Inventory{Agents: []DetectedAgent{{AgentDefinition: Registry[0], Installed: true, Path: "/fixture/codex", AdapterCheck: &AgentCheck{Status: "compatible"}}, {AgentDefinition: Registry[2]}}}
	m.connections()
	m.selectSetupChoice("codex")
	if m.busy || m.setupBackend != "codex" || len(m.choices) != 3 || m.choices[0].Backend != "@install" || m.initial != "Preserve this goal" {
		t.Fatalf("selection silently started setup: %+v", m.choices)
	}
	found := false
	for _, e := range m.entries {
		found = found || strings.Contains(e.Text, "opencode-ai")
	}
	if !found {
		t.Fatal("missing worker package not disclosed")
	}
	if _, err := os.Stat(m.paths.Data); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(m.paths.Data)
		if len(entries) > 0 {
			t.Fatal("review installed or saved anything")
		}
	}
	m.selectSetupChoice("@back")
	if m.setupBackend != "" || m.busy {
		t.Fatal("back started installation")
	}
}

func TestSetupPolicyDeclineSurvivesApprovalOfOnlyNamedPlatforms(t *testing.T) {
	paths := testPaths(t)
	if err := updateSetupPolicy(paths, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := updateSetupPolicy(paths, false, []string{"opencode"}); err != nil {
		t.Fatal(err)
	}
	if err := checkInstallationPolicy(paths, []string{"opencode"}, true); err != nil {
		t.Fatal(err)
	}
	if err := checkInstallationPolicy(paths, []string{"cline"}, true); err == nil {
		t.Fatal("approval widened to another agent")
	}
	p, err := readSetupPolicy(paths)
	if err != nil || !p.DeclineInstall || len(p.Approved) != 1 {
		t.Fatal(p, err)
	}
}

func TestOnboardingInspectionAndDiscoveryDoNotAlterAnActiveGoal(t *testing.T) {
	m := newApp(testPaths(t), t.TempDir(), "", false, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.busy = true
	m.cancel = cancel
	m.connected = true
	m.settings = Settings{Planner: Connection{Backend: "codex", Model: "pinned"}, Worker: Connection{Backend: "opencode", Model: "opencode/pinned"}}
	if cmd := m.handle("/setup"); cmd != nil {
		t.Fatal("setup replaced active job")
	}
	before := m.settings
	m.handle("/freebies status")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(appModel)
	if !m.busy || ctx.Err() != nil || m.settings.Planner.Model != before.Planner.Model || m.settings.Worker.Model != before.Worker.Model {
		t.Fatal("discovery mutated active coding goal")
	}
	if _, err := os.Stat(filepath.Join(m.repo, ".forge", "runs")); !os.IsNotExist(err) {
		t.Fatal("inspection started a goal")
	}
}

func TestOnboardingReviewDisclosesPackagesAndRequiresReadableTerminal(t *testing.T) {
	m := newApp(testPaths(t), t.TempDir(), "", false, false)
	m.inventory = Inventory{}
	m.selectSetupChoice("cline")
	view := Clean(m.selectionView(100, 16))
	if !strings.Contains(view, "cline") || !strings.Contains(view, "opencode-ai") || !strings.Contains(view, "Skip installation") {
		t.Fatal(view)
	}
	m.width, m.height = 40, 15
	if cmd := m.selectSetupChoice("@install"); cmd != nil || m.busy {
		t.Fatal("small terminal approved installation")
	}
	p, err := readSetupPolicy(m.paths)
	if err != nil || len(p.Approved) != 0 || len(m.setupNeeded) != 2 {
		t.Fatal(p, err)
	}
	m.selectSetupChoice("@back")
	if len(m.setupNeeded) != 0 || m.setupBackend != "" {
		t.Fatal("stale approval survives back")
	}
}

func TestSetupPolicyRejectsAmbiguousRecords(t *testing.T) {
	paths := testPaths(t)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`null`, `{}`, `{"version":1,"allow_everything":true}`, `{"version":1} {"version":1}`} {
		if err := os.WriteFile(filepath.Join(paths.Data, "setup-policy.json"), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if err := checkInstallationPolicy(paths, []string{"opencode"}, true); err == nil {
			t.Fatalf("ambiguous install authorization accepted: %s", value)
		}
	}
}
