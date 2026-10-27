package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#71D9C1")).Bold(true)
var muted = lipgloss.NewStyle().Foreground(lipgloss.Color("#929AA6"))
var warning = lipgloss.NewStyle().Foreground(lipgloss.Color("#F6C177"))
var panel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#414955")).Padding(0, 1)

type chatEntry struct{ Role, Text string }
type scanMsg struct{ Inventory Inventory }
type setupMsg struct {
	Settings Settings
	NeedAuth bool
	Err      error
}
type authMsg struct {
	Settings Settings
	Target   Connection
	Err      error
}
type finishedMsg struct {
	Err           error
	Status, RunID string
	PreventQueue  bool
}
type inspectionMsg struct {
	Kind, Text string
	Err        error
}
type modelsMsg struct {
	Models []string
	Err    error
}
type tickMsg time.Time
type choice struct{ Backend, Label, Hint string }
type appModel struct {
	paths            Paths
	repo             string
	settings         Settings
	connected        bool
	inventory        Inventory
	entries          []chatEntry
	editor           textarea.Model
	viewport         viewport.Model
	spinner          spinner.Model
	events           chan Event
	mode             string
	choices          []choice
	cursor           int
	busy             bool
	stage            string
	width, height    int
	cancel           context.CancelFunc
	jobDone          chan struct{}
	initial          string
	initialPlan      bool
	initialFigureOut bool
	workerModels     []string
	demo             bool
	quitting         bool
	history          []string
	historyIndex     int
	loginTarget      Connection
	resumeAfterLogin bool
	progress         Progress
	resumeRunID      string
}

func newApp(paths Paths, repo, initial string, demo, chooseProject bool) appModel {
	editor := textarea.New()
	editor.Placeholder = "Describe what you want to build…"
	editor.CharLimit = 32000
	editor.ShowLineNumbers = false
	editor.SetHeight(3)
	editor.SetWidth(80)
	editor.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	editor.Focus()
	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = accent
	m := appModel{
		paths: paths, repo: repo, initial: initial, demo: demo, editor: editor, viewport: viewport.New(80, 15),
		spinner: spin, events: make(chan Event, 256), mode: "scanning", stage: "Scanning this computer",
		width: 90, height: 30,
	}
	if settings, err := LoadSettings(paths); err == nil {
		m.settings = settings
		m.connected = true
	}
	if chooseProject {
		m.mode = "project"
		m.editor.SetValue(repo)
		m.add("Project", "Choose the folder you want BuildHeadway to work in, then press Enter.")
	}
	if demo {
		m.mode = "chat"
		m.connected = true
		m.settings = Settings{Planner: Connection{Backend: "demo"}, Worker: Connection{Backend: "demo"}}
	}
	m.add("BuildHeadway", "Give your team a goal. Agents discuss the approach, ask each other for help, build with free workers, and check and repair the result. New goals use autonomous team mode with full access. /discussion shows their conversation; /steer changes direction; /workflows lists reusable commands; /stop saves progress.")
	if queue, err := ReadQueue(repo); err == nil {
		for _, item := range queue.Items {
			if item.Status != "complete" && item.Status != "canceled" {
				m.add("Queue", "Saved goals are waiting. Use /queue to inspect them and /next to continue.")
				break
			}
		}
	}
	return m
}
func waitEvent(events <-chan Event) tea.Cmd { return func() tea.Msg { return <-events } }
func tick() tea.Cmd                         { return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }) }
func (m appModel) Init() tea.Cmd {
	commands := []tea.Cmd{textarea.Blink, m.spinner.Tick, tick(), waitEvent(m.events)}
	if m.demo {
		commands = append(commands, func() tea.Msg { return Event{Kind: "start-demo"} })
	} else {
		commands = append(commands, func() tea.Msg { return scanMsg{ScanAgents(context.Background(), m.paths)} })
	}
	return tea.Batch(commands...)
}
func (m *appModel) add(role, text string) {
	text = strings.TrimSpace(Clean(text))
	if text == "" {
		return
	}
	m.entries = append(m.entries, chatEntry{role, text})
	if len(m.entries) > 400 {
		m.entries = m.entries[len(m.entries)-400:]
	}
	m.refresh()
}
func (m *appModel) refresh() {
	var content strings.Builder
	width := max(20, m.width-6)
	for _, entry := range m.entries {
		style := muted
		if entry.Role == "BuildHeadway" || entry.Role == "You" {
			style = accent
		}
		content.WriteString(style.Render(entry.Role) + "\n")
		content.WriteString(lipgloss.NewStyle().Width(width).Render(entry.Text) + "\n\n")
	}
	m.viewport.SetContent(content.String())
	m.viewport.GotoBottom()
}
func (m *appModel) connections() {
	m.mode = "connect"
	m.cursor = 0
	m.choices = nil
	for _, agent := range m.inventory.Agents {
		if agent.Adapter && agent.Installed && agent.ProbeError == "" {
			hint := agent.Version
			if hint == "" {
				hint = "Installed"
			}
			m.choices = append(m.choices, choice{agent.ID, "Use " + agent.Name, hint})
		}
	}
	for _, agent := range m.inventory.Agents {
		if agent.Adapter && (!agent.Installed || agent.ProbeError != "") {
			hint := "BuildHeadway will set up the missing connection"
			if agent.ExtensionOnly {
				hint = "Editor extension found; the CLI will be installed"
			}
			m.choices = append(m.choices, choice{agent.ID, "Set up " + agent.Name, hint})
		}
	}
	m.add("Connections", "Choose an agent for planning and review. Free OpenCode workers are selected automatically. Existing installations and their own saved accounts are reused.")
}
func (m *appModel) startSetup(backend string) tea.Cmd {
	if m.busy {
		return nil
	}
	m.busy = true
	m.stage = "Preparing " + backend
	m.mode = "chat"
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.jobDone = make(chan struct{})
	paths, repo, events, done := m.paths, m.repo, m.events, m.jobDone
	seniors := append([]SeniorAgent{}, m.settings.Seniors...)
	wasInstalled := false
	if agent, ok := m.inventory.Find(backend); ok {
		wasInstalled = agent.Installed
	}
	go func() {
		defer close(done)
		emit := func(event Event) {
			select {
			case events <- event:
			case <-ctx.Done():
			}
		}
		connections, err := InstallAgents(ctx, paths, []string{backend, "opencode"}, emit)
		if err != nil {
			events <- Event{Kind: "setup", Value: setupMsg{Err: err}}
			return
		}
		settings := Settings{Version: 3, Seniors: seniors, Planner: Connection{Backend: backend, Executable: connections[backend]}, Worker: Connection{Backend: "opencode", Executable: connections["opencode"], Model: "auto"}}
		if backend == "claude" {
			settings.Planner.Model = "opus"
		}
		// Repair old incompatible releases privately; never overwrite a global agent.
		for _, target := range []*Connection{&settings.Planner, &settings.Worker} {
			if err := Compatible(ctx, *target, paths); err != nil {
				emit(Event{Kind: "status", Text: "Setting up a compatible " + target.Backend + " connection"})
				fresh, installErr := installAgents(ctx, paths, []string{target.Backend}, emit, true)
				if installErr != nil {
					events <- Event{Kind: "setup", Value: setupMsg{Err: installErr}}
					return
				}
				target.Executable = fresh[target.Backend]
				if err := Compatible(ctx, *target, paths); err != nil {
					events <- Event{Kind: "setup", Value: setupMsg{Err: err}}
					return
				}
			}
		}
		if backend == "opencode" {
			dir, err := os.MkdirTemp("", "buildheadway-models-*")
			if err != nil {
				events <- Event{Kind: "setup", Value: setupMsg{Err: err}}
				return
			}
			defer os.RemoveAll(dir)
			runner := Runner{Paths: paths, Settings: settings, Repo: repo, Dir: dir, Emit: emit}
			_, err = runner.discoverModels(ctx)
			if err != nil {
				events <- Event{Kind: "setup", Value: setupMsg{Err: err}}
				return
			}
			settings.Planner.Model = "auto"
		}
		needsAuth := !Authenticated(ctx, settings.Planner, paths)
		if !wasInstalled && (backend == "pi" || backend == "cline" || backend == "gemini") {
			needsAuth = true
		}
		events <- Event{Kind: "setup", Value: setupMsg{Settings: settings, NeedAuth: needsAuth}}
	}()
	return nil
}
func (m *appModel) startGoal(text string, resume bool, ids ...string) tea.Cmd {
	if m.busy {
		if text != "" {
			mode := ""
			if (m.initialFigureOut || !m.initialPlan) && !m.demo {
				mode = "figureout"
			}
			m.addToQueueMode(text, mode)
			m.initialFigureOut = false
		}
		return nil
	}
	if !m.connected {
		m.initial = text
		m.connections()
		return nil
	}
	planOnly := m.initialPlan
	m.initialPlan = false
	figureout := m.initialFigureOut || (!planOnly && !resume && !m.demo)
	m.initialFigureOut = false
	if text != "" {
		m.add("You", text)
	}
	m.busy = true
	m.stage = "Planning"
	m.mode = "chat"
	m.progress = Progress{}
	id := "latest"
	if len(ids) > 0 && ids[0] != "" {
		id = ids[0]
	}
	m.resumeRunID = id
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.jobDone = make(chan struct{})
	paths, settings, repo, events, done, demo := m.paths, m.settings, m.repo, m.events, m.jobDone, m.demo
	go func() {
		defer close(done)
		emit := func(event Event) {
			select {
			case events <- event:
			case <-ctx.Done():
			}
		}
		var runner *Runner
		var err error
		if demo && !resume {
			runner, err = DemoRunner(paths, emit)
			if err == nil {
				emit(Event{Kind: "project", Text: runner.Repo})
			}
		} else if resume {
			runner, err = ResumeRunner(paths, settings, repo, id, emit)
		} else {
			runner, err = NewRunner(paths, settings, repo, text, resume, emit)
		}
		result := finishedMsg{}
		if err == nil && figureout {
			err = runner.EnableFigureOut("full")
		}
		if err == nil {
			runner.PlanOnly = planOnly
			err = runner.Run(ctx)
			result.Status, result.RunID = runner.State.Status, runner.State.RunID
		}
		result.Err = err
		events <- Event{Kind: "finished", Value: result}
	}()
	return nil
}
func (m *appModel) selectModels() tea.Cmd {
	if m.busy || !m.connected {
		return nil
	}
	m.busy = true
	m.stage = "Finding free models"
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.jobDone = make(chan struct{})
	events, paths, settings, repo, done := m.events, m.paths, m.settings, m.repo, m.jobDone
	go func() {
		defer close(done)
		dir, err := os.MkdirTemp("", "buildheadway-models-*")
		if err != nil {
			events <- Event{Kind: "models", Value: modelsMsg{Err: err}}
			return
		}
		defer os.RemoveAll(dir)
		runner := Runner{Paths: paths, Settings: settings, Repo: repo, Dir: dir, Emit: func(event Event) {
			select {
			case events <- event:
			case <-ctx.Done():
			}
		}}
		models, err := runner.discoverModels(ctx)
		events <- Event{Kind: "models", Value: modelsMsg{models, err}}
	}()
	return nil
}
func (m *appModel) saveConnection(settings Settings) {
	if err := SaveSettings(m.paths, settings); err != nil {
		m.add("BuildHeadway", err.Error())
		return
	}
	m.settings = settings
	m.connected = true
	m.mode = "chat"
	m.stage = "Ready"
	label := settings.Planner.Backend
	if settings.Planner.Model != "" {
		label += " · " + settings.Planner.Model
	}
	m.add("Connected", label+" for planning · free workers for implementation. Type your goal.")
}
func (m *appModel) login(settings Settings) tea.Cmd {
	return m.loginTo(settings.Planner, settings)
}
func (m *appModel) loginTo(target Connection, settings Settings) tea.Cmd {
	cmd, err := AuthCommand(context.Background(), target, m.paths)
	if err != nil {
		m.add("BuildHeadway", err.Error())
		return nil
	}
	cmd.Dir = m.repo
	m.add("Connect", "Complete sign-in in your agent’s own prompt. For Pi, use /login and exit Pi to return to BuildHeadway.")
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return authMsg{settings, target, err} })
}
func (m *appModel) handle(text string) tea.Cmd {
	text = strings.TrimSpace(text)
	if m.mode == "project" {
		if text == "" {
			return nil
		}
		if text == "~" {
			text, _ = os.UserHomeDir()
		} else if strings.HasPrefix(text, "~/") || strings.HasPrefix(text, `~\`) {
			home, _ := os.UserHomeDir()
			text = filepath.Join(home, text[2:])
		}
		absolute, err := filepath.Abs(text)
		if err != nil {
			m.add("BuildHeadway", err.Error())
			return nil
		}
		if err := os.MkdirAll(absolute, 0755); err != nil {
			m.add("BuildHeadway", err.Error())
			return nil
		}
		m.repo = absolute
		m.add("Project", absolute)
		if m.connected {
			m.mode = "chat"
		} else {
			m.connections()
		}
		return nil
	}
	if text == "" {
		return nil
	}
	m.history = append(m.history, text)
	m.historyIndex = len(m.history)
	if command, handled := m.projectCommand(text); handled {
		return command
	}
	switch {
	case text == "/quit" || text == "/exit":
		if m.busy {
			m.quitting = true
			m.stage = "Saving progress"
			m.cancel()
			return nil
		}
		return tea.Quit
	case text == "/stop":
		if m.cancel != nil {
			m.cancel()
			m.stage = "Pausing"
		}
		return nil
	case text == "/help":
		m.add("Help", "Type a message to start a goal.\n/figureout GOAL · autonomous senior team with full access\n/team · senior connections\n/attach AGENT [MODEL] · attach an installed senior\n/detach NAME · remove an attached senior\n/discussion [ID] · saved agent conversation\n/workflows · reusable workflows, skills and prompts\n/extensions · customization sources and starter commands\n/mcp · configured external tool servers\n/workflow NAME [arguments] · run a workflow\n/review, /debug, /test, /docs · built-in workflows\n/skills [NAME] · inspect custom skills\n/prompts [NAME] · inspect prompt templates\n/steer DIRECTION · redirect the active goal\n/steering · pending and applied directions\n/hooks · configured verification hooks\n/acceptance · requirement coverage and executed evidence\n/routing · free-model health, cooldowns and recent decisions\n/logs [LOG_NAME] · inspect saved process logs\n/status · goal, phase, calls, and checks\n/tasks · durable task history\n/history [SEARCH] · saved goals\n/plan GOAL · save a plan before implementation\n/init · project settings and BUILDHEADWAY.md\n/config · show effective project settings\n/check · run verification without a model\n/diff · inspect Git changes\n/report [ID] · run report\n/export [ID] · save a Markdown report\n/memory · saved project notes\n/remember NOTE · save guidance\n/buildheadwayt ID · remove a note\n/events · recent harness activity\n/queue [add GOAL | remove ID | move ID POSITION | run]\n/next · run one saved queued goal\n/doctor · installation and settings diagnostics\n/agents · detected installations and versions\n/connect · choose or repair a connection\n/login · sign in using your agent\n/models · choose a free worker\n/model ID · change the planner model\n/open FOLDER · switch projects\n/resume [ID] · inspect saved work and continue\n/stop · pause\n/quit · close\nAlt+Enter adds a newline. Page Up/Down scrolls.")
		return nil
	case text == "/routing" || text == "/acceptance" || text == "/status" || text == "/tasks" || text == "/history" || text == "/events":
		kind, repo := strings.TrimPrefix(text, "/"), m.repo
		return func() tea.Msg {
			_, formatted, err := InspectRun(repo, "latest", kind)
			return inspectionMsg{kind, formatted, err}
		}
	case text == "/doctor":
		paths, repo := m.paths, m.repo
		return func() tea.Msg {
			return inspectionMsg{"doctor", FormatDoctor(Diagnose(context.Background(), paths, repo)), nil}
		}
	case text == "/agents":
		for _, agent := range m.inventory.Agents {
			status := "Not installed"
			if agent.Installed {
				status = agent.Version + "\n" + agent.Path
				if !agent.Adapter {
					status += "\nDetected; no built-in orchestration adapter."
				}
			}
			if agent.ExtensionOnly {
				status = "Editor extension found; CLI not installed."
			}
			if agent.ProbeError != "" {
				status += "\n" + agent.ProbeError
			}
			m.add(agent.Name, status)
		}
		return nil
	case text == "/connect":
		if !m.busy {
			m.connections()
		}
		return nil
	case text == "/login":
		if !m.busy && m.connected && !m.demo {
			if m.loginTarget.Backend != "" {
				return m.loginTo(m.loginTarget, m.settings)
			}
			return m.login(m.settings)
		}
		return nil
	case text == "/models":
		return m.selectModels()
	case text == "/resume":
		return m.startGoal("", true)
	case strings.HasPrefix(text, "/resume "):
		return m.startGoal("", true, strings.TrimSpace(strings.TrimPrefix(text, "/resume ")))
	case strings.HasPrefix(text, "/model "):
		if !m.busy && m.connected {
			m.settings.Planner.Model = strings.TrimSpace(strings.TrimPrefix(text, "/model "))
			m.saveConnection(m.settings)
		}
		return nil
	case strings.HasPrefix(text, "/open "):
		if m.busy {
			m.add("BuildHeadway", "Pause the current goal before changing projects.")
			return nil
		}
		m.mode = "project"
		return m.handle(strings.TrimSpace(strings.TrimPrefix(text, "/open ")))
	case strings.HasPrefix(text, "/") && !strings.HasPrefix(text, "/goal "):
		name, arguments, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
		values, err := workflowArguments(arguments)
		if err == nil {
			var goal string
			goal, err = ExpandWorkflow(m.paths, m.repo, name, values)
			if err == nil {
				return m.startGoal(goal, false)
			}
		}
		m.add("BuildHeadway", err.Error()+"\nUse /help for commands or /workflows for reusable prompts.")
		return nil
	}
	if m.mode == "connect" {
		for i, item := range m.choices {
			if text == itoa(i+1) {
				return m.startSetup(item.Backend)
			}
		}
		m.add("BuildHeadway", "Choose a connection with the arrow keys and Enter.")
		return nil
	}
	if m.demo {
		m.add("Demo", "This is a simulated repair loop. Start BuildHeadway normally to connect real agents.")
		return nil
	}
	return m.startGoal(strings.TrimSpace(strings.TrimPrefix(text, "/goal ")), false)
}
func (m appModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	switch msg := message.(type) {
	case inspectionMsg:
		if msg.Err != nil {
			m.add("BuildHeadway", msg.Err.Error())
		} else {
			m.add(msg.Kind, msg.Text)
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.editor.SetWidth(max(15, msg.Width-10))
		m.viewport.Width = max(15, msg.Width-6)
		m.viewport.Height = max(3, msg.Height-14)
		m.refresh()
	case scanMsg:
		m.inventory = msg.Inventory
		if m.busy {
			break
		}
		if m.mode != "project" {
			if m.connected {
				_, headErr := ResolveConnection(m.settings.Planner, m.paths)
				_, workerErr := ResolveConnection(m.settings.Worker, m.paths)
				if headErr != nil || workerErr != nil {
					m.connected = false
					m.connections()
				} else {
					m.mode = "chat"
					m.stage = "Ready"
					m.add("Connected", "Using your saved "+m.settings.Planner.Backend+" connection.")
					var latest struct {
						RunID string `json:"run_id"`
					}
					if ReadJSON(filepath.Join(m.repo, ".forge", "latest.json"), &latest) == nil {
						m.add("Saved session", "Use /resume to continue saved work.")
					}
					if m.initial != "" {
						text := m.initial
						m.initial = ""
						commands = append(commands, m.startGoal(text, false))
					}
				}
			} else {
				m.connections()
				m.stage = "Choose a connection"
			}
		}
	case Event:
		commands = append(commands, waitEvent(m.events))
		switch msg.Kind {
		case "status":
			m.stage = msg.Text
		case "progress":
			m.progress = msg.Value.(Progress)
			m.resumeRunID = m.progress.RunID
		case "assistant":
			m.add("BuildHeadway", msg.Text)
		case "discussion":
			m.add("Team", msg.Text)
		case "worker":
			m.add("Building", msg.Text)
		case "check":
			if m.demo {
				msg.Text = "Calculator behavior checks"
			}
			m.add("Check", msg.Text)
		case "check-result":
			m.add("Check", msg.Text)
		case "tool":
			m.stage = "Using " + msg.Text
		case "project":
			m.repo = msg.Text
		case "planned":
			m.stage = "Plan ready"
			m.add("BuildHeadway", msg.Text)
		case "complete":
			m.stage = "Done"
			m.add("BuildHeadway", msg.Text)
		case "paused":
			m.add("BuildHeadway", msg.Text)
		case "start-demo":
			m.add("Demo", "Simulated agents · real local checks · no model calls or accounts.")
			commands = append(commands, m.startGoal("Run the sample calculator repair loop.", false))
		case "setup":
			result := msg.Value.(setupMsg)
			m.busy = false
			if result.Err != nil {
				m.stage = "Needs attention"
				m.add("BuildHeadway", result.Err.Error())
				if m.quitting {
					return m, tea.Quit
				}
				break
			}
			if result.NeedAuth {
				commands = append(commands, m.login(result.Settings))
			} else {
				m.saveConnection(result.Settings)
				commands = append(commands, func() tea.Msg { return scanMsg{ScanAgents(context.Background(), m.paths)} })
				if m.initial != "" {
					text := m.initial
					m.initial = ""
					commands = append(commands, m.startGoal(text, false))
				}
			}
		case "finished":
			result := msg.Value.(finishedMsg)
			m.busy = false
			if result.RunID != "" {
				m.resumeRunID = result.RunID
			}
			if result.Err == nil {
				m.stage = "Done"
				if result.Status == "planned" {
					m.stage = "Plan ready"
				}
			}
			if result.Err != nil && !errors.Is(result.Err, context.Canceled) {
				m.stage = "Needs attention"
				m.add("BuildHeadway", result.Err.Error()+"\nUse /status to inspect saved progress, then /resume.")
				var account authError
				if errors.As(result.Err, &account) {
					m.add("BuildHeadway", "Use /login to repair the connection.")
					m.loginTarget = m.settings.Planner
					if account.agent == m.settings.Worker.Backend {
						m.loginTarget = m.settings.Worker
					}
					for _, member := range m.settings.Seniors {
						if member.Connection.Backend == account.agent {
							m.loginTarget = member.Connection
							break
						}
					}
					m.resumeAfterLogin = true
				}
			}
			if errors.Is(result.Err, context.Canceled) {
				m.stage = "Paused"
			}
			if m.quitting {
				return m, tea.Quit
			}
			if result.Err == nil && result.Status == "complete" && !result.PreventQueue && QueueReady(m.repo) {
				commands = append(commands, m.startQueue(true))
			} else if result.Err != nil && QueueReady(m.repo) {
				m.add("Queue", "Waiting goals are saved. Resume this goal first, or use /next to start the queue.")
			}
		case "check-finished":
			result := msg.Value.(inspectionMsg)
			m.busy = false
			m.stage = "Checks finished"
			m.add(result.Kind, result.Text)
			if result.Err != nil {
				m.stage = "Checks need attention"
				m.add("BuildHeadway", result.Err.Error())
			}
			if m.quitting {
				return m, tea.Quit
			}

		case "models":
			result := msg.Value.(modelsMsg)
			m.busy = false
			if result.Err != nil {
				m.add("BuildHeadway", result.Err.Error())
				break
			}
			m.workerModels = result.Models
			m.cursor = 0
			m.mode = "models"
			m.stage = "Choose a free worker"
		}
	case authMsg:
		known := msg.Target.Backend == "codex" || msg.Target.Backend == "claude"
		if msg.Err != nil && (!known || !Authenticated(context.Background(), msg.Target, m.paths)) {
			m.add("BuildHeadway", "Sign-in was not completed. Use /connect to try again.")
			m.connections()
		} else {
			m.saveConnection(msg.Settings)
			m.loginTarget = Connection{}
			if m.resumeAfterLogin {
				m.resumeAfterLogin = false
				commands = append(commands, m.startGoal("", true, m.resumeRunID))
			} else if m.initial != "" {
				text := m.initial
				m.initial = ""
				commands = append(commands, m.startGoal(text, false))
			}
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			if m.busy {
				m.cancel()
				m.stage = "Pausing"
				return m, nil
			}
			return m, tea.Quit
		case "pgup", "pgdown", "ctrl+u", "ctrl+d":
			var command tea.Cmd
			m.viewport, command = m.viewport.Update(msg)
			commands = append(commands, command)
		case "up", "down":
			if m.mode == "connect" || m.mode == "models" {
				limit := len(m.choices)
				if m.mode == "models" {
					limit = len(m.workerModels) + 1
				}
				if msg.String() == "up" {
					m.cursor = max(0, m.cursor-1)
				} else {
					m.cursor = min(max(0, limit-1), m.cursor+1)
				}
			} else if m.editor.Value() == "" && len(m.history) > 0 && msg.String() == "up" {
				m.historyIndex = max(0, m.historyIndex-1)
				m.editor.SetValue(m.history[m.historyIndex])
			} else {
				m.editor, _ = m.editor.Update(msg)
			}
		case "enter":
			if m.mode == "connect" && m.editor.Value() == "" && len(m.choices) > 0 {
				commands = append(commands, m.startSetup(m.choices[m.cursor].Backend))
			} else if m.mode == "models" && m.editor.Value() == "" {
				model := "auto"
				if m.cursor > 0 {
					model = m.workerModels[m.cursor-1]
				}
				m.settings.Worker.Model = model
				m.saveConnection(m.settings)
			} else {
				text := m.editor.Value()
				m.editor.Reset()
				commands = append(commands, m.handle(text))
			}
		default:
			m.editor, _ = m.editor.Update(msg)
		}
	case spinner.TickMsg:
		var command tea.Cmd
		m.spinner, command = m.spinner.Update(msg)
		commands = append(commands, command)
	case tickMsg:
		commands = append(commands, tick())
	default:
		m.editor, _ = m.editor.Update(message)
	}
	return m, tea.Batch(commands...)
}
func (m appModel) View() string {
	width := max(30, m.width-4)
	header := accent.Render("BUILDHEADWAY") + "  " + muted.Render(Version+"  "+runtime.GOOS+" / "+runtime.GOARCH)
	project := muted.Render(m.repo)
	connection := "Choose your planner"
	if m.connected {
		connection = m.settings.Planner.Backend + " planner · free workers"
	}
	if m.demo {
		connection = "Offline demo · simulated agents"
	}
	body := m.viewport.View()
	if m.mode == "connect" || m.mode == "models" {
		var list strings.Builder
		list.WriteString(accent.Render("Connect your coding team") + "\n\n")
		if m.mode == "connect" {
			for index, item := range m.choices {
				cursor := "  "
				if index == m.cursor {
					cursor = accent.Render("› ")
				}
				list.WriteString(cursor + itoa(index+1) + ". " + item.Label + "  " + muted.Render(item.Hint) + "\n")
			}
			list.WriteString("\n" + muted.Render("Installed tools are reused. Missing tools are installed privately.") + "\n")
		} else {
			names := append([]string{"Automatic free-model selection"}, m.workerModels...)
			for index, name := range names {
				cursor := "  "
				if index == m.cursor {
					cursor = accent.Render("› ")
				}
				list.WriteString(cursor + name + "\n")
			}
		}
		body = lipgloss.NewStyle().Height(max(5, m.height-14)).Width(width).Render(list.String())
	}
	status := muted.Render("● " + m.stage)
	if m.busy {
		status = m.spinner.View() + " " + m.stage
	}
	if m.progress.RunID != "" {
		status += muted.Render(" · cycle " + itoa(m.progress.Cycle) + " · tasks " + itoa(m.progress.Finished) + "/" + itoa(m.progress.Total))
	}
	composer := panel.Width(max(20, m.width-8)).Render(m.editor.View())
	footer := muted.Render("Enter send   Alt+Enter newline   Ctrl+C pause   /help")
	if m.mode == "connect" || m.mode == "models" {
		footer = muted.Render("↑/↓ choose   Enter select   /agents inspect installations   /quit")
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(header + "\n" + project + "\n" + muted.Render(connection) + "\n\n" + body + "\n" + status + "\n" + composer + "\n" + footer)
}
func RunUI(paths Paths, repo, initial string, demo, chooseProject bool) error {
	model := newApp(paths, repo, initial, demo, chooseProject)
	result, err := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	if final, ok := result.(appModel); ok && final.cancel != nil {
		final.cancel()
		if final.jobDone != nil {
			select {
			case <-final.jobDone:
			case <-time.After(5 * time.Second):
			}
		}
	}
	return err
}
