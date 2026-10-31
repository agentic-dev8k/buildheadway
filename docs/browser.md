# Browser tools and UI acceptance

BuildHeadway manages an isolated headless Chromium browser through the official [Playwright MCP server](https://github.com/microsoft/playwright-mcp). Supported full-access Codex, Claude and compatible OpenCode seniors receive navigation, clicks, typing, screenshots and browser-coordinate tools. This is browser automation. Native desktop mouse/keyboard control and Pi/Cline/Gemini forwarding are not provided.

## Enable the browser

In a Windows, macOS or Linux project terminal:

```sh
buildheadway browser enable
buildheadway browser check
buildheadway figureout "Build and exercise a task tracker in the browser"
```

`enable` installs pinned `@playwright/mcp@0.0.83` and its Chromium into BuildHeadway's private data directory, then saves a portable MCP profile. It reuses suitable Node.js/npm or installs a private runtime. No global browser package or compiler is needed. First setup needs network access, disk space and an upstream-supported OS. On Linux, required libraries and sandbox support must be available; BuildHeadway does not invoke sudo or silently disable the sandbox. See official [Playwright system dependencies](https://playwright.dev/docs/browsers#install-system-dependencies).

To select an installed compatible Chromium executable, use `browser enable --executable /absolute/path/to/chromium`; quote Windows paths with spaces. Selected executable and managed critical-file hashes detect subsequent changes and require setup again. They do not authenticate an executable you select or cover files behind a wrapper. npm retains integrity metadata and upstream licenses.

A clean stdio child excludes inherited Playwright CDP/config overrides. It uses headless, isolated, sandboxed browsing and vision tools, without a personal login profile or listening MCP endpoint. Workers and restricted consultations retain their tool limits. New goals pin profiles and plans; configuration edits do not retrofit saved goals.

`browser check` types into an owned diagnostic page, clicks, asserts a result and captures an image under `.forge/browser-diagnostics/`. This tests the browser installation, not your app. The harness rejects obvious smoke-diagnostic commands as application acceptance. It is a heuristic, like existing echo/true exclusions, not shell analysis.

## Require application checks

Save `task-flow.json`, adapting the command, port and unique CSS selectors:

```json
{
  "name": "task-flow",
  "url": "http://127.0.0.1:3000",
  "start_command": "npm run dev -- --host 127.0.0.1",
  "timeout_seconds": 90,
  "steps": [
    {"action": "fill", "selector": "#new-task", "value": "Ship the app"},
    {"action": "click", "selector": "#add-task"},
    {"action": "expect_text", "selector": "#task-list", "value": "Ship the app"}
  ]
}
```

```sh
buildheadway browser add-check --file task-flow.json
buildheadway check
```

This adds mandatory `@browser:task-flow` for new goals. Normal verification and resume execute pinned plans under the existing source snapshots and hook boundaries. An acceptance mapping may reference that exact label: `{"criterion":"A task can be added","checks":["@browser:task-flow"]}`. A planner's statement that it tested the UI cannot replace execution.

For an app being scaffolded during a goal, a senior can create a plan inside the project and add `buildheadway browser verify --file task-flow.json` to executable acceptance checks. It runs one plan without changing settings or the active goal, under its own source snapshots. It does not rediscover package tests, so it can run inside `npm test`. Evidence is saved under `.forge/browser-checks/`; terminal and JSON output locate the report. Keep the plan inside the checked source. Enabling browser tools does not automatically guess which app behavior constitutes acceptance; a plan or an application verification command makes behavior mandatory.

With `start_command`, the harness refuses an occupied port, starts its own foreground process, waits for an HTTP response and terminates that process tree on success, failure or cancellation. Omit the command to explicitly use an existing app; that server is not terminated. Bind the command to the configured loopback port. Server stdout/stderr is discarded; debug the command directly if readiness fails. Commands must remain in the foreground.

Up to eight plans are supported, each with 1–64 steps ending in `expect_text` or `expect_visible`. `expect_text` requires one visible matching element containing expected rendered text. Actions are `fill`, `click`, `press` (key in `value`), `expect_text` and `expect_visible`. A fill step can use `value_env` instead of `value`; the nonempty value is resolved only at execution, never copied into settings, checkpoints or action logs. Screenshots show visible application content, so choose fixtures accordingly.

The 60-second default total deadline can be set to 1–300 seconds, further capped by the project's check timeout and enclosing goal deadline. It includes readiness, startup, actions, assertions and screenshot capture. Check timeouts remain distinct from cancellation and the goal deadline.

## Evidence and limits

Passing requires executed actions, a positive assertion and a fully decoded valid PNG. Results retain completed step/assertion counts, provider version, image path relative to the run/check directory, size, SHA256, duration and failure details. Logs omit raw responses, snapshots, selectors, input values and URLs. Screenshots are capped at 8 MiB, 8192 pixels per dimension and 16 megapixels; harness MCP messages at 1 MiB. Attached agents apply their own MCP response limits. Provider output uses a 32 MiB eviction threshold, not a hard disk quota. Explicit image files also receive the harness checks; a valid PNG header alone does not pass.

For required `@browser:NAME` checks, the goal harness reopens each saved image before and after senior review and compares its complete PNG, SHA256, byte count and dimensions. An image deleted or changed by a hook, reviewer or another process invalidates acceptance even when source files are unchanged. All checks execute again and seniors must approve the new evidence. Three invalidated rounds in one invocation pause for diagnosis; stop the writer or fix the check, then resume. Existing runtime/call budgets, pause requests and provider blockers still stop work. The harness does not dispatch another implementation worker merely to recover a changed image.

Inspect the actual saved files from any project terminal:

```sh
buildheadway browser evidence
buildheadway browser evidence --run RUN_ID --json
buildheadway browser evidence --last-check
buildheadway browser evidence --report BROWSER_VERIFY_REPORT_ID
```

`/browser evidence` supports the same selectors inside the app, including while agents are running. The default selects the latest goal. `--last-check` selects the latest standalone `check`; `--report` selects a report from `.forge/browser-checks/`, not the diagnostic namespace. Select one scope at a time. No browser runtime, connection or model is needed.

Results show the recorded outcome separately from current image integrity (`valid`, `invalid`, `partial` or `not_recorded`). Valid images do not prove that the application, source or tests are currently correct. Missing images after a failed check are reported as not recorded. Invalid recorded images produce a nonzero CLI exit after printing the report. Inspection reads at most 32 MiB of saved JSON and 4096 check records, then applies the existing image bounds; oversized records fail with a diagnostic. Reads are contained to the project and saved evidence directory; symlink records and images are rejected. Files can change between inspection calls.

`status`, `acceptance`, `report` and `export` also project current goal screenshot integrity. They preserve the historical outcome and original execution fields while marking invalidated checks unverified with `evidence_error`. Inspection never rewrites a checkpoint, approves a goal or reruns a test. Restoring a previously invalidated image alone cannot clear a saved integrity failure; execute checks again. These completion gates apply to screenshot records attached to the goal's browser checks; a separate shell command or nested `browser verify` report is not silently imported into a goal's evidence ledger.

Acceptance URLs use numeric loopback HTTP(S), such as `127.0.0.1` or `[::1]`. The top page's origin is checked before every action. This catches accidental navigation; it is not a security boundary for untrusted sites, redirects between checks or subresources. Senior tools retain full-access semantics and can navigate more broadly. Isolated profiles are not an OS sandbox for arbitrary application code.

Missing/corrupt runtime files, missing credential variables, unsupported adapters and MCP startup/initial navigation failures are blockers. A unique, bounded verification handoff also carries these blockers through nested shell/npm commands; a wrapper exit code cannot silently convert them into passing evidence. They stop without model fallback or dropping requirements, including a pause racing with recording. Initial navigation is treated conservatively as a blocker because browser readiness has not been established. Later action/assertion failures remain failed checks for repair. Resume reruns checks before fresh senior approval. Setup, tool listings and screenshots alone cannot authorize completion.

`browser status --json` and `/browser` inspect without model calls or installation. `browser disable` and generic MCP disable/remove refuse to break required plans. Remove plans explicitly with `browser remove-check NAME`; acceptance mappings and other references must also be updated deliberately. Saved goals retain their pinned settings.

Browser components are installed on demand, not included in the main npm tarball. See [MCP connections](mcp.md), [harness controls](harness.md), and [installation](install.md).

## Runtime compatibility in 0.33

The 0.30–0.32 unpublished Go source was lost from scratch; 0.33 reconstructs its documented behavior on the existing public source. Project browser profiles, plans and checkpoint v12 remain supported. A private runtime receipt from an older installer may be rejected by the new integrity checks; run `buildheadway browser setup` to install a fresh pinned runtime without changing the project profile or its required checks. This is an explicit setup blocker and never model fallback.

The pinned provider calls its arbitrary server-process JavaScript tool `browser_run_code_unsafe`. Full-access seniors receive the upstream tool set; the acceptance harness uses only fixed, JSON-escaped templates for configured actions. Browser automation is not a restriction on an authorized full-access senior’s local tools.
