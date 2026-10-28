package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/term"
)

func main() { os.Exit(runMain(os.Args[1:])) }
func runMain(args []string) int {
	if len(args) > 0 && args[0] == "--internal-demo-check" {
		if len(args) != 2 {
			return 2
		}
		if err := DemoCheck(args[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("All three addition checks passed")
		return 0
	}
	paths, err := UserPaths()
	if err != nil {
		return fail(err)
	}
	if len(args) > 0 {
		switch args[0] {
		case "--version", "version", "-v":
			fmt.Printf("BuildHeadway %s · %s/%s\n", Version, runtime.GOOS, runtime.GOARCH)
			return 0
		case "doctor":
			repo, _ := os.Getwd()
			report := Diagnose(context.Background(), paths, repo)
			if err := outputValue(report, FormatDoctor(report), contains(args[1:], "--json")); err != nil {
				return fail(err)
			}
			for _, check := range report.Checks {
				if check.Status == "error" {
					return 1
				}
			}
			return 0
		case "update":
			return runUpdate(paths, args[1:])
		case "scan":
			inventory := ScanAgents(context.Background(), paths)
			if contains(args[1:], "--json") {
				encoder := json.NewEncoder(os.Stdout)
				encoder.SetIndent("", "  ")
				if err := encoder.Encode(inventory); err != nil {
					return fail(err)
				}
			} else {
				fmt.Printf("BuildHeadway %s · %s/%s\n\n", Version, inventory.OS, inventory.Arch)
				for _, agent := range inventory.Agents {
					status := "Not installed"
					if agent.Installed {
						status = "Installed · " + agent.Version + " · " + agent.Path
					}
					if agent.ExtensionOnly {
						status = "Editor extension installed; CLI missing"
					}
					if !agent.Adapter && agent.Installed {
						status += " · detection only"
					}
					if agent.ProbeError != "" {
						status += " · " + agent.ProbeError
					}
					fmt.Printf("%-14s %s\n", agent.Name, status)
				}
			}
			return 0
		case "install":
			if brandEnv("BUILDHEADWAY_INSTALL_METHOD") == "npm" {
				fmt.Println("BuildHeadway is installed through npm. Run buildheadway in your project to start.")
				return 0
			}
			flags := flag.NewFlagSet("buildheadway install", flag.ContinueOnError)
			dir := flags.String("dir", "", "Optional installation directory")
			noPath := flags.Bool("no-path", false, "Do not update the user's shell PATH")
			launch := flags.Bool("launch", false, "Open BuildHeadway after installing")
			if err := flags.Parse(args[1:]); err != nil {
				return 2
			}
			target, err := InstallSelf(paths, *dir, !*noPath)
			if err != nil {
				return fail(err)
			}
			fmt.Printf("BuildHeadway %s installed at %s\nOpen a new terminal and type buildheadway.\n", Version, target)
			if *launch {
				command := exec.Command(target, "--choose-project")
				command.Stdin = os.Stdin
				command.Stdout = os.Stdout
				command.Stderr = os.Stderr
				if err := command.Run(); err != nil {
					return fail(err)
				}
			}
			return 0
		case "uninstall":
			if brandEnv("BUILDHEADWAY_INSTALL_METHOD") == "npm" {
				fmt.Println("Remove this npm installation with: npm uninstall -g buildheadway\nYour settings and project history are preserved.")
				return 0
			}
			if err := UninstallSelf(paths); err != nil {
				return fail(err)
			}
			fmt.Println("BuildHeadway removed. Your settings and project history are preserved.")
			return 0
		case "workflow", "skill", "prompt":
			return runWorkflowCommand(paths, args[1:])
		case "extensions":
			return runExtensionsCommand(paths, args[1:])
		case "mcp":
			return runMCPCommand(paths, args[1:])
		case "logs":
			return runLogsCommand(args[1:])
		case "steer":
			return runSteerCommand(args[1:])
		case "run":
			return runHeadless(paths, args[1:])
		case "figureout", "figure-out":
			return runHeadless(paths, append([]string{"--figureout"}, args[1:]...))
		case "attach", "detach", "team":
			return runTeamCommand(paths, args[0], args[1:])
		case "plan":
			return runHeadless(paths, append([]string{"--plan-only"}, args[1:]...))
		case "pause", "init", "config", "memory", "remember", "buildheadwayt", "check", "diff", "report", "export", "queue", "next", "workflows", "skills", "prompts", "hooks", "steering":
			return runProjectCommand(paths, args[0], args[1:])
		case "resume":
			return runHeadless(paths, append([]string{"--resume"}, args[1:]...))
		case "status", "tasks", "history", "events", "discussion", "acceptance", "routing":
			return inspectRun(args[0], args[1:])
		}
	}
	flags := flag.NewFlagSet("buildheadway", flag.ContinueOnError)
	demo := flags.Bool("demo", false, "Try a simulated repair loop without accounts")
	plain := flags.Bool("plain", false, "Print goal progress without the terminal interface")
	project := flags.String("project", "", "Project folder; defaults to the current directory")
	selfTest := flags.Bool("self-test", false, "Run the bundled offline end-to-end verification")
	chooseProject := flags.Bool("choose-project", false, "Choose the working folder before starting")
	flags.Usage = func() {
		fmt.Printf("BuildHeadway %s — your terminal coding team\n\n", Version)
		fmt.Println("  buildheadway                      Open the terminal app")
		fmt.Println("  buildheadway \"your goal\"          Open BuildHeadway with a goal")
		fmt.Println("  buildheadway --demo               Try the offline repair loop")
		fmt.Println("  buildheadway scan                 Find installed coding agents")
		fmt.Println("  buildheadway install              Install for this user")
		fmt.Println("  buildheadway update               Upgrade a native installation")
		fmt.Println("  buildheadway doctor               Diagnose installation and settings")
		fmt.Println("  buildheadway uninstall            Remove the app, preserve your work")
		fmt.Println("  buildheadway run \"your goal\"      Run from a script or pipeline")
		fmt.Println("  buildheadway figureout \"goal\"    Autonomous senior council and free workers")
		fmt.Println("  buildheadway attach codex         Attach a senior agent (optional --model ID)")
		fmt.Println("  buildheadway team                 Show the senior team")
		fmt.Println("  buildheadway discussion           Read the agents' saved conversation")
		fmt.Println("  buildheadway resume [ID]          Recheck and continue saved work")
		fmt.Println("  buildheadway pause [ID]           Pause a goal from another terminal")
		fmt.Println("  buildheadway extensions          Inspect or create custom skills and prompts")
		fmt.Println("  buildheadway mcp                 Configure and check external tool servers")
		fmt.Println("  buildheadway status               Inspect the current goal")
		fmt.Println("  buildheadway acceptance           Show evidence for each requirement")
		fmt.Println("  buildheadway routing              Inspect free-model health and cooldowns")
		fmt.Println("  buildheadway tasks                Show durable task history")
		fmt.Println("  buildheadway history              List saved goals")
		fmt.Println("  buildheadway logs                 Inspect full saved verification and agent logs")
		fmt.Println("  buildheadway events               Show recent harness activity")
		fmt.Println("  buildheadway init                 Add project guidance and settings")
		fmt.Println("  buildheadway plan \"your goal\"     Save a plan for later implementation")
		fmt.Println("  buildheadway check                Run project checks without a model")
		fmt.Println("  buildheadway queue add \"goal\"     Save a goal in the project queue")
		fmt.Println("  buildheadway queue run            Work through the saved queue")
		fmt.Println("  buildheadway memory               Show saved project preferences")
		fmt.Println("  buildheadway diff                 Inspect current Git changes")
		fmt.Println("  buildheadway report               Read the latest run report")
		fmt.Println("  buildheadway export               Save a run report to a file")
		fmt.Println("  buildheadway workflows            List built-in and custom skills/prompts")
		fmt.Println("  buildheadway workflow review      Run a reusable team workflow")
		fmt.Println("  buildheadway steer \"new direction\" Redirect the active team")
		fmt.Println("  buildheadway steering             Show waiting and applied directions")
		fmt.Println("  buildheadway hooks                Inspect project verification hooks")
		fmt.Println("  buildheadway --version            Show the native build")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	repo := *project
	if repo == "" {
		repo, _ = os.Getwd()
	}
	repo, err = filepath.Abs(repo)
	if err != nil {
		return fail(err)
	}
	if info, err := os.Stat(repo); err != nil || !info.IsDir() {
		return fail(errors.New("choose an existing project folder with --project"))
	}
	if err := paths.Ensure(); err != nil {
		return fail(err)
	}
	goal := strings.Join(flags.Args(), " ")
	if *selfTest || (*demo && (*plain || !term.IsTerminal(int(os.Stdin.Fd())))) {
		return runDemoPlain(paths, *selfTest)
	}
	if *plain || !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		if goal == "" {
			flags.Usage()
			return 0
		}
		return runHeadless(paths, []string{"--figureout", "--project", repo, goal})
	}
	if err := RunUI(paths, repo, goal, *demo, *chooseProject); err != nil {
		return fail(err)
	}
	return 0
}
func runHeadless(paths Paths, args []string) int {
	name, stallDefault := "buildheadway run", 3
	if contains(args, "--figureout") {
		name, stallDefault = "buildheadway figureout", 0
	}
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	project := flags.String("project", "", "Project folder")
	resume := flags.Bool("resume", false, "Resume the latest checkpoint")
	planOnly := flags.Bool("plan-only", false, "Save the plan without dispatching workers")
	figureout := flags.Bool("figureout", false, "Autonomous senior council; full access by default")
	access := flags.String("access", "full", "Figureout access: full or workspace")
	jsonEvents := flags.Bool("json", false, "Stream newline-delimited JSON events")
	cycles := flags.Int("max-cycles", 0, "Optional batch limit; zero is unlimited")
	calls := flags.Int("max-planner-calls", 0, "Optional planner-call limit; zero is unlimited")
	stalled := flags.Int("max-stalled-cycles", stallDefault, "Pause after this many unchanged verification rounds; zero disables")
	workers := flags.Int("max-worker-calls", 0, "Worker invocation budget; zero is unlimited")
	maxRuntime := flags.Duration("max-runtime", 0, "Total active run-time budget, for example 30m; zero is unlimited")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *cycles < 0 || *calls < 0 || *stalled < 0 || *workers < 0 || *maxRuntime < 0 {
		return fail(errors.New("limits must be zero or a positive number"))
	}
	if *resume && *planOnly {
		return fail(errors.New("use buildheadway plan with a new goal, or buildheadway resume to implement a saved plan"))
	}
	repo := *project
	if repo == "" {
		repo, _ = os.Getwd()
	}
	settings, err := LoadSettings(paths)
	if err != nil && !(*figureout && errors.Is(err, os.ErrNotExist)) {
		return fail(errors.New("open buildheadway once to choose a connection"))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	var runner *Runner
	emit := printEvent
	if *jsonEvents {
		emit = printJSONEvent
	}
	if err != nil {
		settings, err = AutoSettings(ctx, paths, emit)
		if err != nil {
			return fail(err)
		}
	}
	if *resume && *figureout {
		return fail(errors.New("use buildheadway resume; the saved goal already contains its mode and team"))
	}
	accessSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "access" {
			accessSet = true
		}
	})
	if accessSet && !*figureout {
		return fail(errors.New("--access belongs to a new figureout goal; resume keeps saved permissions"))
	}
	if *resume {
		if len(flags.Args()) > 1 {
			return fail(errors.New("resume accepts one saved run ID"))
		}
		runner, err = ResumeRunner(paths, settings, repo, strings.Join(flags.Args(), ""), emit)
	} else {
		runner, err = NewRunner(paths, settings, repo, strings.Join(flags.Args(), " "), false, emit)
	}
	if err != nil {
		return fail(err)
	}
	if *figureout {
		if err := runner.EnableFigureOut(*access); err != nil {
			return fail(err)
		}
	}
	runner.PlanOnly = *planOnly
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "max-cycles":
			runner.MaxCycles = *cycles
		case "max-planner-calls":
			runner.MaxCalls = *calls
		case "max-worker-calls":
			runner.MaxWorkerCalls = *workers
		case "max-stalled-cycles":
			runner.MaxStalledCycles = *stalled
		case "max-runtime":
			runner.MaxRuntime = *maxRuntime
		}
	})
	if err := runner.Run(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return fail(err)
	}
	return 0
}
func inspectRun(kind string, args []string) int {
	flags := flag.NewFlagSet("buildheadway "+kind, flag.ContinueOnError)
	project := flags.String("project", ".", "Project folder")
	jsonOutput := flags.Bool("json", false, "Print machine-readable output")
	search := flags.String("search", "", "Filter goal history by text, status, or ID")
	limit := flags.Int("limit", 20, "Maximum history entries; zero shows all")
	taskKey := flags.String("task", "", "Inspect one full task record by key (tasks command)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if len(flags.Args()) > 1 || (kind == "history" && len(flags.Args()) > 0) {
		return fail(errors.New("expected at most one run ID (none for history)"))
	}
	repo, err := filepath.Abs(*project)
	if err != nil {
		return fail(err)
	}
	id := "latest"
	if len(flags.Args()) == 1 {
		id = flags.Args()[0]
	}
	taskSelected := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "task" {
			taskSelected = true
		}
	})
	if taskSelected && (kind != "tasks" || *taskKey == "") {
		return fail(errors.New("--task requires a task key and applies only to tasks"))
	}
	var value any
	var formatted string
	if taskSelected {
		value, formatted, err = InspectTask(repo, id, *taskKey)
	} else {
		value, formatted, err = InspectRun(repo, id, kind)
	}
	if kind == "history" && err == nil {
		if *limit < 0 {
			return fail(errors.New("history limit must be zero or positive"))
		}
		rows := FilterHistory(value.([]RunSummary), *search, *limit)
		value, formatted = rows, FormatHistory(rows)
	}
	if err != nil {
		return fail(err)
	}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(value); err != nil {
			return fail(err)
		}
	} else {
		fmt.Println(Clean(formatted))
	}
	return 0
}
func runDemoPlain(paths Paths, selfTest bool) int {
	fmt.Printf("BUILDHEADWAY %s · offline simulated agents · %s/%s\n", Version, runtime.GOOS, runtime.GOARCH)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	runner, err := DemoRunner(paths, printEvent)
	if err != nil {
		return fail(err)
	}
	if selfTest {
		defer os.RemoveAll(runner.Repo)
	}
	if err := runner.Run(ctx); err != nil {
		return fail(err)
	}
	if runner.State.Status != "complete" || runner.State.Cycle != 2 {
		return fail(errors.New("offline self-test did not finish its repair loop"))
	}
	if selfTest {
		fmt.Println("Self-test passed: initial failure, automatic repair, passing checks, and final review.")
	}
	return 0
}
func printEvent(event Event) {
	switch event.Kind {
	case "status":
		fmt.Println("• " + event.Text)
	case "check":
		if event.Text == "@buildheadway-demo-check" {
			fmt.Println("Check: calculator behavior")
		} else {
			fmt.Println("Check: " + event.Text)
		}
	case "check-result", "assistant", "worker", "complete", "paused", "planned", "discussion":
		fmt.Println(event.Text)
	}
}
func fail(err error) int { fmt.Fprintln(os.Stderr, "BuildHeadway:", err); return 2 }
func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
