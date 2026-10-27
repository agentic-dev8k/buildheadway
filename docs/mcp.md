# Custom MCP servers

BuildHeadway connects full-access senior agents to external tools through the Model Context Protocol. Local servers use stdio; remote servers use Streamable HTTP. Configure them from the terminal without changing Go code or an attached agent's global settings.

## Add and check

For a server you already installed:

```sh
buildheadway mcp add local-tools -- node scripts/my-mcp-server.cjs
buildheadway mcp check local-tools
buildheadway mcp list
```

For a real HTTP endpoint, set `DOCS_TOKEN` in your shell and run:

```sh
buildheadway mcp add --url https://your-server.example/mcp --token-env DOCS_TOKEN docs
buildheadway mcp check --json docs
```

Omit `--token-env` for a public endpoint. Use repeatable `--env NAME` flags to forward variables to a local server. Put flags before the server name; arguments after `--` belong to the server executable. Commands and arguments stay separate, including Windows paths with spaces.

Adding saves configuration; it does not install software or claim a working connection. `mcp check` actually initializes the server and lists tools without calling them or an LLM. It exits nonzero for missing credentials, failed connections, timeouts or oversized/invalid replies. Ctrl+C cancels the check. Success describes that connection at that time.

The diagnostic supports public endpoints and environment-based bearer tokens. It does not perform OAuth sign-in, follow redirects or support legacy SSE endpoints. Local diagnostics inherit standard process variables plus the explicitly named `--env` variables. Server startup runs the configured executable under your account.

## Manage and customize

```sh
buildheadway mcp disable docs
buildheadway mcp enable docs
buildheadway mcp remove docs
buildheadway extensions
```

`/mcp` lists servers inside the terminal app; `/extensions` lists customization sources. Listing is read-only and starts no server. Use the CLI check for a connection test.

Entries live in the project's `.buildheadway.json` alongside its existing checks:

```json
{
  "version": 1,
  "checks": ["npm test"],
  "mcp_servers": {
    "local-tools": {
      "command": "node",
      "args": ["scripts/my-mcp-server.cjs"],
      "env": ["TOOLS_API_KEY"],
      "timeout_seconds": 30
    },
    "docs": {
      "url": "https://your-server.example/mcp",
      "bearer_token_env": "DOCS_TOKEN",
      "disabled": true
    }
  }
}
```

Use exactly one command or URL per entry, at most 16 servers. Names begin with a letter and contain letters, digits, underscores or hyphens; hyphen/underscore aliases cannot coexist. Timeout defaults to 30 seconds and accepts 1–300. `env` and `bearer_token_env` store variable names; expanded values do not enter configuration, generated session files or checkpoints. Commands, arguments and URLs are saved literally, so keep secrets out of those fields.

Configuration commands retain other checks, hooks, budgets and acceptance requirements. They leave earlier-brand configuration and saved goals unchanged. New goals capture the current MCP entries; resumes retain their saved configuration. Start a new goal to use a changed server set, and restore required environment variables before resume.

## Agent access

| Connection | Configured MCP tools |
| --- | --- |
| Full-access Codex senior | Per-invocation configuration; enabled servers are required at startup |
| Full-access Claude senior | Explicit session file and strict MCP flags, which the installed CLI must support |
| Full-access OpenCode senior | Session configuration through the existing compatible CLI adapter |
| Free workers and restricted consultations | No configured MCP tools attached; model pins and plugin/delegation restrictions remain |
| Pi, Cline and Gemini seniors | Forwarding is not implemented; enabled MCP entries produce a clear blocker |

The attached agent performs real tool calls. The diagnostic neither establishes authenticated model inference nor approves completion. Native tool availability remains subject to that client's policy and authentication. MCP services can have separate permissions and charges; zero-price model checks do not describe service pricing.

Setup errors stop instead of rotating models. Existing budgets, fresh zero-price checks, output limits, source verification and senior approval remain enforced. Diagnostic input is bounded to 1 MiB per frame/HTTP response, 256 tools and 16 pages. Local diagnostic cancellation uses the existing process-tree termination; native agents retain their own MCP transport limits inside the goal's timeout/cancellation.

This is BuildHeadway's integration, not a Pi/Claude plugin compatibility layer. Design references: [Pi MCP](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/mcp.md), [Codex MCP](https://developers.openai.com/codex/mcp), [Claude flags](https://code.claude.com/docs/en/cli-reference), [OpenCode MCP](https://opencode.ai/docs/mcp-servers/) and the [official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk).
