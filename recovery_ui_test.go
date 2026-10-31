package main

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func uiSend(m appModel, k tea.KeyMsg) appModel { v, _ := m.Update(k); return v.(appModel) }
func TestRecoveredPaletteNeedsExplicitDispatchAndPreservesDraft(t *testing.T) {
	p := testPaths(t)
	m := newApp(p, t.TempDir(), "", false, false)
	m.width, m.height = 80, 24
	m.sizeUI()
	m.editor.SetValue("original unsent goal 界")
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyCtrlP})
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("status")})
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.paletteOpen || m.editor.Value() != "/status" || m.busy || !m.hasSavedDraft {
		t.Fatal("selection dispatched or lost draft")
	}
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyCtrlP})
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.editor.Value() != "original unsent goal 界" || m.hasSavedDraft {
		t.Fatal("repeated selection lost original draft")
	}
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyCtrlP})
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyCtrlP})
	if m.paletteOpen || m.hasSavedDraft || m.editor.Value() != "original unsent goal 界" {
		t.Fatal("palette shortcut did not restore the draft when toggled closed")
	}
	m.openPalette()
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("界", 200))})
	if len([]rune(m.paletteQuery)) != 128 {
		t.Fatal("unbounded palette query")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.busy, m.cancel = true, cancel
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if ctx.Err() == nil {
		t.Fatal("palette swallowed pause")
	}
}
func TestRecoveredScrollResizeAndTinyWindowKeepState(t *testing.T) {
	m := newApp(testPaths(t), t.TempDir(), "", false, false)
	m.width, m.height = 60, 20
	m.sizeUI()
	for i := 0; i < 40; i++ {
		m.add("Team", strings.Repeat("row ", 30))
	}
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyCtrlHome})
	m.add("Team", "new message")
	if m.following || m.unread != 1 || m.viewport.YOffset != 0 {
		t.Fatal("new output stole scroll position")
	}
	m.width, m.height = 80, 80
	m.sizeUI()
	if m.following {
		t.Fatal("resize silently resumed following")
	}
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyCtrlEnd})
	if !m.following || m.unread != 0 {
		t.Fatal("explicit follow failed")
	}
	for i := 0; i < 410; i++ {
		m.add("Team", "bounded entry")
	}
	if len(m.entries) != 400 {
		t.Fatal("unbounded transcript")
	}
	m.editor.SetValue("draft")
	m.width, m.height = 10, 5
	m.sizeUI()
	m = uiSend(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.editor.Value() != "draft" || m.busy {
		t.Fatal("tiny terminal submitted hidden input")
	}
	for _, size := range [][2]int{{20, 10}, {40, 15}, {80, 24}, {120, 36}} {
		m.width, m.height = size[0], size[1]
		m.sizeUI()
		view := m.View()
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > m.width {
				t.Fatalf("width overflow %d > %d", lipgloss.Width(line), m.width)
			}
		}
		if lipgloss.Height(view) > m.height {
			t.Fatalf("height overflow %d > %d", lipgloss.Height(view), m.height)
		}
	}
}
