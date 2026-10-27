package main

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *appModel) addToQueue(goal string) {
	mode := "figureout"
	if m.demo {
		mode = ""
	}
	m.addToQueueMode(goal, mode)
}
func (m *appModel) addToQueueMode(goal, mode string) {
	item, err := QueueAddMode(m.repo, goal, mode)
	if err != nil {
		m.add("BuildHeadway", err.Error())
		return
	}
	m.add("Queue", "Saved "+item.ID+". It will run after the current goal succeeds, or with /next.")
}

func (m *appModel) startQueue(all bool) tea.Cmd {
	if m.busy {
		m.add("Queue", "A job is already running. The queue is saved.")
		return nil
	}
	if m.demo {
		m.add("Demo", "Queued goals use real connections. Open BuildHeadway normally to run them.")
		return nil
	}
	if !m.connected {
		m.connections()
		return nil
	}
	m.busy, m.mode, m.stage = true, "chat", "Starting queued goals"
	m.progress = Progress{}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.jobDone = cancel, make(chan struct{})
	paths, settings, repo, events, done := m.paths, m.settings, m.repo, m.events, m.jobDone
	go func() {
		defer close(done)
		err := RunQueued(ctx, paths, settings, repo, all, func(event Event) {
			select {
			case events <- event:
			case <-ctx.Done():
			}
		})
		events <- Event{Kind: "finished", Value: finishedMsg{Err: err, Status: "complete", PreventQueue: true}}
	}()
	return nil
}

func (m *appModel) startCheck() tea.Cmd {
	if m.busy {
		m.add("Check", "Pause the active job before running separate checks.")
		return nil
	}
	m.busy, m.stage, m.mode = true, "Checking project", "chat"
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.jobDone = cancel, make(chan struct{})
	paths, repo, events, done := m.paths, m.repo, m.events, m.jobDone
	go func() {
		defer close(done)
		report, err := CheckProject(ctx, paths, repo, func(event Event) {
			select {
			case events <- event:
			case <-ctx.Done():
			}
		})
		events <- Event{Kind: "check-finished", Value: inspectionMsg{"Check", FormatVerification(report), err}}
	}()
	return nil
}

func (m *appModel) projectCommand(text string) (tea.Cmd, bool) {
	command, argument, _ := strings.Cut(text, " ")
	argument = strings.TrimSpace(argument)
	repo, paths := m.repo, m.paths
	switch command {
	case "/logs":
		return func() tea.Msg {
			report, err := ReadLogs(repo, "latest", false, argument, 16000)
			return inspectionMsg{"Logs", FormatLogs(report), err}
		}, true
	case "/steer":
		id := "latest"
		if m.busy {
			id = m.progress.RunID
			if id == "" {
				m.add("Steer", "Wait until the goal starts before steering it.")
				return nil, true
			}
		}
		if argument == "" {
			m.add("Steer", "Use /steer YOUR UPDATED DIRECTION. Use /steering to inspect delivery.")
			return nil, true
		}
		msg, err := SendSteering(repo, id, argument)
		if err != nil {
			m.add("Steer", err.Error())
		} else {
			m.add("Steer", "Saved "+msg.ID+". Applies after the current agent call or on /resume.")
		}
		return nil, true
	case "/workflow", "/skill", "/prompt":
		fields, err := workflowArguments(argument)
		if err != nil {
			m.add("Workflow", err.Error())
			return nil, true
		}
		if len(fields) == 0 {
			m.add("Workflow", "Use /workflow NAME [arguments]. /workflows lists available names.")
			return nil, true
		}
		goal, err := ExpandWorkflow(paths, repo, fields[0], fields[1:])
		if err != nil {
			m.add("Workflow", err.Error())
			return nil, true
		}
		return m.startGoal(goal, false), true
	case "/workflows", "/skills", "/prompts", "/hooks", "/steering", "/extensions", "/mcp":
		return func() tea.Msg {
			var formatted string
			var err error
			switch command {
			case "/extensions":
				var inventory ExtensionInventory
				inventory, err = InspectExtensions(paths, repo)
				formatted = FormatExtensions(inventory)
			case "/mcp":
				var config ProjectConfig
				config, err = LoadProjectConfig(repo)
				formatted = FormatMCPServers(config.MCPServers)
			case "/hooks":
				var config ProjectConfig
				config, err = LoadProjectConfig(repo)
				formatted = FormatHooks(config)
			case "/steering":
				formatted, err = SteeringStatus(repo, argument)
			default:
				var items []Workflow
				items, err = DiscoverWorkflows(paths, repo)
				items = selectWorkflows(items, strings.TrimPrefix(command, "/"))
				formatted = FormatWorkflows(items)
				if argument != "" && err == nil {
					found := false
					for _, w := range items {
						if w.Name == argument {
							formatted, found = w.Source+"\n\n"+w.Body, true
							break
						}
					}
					if !found {
						err = errors.New("workflow not found; use /workflows")
					}
				}
			}
			return inspectionMsg{strings.TrimPrefix(command, "/"), formatted, err}
		}, true
	case "/figureout", "/figure-out":
		if argument == "" {
			m.add("BuildHeadway", "Usage: /figureout YOUR GOAL · autonomous full access")
			return nil, true
		}
		if m.demo {
			m.add("Demo", "Open BuildHeadway normally to use figureout with your connected agents.")
			return nil, true
		}
		m.initialFigureOut = true
		return m.startGoal(argument, false), true
	case "/team":
		team, err := FigureOutTeam(m.settings)
		if err != nil {
			m.add("Team", err.Error())
		} else {
			m.add("Team", FormatTeam(team))
		}
		return nil, true
	case "/discussion":
		id := argument
		if id == "" {
			id = "latest"
		}
		return func() tea.Msg {
			_, formatted, err := InspectRun(repo, id, "discussion")
			return inspectionMsg{"Discussion", formatted, err}
		}, true
	case "/attach", "/detach":
		if m.busy {
			m.add("Team", "Pause the current goal before changing the team. Saved runs keep their original team.")
			return nil, true
		}
		values := strings.Fields(argument)
		var updated Settings
		var err error
		if command == "/detach" && len(values) == 1 {
			updated, err = DetachSenior(m.settings, values[0])
		} else if command == "/attach" && len(values) >= 1 && len(values) <= 2 {
			c := Connection{Backend: values[0]}
			if len(values) == 2 {
				c.Model = values[1]
			}
			c.Executable, err = ResolveConnection(c, paths)
			if err == nil {
				updated, err = AttachSenior(m.settings, SeniorAgent{Name: c.Backend, Connection: c}, false)
			}
		} else {
			err = errors.New("Use /attach AGENT [MODEL] or /detach NAME. For installation and reasoning options, run buildheadway attach in your terminal.")
		}
		if err == nil {
			err = SaveSettings(paths, updated)
		}
		if err != nil {
			m.add("Team", err.Error())
		} else {
			m.settings, m.connected = updated, true
			team, _ := FigureOutTeam(updated)
			m.add("Team", FormatTeam(team))
		}
		return nil, true
	case "/plan":
		if argument == "" {
			m.add("BuildHeadway", "Usage: /plan YOUR GOAL")
			return nil, true
		}
		if m.busy {
			m.add("BuildHeadway", "Pause the current job before planning another goal.")
			return nil, true
		}
		if m.demo {
			m.add("Demo", "Open BuildHeadway normally to plan your own goal.")
			return nil, true
		}
		m.initialPlan = true
		return m.startGoal(argument, false), true
	case "/check":
		return m.startCheck(), true
	case "/next":
		return m.startQueue(false), true
	case "/queue":
		action, value, _ := strings.Cut(argument, " ")
		value = strings.TrimSpace(value)
		switch action {
		case "run":
			return m.startQueue(true), true
		case "next":
			return m.startQueue(false), true
		case "add", "figureout":
			mode := ""
			if action == "figureout" || !m.demo {
				mode = "figureout"
			}
			m.addToQueueMode(value, mode)
			return nil, true
		case "remove", "move":
			var err error
			if action == "remove" {
				err = QueueRemove(repo, value)
			} else {
				fields := strings.Fields(value)
				if len(fields) != 2 {
					err = errors.New("usage: /queue move ID POSITION")
				} else {
					var position int
					position, err = strconv.Atoi(fields[1])
					if err == nil {
						err = QueueMove(repo, fields[0], position)
					}
				}
			}
			if err != nil {
				m.add("Queue", err.Error())
			} else {
				m.add("Queue", "Queue updated.")
			}
			return nil, true
		case "", "list":
			return func() tea.Msg { queue, err := ReadQueue(repo); return inspectionMsg{"Queue", FormatQueue(queue), err} }, true
		default:
			m.add("Queue", "Use /queue add GOAL, /queue run, /next, /queue remove ID, or /queue move ID POSITION.")
			return nil, true
		}
	case "/init", "/config", "/memory", "/remember", "/buildheadwayt", "/diff", "/report", "/export", "/history":
		return func() tea.Msg {
			var formatted string
			var err error
			switch command {
			case "/init":
				var created []string
				created, err = InitProject(repo)
				formatted = "Project ready. Edit BUILDHEADWAY.md for guidance and .buildheadway.json for checks and limits."
				if len(created) > 0 {
					formatted = "Created " + strings.Join(created, ", ") + ".\n" + formatted
				}
			case "/config":
				var config ProjectConfig
				config, err = LoadProjectConfig(repo)
				data, _ := json.MarshalIndent(config, "", "  ")
				formatted = string(data)
			case "/memory":
				var memory ProjectMemory
				memory, err = ReadMemory(repo)
				formatted = FormatMemory(memory)
			case "/remember":
				var entry MemoryEntry
				entry, err = Remember(repo, argument)
				formatted = "Saved " + entry.ID + ". Future goals and resumes include this note."
			case "/buildheadwayt":
				err = BuildHeadwayt(repo, argument)
				formatted = "Project note removed."
			case "/diff":
				formatted, err = GitChanges(context.Background(), paths, repo)
			case "/history":
				var rows []RunSummary
				rows, err = RunHistory(repo)
				formatted = FormatHistory(FilterHistory(rows, argument, 20))
			case "/report", "/export":
				if argument == "" {
					argument = "latest"
				}
				if command == "/export" {
					formatted, err = ExportRun(repo, argument, "", false, false)
				} else {
					var state State
					state, _, err = LoadRun(repo, argument)
					formatted = MarkdownReport(state)
				}
			}
			return inspectionMsg{strings.TrimPrefix(command, "/"), formatted, err}
		}, true
	}
	return nil, false
}
