package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ExtensionSummary struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

type ExtensionInventory struct {
	Workflows []ExtensionSummary   `json:"workflows"`
	Hooks     []ProjectHook        `json:"hooks"`
	MCP       map[string]MCPServer `json:"mcp_servers"`
}

func InspectExtensions(paths Paths, repo string) (ExtensionInventory, error) {
	result := ExtensionInventory{Workflows: []ExtensionSummary{}, Hooks: []ProjectHook{}, MCP: map[string]MCPServer{}}
	items, err := DiscoverWorkflows(paths, repo)
	if err != nil {
		return result, err
	}
	config, err := LoadProjectConfig(repo)
	if err != nil {
		return result, err
	}
	for _, item := range items {
		result.Workflows = append(result.Workflows, ExtensionSummary{item.Name, item.Kind, item.Description, item.Source})
	}
	result.Hooks = append(result.Hooks, config.Hooks...)
	if config.MCPServers != nil {
		result.MCP = config.MCPServers
	}
	return result, nil
}

func FormatExtensions(inventory ExtensionInventory) string {
	var out strings.Builder
	for _, item := range inventory.Workflows {
		fmt.Fprintf(&out, "%s · %s · %s\n  %s\n", item.Name, item.Kind, item.Description, item.Source)
	}
	fmt.Fprintf(&out, "\n%d verification hooks · %d MCP servers\n", len(inventory.Hooks), len(inventory.MCP))
	out.WriteString("Create a skill: buildheadway extensions init NAME\nCreate a prompt: buildheadway extensions init --kind prompt NAME\nInspect hooks: buildheadway hooks\nInspect MCP: buildheadway mcp list")
	return out.String()
}

func InitExtension(paths Paths, repo, name, kind string, personal bool) (Workflow, error) {
	var result Workflow
	if !workflowName.MatchString(name) || (kind != "skill" && kind != "prompt") {
		return result, errors.New("choose a lowercase extension name and kind skill or prompt")
	}
	root, directory := repo, ".buildheadway-skills"
	if kind == "prompt" {
		directory = ".buildheadway-prompts"
	}
	if personal {
		root, directory = paths.Config, "skills"
		if kind == "prompt" {
			directory = "prompts"
		}
	}
	if root == "" {
		return result, errors.New("no extension configuration directory")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return result, err
	}
	parent := root
	parts := []string{directory}
	if kind == "skill" {
		parts = append(parts, name)
	}
	for _, part := range parts {
		parent = filepath.Join(parent, part)
		info, err := os.Lstat(parent)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(parent, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return result, err
			}
			info, err = os.Lstat(parent)
		}
		if err != nil {
			return result, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return result, errors.New("extension directories must be real directories, not symlinks")
		}
	}
	filename := filepath.Join(parent, name+".md")
	if kind == "skill" {
		filename = filepath.Join(parent, "SKILL.md")
	}
	body := "---\nname: " + name + "\ndescription: Custom " + name + " workflow\n---\nWork on this project request: $ARGUMENTS\nRead the project's guidance and inspect the relevant files before changing code.\nPreserve unrelated work. Implement the requested behavior and run meaningful checks.\nReport the actual changes, executed verification, and any remaining blockers.\n"
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		return result, errors.New("extension already exists; its files were left unchanged")
	}
	if err != nil {
		return result, err
	}
	_, writeErr := file.WriteString(body)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return result, err
	}
	return parseWorkflow(name, kind, filename, body)
}

func runExtensionsCommand(paths Paths, args []string) int {
	action := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("buildheadway extensions "+action, flag.ContinueOnError)
	project := flags.String("project", ".", "Project folder")
	personal := flags.Bool("user", false, "Create the extension in personal configuration")
	kind := flags.String("kind", "skill", "Extension type: skill or prompt")
	asJSON := flags.Bool("json", false, "Print JSON")
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
	if action != "init" && (*personal || *kind != "skill") {
		return fail(errors.New("--user and --kind apply only to extensions init"))
	}
	var value any
	var formatted string
	switch action {
	case "list":
		if len(flags.Args()) != 0 {
			return fail(errors.New("extensions list accepts no names"))
		}
		var inventory ExtensionInventory
		inventory, err = InspectExtensions(paths, repo)
		value, formatted = inventory, FormatExtensions(inventory)
	case "init":
		if len(flags.Args()) != 1 {
			return fail(errors.New("usage: buildheadway extensions init [--kind skill|prompt] [--user] NAME"))
		}
		var workflow Workflow
		workflow, err = InitExtension(paths, repo, flags.Args()[0], *kind, *personal)
		value = workflow
		formatted = "Created " + workflow.Source + ". Edit its instructions, then run buildheadway workflow " + workflow.Name + " YOUR_REQUEST."
	default:
		return fail(errors.New("use extensions list or extensions init NAME"))
	}
	if err != nil {
		return fail(err)
	}
	if err := outputValue(value, formatted, *asJSON); err != nil {
		return fail(err)
	}
	return 0
}
