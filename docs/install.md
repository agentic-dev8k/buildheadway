# Install and use the terminal app

BuildHeadway is a terminal program for Windows, macOS, and Linux, on x64 and ARM64. Open your terminal, install the command, change into a project, and run `buildheadway`.

## Install the current download

With Node.js 20+ and npm, run this in the folder containing the downloaded package:

```sh
npm install --global ./buildheadway-0.21.0.tgz
buildheadway --version
cd my-project
buildheadway
```

The package includes all six native binaries. Its launcher chooses and verifies the correct one. It needs no compiler, Python, or installation scripts; `npm install --ignore-scripts` also works.

Without Node.js, extract `buildheadway.zip` and open a terminal in its `BuildHeadway` folder:

| Terminal | Install command |
| --- | --- |
| macOS, Linux, WSL | `sh install.sh` |
| Windows PowerShell | `./install.ps1` |

Open a new terminal, run `buildheadway --version`, then run `buildheadway` in your project. Installation is per user and does not require administrator access. On Windows, use PowerShell for the `.ps1` installer; CMD can run the installed `buildheadway` command. WSL installs the Linux build inside WSL.

The native app needs no Node.js runtime for its own interface and harness. Attached coding agents have their own requirements; BuildHeadway discovers existing installations and can prepare private dependencies when connecting them.

## Give the team a goal

```sh
buildheadway figureout "Build a task tracker with search, persistence and tests"
```

Or open `buildheadway` and type the goal. Use `/acceptance` to see which requirements have passing verification, `/discussion` to see agent messages, `/steer TEXT` to redirect the work, and `/stop` then `/resume` to pause and continue.

Optional senior connections:

```sh
buildheadway attach codex --lead
buildheadway attach claude
buildheadway attach pi
buildheadway team
```

Each agent uses its own provider configuration and sign-in. Installing a CLI does not make its account authenticated or its models free. Workers use the existing verified zero-price routing gate.

## Update

For a native installation, download and extract the new release, then run:

```sh
buildheadway update --from "/path/to/new/BuildHeadway"
```

In PowerShell, the path can be `"C:\Downloads\BuildHeadway"`. The updater verifies the platform binary against the release manifest before installation, preserves the existing installation directory and settings, and leaves the old executable in place if validation fails. Windows replaces the running executable by moving it aside; a still-open backup is cleaned during a later installation after that session exits.

After the project has a published GitHub release and was built with its repository identity, the shorter command is:

```sh
buildheadway update
buildheadway update --version 0.21.0
```

The updater resolves `latest` once, then downloads the binary and manifest from that exact release. A new release appearing during download cannot mix their versions. A local build without a repository explains how to update offline instead.

For npm installations, install the new downloaded `.tgz` with the same `npm install --global` command. `buildheadway update` identifies npm ownership and prints npm instructions. The public package has not been published yet, so a registry installation command is not advertised as usable.

## Diagnose a problem

```sh
buildheadway doctor
buildheadway doctor --json
buildheadway scan
```

`doctor` checks the running platform, command resolution, native installation receipt and checksum, connection settings, project configuration, root guidance files, verification shell, and discovered agents. It reports specific fixes. It makes no model calls and does not repair or migrate settings. Agent `--version` probes can run; authentication is explicitly not claimed by those probes. A diagnostic error produces exit status 1; informational notices and warnings alone produce 0.

If `buildheadway` is not found immediately after installation, open a new terminal. If another program already owns the `buildheadway` command, the native installer refuses to overwrite it: choose a different installation directory and check PATH precedence. `doctor` reports another command taking precedence for native installations.

A checksum error means the release needs to be downloaded again. For npm, reinstall the downloaded package. For native installation, run the installer from the newly extracted folder.

## Remove

```sh
npm uninstall --global buildheadway
```

For a native installation:

```sh
buildheadway uninstall
```

Settings, saved project goals and memory remain. Native removal only undoes a PATH entry that BuildHeadway recorded adding.

## One-command internet installation after publication

The release builder produces standalone `install.sh`, `install.ps1`, a checksum manifest, and six platform binaries. The installers download only the required platform asset and require no Node.js for BuildHeadway. A build with `--repository OWNER/REPOSITORY` embeds the actual source repository in its installer and binary.

Once that repository and release exist, the maintainer can publish these commands with the **actual** repository substituted:

```sh
curl -fsSL https://github.com/OWNER/REPOSITORY/releases/latest/download/install.sh | sh
```

```powershell
irm https://github.com/OWNER/REPOSITORY/releases/latest/download/install.ps1 | iex
```

These are publication templates, not live installation URLs. `BUILDHEADWAY_VERSION` pins a release version; `BUILDHEADWAY_RELEASE_REPOSITORY` can explicitly select the repository for a source installer. No public repository, package, domain or signature is created by the local build.

## Documentation reviewed for this release

Reviewed on 2026-10-23:

- [Claude Code setup](https://code.claude.com/docs/en/setup) and [repository README](https://github.com/anthropics/claude-code): native terminal installation, platform guidance, diagnostics and update lifecycle.
- [Codex CLI documentation](https://learn.chatgpt.com/docs/codex/cli) and [repository README](https://github.com/openai/codex): native and package-manager installation, downloadable platform assets, and a single terminal command.
- [Pi website](https://pi.dev/) and [current coding-agent README](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/README.md): terminal installers, npm without lifecycle scripts, extensibility, steering and saved sessions. Pi's current repository and npm scope are `earendil-works/pi` and `@earendil-works`.

BuildHeadway implements its own installation and verification code. The named projects are references, not bundled implementations or endorsements.
