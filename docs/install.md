# Install and use the terminal app

BuildHeadway is a terminal program for Windows, macOS, and Linux, on x64 and ARM64. Open your terminal, install the command, change into a project, and run `buildheadway`.

For the complete first-run flow, start with the [quickstart](quickstart.md). Choose a platform below for native installation; the npm download works across all three operating systems.

## Install the current download

With Node.js 20+ and npm, run this in the folder containing the downloaded package:

```sh
npm install --global --ignore-scripts ./buildheadway-0.33.0.tgz
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

## Windows

Use PowerShell or CMD for the npm command above. For the native download, extract `buildheadway.zip`, open PowerShell in the extracted `BuildHeadway` directory, then run:

```powershell
.\install.ps1
```

Open a new terminal:

```powershell
buildheadway --version
cd "C:\path to\my-project"
buildheadway
```

The installer detects x64/ARM64 and adds a per-user command location to PATH. No administrator window or WSL is required for BuildHeadway's native interface and harness. If policy blocks the script, use the npm download or the matching verified executable's `install` command; organization policy still applies. Avoid changing machine-wide execution policy just to run the installer.

Attached agents keep their own shell requirements. `buildheadway doctor` verifies the native command shell with actual passing/failing probes. Install and authenticate connections in the same Windows environment in which BuildHeadway runs.

## macOS

Use Terminal or another interactive terminal. With the extracted native download, change into `BuildHeadway` and run:

```sh
sh install.sh
```

Open a new terminal, then:

```sh
buildheadway --version
cd "/path to/my-project"
buildheadway
```

The installer selects Apple silicon or Intel automatically, including Apple silicon detected from a translated shell. The default user command location is `~/.local/bin`. Zsh setup respects an absolute `ZDOTDIR`; Bash setup covers both interactive and login terminals. These builds are unsigned; use the npm route if the native download is blocked rather than disabling platform protections.

## Linux

From the extracted native download's `BuildHeadway` folder:

```sh
sh install.sh
```

Open a new terminal, then:

```sh
buildheadway --version
cd "/path to/my-project"
buildheadway
```

The installer selects x64 or ARM64 and installs per user into `~/.local/bin` by default. No sudo or system package changes are needed. Bash interactive/login profiles are supported; Zsh respects `ZDOTDIR` and Fish uses an absolute `XDG_CONFIG_HOME` or `~/.config`. For an unsupported shell, use `--no-path` and add the chosen command directory using that shell's own configuration.

## WSL

Install and run BuildHeadway inside the selected WSL distribution using the Linux instructions or the npm package. For example:

```sh
cd ~/my-project
buildheadway
```

WSL is a separate Linux environment: use its Linux build, paths, agents and provider sign-in. A native Windows installation does not automatically install or authenticate those Linux connections. Choose the environment where your project tools run.

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
buildheadway update --version 0.29.0
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

### PATH and terminal troubleshooting

For npm installations, `npm prefix --global` shows the prefix. The command directory is that prefix on Windows and its `bin` child on macOS/Linux; make sure it is on the terminal's PATH. Native installations print their executable path, which can be used directly to run `doctor` if command lookup fails.

Native Bash setup adds one managed block to `.bashrc` and to the first existing login profile in this order: `.bash_profile`, `.bash_login`, `.profile`; if none exists, it creates `.bash_profile`. It preserves existing settings, file permissions and symlinked dotfiles. Reinstallation does not append another block. Zsh uses `$ZDOTDIR/.zshrc` when configured; Fish uses its `conf.d` directory. The receipt records the actual files updated, so removal can clean up the same files after a shell change. A block replaced or edited to point at another directory is retained. Older receipts without recorded files retain the legacy current-shell cleanup behavior.

BuildHeadway reads `SHELL` when choosing Unix startup files. If you switch shells, rerun the native installer from that shell, or use `--no-path` and manage PATH yourself. Windows PATH setup uses the user environment rather than a PowerShell profile. The first shell still needs to be reopened after installation; a child installer cannot alter its parent's environment.

Use Enter to send and Alt+Enter for a newline in the app. Windows Terminal may reserve Alt+Enter; adjust that terminal shortcut if needed, or use the shell command `buildheadway figureout "YOUR GOAL"` for progress without the interactive editor. `/help` lists supported commands; BuildHeadway does not claim every OpenCode/Pi shortcut.

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

Earlier installation references, reviewed on 2026-10-23:

- [Claude Code setup](https://code.claude.com/docs/en/setup) and [repository README](https://github.com/anthropics/claude-code): native terminal installation, platform guidance, diagnostics and update lifecycle.
- [Codex CLI documentation](https://learn.chatgpt.com/docs/codex/cli) and [repository README](https://github.com/openai/codex): native and package-manager installation, downloadable platform assets, and a single terminal command.
- [Pi website](https://pi.dev/) and [current coding-agent README](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/README.md): terminal installers, npm without lifecycle scripts, extensibility, steering and saved sessions. Pi's current repository and npm scope are `earendil-works/pi` and `@earendil-works`.

Reviewed on 2026-10-28 for this update:

- [Pi quickstart](https://pi.dev/docs/latest/quickstart), its [repository version](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/quickstart.md) and [Windows guidance](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/windows.md): install/verify/project/connect/task flow and distinct native Windows/WSL environments. The live quickstart also documents a native PowerShell installer; BuildHeadway provides its own PowerShell/native and npm routes.
- [OpenCode 1 introduction](https://docs.opencode.ai/docs/) and [Windows/WSL guide](https://docs.opencode.ai/docs/windows-wsl/) and [OpenCode 2 introduction](https://opencode.ai/v2/docs): terminal entry point, platform downloads, connection setup and customization. OpenCode 1 and 2 share a command name but have different interfaces. BuildHeadway still checks its adapter's required flags; this setup update does not silently claim OpenCode 2 adapter compatibility.

BuildHeadway implements its own installation and verification code. The named projects are references, not bundled implementations or endorsements.
