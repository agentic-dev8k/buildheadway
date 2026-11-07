package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAboutCapabilitiesMatchAdaptersAndToolBoundaries(t *testing.T) {
	report, err := BuildAboutReport("")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, agent := range report.AgentProjects {
		if seen[agent.ID] || !SupportedBackend(agent.ID) || !strings.HasPrefix(agent.URL, "https://github.com/") {
			t.Fatal("invalid upstream agent credit", agent)
		}
		seen[agent.ID] = true
		if agent.FreeWorkerAdapter != (agent.ID == "opencode") || agent.MCPProfileForwarding != (agent.ID == "codex" || agent.ID == "claude" || agent.ID == "opencode") {
			t.Fatal("credits granted unsupported worker/tool capabilities", agent)
		}
		if !reflect.DeepEqual(agent.Roles[:3], []string{"planning", "review", "consultation"}) {
			t.Fatal("missing senior adapter roles")
		}
	}
	for _, definition := range Registry {
		if seen[definition.ID] != definition.Adapter {
			t.Fatal("credits differ from registered execution adapters", definition.ID)
		}
	}
	if !strings.Contains(FormatAbout(report), "actual commit authorship") || !strings.Contains(FormatAbout(report), "freshly verified zero prices") {
		t.Fatal("credits confused acknowledgments with author/inference evidence")
	}
	if _, err := BuildAboutReport("demo"); err == nil {
		t.Fatal("simulation was represented as an upstream integration")
	}
	for _, command := range []string{"about", "credits"} {
		if got := completionCandidates([]string{command, "--agent", "cl"}); !reflect.DeepEqual(got, []string{"claude", "cline"}) {
			t.Fatal("credits completion lost supported choices", got)
		}
	}
}

func TestCreditsCanBeInspectedDuringAnActiveGoal(t *testing.T) {
	m := newApp(testPaths(t), t.TempDir(), "", false, false)
	m.mode, m.busy, m.stage = "chat", true, "Implementing"
	m.progress = Progress{RunID: "saved-run", PlannerCalls: 4, WorkerCalls: 2}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.cancel = cancel
	beforeProgress, beforeSettings := m.progress, m.settings
	for _, command := range []string{"/about claude", "/credits", "/credits unknown", "/about claude extra"} {
		cmd := m.handle(command)
		if cmd == nil {
			t.Fatal("credits inspection was not dispatched", command)
		}
		msg, ok := cmd().(inspectionMsg)
		if !ok {
			t.Fatal("credits started a goal instead of an inspection")
		}
		if command == "/about claude" && (msg.Err != nil || !strings.Contains(msg.Text, "Claude Code") || strings.Contains(msg.Text, "Gemini CLI")) {
			t.Fatal("Claude credits were not rendered", msg)
		}
		if strings.Contains(command, "unknown") || strings.Contains(command, "extra") {
			if msg.Err == nil {
				t.Fatal("invalid credits command accepted", command)
			}
		}
		updated, _ := m.Update(msg)
		m = updated.(appModel)
		if !m.busy || m.stage != "Implementing" || m.progress != beforeProgress || !reflect.DeepEqual(m.settings, beforeSettings) || ctx.Err() != nil {
			t.Fatal("credits changed the active goal or connection")
		}
	}
	m.width, m.height = 80, 24
	m.sizeUI()
	m.editor.SetValue("unsent direction")
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyCtrlP})
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("credits")})
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.editor.Value() != "/credits" || !m.hasSavedDraft || ctx.Err() != nil {
		t.Fatal("palette selection executed credits or lost the draft")
	}
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.editor.Value() != "unsent direction" {
		t.Fatal("credits palette did not recover the draft")
	}
}
