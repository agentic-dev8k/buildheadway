package main

import (
	"errors"
	"flag"
	"strings"
)

func runWorkflowCommand(paths Paths, args []string) int {
	flags := flag.NewFlagSet("buildheadway workflow", flag.ContinueOnError)
	project := flags.String("project", ".", "Project folder")
	preview := flags.Bool("preview", false, "Show the expanded instructions without running agents")
	asJSON := flags.Bool("json", false, "Stream JSON events (or print a JSON preview)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	values := flags.Args()
	if len(values) == 0 {
		return fail(errors.New("usage: buildheadway workflow [--project FOLDER] [--preview] NAME [arguments]"))
	}
	goal, err := ExpandWorkflow(paths, *project, values[0], values[1:])
	if err != nil {
		return fail(err)
	}
	if *preview {
		if err := outputValue(map[string]string{"goal": goal}, goal, *asJSON); err != nil {
			return fail(err)
		}
		return 0
	}
	runArgs := []string{"--figureout", "--project", *project}
	if *asJSON {
		runArgs = append(runArgs, "--json")
	}
	return runHeadless(paths, append(runArgs, goal))
}
func runSteerCommand(args []string) int {
	flags := flag.NewFlagSet("buildheadway steer", flag.ContinueOnError)
	project := flags.String("project", ".", "Project folder")
	id := flags.String("run", "latest", "Saved run ID")
	source := flags.String("source", "", "Stable source name for duplicate-safe delivery (requires --event and explicit --run)")
	eventID := flags.String("event", "", "Stable source event ID; retries with the same text are acknowledged once")
	asJSON := flags.Bool("json", false, "Print a structured delivery receipt")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	keyed := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "source" || f.Name == "event" {
			keyed = true
		}
	})
	if keyed && (*source == "" || *eventID == "") {
		return fail(errors.New("event delivery requires nonempty --source and --event values"))
	}
	text := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if text == "" {
		return fail(errors.New("usage: buildheadway steer [--project FOLDER] [--run ID] [--source NAME --event ID] [--json] YOUR UPDATED DIRECTION"))
	}
	receipt, err := DeliverSteering(*project, *id, text, *source, *eventID)
	if err != nil {
		return fail(err)
	}
	formatted := "Saved " + receipt.Message.ID + ". The team applies it after the current agent call, or when you resume. Use buildheadway steering to see its status."
	if receipt.Duplicate {
		formatted = "Already received " + receipt.Message.ID + " for " + receipt.RunID + " (" + receipt.Status + "). No new update was added."
	}
	if err := outputValue(receipt, formatted, *asJSON); err != nil {
		return fail(err)
	}
	return 0
}
func selectWorkflows(items []Workflow, kind string) []Workflow {
	if kind != "skills" && kind != "prompts" {
		return items
	}
	out := []Workflow{}
	for _, w := range items {
		if (kind == "skills" && w.Kind == "skill") || (kind == "prompts" && w.Kind != "skill") {
			out = append(out, w)
		}
	}
	return out
}
