package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
)

func outputValue(value any, formatted string, asJSON bool) error {
	if asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
	fmt.Println(Clean(formatted))
	return nil
}
func printJSONEvent(event Event) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"kind": event.Kind, "text": event.Text, "data": event.Value})
}
func runProjectCommand(paths Paths, kind string, args []string) int {
	flags := flag.NewFlagSet("buildheadway "+kind, flag.ContinueOnError)
	project := flags.String("project", ".", "Project folder")
	asJSON := flags.Bool("json", false, "Print machine-readable output")
	destination := flags.String("output", "", "Export file path (export command)")
	overwrite := flags.Bool("force", false, "Replace an existing export file")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	repo, err := filepath.Abs(*project)
	if err != nil {
		return fail(err)
	}
	if info, err := os.Stat(repo); err != nil || !info.IsDir() {
		return fail(errors.New("choose an existing project folder"))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	values := flags.Args()
	var value any
	var formatted string
	emit := printEvent
	if *asJSON {
		emit = noEmit
	}
	switch kind {
	case "workflows", "skills", "prompts":
		var items []Workflow
		items, err = DiscoverWorkflows(paths, repo)
		items = selectWorkflows(items, kind)
		value, formatted = items, FormatWorkflows(items)
		if len(values) > 1 {
			return fail(errors.New("use at most one workflow name"))
		}
		if len(values) == 1 && err == nil {
			found := false
			for _, w := range items {
				if w.Name == values[0] {
					value, formatted, found = w, w.Source+"\n\n"+w.Body, true
					break
				}
			}
			if !found {
				err = errors.New("workflow not found; use buildheadway workflows")
			}
		}
	case "hooks":
		if len(values) > 0 {
			return fail(errors.New("buildheadway hooks accepts no arguments"))
		}
		var config ProjectConfig
		config, err = LoadProjectConfig(repo)
		value, formatted = config.Hooks, FormatHooks(config)
	case "steering":
		id := "latest"
		if len(values) > 1 {
			return fail(errors.New("expected at most one run ID"))
		}
		if len(values) == 1 {
			id = values[0]
		}
		formatted, err = SteeringStatus(repo, id)
		value = map[string]string{"status": formatted}
	case "init":
		if len(values) > 0 {
			return fail(errors.New("buildheadway init accepts no positional arguments"))
		}
		var created []string
		created, err = InitProject(repo)
		value = map[string]any{"created": created, "project": repo}
		formatted = "Project ready. Edit BUILDHEADWAY.md for guidance and .buildheadway.json for checks and limits."
		if len(created) > 0 {
			formatted = "Created " + strings.Join(created, ", ") + ".\n" + formatted
		}
	case "config":
		if len(values) > 0 {
			return fail(errors.New("buildheadway config accepts no positional arguments"))
		}
		value, err = LoadProjectConfig(repo)
		*asJSON = true
	case "memory":
		if len(values) > 0 {
			return fail(errors.New("buildheadway memory accepts no positional arguments"))
		}
		var memory ProjectMemory
		memory, err = ReadMemory(repo)
		value = memory
		formatted = FormatMemory(memory)
	case "remember":
		var entry MemoryEntry
		entry, err = Remember(repo, strings.Join(values, " "))
		value = entry
		formatted = "Saved project note " + entry.ID + " for future goals and resumes."
	case "buildheadwayt":
		if len(values) != 1 {
			return fail(errors.New("usage: buildheadway buildheadwayt NOTE_ID"))
		}
		err = BuildHeadwayt(repo, values[0])
		value = map[string]any{"forgotten": values[0]}
		formatted = "Project note removed."
	case "check":
		if len(values) > 0 {
			return fail(errors.New("configure checks in .buildheadway.json; buildheadway check accepts no positional arguments"))
		}
		var report VerificationReport
		report, err = CheckProject(ctx, paths, repo, emit)
		value = report
		formatted = FormatVerification(report)
		if report.ID != "" {
			if outErr := outputValue(value, formatted, *asJSON); outErr != nil {
				return fail(outErr)
			}
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return 130
				}
				return fail(err)
			}
			return 0
		}
	case "diff":
		if len(values) > 0 {
			return fail(errors.New("buildheadway diff accepts no positional arguments"))
		}
		formatted, err = GitChanges(ctx, paths, repo)
		value = map[string]string{"diff": formatted}
	case "report", "export":
		if len(values) > 1 {
			return fail(errors.New("expected at most one run ID"))
		}
		id := "latest"
		if len(values) == 1 {
			id = values[0]
		}
		if kind == "report" {
			var state State
			state, _, err = LoadRun(repo, id)
			value = state
			formatted = MarkdownReport(state)
		} else {
			var path string
			path, err = ExportRun(repo, id, *destination, *asJSON, *overwrite)
			value = map[string]string{"path": path}
			formatted = path
		}
	case "queue", "next":
		action := "list"
		if kind == "next" {
			action = "next"
		} else if len(values) > 0 {
			action = values[0]
			values = values[1:]
		}
		switch action {
		case "add", "figureout":
			var item QueuedGoal
			item, err = QueueAddMode(repo, strings.Join(values, " "), "figureout")
			value = item
			formatted = "Queued " + item.ID + "\n" + item.Goal
		case "remove":
			if len(values) != 1 {
				return fail(errors.New("usage: buildheadway queue remove QUEUE_ID"))
			}
			err = QueueRemove(repo, values[0])
			value = map[string]string{"removed": values[0]}
			formatted = "Queued goal canceled; any run checkpoint is preserved."
		case "move":
			if len(values) != 2 {
				return fail(errors.New("usage: buildheadway queue move QUEUE_ID POSITION"))
			}
			position, parseErr := strconv.Atoi(values[1])
			if parseErr != nil {
				return fail(errors.New("queue position must be a number"))
			}
			err = QueueMove(repo, values[0], position)
			value = map[string]any{"id": values[0], "position": position}
			formatted = "Queue order updated."
		case "run", "next":
			if len(values) > 0 {
				return fail(errors.New("queue run/next accepts no goal text; use queue add first"))
			}
			settings, loadErr := LoadSettings(paths)
			if loadErr != nil {
				return fail(errors.New("open buildheadway once to choose a connection"))
			}
			err = RunQueued(ctx, paths, settings, repo, action == "run", emit)
			queue, readErr := ReadQueue(repo)
			value = queue
			formatted = FormatQueue(queue)
			if err == nil {
				err = readErr
			}
		case "list":
			if len(values) > 0 {
				return fail(errors.New("queue list accepts no extra arguments"))
			}
			var queue GoalQueue
			queue, err = ReadQueue(repo)
			value = queue
			formatted = FormatQueue(queue)
		default:
			return fail(errors.New("queue actions: add, figureout, list, run, next, remove, move"))
		}
	default:
		return fail(errors.New("unknown project command"))
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return fail(err)
	}
	if err := outputValue(value, formatted, *asJSON); err != nil {
		return fail(err)
	}
	return 0
}
