package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var paletteCommands = []string{"/about", "/acceptance", "/agents", "/attach ", "/browser", "/browser evidence", "/check", "/commands", "/config", "/connect", "/context", "/credits", "/debug ", "/detach ", "/diff", "/discussion", "/docs ", "/doctor", "/events", "/export", "/extensions", "/extensions catalog", "/figureout ", "/freebies", "/freebies check", "/freebies offers ", "/freebies watch", "/freebies stop", "/help", "/history", "/hooks", "/init", "/inputs", "/login", "/logs", "/mcp", "/mcp catalog", "/memory", "/model ", "/models", "/next", "/open ", "/plan ", "/prompts", "/queue", "/queue add ", "/queue archives", "/queue prune", "/quit", "/remember ", "/report", "/resume", "/review ", "/routing", "/setup", "/skills", "/status", "/steer ", "/steering", "/stop", "/tasks", "/team", "/test ", "/workflows"}

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
	title := "Connect your coding team"
	if m.mode == "models" {
		title = "Choose a free worker"
	}
	rows := []string{accent.Render(shortUI(title, w))}
	if m.setupBackend != "" {
		rows = []string{accent.Render(shortUI("Review private installation", w)), shortUI("Senior: "+m.setupBackend+" · worker: OpenCode", w)}
		for _, id := range m.setupNeeded {
			for _, a := range Registry {
				if a.ID == id {
					rows = append(rows, shortUI("Package: "+a.Package, w))
				}
			}
		}
		rows = append(rows, shortUI("Node / Windows shell may also be set up.", w))
	}
	start := max(0, m.cursor-max(0, h-3))
	for i := start; i < len(names) && len(rows) < h; i++ {
		if i == m.cursor {
			rows = append(rows, accent.Render(shortUI("› "+names[i], w)))
		} else {
			rows = append(rows, shortUI("  "+names[i], w))
		}
	}
	return lipgloss.NewStyle().Width(w).Height(h).Render(strings.Join(rows, "\n"))
}

func (m appModel) freeRouteView(width int) string {
	badge, detail := "Free auto", "/models to choose"
	style := accent
	route := m.freeRoute
	role := "Worker"
	if route.Role == "planner" {
		role = "Planner"
	}
	if route.Pinned || (route.State == "" && pinnedModel(m.settings.Worker.Model)) {
		badge = "Free pin"
	}
	switch route.State {
	case "checking":
		detail = role + " · checking prices"
	case "selected":
		model := freebieTerminalLine(route.Model, 256)
		detail = role + " " + model
		if !route.Pinned {
			backupText := fmt.Sprintf("%d backups", route.Backups)
			if route.Backups == 1 {
				backupText = "1 backup"
			}
			if width < 68 {
				detail = role + " · " + backupText + " · " + model
			} else {
				detail += " · " + backupText
				if route.Next != "" {
					detail += " · next " + freebieTerminalLine(route.Next, 256)
				}
			}
		}
	case "cooldown":
		style = warning
		detail = role + " cooling · " + route.RetryAt.Local().Format("15:04:05")
	case "empty":
		badge, style = "Free paused", warning
		detail = role + " · no available offers"
		if !route.RetryAt.IsZero() {
			detail += " · recheck " + route.RetryAt.Local().Format("15:04")
		}
	case "quota":
		badge, style = "Free paused", warning
		detail = "Provider rate limit · /routing"
	default:
		if m.demo {
			badge, detail = "Demo", "Simulated agents"
		} else if !m.connected {
			badge, detail = "Setup", "Connect your coding team"
		} else if badge == "Free pin" {
			detail = freebieTerminalLine(m.settings.Worker.Model, 256)
		}
	}
	return ansi.Truncate(style.Render(badge)+"  "+shortUI(detail, width), width, "…")
}

func (m appModel) harnessView() string {
	if m.tinyTerminal() {
		return shortUI("Resize terminal (20×10 minimum). Ctrl+C pauses or quits.", m.width)
	}
	w := max(1, m.width-2)
	project := m.repo
	if w < 83 {
		project = filepath.Base(project)
	}
	if m.connected {
		project = m.settings.Planner.Backend + " senior · " + project
	}
	if m.demo {
		project = "Offline · " + project
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
	if w >= 100 && m.progress.RunID != "" {
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
	title := accent.Render("BUILDHEADWAY") + muted.Render("  v"+Version)
	return lipgloss.NewStyle().Padding(0, 1).Render(strings.Join([]string{
		ansi.Truncate(title, w, "…"),
		muted.Render(shortUI(freebieTerminalLine(project, 1024), w)),
		m.freeRouteView(w),
		body,
		shortUI(status, w),
		composer,
		muted.Render(shortUI(footer, w)),
	}, "\n"))
}
