<p align="center">
  <img src="assets/brand/buildheadway-logo-on-white-v1.png" alt="BuildHeadway" width="720">
</p>

# BuildHeadway

A terminal coding agent for Windows, macOS, and Linux. Connect your installed coding agents, let senior models plan and review, and delegate implementation to workers verified to have zero listed prices.

BuildHeadway runs checks, coordinates real agent consultations, and continues through repairs. Checkpoints preserve progress; you can pause, resume, or redirect the goal from another terminal.

## Install

Download the installable `.tgz` package, then run with Node.js 20+ and npm:

```sh
npm install --global ./buildheadway-0.22.0.tgz
cd my-project
buildheadway
```

This command works in Windows, macOS, and Linux terminals. All six x64/ARM64 binaries are included and verified by the launcher. No compiler, Python, or npm installation scripts are needed. The package is **not yet published to the npm registry**.

Prefer a native installation without Node.js? See the [installation guide](docs/install.md).

## Give it a goal

```sh
buildheadway figureout "Build a task tracker with persistence, search, and tests"
```

On first launch, select an installed senior agent and complete its own sign-in. You can attach additional seniors with `buildheadway attach codex` or `buildheadway attach claude`. Free workers use OpenCode and a freshly checked zero-price catalog. Account allowances and provider restrictions still apply.

Inside the terminal, `/discussion` shows the agent conversation, `/acceptance` shows requirement coverage, `/steer TEXT` changes direction, and `/stop` saves progress. `/resume` continues it.

From another terminal in the same project, `buildheadway pause` requests a saved pause. Inspect `buildheadway status`, then continue with `buildheadway resume`. Add a run ID to target a particular saved goal.

Customize it with `buildheadway extensions init NAME` and connect external tools with `buildheadway mcp add`. See [extensions](docs/extensions.md) and [MCP connections](docs/mcp.md) for usage and supported agents.

Completion requires executed passing checks, requirement coverage, unchanged checked source, and senior approval. BuildHeadway must stay running to work; it stops for interruption, configured budgets, or genuine access and provider blockers. Full-access goals use the attached agents' unattended tools; use `--access workspace` when that is appropriate for your goal. Read the [harness guide](docs/harness.md) for the boundaries of these controls.

## Documentation

Start with the [documentation index](docs/README.md).

| Guide | Covers |
| --- | --- |
| [Install and troubleshoot](docs/install.md) | Windows, macOS, Linux, updates, and diagnostics |
| [Terminal usage](docs/usage.md) | Agents, goals, commands, steering, queues, and settings |
| [Harness and recovery](docs/harness.md) | Verification, acceptance, interruption, budgets, and completion gates |
| [Extensions](docs/extensions.md) | Workflows, prompt templates, skills, and hooks |
| [Contributing](CONTRIBUTING.md) | Local development and validation |
| [Changelog](CHANGELOG.md) | Release changes |

MIT licensed. [Source](https://github.com/agentic-dev8k/buildheadway) · [Issues](https://github.com/agentic-dev8k/buildheadway/issues) · [Brand assets](docs/branding.md)
