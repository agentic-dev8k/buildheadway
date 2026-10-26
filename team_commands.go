package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
)

// No model call is made during setup. Native credentials stay in the agent's
// own store; BuildHeadway saves only the executable, model, and requested role.
func AutoSettings(ctx context.Context, paths Paths, emit Emit) (Settings, error) {
	planner := Connection{Backend: "opencode", Model: "auto"}
	for _, backend := range []string{"codex", "claude"} {
		if FindTool(backend, paths) != "" && Authenticated(ctx, Connection{Backend: backend}, paths) {
			planner = Connection{Backend: backend}
			if backend == "claude" {
				planner.Model = "opus"
			}
			break
		}
	}
	connections, err := InstallAgents(ctx, paths, Unique([]string{planner.Backend, "opencode"}), emit)
	if err != nil {
		return Settings{}, err
	}
	planner.Executable = connections[planner.Backend]
	settings := Settings{Planner: planner, Worker: Connection{Backend: "opencode", Model: "auto", Executable: connections["opencode"]}}
	for _, c := range []Connection{settings.Planner, settings.Worker} {
		if err := Compatible(ctx, c, paths); err != nil {
			return settings, fmt.Errorf("%w; use buildheadway and /connect to repair", err)
		}
	}
	if err := SaveSettings(paths, settings); err != nil {
		return settings, err
	}
	return settings, nil
}
func AttachSenior(settings Settings, member SeniorAgent, lead bool) (Settings, error) {
	if err := ValidateTeam([]SeniorAgent{member}); err != nil {
		return settings, err
	}
	if lead || settings.Planner.Backend == "" {
		settings.Planner = member.Connection
	} else {
		if member.Name == "lead" {
			return settings, errors.New("lead is reserved; use --lead to change the coordinator")
		}
		found := false
		for i := range settings.Seniors {
			if settings.Seniors[i].Name == member.Name {
				settings.Seniors[i] = member
				found = true
			}
		}
		if !found {
			settings.Seniors = append(settings.Seniors, member)
		}
	}
	if settings.Worker.Backend == "" {
		settings.Worker = Connection{Backend: "opencode", Model: "auto"}
	}
	_, err := FigureOutTeam(settings)
	return settings, err
}
func DetachSenior(settings Settings, name string) (Settings, error) {
	for i, member := range settings.Seniors {
		if member.Name == name {
			settings.Seniors = append(append([]SeniorAgent{}, settings.Seniors[:i]...), settings.Seniors[i+1:]...)
			return settings, nil
		}
	}
	return settings, errors.New("attached senior not found; use buildheadway team to see names")
}
func runTeamCommand(paths Paths, kind string, args []string) int {
	flags := flag.NewFlagSet("buildheadway "+kind, flag.ContinueOnError)
	model := flags.String("model", "", "Exact model ID or native agent alias")
	provider := flags.String("provider", "", "Provider name for Pi or Cline")
	reasoning := flags.String("reasoning", "", "Codex reasoning effort supported by the selected model")
	name := flags.String("name", "", "Senior name; defaults to the agent name")
	lead := flags.Bool("lead", false, "Use this connection as the lead coordinator")
	asJSON := flags.Bool("json", false, "Print JSON")
	// Accept the natural spelling: buildheadway attach codex --model MODEL.
	positionals := []string{}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positionals, args = append(positionals, args[0]), args[1:]
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	positionals = append(positionals, flags.Args()...)
	settings, err := LoadSettings(paths)
	if err != nil && !(kind == "attach" && errors.Is(err, os.ErrNotExist)) {
		return fail(err)
	}
	if kind == "attach" {
		if len(positionals) != 1 {
			return fail(errors.New("usage: buildheadway attach AGENT [--model ID] [--reasoning LEVEL] [--lead]"))
		}
		backend := strings.ToLower(positionals[0])
		if *name == "" {
			*name = backend
		}
		c := Connection{Backend: backend, Model: *model, Provider: *provider, Reasoning: *reasoning}
		if backend == "demo" {
			return fail(errors.New("use buildheadway --demo for simulated agents"))
		}
		if backend == "opencode" && c.Model == "" {
			c.Model = "auto"
		}
		member := SeniorAgent{Name: *name, Connection: c}
		candidate, err := AttachSenior(settings, member, *lead)
		if err != nil {
			return fail(err)
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		emit := printEvent
		if *asJSON {
			emit = noEmit
		}
		connections, err := InstallAgents(ctx, paths, Unique([]string{backend, "opencode"}), emit)
		if err != nil {
			return fail(err)
		}
		member.Connection.Executable = connections[backend]
		if err := Compatible(ctx, member.Connection, paths); err != nil {
			return fail(err)
		}
		candidate, err = AttachSenior(settings, member, *lead)
		if err != nil {
			return fail(err)
		}
		if candidate.Worker.Executable == "" {
			candidate.Worker.Executable = connections["opencode"]
		}
		if err := SaveSettings(paths, candidate); err != nil {
			return fail(err)
		}
		settings = candidate
	} else if kind == "detach" {
		if len(positionals) != 1 {
			return fail(errors.New("usage: buildheadway detach SENIOR_NAME"))
		}
		settings, err = DetachSenior(settings, positionals[0])
		if err != nil {
			return fail(err)
		}
		if err := SaveSettings(paths, settings); err != nil {
			return fail(err)
		}
	} else if len(positionals) > 0 {
		return fail(errors.New("buildheadway team accepts no positional arguments"))
	}
	team, err := FigureOutTeam(settings)
	if err != nil {
		return fail(err)
	}
	formatted := FormatTeam(team) + "\n\nNew figureout goals use this team. Existing goals keep their saved team."
	if err := outputValue(team, formatted, *asJSON); err != nil {
		return fail(err)
	}
	return 0
}
