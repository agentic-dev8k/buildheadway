# BuildHeadway

The user selected BuildHeadway on 26 October 2026, replacing the earlier Forge/Codentrail names. Headway means progress toward a goal. The terminal command and npm package are `buildheadway`. The public source repository is [agentic-dev8k/buildheadway](https://github.com/agentic-dev8k/buildheadway). No public npm release is claimed.

## Existing installations and projects

Saved goals, queues, logs and locks stay in `.forge/`. Nothing is deleted or moved. Configuration precedence is `.buildheadway.json`, then `.codentrail.json`, then `.forge.json`; an invalid newer configuration blocks instead of silently falling back. Root guidance includes BUILDHEADWAY.md, CODENTRAIL.md, FORGE.md, AGENTS.md and CLAUDE.md.

Environment precedence is `BUILDHEADWAY_`, then `CODENTRAIL_`, then `FORGE_`; explicit values, including empty values, take precedence in the app. Hooks receive all three spellings. Terminal installers also accept the older repository/version environment variables.

New installations use BuildHeadway user locations. When none exists, existing Codentrail locations are preferred, then Forge locations. Existing directories are reused, not merged or deleted. The older commands are not uninstalled.

Native BuildHeadway installations use `buildheadway-installation.json` in the selected data directory. Existing Forge/Codentrail installation receipts and executables remain untouched, including when the commands share a data directory.

## Approved artwork

The H-and-forward-arrow logo and matching square icon are included under `assets/brand/`. See [BRANDING.md](BRANDING.md) for the approved files. Keep these assets when packaging future source and npm releases.
