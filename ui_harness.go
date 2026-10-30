package main

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var paletteCommands = []string{"/acceptance", "/agents", "/attach ", "/browser", "/check", "/commands", "/config", "/connect", "/context", "/debug ", "/detach ", "/diff", "/discussion", "/docs ", "/doctor", "/events", "/export", "/extensions", "/figureout ", "/help", "/history", "/hooks", "/init", "/inputs", "/login", "/logs", "/mcp", "/memory", "/model ", "/models", "/next", "/open ", "/plan ", "/prompts", "/queue", "/queue add ", "/queue archives", "/queue prune", "/quit", "/remember ", "/report", "/resume", "/review ", "/routing", "/skills", "/status", "/steer ", "/steering", "/stop", "/tasks", "/team", "/test ", "/workflows"}

func (m appModel) paletteMatches() []string {
	var out []string
	for _, c := range paletteCommands {
		if strings.Contains(strings.ToLower(c), strings.ToLower(m.paletteQuery)) {
			out = append(out, c)
		}
	}
	return out
}
func (m *appModel) openPalette() {
	if !m.hasSavedDraft {
		m.savedDraft = m.editor.Value()
		m.hasSavedDraft = true
	}
	m.paletteOpen = true
	m.paletteQuery = ""
	m.paletteCursor = 0
}
func (m *appModel) restoreDraft() {
	if m.hasSavedDraft {
		m.editor.SetValue(m.savedDraft)
		m.savedDraft = ""
		m.hasSavedDraft = false
	}
	m.paletteOpen = false
}
func (m appModel) tinyTerminal() bool {
	return m.width > 0 && m.height > 0 && (m.width < 20 || m.height < 10)
}
func (m *appModel) uiKey(k tea.KeyMsg) (bool, tea.Cmd) {
	key := k.String()
	if key == "ctrl+c" {
		return false, nil
	}
	if m.tinyTerminal() {
		return true, nil
	}
	if key == "ctrl+p" {
		if m.paletteOpen {
			m.restoreDraft()
		} else {
			m.openPalette()
		}
		return true, nil
	}
	if m.paletteOpen {
		choices := m.paletteMatches()
		switch key {
		case "esc":
			m.restoreDraft()
		case "up":
			m.paletteCursor = max(0, m.paletteCursor-1)
		case "down":
			m.paletteCursor = min(max(0, len(choices)-1), m.paletteCursor+1)
		case "enter", "tab":
			if len(choices) > 0 {
				m.editor.SetValue(choices[min(m.paletteCursor, len(choices)-1)])
				m.paletteOpen = false
			}
		case "backspace", "ctrl+h":
			r := []rune(m.paletteQuery)
			if len(r) > 0 {
				m.paletteQuery = string(r[:len(r)-1])
				m.paletteCursor = 0
			}
		default:
			if k.Type == tea.KeyRunes {
				r := []rune(m.paletteQuery)
				for _, c := range k.Runes {
					if len(r) >= 128 {
						break
					}
					if !unicode.IsControl(c) {
						r = append(r, c)
					}
				}
				m.paletteQuery = string(r)
				m.paletteCursor = 0
			}
		}
		return true, nil
	}
	if key == "esc" && m.hasSavedDraft {
		m.restoreDraft()
		return true, nil
	}
	switch key {
	case "ctrl+home":
		m.viewport.GotoTop()
		m.following = false
		return true, nil
	case "ctrl+end":
		m.viewport.GotoBottom()
		m.following = true
		m.unread = 0
		return true, nil
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var c tea.Cmd
		m.viewport, c = m.viewport.Update(k)
		m.following = m.viewport.AtBottom()
		if m.following {
			m.unread = 0
		}
		return true, c
	}
	return false, nil
}
func shortUI(s string, width int) string {
	return ansi.Truncate(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " "), max(1, width), "…")
}
func (m *appModel) sizeUI() {
	w := max(1, m.width-2)
	m.viewport.Width = w
	m.editor.SetWidth(max(1, w-4))
	h := 2
	if m.height >= 25 {
		h = 3
	}
	m.editor.SetHeight(h)
	m.viewport.Height = max(1, m.height-h-8)
	m.refresh()
}
func (m appModel) paletteView(w, h int) string {
	rows := []string{accent.Render(shortUI("Commands · "+m.paletteQuery, w))}
	choices := m.paletteMatches()
	start := max(0, m.paletteCursor-max(0, h-3))
	for i := start; i < len(choices) && len(rows) < h; i++ {
		mark := "  "
		if i == m.paletteCursor {
			mark = "› "
		}
		rows = append(rows, shortUI(mark+choices[i], w))
	}
	if len(choices) == 0 {
		rows = append(rows, "No matching commands")
	}
	return lipgloss.NewStyle().Width(w).Height(h).Render(strings.Join(rows, "\n"))
}
func (m appModel) selectionView(w, h int) string {
	names := []string{}
	if m.mode == "connect" {
		for _, c := range m.choices {
			names = append(names, c.Label+" · "+c.Hint)
		}
	} else {
		names = append([]string{"Automatic free-model selection"}, m.workerModels...)
	}
	rows := []string{accent.Render(shortUI("Connect your coding team", w))}
	start := max(0, m.cursor-max(0, h-3))
	for i := start; i < len(names) && len(rows) < h; i++ {
		mark := "  "
		if i == m.cursor {
			mark = "› "
		}
		rows = append(rows, shortUI(mark+names[i], w))
	}
	return lipgloss.NewStyle().Width(w).Height(h).Render(strings.Join(rows, "\n"))
}
func (m appModel) harnessView() string {
	if m.tinyTerminal() {
		return shortUI("Resize terminal (20×10 minimum). Ctrl+C pauses or quits.", m.width)
	}
	w := max(1, m.width-2)
	conn := "Choose your planner"
	if m.connected {
		conn = m.settings.Planner.Backend + " planner · free workers"
	}
	if m.demo {
		conn = "Offline demo · simulated agents"
	}
	body := m.viewport.View()
	if m.mode == "connect" || m.mode == "models" {
		body = m.selectionView(w, m.viewport.Height)
	}
	if m.paletteOpen {
		body = m.paletteView(w, m.viewport.Height)
	}
	status := m.stage
	if m.busy {
		status = m.spinner.View() + " " + status
	}
	if m.progress.RunID != "" {
		status += fmt.Sprintf(" · cycle %d · tasks %d/%d", m.progress.Cycle, m.progress.Finished, m.progress.Total)
	}
	if w >= 100 {
		status += fmt.Sprintf(" · senior %d · worker %d", m.progress.PlannerCalls, m.progress.WorkerCalls)
	}
	if !m.following {
		status += fmt.Sprintf(" · %d unread · Ctrl+End live", m.unread)
	}
	footer := "Enter send · Ctrl+P commands · Ctrl+C quit"
	if m.busy {
		footer = "Enter send · Ctrl+P commands · Ctrl+C pause"
	}
	if w < 50 {
		footer = "Enter send · ^P commands · ^C"
	}
	composer := panel.Width(max(1, w-2)).Render(m.editor.View())
	return lipgloss.NewStyle().Padding(0, 1).Render(strings.Join([]string{accent.Render(shortUI("BUILDHEADWAY · "+Version, w)), muted.Render(shortUI(m.repo, w)), muted.Render(shortUI(conn, w)), body, shortUI(status, w), composer, muted.Render(shortUI(footer, w))}, "\n"))
}
