package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type mcpSetupError struct{ reason string }

func (e mcpSetupError) Error() string { return e.reason }

func tomlMCP(value any) string {
	switch v := value.(type) {
	case string:
		data, _ := json.Marshal(v)
		return string(data)
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case []string:
		parts := make([]string, len(v))
		for i, s := range v {
			parts[i] = tomlMCP(s)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(v))
		for _, key := range keys {
			parts = append(parts, tomlMCP(key)+"="+tomlMCP(v[key]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	panic("unsupported internal MCP configuration type")
}

// Only full-access seniors receive external tools. Workers keep their pinned
// plugin-free configuration; restricted consultations cannot acquire MCP access.
func (r *Runner) prepareMCP(ctx context.Context, connection Connection, planner, full bool, args, env []string) ([]string, []string, error) {
	if len(r.State.Config.MCPServers) == 0 {
		return args, env, nil
	}
	active := map[string]MCPServer{}
	if planner && full {
		for name, server := range r.State.Config.MCPServers {
			if !server.Disabled {
				active[name] = server
			}
		}
	}
	if len(active) != 0 && connection.Backend != "codex" && connection.Backend != "claude" && connection.Backend != "opencode" {
		return nil, nil, mcpSetupError{"configured MCP servers require a full-access Codex, Claude or OpenCode senior; disable them or choose a supported senior"}
	}
	for name, server := range active {
		resolved, err := resolveBrowserServer(r.Paths, server, filepath.Join(r.Dir, "browser-agent"), true)
		if err != nil {
			return nil, nil, err
		}
		active[name] = resolved
		if err := mcpCredentials(name, resolved); err != nil {
			return nil, nil, mcpSetupError{err.Error()}
		}
	}
	servers := map[string]any{}
	for _, name := range sortedMCPNames(active) {
		s := active[name]
		item := map[string]any{}
		switch connection.Backend {
		case "codex":
			item["required"], item["startup_timeout_sec"], item["tool_timeout_sec"] = true, int(s.timeout().Seconds()), int(s.timeout().Seconds())
			if s.Command != "" {
				item["command"], item["args"], item["env_vars"], item["cwd"] = s.Command, s.Args, s.Env, r.Repo
			} else {
				item["url"] = s.URL
				if s.BearerTokenEnv != "" {
					item["bearer_token_env_var"] = s.BearerTokenEnv
				}
			}
		case "claude":
			if s.Command != "" {
				item["type"], item["command"], item["args"] = "stdio", s.Command, append([]string{}, s.Args...)
				variables := map[string]string{}
				for _, key := range s.Env {
					variables[key] = "${" + key + "}"
				}
				item["env"] = variables
			} else {
				item["type"], item["url"] = "http", s.URL
				if s.BearerTokenEnv != "" {
					item["headers"] = map[string]string{"Authorization": "Bearer ${" + s.BearerTokenEnv + "}"}
				}
			}
		case "opencode":
			item["enabled"], item["timeout"] = true, int(s.timeout().Milliseconds())
			if s.Command != "" {
				item["type"], item["command"] = "local", append([]string{s.Command}, s.Args...)
				variables := map[string]string{}
				for _, key := range s.Env {
					variables[key] = "{env:" + key + "}"
				}
				item["environment"] = variables
			} else {
				item["type"], item["url"], item["oauth"] = "remote", s.URL, false
				if s.BearerTokenEnv != "" {
					item["headers"] = map[string]string{"Authorization": "Bearer {env:" + s.BearerTokenEnv + "}"}
				}
			}
		}
		servers[name] = item
	}
	switch connection.Backend {
	case "codex":
		args = append(args, "-c", "mcp_servers="+tomlMCP(servers))
	case "claude":
		help, err := AgentHelp(ctx, connection, r.Paths)
		if err != nil {
			return nil, nil, err
		}
		if !strings.Contains(help.Global, "--mcp-config") || !strings.Contains(help.Global, "--strict-mcp-config") {
			return nil, nil, mcpSetupError{"installed Claude lacks --mcp-config or --strict-mcp-config; update it before using MCP"}
		}
		path := filepath.Join(r.Dir, "mcp-session.json")
		if err := AtomicJSON(path, map[string]any{"mcpServers": servers}); err != nil {
			return nil, nil, err
		}
		args = append(args, "--strict-mcp-config", "--mcp-config", path)
	case "opencode":
		var config map[string]any
		if err := json.Unmarshal([]byte(envValue(env, "OPENCODE_CONFIG_CONTENT")), &config); err != nil {
			return nil, nil, fmt.Errorf("preparing MCP configuration: %w", err)
		}
		config["mcp"] = servers
		data, err := json.Marshal(config)
		if err != nil {
			return nil, nil, err
		}
		env = setEnv(env, "OPENCODE_CONFIG_CONTENT", string(data))
	}
	return args, env, nil
}
