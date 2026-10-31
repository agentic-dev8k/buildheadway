package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Credentials are environment-variable names, never expanded checkpoint values.
type MCPServer struct {
	Builtin        string   `json:"builtin,omitempty"`
	BuiltinVersion string   `json:"builtin_version,omitempty"`
	Command        string   `json:"command,omitempty"`
	Args           []string `json:"args,omitempty"`
	URL            string   `json:"url,omitempty"`
	Env            []string `json:"env,omitempty"`
	BearerTokenEnv string   `json:"bearer_token_env,omitempty"`
	Disabled       bool     `json:"disabled,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

var mcpName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var mcpEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (s MCPServer) timeout() time.Duration {
	if s.TimeoutSeconds == 0 {
		return 30 * time.Second
	}
	return time.Duration(s.TimeoutSeconds) * time.Second
}

func validateMCPServers(servers map[string]MCPServer) error {
	if len(servers) > 16 {
		return errors.New("configure at most 16 MCP servers")
	}
	identities := map[string]bool{}
	builtins := 0
	for name, server := range servers {
		identity := strings.ReplaceAll(name, "-", "_")
		if !mcpName.MatchString(name) || identities[identity] {
			return errors.New("MCP servers need distinct names beginning with a letter; hyphens and underscores must not collide")
		}
		identities[identity] = true
		fail := func(reason string) error { return fmt.Errorf("MCP server %s: %s", name, reason) }
		if server.Builtin != "" {
			builtins++
			if builtins > 1 {
				return fail("configure only one built-in browser")
			}
			if server.Builtin != "browser" || server.BuiltinVersion != browserProvider || server.Command != "" || server.URL != "" || len(server.Args) > 0 || len(server.Env) > 0 || server.BearerTokenEnv != "" || server.TimeoutSeconds < 0 || server.TimeoutSeconds > 300 {
				return fail("invalid pinned built-in browser profile")
			}
			continue
		}
		if server.BuiltinVersion != "" {
			return fail("builtin_version requires a builtin")
		}
		if (server.Command == "") == (server.URL == "") {
			return fail("set exactly one executable command or HTTP URL")
		}
		if server.TimeoutSeconds < 0 || server.TimeoutSeconds > 300 {
			return fail("timeout_seconds must be 0 (30 seconds) or 1–300")
		}
		if len(server.Args) > 64 || len(server.Env) > 32 || len(server.Command) > 4096 || strings.ContainsRune(server.Command, 0) || strings.TrimSpace(server.Command) != server.Command {
			return fail("invalid or oversized executable, arguments or environment list")
		}
		for _, arg := range server.Args {
			if len(arg) > 4096 || strings.ContainsRune(arg, 0) {
				return fail("invalid command argument")
			}
		}
		seen := map[string]bool{}
		for _, key := range server.Env {
			if !mcpEnvName.MatchString(key) || seen[key] {
				return fail("env must contain distinct environment-variable names, not values")
			}
			seen[key] = true
		}
		if server.URL != "" {
			u, err := url.Parse(server.URL)
			if err != nil || len(server.URL) > 4096 || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" {
				return fail("use an absolute HTTP(S) URL without user credentials or a fragment")
			}
			if len(server.Args) != 0 || len(server.Env) != 0 {
				return fail("args and env apply only to stdio servers")
			}
		} else if server.BearerTokenEnv != "" {
			return fail("bearer_token_env applies only to HTTP servers")
		}
		if server.BearerTokenEnv != "" && !mcpEnvName.MatchString(server.BearerTokenEnv) {
			return fail("bearer_token_env must name an environment variable")
		}
	}
	return nil
}

func sortedMCPNames(servers map[string]MCPServer) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func FormatMCPServers(servers map[string]MCPServer) string {
	if len(servers) == 0 {
		return "No MCP servers configured. Use buildheadway mcp add NAME -- COMMAND [ARGS], or buildheadway mcp add --url URL NAME."
	}
	var out strings.Builder
	for _, name := range sortedMCPNames(servers) {
		s := servers[name]
		state, transport := "configured", "stdio"
		if s.Disabled {
			state = "disabled"
		}
		if s.Builtin != "" {
			transport = "builtin/" + s.Builtin + "@" + s.BuiltinVersion
		}
		if s.URL != "" {
			transport = "http"
		}
		fmt.Fprintf(&out, "%s · %s · %s\n", name, transport, state)
	}
	out.WriteString("Use buildheadway mcp check NAME to connect and list tools. Configuration alone does not establish a healthy connection.")
	return out.String()
}

func changeMCPServer(repo, action, name string, server MCPServer) error {
	if !mcpName.MatchString(name) {
		return errors.New("invalid MCP server name")
	}
	lock, err := acquireProjectLock(repo, "config-lock", true)
	if err != nil {
		return err
	}
	defer lock.Close()
	config, err := LoadProjectConfig(repo)
	if err != nil {
		return err
	}
	if config.MCPServers == nil {
		config.MCPServers = map[string]MCPServer{}
	}
	old, exists := config.MCPServers[name]
	switch action {
	case "add":
		if exists {
			return errors.New("MCP server already exists; remove it before replacing its configuration")
		}
		config.MCPServers[name] = server
	case "remove", "enable", "disable":
		if !exists {
			return errors.New("MCP server not found")
		}
		if action == "remove" {
			delete(config.MCPServers, name)
		} else {
			old.Disabled = action == "disable"
			config.MCPServers[name] = old
		}
	default:
		return errors.New("unknown MCP configuration action")
	}
	if err := ValidateProjectConfig(config); err != nil {
		return err
	}
	data, err := json.Marshal(config)
	if err != nil || len(data) > 128<<10 {
		return errors.New("project configuration exceeds its 128 KiB size limit")
	}
	return AtomicJSON(filepath.Join(repo, ".buildheadway.json"), config)
}
