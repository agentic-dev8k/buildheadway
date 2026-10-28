# Changelog

## 0.26.0 — 2026-10-28

- Added searchable archived-goal browsing with `queue archives` and `/queue archives [SEARCH]`. CLI pages contain up to 100 bounded goal summaries, sorted by ID, with stable `--after` continuation and case-insensitive ID/status/goal filtering.
- Archive scans stream directory entries, retain at most one page plus a continuation candidate, and respond to cancellation/timeouts. Malformed records fail clearly; symlink/non-record entries are excluded and full archived evidence stays unchanged.
- Added reader validation for goal metadata and discovery hints after pruning. Regression coverage exercises multi-batch ordering/pagination, Unicode summaries, case-insensitive search, cancellation, malformed data, read-only behavior, in-app commands and the actual installed CLI.
- This release also includes the previously local 0.23–0.25 task evidence, log paging and queue archive improvements. Existing verification, price/budget, blocker and senior-approval gates remain intact.

## 0.25.0 — 2026-10-28

- Added `queue prune` and `/queue prune` to recover queue capacity by archiving completed/canceled entries before removing them from the active queue. Pending, running and blocked goals retain their order, identity and checkpoint bytes.
- Added terminal/JSON and in-app archive inspection by queue ID. Independent version-1 archives retain full goal, mode, outcome, timestamps, errors and checkpoint references; each is limited to 1 MiB.
- Interrupted pruning can reuse byte-identical per-goal archives. Conflicts, malformed data, invalid IDs, symlinks and oversized records fail without dropping queue entries. Run checkpoints and existing harness gates remain unchanged.

## 0.24.0 — 2026-10-28

- Added bounded byte-range log inspection with `logs --offset N`, continuation offsets and explicit remaining-output metadata. Default tail inspection is preserved; named-log containment, regular-file checks and the 1 MiB read cap apply to every page.
- Historical check and hook prompt excerpts retain the beginning and end of captured diagnostics with valid UTF-8 and preserved failure, duration, source-change and log metadata. Saved outputs and acceptance gates remain unchanged.
- Added paging, EOF, invalid-range, containment, read-only evidence and installed-launcher regressions. Checkpoint remains v12; full logs and original check/hook records are retained.

## 0.23.0 — 2026-10-27

- Retained the beginning and end of historical task instructions, reports and errors in bounded prompt excerpts, with explicit truncation flags and indexed references to complete saved evidence. Historical task state, attempts and model identity remain visible without modifying the durable ledger.
- Added `buildheadway tasks --task KEY [RUN_ID]` for full, read-only terminal/JSON inspection of one saved task. Task lists now show durable keys so repeated model task IDs remain distinguishable.
- Added regression coverage for real planning/worker prompt construction, Unicode boundaries, exact small values, empty windows, full checkpoint preservation and installed-package task inspection. Checkpoint v12 and all existing harness gates remain unchanged.

## 0.22.0 — 2026-10-27

- Added project MCP configuration and terminal add/list/check/enable/disable/remove commands. Connection diagnostics use the official Go MCP SDK with real stdio/Streamable HTTP initialization and tool discovery, cancellation, bounded responses and credential-safe errors. Diagnostics do not invoke tools or models.
- Forwarded configured MCP tools to full-access Codex, Claude and compatible OpenCode seniors through invocation-scoped settings. Free-worker model pins and plugin/delegation restrictions remain; restricted consultations receive no configured MCP tools. Unsupported senior adapters fail clearly. Setup failures remain blockers even when cancellation arrives, and do not trigger model fallback.
- Added `extensions init` and `extensions list`, project/personal skill and prompt starters, and `/extensions` and `/mcp` inspection. Starters preserve existing files and reject symlink directories. Existing workflow expansion, hooks and harness gates remain in use.
- Pinned server configurations with saved goals, retained other checks/budgets/acceptance settings during edits, and stored credential variable names rather than expanded values. Added real subprocess/HTTP/installed-package regressions and a packaged MCP guide.
- Source builds now require Go 1.25+ for the pinned MCP SDK; installed packages still need no compiler and retain all six native targets. Checkpoint remains v12 with additive optional configuration.

## 0.21.0 — 2026-10-27

- Added `buildheadway pause [RUN_ID]`, with project selection and JSON output, to save a pause request from another terminal without model access or overwriting the active checkpoint. Repeated and concurrent requests coalesce; completed goals refuse requests.
- Active goals cancel attached calls, verification and retry waits, retain partial edits and execution evidence, and save a paused checkpoint. Explicit resume acknowledges the old request, rechecks interrupted work and obtains fresh senior review. Queues hold later goals and resume the same saved run.
- Serialized pause requests with final completion and retained authentication/quota, policy, reported-cost and output-limit blockers when cancellation races with a provider failure. Invalid bounded control files stop safely rather than being ignored.
- Added real subprocess and installed-CLI pause/resume coverage, interrupted verification recovery, concurrent-writer and per-goal isolation regressions, queue recovery, malformed-control handling and provider-blocker races. Checkpoint v12, pricing, model selection, budgets and acceptance gates remain intact.

## 0.20.0 — 2026-10-27

- Reorganized detailed guides under `docs/`, with a documentation index, a concise root README and contributor instructions. Preserved installation, harness, extension, identity and historical validation material. Packages and source archives retain the guides and approved artwork.
- Added local file/heading link checks and installed-package regressions that reject missing documentation. Release packaging and native CI run these checks.
- Retained executed verification evidence on cancellation, including partial output, the exact log reference, elapsed time and a structured failure cause. Commands and hooks distinguish child exit failures, command timeouts, upstream cancellation and execution/setup failures.
- Interrupted goals save failed evidence and rerun verification before obtaining fresh senior approval on resume. Acceptance, hook and completion gates reject execution failures even if a malformed checkpoint says exit zero. Existing budget, price, source-consistency and senior-review rules remain enforced.
- Kept checkpoint v12: new execution fields are additive and old exit results remain readable without inventing durations or past failure classifications.

## 0.19.2 — 2026-10-26

- Preserved native Windows command-shell quoting for executable paths, argument quotes, redirection and conditions. Corrected checks still require actual success; failed checks remain failures. Added native Windows subprocess regressions.
- Doctor now executes isolated passing and failing quoted checks before reporting the verification shell as usable, with cancellation and bounded process timeouts. It leaves project files and saved settings unchanged.
- Made native shell fixtures explicit about CRLF and LF contracts, and normalized host path separators in cross-OS discovery assertions. Windows worker fixtures verify exact content through the native PowerShell reader.
- Fixed the agent-discovery regression assertion on case-insensitive filesystems: Windows shim lookup must resolve to the same file, even when the returned path uses different casing. The test still rejects Windows-only shims as Linux executables.
- Removed the Windows installer dependency on the Get-FileHash utility cmdlet: .NET streams the binary through SHA-256, and native integration coverage shadows the old cmdlet while still rejecting a tampered binary.
- Added per-branch CI concurrency so a newer verification run cancels superseded checks while the current source still receives the complete native matrix.
- Retained the approved artwork, source/issue links, compatibility settings and existing runtime harness gates.

## 0.19.1 — 2026-10-26

- Integrated the approved BuildHeadway logo and square icon, added branding documentation and displayed the full logo in the README.
- Included the artwork in native/source ZIP and npm packages. Package checks reject missing or invalid brand images; installed-package coverage verifies that the exact approved files survive npm packing and installation.
- Added a brand-asset index and public repository links; npm metadata identifies the source and issue tracker.
- This is a branding and packaging patch; coding-agent runtime behavior is unchanged apart from the release version.


## 0.19.0 — 2026-10-26

- Isolated the native installation receipt from Forge/Codentrail receipts; a real install/uninstall regression reproduced the overwrite before the fix and now requires old receipts and executables to stay unchanged.
- Selected BuildHeadway as the product name. Updated the terminal command, package, native executables, installers, module, release metadata and current documentation.
- Preserved Forge and Codentrail user directories, project configuration/guidance and environment aliases. New BuildHeadway values take precedence; saved goals, queues, locks and evidence continue using .forge storage without being rewritten. Added compatibility and precedence regression coverage.


## 0.18.0 — 2026-10-26

- Renamed Forge to Codentrail: terminal command, npm launcher/package, native binaries, installers, module, documentation and release assets.
- Preserved existing project checkpoints and queues in `.forge/`, legacy configuration and guidance, old environment variable aliases and existing user settings/data directories. Added rename compatibility coverage.


## 0.17.0 — 2026-10-26

- Replaced full nested decisions in historical discussion context with bounded text/summary excerpts, explicit truncation, route/thread/sequence identifiers, approval/veto flags and saved-state references. Current council decisions and active routed answers remain complete.
- Bounded the six retained planning-history summaries without changing saved history, discussion, reports or completion gates. Added regressions that reproduced multi-megabyte prompt duplication before the fix, plus Unicode, empty-window, saved-evidence and active-context retention coverage.
- Checkpoint v12, installation commands, fresh zero-price dispatch, explicit model choices, budgets, hard blockers, unchanged-source verification and unanimous senior approval remain unchanged.

## 0.16.0 — 2026-10-26

- Added bounded, prompt-only worker evidence for senior reviews and team consultations. Every worker's task, model, success/failure, communication status and saved-result reference remain present; report excerpts keep both their beginning and end instead of only a tail.
- Bound worker handoff reports in consultation and continuation prompts without mutating saved sessions, routed replies, reports or approval evidence. Truncation is explicit and full worker evidence remains in the saved run's state.json.
- Kept checkpoint v12 and all existing free-price, blocker, budget, verification and senior-approval gates unchanged. Added whole-batch, Unicode boundary, prompt integration and durable-report regression coverage.

## 0.15.0 — 2026-10-25

- Fingerprinted source before and after every verification command and each `after_check` hook. A source-changing command stops the round immediately, so a later command cannot restore old files and conceal checks run against different source versions.
- Saved `source_changed` on invalidated check/hook results while preserving actual exit codes and full log references. Acceptance, hook gates, senior completion, status and exported reports reject or identify invalidated evidence even when the command exited zero.
- Retained full-round retries, the three-unstable-round pause, fresh verification/review on resume and allowed `before_check` formatters. Checkpoint v12 preserves invalidation through restart and migrates previous records without inventing historical results.
- Added regressions that first reproduced both check-restoration and hook-restoration false approvals in 0.14.0, plus concurrent edits, formatter baselines, actual exit-code retention, pause/resume and installed-package validation. Native Windows package tests now permit the safe log filenames used by their own inspection assertions.

## 0.14.0 — 2026-10-25

- Added `forge logs`, `/logs`, JSON inspection, saved-goal selection and last standalone-check inspection. Bounded tail reads preserve full on-disk logs and reject traversal, symlinks and non-log paths.
- Checks and verification hooks retain their exact log filename and mark shortened output. Acceptance JSON, exports and agent handoffs preserve these references; senior, worker and consultation contexts include the log directory for focused diagnosis.
- Bounded hook excerpts and Git evidence capture. Truncated or incomplete Git inspection is explicitly identified instead of looking complete.
- Checkpoint v11 preserves log evidence across resume and migrates older runs without inventing missing references. Existing zero-price gates, blockers, budgets, acceptance checks, snapshot invalidation and senior approval remain enforced.
- Added real subprocess, restart, read-only inspection, containment, large-diff, long-run listing and installed-package coverage.

## 0.13.0 — 2026-10-25

- Bounded subprocess output capture to 8 MiB while retaining complete logs on disk, preventing large command logs from being loaded wholesale into memory.
- Oversized model output and free-model catalogs now block instead of parsing truncated evidence. Truncated replies cannot hide earlier cost events or approve completion, and do not trigger automatic model failover.
- Checkpoint v10 records output-limit blockers and migrates older checkpoints.
- Bounded Codex last-message files as well as streamed output. Preserved command exit codes, cancellation and timeout handling.
- Added native subprocess regressions for large logs, exact capture boundaries, missing log access, hidden early charges and output-limit routing blockers.

## 0.12.0 — 2026-10-24

- Saved per-model health across tasks and resume, with separate planner/worker observations and increasing cooldowns capped at five minutes.
- Free routing prefers ready, healthier models and refreshes zero-price verification before every dispatch, fallback and retry. Explicit choices remain pinned; authorization, quota, policy and cost errors remain immediate blockers.
- Added interruptible cooldown waits, live steering during retries, and prompt call/runtime-budget handling. Retry context remains bounded.
- Workers that fail after source edits return partial work to verification and senior review before further implementation.
- Added `forge routing [ID]`, JSON output, `/routing`, active-wait status and routing history in run reports. Usable responses are distinguished from verified project correctness.
- Checkpoint v9 retains routing history while preserving older-run migration and read-only inspection. Added routing, restart, pricing, blocker, budget and partial-edit regression coverage.
- Retained Forge as the temporary name; naming and GitHub publication remain deferred.

## 0.11.0 — 2026-10-23

- Native installer scripts support verified per-platform GitHub release downloads when the actual repository is configured, alongside existing offline installation. The release builder emits the individual binaries, manifest and installers.
- Added `forge update` for native installations, pinned versions, offline release folders, preservation of user settings, and running-executable replacement on Windows. npm-owned installations receive npm update instructions.
- Expanded `forge doctor` and `/doctor` with read-only installation, command resolution, receipt checksum, configuration, guidance, shell and agent diagnostics; JSON output and diagnostic exit status are supported.
- Added durable acceptance mappings, project-pinned requirements, actual executed evidence, and `forge acceptance` / `/acceptance`. Missing, failed or stale coverage prevents completion even when a senior votes done.
- Source changes anywhere during verification invalidate earlier results and rerun the whole round.
- Root AGENTS.md and CLAUDE.md are automatically included with FORGE.md for every agent role.
- Checkpoint format v8 preserves acceptance data and migrates previous runs on resume. Added installation, harness and naming documentation.

## 0.10.0

- Added built-in review, debug, test and documentation workflows, project/personal Markdown prompt templates, and explicitly invoked skills with supporting directories. Expanded instructions are saved with the goal.
- Added workflow listing, source inspection, CLI preview, slash shortcuts, safe text-only argument substitution, deterministic precedence, and bounded file discovery.
- Added durable live steering from the terminal UI or a second CLI, delivery inspection, concurrent-writer locking, and applied-update reports. Direction changes discard stale tasks, conversations and approvals before more implementation.
- Added configured before_check and after_check hooks with platform filters, JSON stdin, timeouts, logs, pinned configuration, and repair evidence. Failed or missing gates block completion. Source changes after checks trigger re-verification, with a pause for repeatedly unstable hooks.
- Standalone verification and run exports include hook outcomes. Existing checkpoints migrate to format 7.
- Added regression coverage for mid-worker and final-review steering, concurrent messages, workflow precedence and persistence, hook failures and timeouts, interrupted hook recovery, source-changing hooks, installed CLI features, and a real terminal workflow/steering/hook test with simulated agent subprocesses.

## 0.9.0

- Plain terminal goals, `/goal`, and new queued goals now start the autonomous senior-team workflow by default. Explicit `forge run` scripts retain their single-planner behavior.
- Added real routed questions, challenges, and suggestions between named seniors, the senior team, free coding peers, and implementation workers. Recipients can consult another teammate before replying.
- Workers can return a saved handoff, receive actual answers, and continue the same assignment. Requests to replan return to the team before dependent tasks run.
- Added durable nested-discussion and worker-session checkpoints, reply references, and source-change invalidation of saved advice. Interrupted implementation still requires verification.
- Bounded circular discussion with eight routed advice calls per discussion and eight worker handoffs per assignment, followed by concrete investigation or replanning. Existing goal budgets, free-price checks, passing-check gates, and unanimous review remain enforced.
- Added multi-party routing, consultation resume, stale-advice, replanning, pricing, protocol, default-mode, native adapter, and opt-in live worker-conversation tests.

## 0.8.0

- Added `forge figureout` and `/figureout`: autonomous senior proposals, peer critiques, lead synthesis, free-worker handoffs, checks, and unanimous completion review.
- Added `attach`, `detach`, `team`, and `discussion` commands, named senior connections, Codex reasoning settings, structured discussion events, and conversation sections in run reports.
- Figureout pins its team, worker connection, and access mode to each goal. Partial planning survives interruption; source changes invalidate votes, and resumed reviews rerun checks.
- Full-access execution uses the native unattended modes of Codex, Claude, OpenCode, Pi, Cline, and Gemini; optional workspace mode retains ordinary adapter controls. Added a Gemini headless adapter and error parsing.
- All senior calls count against configured budgets. Figureout continues stalled repairs by default; explicit limits, missing credentials, provider policies, and pricing failures remain real stop conditions.
- Persistent queues retain figureout mode. Fresh headless setup detects an authenticated installed Codex/Claude or prepares free OpenCode planning automatically.
- Added council veto, repair, restart, source-change, budget, free-pricing, team, queue, native subprocess adapter, and opt-in live council tests.

## 0.7.0

- Project configuration, FORGE.md, scoped context files, and persistent project notes.
- Plan-only goals that resume into their saved implementation tasks.
- Durable ordered goal queues with crash recovery, cancellation, reordering, and failure holds.
- Model-independent verification, Git change inspection, and Markdown/JSON run exports.
- Worker-call and cumulative runtime budgets, per-command timeouts, searchable history, and JSON event streaming.
- Additional npm lint/typecheck, Maven, and common pytest verification discovery.
- Native terminal commands and installed-package regression coverage for the new workflows.
- Release ZIP publication now validates a temporary archive before atomic replacement.

## 0.6.0

- Failed workers return the batch to review before dependent tasks run; invalid check replacements are sent back to the planner for automatic correction.
- Durable task ledger and per-attempt checkpoints preserve implementation progress across repair cycles, interruption, and process crashes.
- Resume rechecks the actual files and reviews uncertain partial work before assigning workers. Specific run IDs can be resumed, including older sessions.
- Verification commands and success criteria survive replanning. New project manifests are rediscovered, source changes during review invalidate old evidence, and only explicitly unavailable suggested commands may be replaced.
- Repeated verification rounds with unchanged source and check outcomes trigger a saved pause. The planner and workers receive recent failures and task history to improve the next repair.
- Added `/status`, `/tasks`, `/history`, `/events`, `/queue`, `/resume ID`, live cycle/task progress, and corresponding inspection CLI commands with JSON output.
- Queued goals wait after interruptions or errors. Legacy checkpoints migrate, malformed counters and paths are rejected, and state is reloaded after acquiring the repository lock.
- Added real process-crash recovery, stale-evidence, check-retention, no-progress, state-validation, UI-queue, and installed-package inspection tests.

Installation remains a terminal command: `npm install -g ./forge-coding-agent-0.6.0.tgz`, then `forge`. Native Windows/macOS execution and public registry publication are recorded separately in VALIDATION.md.

## 0.5.0

- Install from a terminal with npm; run `forge` from a project on Windows, macOS, or Linux.
- The npm package includes all six native builds, selects the host platform automatically, verifies its checksum, and preserves the terminal, arguments, exit code, and working directory.
- Installing needs no lifecycle scripts, compiler, or additional binary downloads. Package removal and upgrades preserve user settings and checkpoints.
- Removed the double-click installers and launcher files from the release. Standalone native installation uses terminal shell commands.
- Native CI now covers real npm installation, launch, offline repair verification, upgrade, tamper rejection, and removal.
- The terminal chat, installed-agent picker, native sign-in, premium planning, free implementation workers, autonomous repair loop, and resume behavior remain inside Forge.

The npm release package is built locally; public registry publication requires a maintainer account. Native Windows/macOS execution remains pending unless recorded in VALIDATION.md.

## 0.4.0

- Child agents use the project you selected, including when the parent terminal has a different working directory.
- OpenCode receives an explicit project path. Planner evidence includes the actual OS and verification shell, and private Git installations are available to repository inspection.
- Automatic free planning can recover from incomplete replies or unavailable models, retain a working model for review, and stop if a charge is reported.
- Valid planner JSON can be read from a Markdown fence or surrounding explanation. Type and completion checks remain strict.
- The OpenCode planning agent avoids native plan-file workflows and can repeat checks already executed by Forge. File-edit tools and other commands remain subject to approval, with automatic approval disabled for the planner.
- Fresh Windows setup detects Git Bash and can install a verified official portable build privately for Intel/AMD or ARM64.
- Added a real free-service goal test, selected-project regression coverage, free-planner recovery checks, and a native Windows runtime bootstrap CI check.

Native Windows/macOS execution and signing must be verified separately from cross-compilation. The included validation report records what was actually executed.
