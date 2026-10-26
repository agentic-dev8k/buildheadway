# Workflows, steering, and verification hooks

BuildHeadway 0.10 adds reusable instructions and local automation to the existing terminal team. You still start it with `buildheadway` and give it a goal.

## Everyday commands

| In BuildHeadway | In another terminal | What it does |
| --- | --- | --- |
| `/workflows` | `buildheadway workflows` | List built-in and custom workflows with their sources |
| `/review API changes` | `buildheadway workflow review "API changes"` | Review, repair, and verify changes |
| `/debug login fails` | `buildheadway workflow debug "login fails"` | Reproduce and repair a problem |
| `/test checkout` | `buildheadway workflow test checkout` | Improve meaningful test coverage |
| `/docs installation` | `buildheadway workflow docs installation` | Update and verify documentation |
| `/steer Use SQLite locally` | `buildheadway steer "Use SQLite locally"` | Change the current goal's direction |
| `/steering` | `buildheadway steering` | Show waiting and applied user updates |
| `/hooks` | `buildheadway hooks` | Show the project's configured verification hooks |

CLI options go **before** the workflow name or goal text. For example:

```sh
buildheadway workflow --project ./my-app --preview debug "login fails"
buildheadway workflow --project ./my-app --json review "API compatibility"
buildheadway steer --project ./my-app --run SAVED_RUN_ID "Use SQLite locally"
```

`--preview` shows the exact expanded instructions without calling any model. `/workflows NAME` or `buildheadway workflows NAME` shows a workflow's original text. Built-ins use the same senior discussion, free-worker routing, verification and completion gates as ordinary goals.

## Add a prompt template

Create `.buildheadway-prompts/api-review.md` in your project:

```markdown
---
description: Inspect a public API and its compatibility
---
Review ${1:-the public API} for compatibility and correctness.
Additional focus: ${@:-error handling and documentation}.
Fix supported problems and verify the affected behavior.
```

Then run `/api-review "the billing API"`, `/workflow api-review "the billing API"`, or `buildheadway workflow api-review "the billing API"`. Commands reload resources when invoked; a restart is unnecessary.

Substitution supports `$1`, `$2`, further numbered arguments, `$@`, `$ARGUMENTS`, `${1:-default}`, and `${@:-default}`. Quoted arguments stay together in the terminal UI; backslashes remain literal so Windows paths work. Arguments are substituted once. They are never evaluated as shell commands or environment variables. If the template has no placeholders, BuildHeadway appends the supplied arguments as the user request.

Only direct `.md` files are discovered. Names use lowercase letters, digits and hyphens, beginning with a letter. Descriptions must fit on one line. Files must be regular UTF-8 text of at most 64 KiB. Discovery rejects paths resolving outside the project or personal configuration folder.

## Add a skill

Create `.buildheadway-skills/database/SKILL.md`:

```markdown
---
name: database
description: Implement database changes with migration coverage
---
Read references/conventions.md beside this skill.
Implement the requested change: $ARGUMENTS
Check migration behavior, rollback expectations and affected queries.
Preserve existing data and verify the result with meaningful local checks.
```

Put supporting files under that same `database` directory. Run `/skill database "add task labels"` or `buildheadway workflow database "add task labels"`. BuildHeadway expands the instructions into the goal and supplies the supporting directory to the agents. They can read referenced files as needed. `/skills` lists skills; `/prompts` lists templates and built-in workflows.

These are explicit, text-based skills. BuildHeadway does not implement Pi's TypeScript extensions, Claude plugins, dynamic shell expansion, automatic skill selection, or every field of either product's skill format. `name` and `description` are read from simple frontmatter; other frontmatter is ignored. Supporting files are read from disk when needed, while the expanded top-level instructions are saved with the goal and retained on resume.

For personal resources, use `prompts/` and `skills/` under BuildHeadway's config directory. `BUILDHEADWAY_HOME` places that directory at `$BUILDHEADWAY_HOME/config`; otherwise the platform defaults are:

| Platform | Personal config directory |
| --- | --- |
| Linux | `$XDG_CONFIG_HOME/buildheadway`, or `~/.config/buildheadway` |
| macOS | `~/Library/Application Support/BuildHeadway` |
| Windows | `%APPDATA%\BuildHeadway` |

Project resources override personal resources, which override built-ins. Duplicate prompt and skill names within the same scope produce an error. Core terminal commands take precedence over workflow shortcuts; `/workflow NAME` always selects the workflow explicitly. Project `.buildheadway-prompts` and `.buildheadway-skills` folders can be committed to source control; transient runs remain in the ignored `.buildheadway` folder.

## Redirect an active team

`/steer` writes a durable user update for the current run. A second terminal can use `buildheadway steer` while the main terminal is busy. It does not kill the current child process: BuildHeadway applies the update when that agent call returns, before another model invocation or implementation task. If the run is paused or blocked, the update waits for `/resume`. `/steering` shows delivery status.

Applying a direction invalidates the old plan, pending tasks, consultations and review votes. If implementation has started, BuildHeadway checks the current files before further review. The team receives the original goal and all user updates, with later updates taking precedence. Existing required checks remain required. Updates and their application are checkpointed; concurrent senders cannot overwrite one another, and completion is serialized with message submission.

Typing an ordinary new goal while busy still queues it for later. Use `/steer` to change the active goal. Completed runs reject steering; start a new goal instead. A run supports up to 64 updates, each at most 8,000 bytes, and 128 KiB of update text in total.

## Automate verification

Add hooks to `.buildheadway.json`. For an existing JavaScript project whose `format` and `lint` scripts are configured:

```json
{
  "version": 1,
  "checks": ["npm test"],
  "hooks": [
    {
      "name": "format",
      "event": "before_check",
      "command": "npm run format",
      "timeout_seconds": 120
    },
    {
      "name": "lint-gate",
      "event": "after_check",
      "command": "npm run lint",
      "timeout_seconds": 120
    }
  ]
}
```

Hooks run automatically in autonomous goals and `buildheadway check`. `before_check` runs before the project's tests; `after_check` runs after them, including when tests fail. Hooks are ordered within each event. Every applicable hook must succeed for the goal to complete, in addition to executed checks and the team's review. A failure is returned to the seniors and workers as repair evidence. A passing hook cannot substitute for real project checks.

Use repeatable local commands such as formatters, validators, and build checks. They may run again after a repair, interruption or resume. Do not put one-shot actions in verification hooks. Hook **configuration** is captured when a goal starts, so an agent's edit to `.buildheadway.json` cannot silently replace that goal's gates. Referenced script contents remain editable for legitimate repairs. A new goal or standalone `buildheadway check` reads the current config.

If an `after_check` hook changes tracked source content, BuildHeadway reruns verification before review. Three consecutive rounds that change source this way pause the run. Make the script stable before resuming, or move formatting to `before_check` for a new goal. As with ordinary verification, source snapshots exclude dependency, generated and runtime directories; they are not a filesystem sandbox.

Commands use the project's directory and the operating system's verification shell: `sh` on macOS/Linux, `cmd.exe` on Windows. Prefer portable script entry points such as `node scripts/gate.cjs`. For OS-specific commands, add `"platforms": ["windows"]`, `["linux"]`, or `["darwin"]`; omission means all platforms. Set `timeout_seconds` to 1–3600; zero or omission means 120 seconds. At most 20 uniquely named hooks can be configured.

Each command receives JSON on stdin:

```json
{
  "version": 1,
  "event": "after_check",
  "name": "lint-gate",
  "project": "/absolute/project/path",
  "run_id": "saved-run-or-check-id",
  "goal": "the goal and any user updates",
  "checks": [{"command": "npm test", "exit_code": 0, "output": "test output"}]
}
```

`goal` is omitted for standalone checks. `checks` is empty before verification. `BUILDHEADWAY_PROJECT`, `BUILDHEADWAY_HOOK_EVENT`, and `CI=1` are also set. Exit zero passes; any nonzero exit fails; timeouts record exit 124. Hook output and status appear in run reports and JSON exports, with full logs under the run directory. Hooks run under the current user's account, with the same environment as the project's verification commands.

## Design references

This implementation draws on Pi's reusable prompt commands and steering model, and Claude Code's explicit skills and deterministic hooks. It implements BuildHeadway's own formats and harness boundaries, not a drop-in plugin compatibility layer.

- [Pi prompt templates](https://pi.dev/docs/latest/prompt-templates)
- [Pi terminal usage](https://pi.dev/docs/latest/usage)
- [Claude Code skills](https://code.claude.com/docs/en/skills)
- [Claude Code hooks](https://code.claude.com/docs/en/hooks-guide)

Research checked on 22 October 2026.

## Requirement coverage

Version 0.11 adds the optional `acceptance` array to project settings. Each item contains a `criterion` string and non-empty `checks` array. Its commands become pinned required checks. Inspect coverage through `/acceptance` or `buildheadway acceptance --json`. See [HARNESS.md](HARNESS.md) for examples and how planner-generated mappings accumulate.

All root `AGENTS.md`, `CLAUDE.md` and `BUILDHEADWAY.md` files are included in shared context once. The existing containment and size limits apply to these files too.
