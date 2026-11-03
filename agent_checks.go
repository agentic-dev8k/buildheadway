package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

type AgentCheck struct {
	Status    string `json:"status"`
	Detail    string `json:"detail"`
	CheckedAt string `json:"checked_at"`
}

type FreeModelCheck struct {
	AgentCheck
	Models []string `json:"models"`
}

// Checks describe this invocation only. They never authenticate, call a model,
// save a goal, or authorize dispatch using an old price snapshot.
func checkDetectedAgent(ctx context.Context, agent *DetectedAgent, paths Paths, models bool) {
	check := &AgentCheck{Status: "compatible", Detail: "Required CLI controls are present; authentication and inference are not checked."}
	agent.AdapterCheck = check
	defer func() { check.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano) }()
	if !agent.Installed {
		check.Status, check.Detail = "not_installed", "No executable was found."
	} else if !agent.Adapter {
		check.Status, check.Detail = "unsupported", "Detection only; no execution adapter is registered."
	} else if agent.ProbeError != "" {
		check.Status, check.Detail = "error", agent.ProbeError
	} else if err := Compatible(ctx, Connection{Backend: agent.ID, Executable: agent.Path}, paths); err != nil {
		check.Status, check.Detail = "incompatible", err.Error()
	}
	if !models {
		return
	}
	free := &FreeModelCheck{Models: []string{}, AgentCheck: AgentCheck{Status: "unsupported", Detail: "Automatic zero-price verification is currently supported through OpenCode 1 only; no provider or model was called."}}
	agent.FreeModels = free
	defer func() { free.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano) }()
	if agent.ID != "opencode" {
		return
	}
	if check.Status != "compatible" {
		free.Status, free.Detail = "blocked", check.Detail
		return
	}
	dir, err := os.MkdirTemp("", "buildheadway-catalog-check-")
	if err != nil {
		free.Status, free.Detail = "error", err.Error()
		return
	}
	defer os.RemoveAll(dir)
	argv := []string{agent.Path, "--pure", "models", "opencode", "--verbose", "--refresh"}
	free.Models, err = readFreeModelCatalog(ctx, argv, paths, dir, filepath.Join(dir, "models.log"))
	if err != nil {
		free.Models = []string{}
		free.Status, free.Detail = "error", err.Error()
		return
	}
	free.Status, free.Detail = "verified", "Fresh catalog lists zero prices for these tool-capable models. Authentication, quota, availability and inference are not checked. Dispatch refreshes prices again."
}

func FormatInventory(inventory Inventory) string {
	var out strings.Builder
	fmt.Fprintf(&out, "BuildHeadway %s · %s/%s\n\n", Version, inventory.OS, inventory.Arch)
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
		fmt.Fprintf(&out, "%-14s %s\n", agent.Name, status)
		if c := agent.AdapterCheck; c != nil {
			fmt.Fprintf(&out, "  Adapter: %s · %s\n", c.Status, c.Detail)
		}
		if c := agent.FreeModels; c != nil {
			fmt.Fprintf(&out, "  Free catalog: %s · %s\n", c.Status, c.Detail)
			for _, model := range c.Models {
				fmt.Fprintf(&out, "    %s\n", model)
			}
		}
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func runScanCommand(paths Paths, args []string) int {
	flags := flag.NewFlagSet("buildheadway scan", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "Output JSON")
	check := flags.Bool("check", false, "Check installed adapter compatibility without inference")
	models := flags.Bool("models", false, "Refresh the verified zero-price catalog; implies --check")
	selected := flags.String("agent", "", "Inspect one agent, such as opencode or cline")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		return fail(errors.New("scan takes no positional arguments; use --agent NAME"))
	}
	emptySelection := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "agent" && *selected == "" {
			emptySelection = true
		}
	})
	if emptySelection {
		return fail(errors.New("--agent requires a nonempty agent name"))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	inventory := ScanAgents(ctx, paths)
	if *selected != "" {
		agent, ok := inventory.Find(*selected)
		if !ok {
			return fail(fmt.Errorf("unknown agent %q", *selected))
		}
		inventory.Agents = []DetectedAgent{agent}
	}
	failed := false
	if *check || *models {
		for i := range inventory.Agents {
			a := &inventory.Agents[i]
			// An ordinary inventory includes missing/detection-only agents; do
			// not turn those expected entries into unrelated check failures.
			if !a.Installed && *selected == "" {
				continue
			}
			checkDetectedAgent(ctx, a, paths, *models)
			c := a.AdapterCheck.Status
			failed = failed || c == "error" || c == "incompatible" || (*selected != "" && c != "compatible")
			if f := a.FreeModels; f != nil && (a.ID == "opencode" || *selected != "") && f.Status != "verified" {
				failed = true
			}
		}
	}
	if err := outputValue(inventory, FormatInventory(inventory), *asJSON); err != nil {
		return fail(err)
	}
	if failed || ctx.Err() != nil {
		return 1
	}
	return 0
}
