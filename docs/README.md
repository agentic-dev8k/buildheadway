# BuildHeadway documentation

## Use the terminal app

- [Quickstart](quickstart.md): install, verify the command, open a project, connect and give the team a goal.

- [Installation and troubleshooting](install.md): platform instructions, updates, diagnostics, and removal.
- [Shell completions](completions.md): command, option and fixed-value suggestions for Bash, Zsh, Fish and PowerShell.
- [Terminal usage](usage.md): connect agents, run goals, steer work, inspect evidence, and manage queues.
- [Harness and recovery](harness.md): acceptance coverage, verified free routing, source consistency, interruption, and review.
- [Extensions](extensions.md): project workflows, templates, skills, and executable hooks.
- [MCP servers](mcp.md): connect external tools, check servers, and manage agent access.

## Work on BuildHeadway

- [Contributing](../CONTRIBUTING.md): development commands and test requirements.
- [Publishing](maintainers/publishing.md): build artifacts and the separate publication steps.
- [Validation record](maintainers/validation.md): executed checks, environment limits, and continuation notes.
- [Changelog](../CHANGELOG.md): user-visible changes by version.
- [Branding](branding.md) and [identity compatibility](maintainers/naming.md).

Documentation ships with the installable package and source archive. The root README stays focused on installation and first use; detailed guides belong here. Run `node npm/check-docs.cjs` to check local links before packaging.
