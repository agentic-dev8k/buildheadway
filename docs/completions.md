# Shell completions

Generate completions from your installed version. `buildheadway completion SHELL` prints a script; it does not change your profiles. Supported shells are Bash, Zsh, Fish and PowerShell. The terminal app's slash commands are separate from shell completion.

## Bash

Load for this terminal (Bash 3.2 or later):

```bash
source <(buildheadway completion bash)
```

To keep it, add that line to your existing Bash configuration after PATH is set. A login terminal may use `.bash_profile`, `.bash_login` or `.profile`; interactive non-login terminals use `.bashrc`. Keep your existing profile sourcing arrangement.

## Zsh

Load after initializing Zsh completion:

```zsh
autoload -Uz compinit
compinit
source <(buildheadway completion zsh)
```

To keep it, add the final line after your existing `compinit` in `.zshrc` (under `ZDOTDIR` when configured). Do not add a second `compinit` if your shell framework already runs it.

## Fish

For this terminal:

```fish
buildheadway completion fish | source
```

For future terminals, save the script in your Fish completions directory:

```fish
mkdir -p $__fish_config_dir/completions
buildheadway completion fish > $__fish_config_dir/completions/buildheadway.fish
```

Regenerate that file after upgrading BuildHeadway.

## PowerShell

Save and load the generated script:

```powershell
buildheadway completion powershell | Out-File -Encoding utf8 "$HOME/buildheadway-completion.ps1"
. "$HOME/buildheadway-completion.ps1"
```

For future terminals, add the dot-source line to your existing `$PROFILE`. Use the same shell/profile where `buildheadway` is on PATH. Your existing execution policy still applies. This does not change that policy. Regenerate the saved script after upgrading BuildHeadway.

## What Tab completes

- Commands such as `figureout`, `resume`, `context`, `logs` and `queue`.
- Actions such as `mcp add`, `extensions init` and `queue archives`.
- Supported options and fixed values, such as `figureout --access workspace`, `extensions init --kind prompt` and `attach claude --model`.

Completion observes argument boundaries, including quoted paths and `--`. Most options must come before the first positional argument; queue options come before its action, while MCP/extension options come after their action. The engine avoids suggesting flags after goal text, where the normal CLI would treat them as part of the goal.

Suggestions are fixed CLI syntax. Completion does not discover installed agents, enumerate provider models, read project settings or credentials, inspect saved goals, call MCP servers, invoke models or start work. It does not complete run IDs, task keys, custom workflow names, server names or arbitrary model IDs. Shell-native filename completion may still apply when there are no fixed suggestions.

The generated script invokes the installed `buildheadway` command with a bounded syntax-only request on Tab. Keep the executable on PATH. Close the terminal or remove its completion registration/profile line to stop using it. No completion files are installed or removed automatically by BuildHeadway.

See [installation](install.md) for PATH troubleshooting and [terminal usage](usage.md) for commands.
