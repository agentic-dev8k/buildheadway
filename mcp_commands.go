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
)

func runMCPCommand(paths Paths, args []string) int {
	action := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("buildheadway mcp "+action, flag.ContinueOnError)
	project := flags.String("project", ".", "Project folder")
	asJSON := flags.Bool("json", false, "Print JSON")
	url := flags.String("url", "", "Streamable HTTP server URL")
	token := flags.String("token-env", "", "Environment variable containing the HTTP bearer token")
	timeout := flags.Int("timeout", 0, "Request timeout in seconds, 1–300 (default 30)")
	preset := flags.String("preset", "", "Catalog preset to configure; see mcp catalog")
	preview := flags.Bool("preview", false, "Show configuration without saving or starting a server")
	readWrite := flags.Bool("read-write", false, "Allow editing through the filesystem preset (default read-only)")
	var directories []string
	flags.Func("path", "Directory for a filesystem/git preset, relative to the project; repeat for filesystem", func(value string) error { directories = append(directories, value); return nil })
	var forwarded []string
	flags.Func("env", "Environment variable name to forward to a stdio server; repeat as needed", func(value string) error { forwarded = append(forwarded, value); return nil })
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
	values := flags.Args()
	if action != "add" && (*preset != "" || *preview || *readWrite || len(directories) > 0) {
		return fail(errors.New("--preset, --preview, --path and --read-write apply only to mcp add"))
	}
	if *preset == "" && (*readWrite || len(directories) > 0) {
		return fail(errors.New("--path and --read-write require --preset"))
	}
	if action != "add" && (*url != "" || *token != "" || len(forwarded) != 0) {
		return fail(errors.New("--url, --token-env and --env apply only to mcp add"))
	}
	if action != "add" && action != "check" && *timeout != 0 {
		return fail(errors.New("--timeout applies only to mcp add or mcp check"))
	}
	var value any
	var formatted string
	switch action {
	case "catalog":
		if len(values) > 1 {
			return fail(errors.New("mcp catalog accepts at most one preset name"))
		}
		id := ""
		if len(values) == 1 {
			id = values[0]
		}
		var items []MCPPreset
		items, err = selectMCPPresets(id)
		value, formatted = items, formatMCPPresets(items)
	case "list":
		if len(values) != 0 {
			return fail(errors.New("mcp list accepts no server names"))
		}
		var config ProjectConfig
		config, err = LoadProjectConfig(repo)
		value, formatted = config.MCPServers, FormatMCPServers(config.MCPServers)
	case "add":
		if len(values) == 0 && *preset == "" {
			return fail(errors.New("usage: buildheadway mcp add [--env NAME] SERVER -- COMMAND [ARGS], or mcp add --url URL SERVER"))
		}
		plan := MCPConfigurationPlan{}
		if *preset != "" {
			if len(values) > 1 || *url != "" {
				return fail(errors.New("a preset accepts an optional server name, without --url or a custom command"))
			}
			name := ""
			if len(values) == 1 {
				name = values[0]
			}
			plan, err = planMCPPreset(repo, *preset, name, directories, *readWrite)
			if err != nil {
				return fail(err)
			}
		} else {
			plan.Name, plan.Server.URL = values[0], *url
			command := values[1:]
			if len(command) > 0 && command[0] == "--" {
				command = command[1:]
			}
			if len(command) > 0 {
				plan.Server.Command, plan.Server.Args = command[0], command[1:]
			}
		}
		plan.Server.Env = append(plan.Server.Env, forwarded...)
		plan.Server.TimeoutSeconds = *timeout
		if *token != "" {
			plan.Server.BearerTokenEnv = *token
		}
		if err := validateMCPServers(map[string]MCPServer{plan.Name: plan.Server}); err != nil {
			return fail(err)
		}
		if *preview {
			value, formatted = plan, formatMCPPlan(plan)
			break
		}
		err = changeMCPServer(repo, action, plan.Name, plan.Server)
		value, formatted = map[string]string{"server": plan.Name, "action": action}, "Saved MCP server "+plan.Name+" for new goals. Use buildheadway mcp check "+plan.Name+" to test it."
	case "remove", "enable", "disable":
		if len(values) != 1 {
			return fail(errors.New("expected exactly one MCP server name"))
		}
		err = changeMCPServer(repo, action, values[0], MCPServer{})
		value, formatted = map[string]string{"server": values[0], "action": action}, "MCP configuration updated for new goals. Saved goals retain their pinned configuration."
	case "check":
		if len(values) != 1 {
			return fail(errors.New("usage: buildheadway mcp check [--project FOLDER] [--json] SERVER"))
		}
		var config ProjectConfig
		config, err = LoadProjectConfig(repo)
		if err != nil {
			return fail(err)
		}
		server, exists := config.MCPServers[values[0]]
		if !exists {
			return fail(errors.New("MCP server not found"))
		}
		if *timeout != 0 {
			server.TimeoutSeconds = *timeout
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		var result MCPCheckReport
		result, err = CheckMCP(ctx, paths, repo, values[0], server)
		value, formatted = result, fmt.Sprintf("%s connected · %d tools · protocol %s", result.Name, len(result.Tools), result.Protocol)
	default:
		return fail(errors.New("use mcp catalog, list, add, check, enable, disable or remove"))
	}
	if err != nil {
		return fail(err)
	}
	if err := outputValue(value, formatted, *asJSON); err != nil {
		return fail(err)
	}
	return 0
}
