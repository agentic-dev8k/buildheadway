package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Presets resolve to ordinary saved MCP configuration. They neither install
// servers nor introduce a second dispatch path or change saved goal schemas.
type MCPPreset struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Transport     string   `json:"transport"`
	Package       string   `json:"package,omitempty"`
	Version       string   `json:"version"`
	Documentation string   `json:"documentation"`
	Requirements  []string `json:"requirements"`
	Notes         []string `json:"notes"`
}

type MCPConfigurationPlan struct {
	Name         string    `json:"name"`
	Preset       string    `json:"preset,omitempty"`
	Requirements []string  `json:"requirements,omitempty"`
	Notes        []string  `json:"notes,omitempty"`
	Server       MCPServer `json:"server"`
}

func mcpPresetCatalog() []MCPPreset {
	const reference = "https://github.com/modelcontextprotocol/servers/tree/v2.4.0/src/"
	return []MCPPreset{
		{ID: "filesystem", Title: "Project files", Description: "Read and search selected directories; writing is optional", Transport: "stdio", Package: "@modelcontextprotocol/server-filesystem", Version: "2.3.0", Documentation: reference + "filesystem", Requirements: []string{"Node.js 24+ and npx"}, Notes: []string{"Defaults to read-only access to this project. --path selects other existing directories; --read-write enables editing.", "Client roots cannot widen the selected directories."}},
		{ID: "git", Title: "Local Git", Description: "Inspect and modify one selected Git repository", Transport: "stdio", Package: "mcp-server-git", Version: "2026.11.11", Documentation: reference + "git", Requirements: []string{"uvx, Python 3.10+ and Git 2.31+"}, Notes: []string{"Defaults to this project. --path selects one existing repository directory.", "The server includes write operations; Git identity and filesystem permissions still apply."}},
		{ID: "fetch", Title: "Web content", Description: "Fetch public pages as readable content", Transport: "stdio", Package: "mcp-server-fetch", Version: "2026.11.11", Documentation: reference + "fetch", Requirements: []string{"uvx and Python 3.10+"}, Notes: []string{"Fetching contacts external sites. Upstream robots and private-network protections remain enabled."}},
		{ID: "memory", Title: "Persistent knowledge graph", Description: "Keep entities, relationships and observations in a local file", Transport: "stdio", Package: "@modelcontextprotocol/server-memory", Version: "2.1.1", Documentation: reference + "memory", Requirements: []string{"Node.js 24+ and npx", "MEMORY_FILE_PATH must name an absolute file path in an existing directory you own"}, Notes: []string{"Only the environment-variable name is saved. Set MEMORY_FILE_PATH before checking or starting a goal.", "Keep the data file outside npm's cache so package updates cannot remove it."}},
		{ID: "sequential-thinking", Title: "Sequential thinking", Description: "Expose structured thought sequences and revisions", Transport: "stdio", Package: "@modelcontextprotocol/server-sequential-thinking", Version: "2.1.1", Documentation: reference + "sequentialthinking", Requirements: []string{"Node.js 24+ and npx"}, Notes: []string{"Provides a reasoning tool; it does not establish correctness or approve a goal."}},
		{ID: "time", Title: "Time conversion", Description: "Inspect current time and convert between time zones", Transport: "stdio", Package: "mcp-server-time", Version: "2026.11.11", Documentation: reference + "time", Requirements: []string{"uvx and Python 3.10+"}, Notes: []string{"Uses the server host's local time zone unless the tool request selects another."}},
		{ID: "github", Title: "GitHub read access", Description: "Inspect repositories, issues and pull requests through GitHub's hosted server", Transport: "http", Version: "hosted", Documentation: "https://github.com/github/github-mcp-server/blob/main/docs/remote-server.md", Requirements: []string{"GITHUB_MCP_TOKEN with access to the intended repositories; --token-env selects another variable"}, Notes: []string{"Uses GitHub's read-only endpoint. Configuration does not create a token, sign in or expand repository access.", "Hosted services retain their own account limits and charges."}},
		{ID: "context7", Title: "Library documentation", Description: "Find current library documentation and code examples", Transport: "http", Version: "hosted", Documentation: "https://github.com/upstash/context7", Requirements: []string{"Network access; optional --token-env CONTEXT7_API_KEY for an existing account"}, Notes: []string{"Uses Context7's hosted Streamable HTTP endpoint with optional bearer authentication.", "Hosted services retain their own account limits and charges."}},
	}
}

func selectMCPPresets(id string) ([]MCPPreset, error) {
	items := mcpPresetCatalog()
	if id == "" {
		return items, nil
	}
	for _, item := range items {
		if item.ID == id {
			return []MCPPreset{item}, nil
		}
	}
	return nil, errors.New("unknown MCP preset; use buildheadway mcp catalog")
}

func formatMCPPresets(items []MCPPreset) string {
	var out strings.Builder
	for _, item := range items {
		fmt.Fprintf(&out, "%s · %s · %s\n  %s\n", item.ID, item.Version, item.Transport, item.Description)
		if len(items) == 1 {
			fmt.Fprintf(&out, "  Requirements: %s\n  Source: %s\n", strings.Join(item.Requirements, "; "), item.Documentation)
			for _, note := range item.Notes {
				fmt.Fprintf(&out, "  %s\n", note)
			}
		}
	}
	out.WriteString("\nInspect: buildheadway mcp catalog PRESET\nPreview: buildheadway mcp add --preset PRESET --preview\nSave: buildheadway mcp add --preset PRESET [NAME]\nCatalog and preview start no server. A later check or goal can download the pinned local package through npx/uvx. Use browser enable for managed Playwright tools.")
	return out.String()
}

func presetDirectories(repo string, paths []string) ([]string, error) {
	if len(paths) > 16 {
		return nil, errors.New("select at most 16 MCP directories")
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	directories := []string{}
	seen := map[string]bool{}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return nil, errors.New("MCP directory paths must not be empty")
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(repo, path)
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		canonical, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return nil, errors.New("MCP directories must already exist")
		}
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			return nil, errors.New("MCP paths must select existing directories")
		}
		if !seen[canonical] {
			seen[canonical] = true
			directories = append(directories, canonical)
		}
	}
	return directories, nil
}

func planMCPPreset(repo, id, name string, paths []string, readWrite bool) (MCPConfigurationPlan, error) {
	var plan MCPConfigurationPlan
	items, err := selectMCPPresets(id)
	if err != nil || id == "" {
		return plan, errors.New("select an MCP preset from buildheadway mcp catalog")
	}
	preset := items[0]
	if name == "" {
		name = preset.ID
	}
	plan = MCPConfigurationPlan{Name: name, Preset: preset.ID, Requirements: preset.Requirements, Notes: preset.Notes}
	if len(paths) > 0 && id != "filesystem" && id != "git" {
		return plan, errors.New("--path applies only to filesystem or git presets")
	}
	if readWrite && id != "filesystem" {
		return plan, errors.New("--read-write applies only to the filesystem preset")
	}
	server := MCPServer{}
	if preset.Transport == "stdio" {
		server.Command = "npx"
		server.Args = []string{"--yes", preset.Package + "@" + preset.Version}
		if id == "git" || id == "fetch" || id == "time" {
			server.Command, server.Args = "uvx", []string{preset.Package + "==" + preset.Version}
		}
	}
	switch id {
	case "filesystem", "git":
		directories, err := presetDirectories(repo, paths)
		if err != nil {
			return plan, err
		}
		if id == "git" {
			if len(directories) != 1 {
				return plan, errors.New("the git preset selects exactly one repository directory")
			}
			server.Args = append(server.Args, "--repository", directories[0])
		} else {
			server.Args = append(server.Args, "--confine-roots")
			if !readWrite {
				server.Args = append(server.Args, "--read-only")
			} else {
				plan.Notes = append(append([]string{}, plan.Notes...), "Editing is enabled for the selected directories.")
			}
			server.Args = append(server.Args, "--")
			server.Args = append(server.Args, directories...)
		}
	case "memory":
		server.Env = []string{"MEMORY_FILE_PATH"}
	case "github":
		server.URL, server.BearerTokenEnv = "https://api.githubcopilot.com/mcp/readonly", "GITHUB_MCP_TOKEN"
	case "context7":
		server.URL = "https://mcp.context7.com/mcp"
	}
	plan.Server = server
	return plan, validateMCPServers(map[string]MCPServer{name: server})
}

func formatMCPPlan(plan MCPConfigurationPlan) string {
	var out strings.Builder
	fmt.Fprintf(&out, "MCP configuration preview · %s\n", plan.Name)
	if plan.Preset != "" {
		fmt.Fprintf(&out, "Preset: %s\n", plan.Preset)
	}
	if plan.Server.URL != "" {
		fmt.Fprintf(&out, "URL: %q\n", plan.Server.URL)
	} else {
		fmt.Fprintf(&out, "Command: %q\n", plan.Server.Command)
		for i, arg := range plan.Server.Args {
			fmt.Fprintf(&out, "  argument %d: %q\n", i+1, arg)
		}
	}
	if len(plan.Server.Env) > 0 {
		fmt.Fprintf(&out, "Forward environment names: %s\n", strings.Join(plan.Server.Env, ", "))
	}
	if plan.Server.BearerTokenEnv != "" {
		fmt.Fprintf(&out, "Bearer environment name: %s\n", plan.Server.BearerTokenEnv)
	}
	for _, requirement := range plan.Requirements {
		fmt.Fprintf(&out, "Requires: %s\n", requirement)
	}
	for _, note := range plan.Notes {
		fmt.Fprintf(&out, "%s\n", note)
	}
	out.WriteString("No configuration was saved and no server was started. Remove --preview to save for new goals; mcp check NAME performs a connection test.")
	return out.String()
}
