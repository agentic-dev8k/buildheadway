# Custom MCP servers

For managed browser tools and required UI checks, use `buildheadway browser enable`. See the [browser guide](browser.md).

BuildHeadway connects full-access senior agents to external tools through the Model Context Protocol. Local servers use stdio; remote servers use Streamable HTTP. Configure them from the terminal without changing Go code or an attached agent's global settings.

## Choose a preset

Browse the catalog and preview the exact executable, arguments, directory scope and credential variable names:

```sh
buildheadway mcp catalog
buildheadway mcp catalog filesystem
buildheadway mcp add --preset filesystem --preview
buildheadway mcp add --preset filesystem project-files
buildheadway mcp check project-files
```

Add `--json` for structured catalog or preview output. Catalog inspection and `--preview` start no server, create no settings and work even when project configuration needs repair. Saving creates ordinary project MCP entries for new goals. It preserves existing checks, hooks, budgets, acceptance criteria and saved goals; duplicate server names are rejected.

| Preset | Tools | Runtime or credential requirement |
| --- | --- | --- |
| `filesystem` | Read and search selected directories; optional editing | Node.js 24+ and npx; pinned `@modelcontextprotocol/server-filesystem@2.3.0` |
| `git` | Local repository inspection and Git operations | uvx, Python 3.10+ and Git 2.31+; pinned `mcp-server-git==2026.11.11` |
| `fetch` | Public web content | uvx and Python 3.10+; pinned `mcp-server-fetch==2026.11.11` |
| `memory` | Persistent entities, relationships and observations | Node.js 24+ and npx; pinned `@modelcontextprotocol/server-memory@2.1.1`; set `MEMORY_FILE_PATH` |
| `sequential-thinking` | Structured thought sequences and revisions | Node.js 24+ and npx; pinned `@modelcontextprotocol/server-sequential-thinking@2.1.1` |
| `time` | Current time and time-zone conversion | uvx and Python 3.10+; pinned `mcp-server-time==2026.11.11` |
| `github` | Hosted repository, issue and pull-request inspection | Set `GITHUB_MCP_TOKEN`, or select another variable with `--token-env`; read-only endpoint |
| `context7` | Hosted library documentation and examples | Public endpoint with service limits; optional `--token-env CONTEXT7_API_KEY` |

These are optional configurations, not bundled provider executables or a new agent adapter. A later `mcp check` or full-access senior goal can download and start a pinned local package through npx/uvx. Install the listed runtimes first; proxy and trust-store variables can be explicitly forwarded with `--env HTTPS_PROXY` and `--env NODE_EXTRA_CA_CERTS` when needed. Keep certificate verification enabled. Remote providers manage their own versions, access and charges. Configuring one does not authenticate an account or establish free service usage.

`filesystem` selects this project by default and withholds writing tools. Its client roots cannot widen the selected directories. Repeat `--path` for existing directories; relative paths resolve against `--project`, and the preview shows their resolved locations. Use `--read-write` when you intend to expose editing tools:

```sh
buildheadway mcp add --project ./app --preset filesystem --path ./docs --preview docs-reference
buildheadway mcp add --project ./app --preset filesystem --read-write editable-project
buildheadway mcp add --project ./app --preset git repo-history
buildheadway mcp add --preset context7 documentation
buildheadway mcp add --preset github --token-env MY_GITHUB_TOKEN github-read
```

The Git preset selects one existing repository directory and includes Git write operations. `--path` can select that directory explicitly; its own Git identity and permissions remain relevant.

For `memory`, set `MEMORY_FILE_PATH` to an absolute JSONL file path in an existing folder you own, outside npm's cache. Only that variable's name is saved, and startup rejects a missing value. In a POSIX shell, for example, `export MEMORY_FILE_PATH="/absolute/existing/folder/memory.jsonl"`; in PowerShell, `$env:MEMORY_FILE_PATH = 'C:\existing\folder\memory.jsonl'`. Then configure and check it:

```sh
buildheadway mcp add --preset memory project-memory
buildheadway mcp check project-memory
```

`/mcp catalog` and `/mcp catalog PRESET` inspect these choices inside the terminal, including during an active goal. They preserve the current draft and do not install providers. The command palette and shell completion expose preset and template names.

Provider references: [MCP reference-server v2.4.0 release](https://github.com/modelcontextprotocol/servers/releases/tag/v2.4.0), [filesystem scope and read-only modes](https://github.com/modelcontextprotocol/servers/tree/v2.4.0/src/filesystem), [persistent memory storage](https://github.com/modelcontextprotocol/servers/tree/v2.4.0/src/memory), [GitHub's hosted server](https://github.com/github/github-mcp-server/blob/main/docs/remote-server.md) and [Context7](https://github.com/upstash/context7). The reference-server packages are upstream implementations; version pins do not guarantee compatibility with every host or successful model execution.

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
