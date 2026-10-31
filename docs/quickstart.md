# Quickstart

BuildHeadway works from a terminal on Windows, macOS and Linux, with x64 and ARM64 builds. Install the command, open a project folder, connect an agent, then describe what you want built.

## Choose your terminal

| Environment | Start here | Agent connections |
| --- | --- | --- |
| Windows | PowerShell or CMD; use PowerShell for the native `.ps1` installer | Use agents installed and signed in on Windows |
| macOS | Terminal with Zsh or Bash | Use agents installed and signed in on macOS |
| Linux | An interactive terminal with Bash, Zsh or Fish | Use agents installed and signed in in that Linux environment |
| WSL on Windows | Open your WSL distribution's terminal and follow the Linux steps | Install and sign in to Linux agents inside that distribution |

Choose the environment that contains your project and development tools. After installation, the same `buildheadway` command opens the terminal app. Both npm and native downloads select x64 or ARM64 automatically; npm needs Node.js 20+, while the native download needs no Node.js for BuildHeadway itself. Detailed commands are in the [platform installation guide](install.md).

## 1. Install and verify

Download `buildheadway-0.34.0.tgz`. With Node.js 20+ and npm, open a terminal in the download folder and run:

```sh
npm install --global --ignore-scripts ./buildheadway-0.34.0.tgz
buildheadway --version
```

This works in PowerShell, CMD, macOS and Linux shells. The package selects and verifies its native binary automatically; no compiler or installation scripts are needed. The package is not yet published to the npm registry, so use the downloaded filename.

Prefer to install without Node.js? Extract `buildheadway.zip`, then follow the native command for [Windows](install.md#windows), [macOS](install.md#macos), [Linux](install.md#linux) or [WSL](install.md#wsl). Native installations are per user and need no administrator privileges.

## 2. Open your project

```sh
cd my-project
buildheadway
```

The current folder is where agents inspect and edit files, run checks and save progress. For a different folder without changing the terminal directory:

```sh
buildheadway --project "path to my project"
```

## 3. Connect your agents

On first launch, BuildHeadway discovers installed coding agents and lets you choose a senior connection. Follow that agent's own sign-in when needed. Use `/connect` to change or repair a connection and `/login` to open the selected agent's sign-in.

You can attach installed senior agents from the terminal too:

```sh
buildheadway attach codex --lead
buildheadway attach claude
buildheadway attach pi
buildheadway team
```

Run `buildheadway scan` to see detected installations and `buildheadway doctor` to verify the setup without model calls.

Choose agents you already use; installing all of them is unnecessary. Native agents can have their own Node.js or Git requirements, separate from BuildHeadway itself. Discovery does not prove authentication or quota. Implementation workers still require a refreshed zero-price catalog; account, cost and policy blockers remain enforced.

## 4. Give it a goal

Type a request in the app, for example:

```text
Build a task tracker with persistence, search and tests.
```

Or start the same autonomous team workflow directly from your shell:

```sh
buildheadway figureout "Build a task tracker with persistence, search and tests"
```

Seniors plan and review; workers implement; the harness executes checks. Inspect the resulting files and verification evidence. Completion requires the existing acceptance, unchanged-source and senior-approval gates.

## Continue and customize

| Action | Command inside BuildHeadway |
| --- | --- |
| Inspect progress and checks | `/status` |
| See requirement coverage | `/acceptance` |
| Read the agent conversation | `/discussion` |
| See context sizes and limits | `/context` |
| Redirect ongoing work | `/steer YOUR UPDATE` |
| Pause and save | `/stop` |
| Continue saved work | `/resume` |
| See help | `/help` |

From another terminal, `buildheadway pause` requests a saved pause and `buildheadway resume` continues it. Use `buildheadway history` to find earlier goals. The app must remain running to work; it is not a background scheduler.

Add project guidance with `buildheadway init`, create a reusable skill/prompt with `buildheadway extensions init NAME`, or connect external tools with `buildheadway mcp add`. See [extensions](extensions.md), [MCP](mcp.md) and [terminal usage](usage.md).

## If setup fails

Run `buildheadway doctor` and follow its specific diagnostics. It checks installation, command resolution, settings, shell execution and detected agents without model calls. If the command is missing, open a new terminal and read [PATH and terminal troubleshooting](install.md#path-and-terminal-troubleshooting). Updates and removal are covered in the [installation guide](install.md#update).

For command and option suggestions with Tab, enable [shell completions](completions.md).
