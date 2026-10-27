# BuildHeadway 0.20.0 documentation and verification validation — 2026-10-27

- Reproduced the interrupted-check bug before the fix with a real helper subprocess: CheckProject returned interrupted with an empty results array after the child printed diagnostics. The regression now requires saved partial output, a log reference, cancellation classification, nonzero harness outcome and elapsed time.
- New regressions execute real command timeouts, child exit 7, setup/log creation failures, interrupted hooks and interrupted goals. Resume must rerun the check and receive fresh senior approval; saved execution failures cannot pass completion, acceptance or hook gates by claiming exit zero. Simulated seniors are used for review; there is no authenticated model inference in these tests.
- The full Linux x64 Go race-enabled suite passed in 105.211 seconds (110.464 seconds including command overhead), using go test -p 2 -buildvcs=false -race -timeout=300s ./.... Go vet passed. Documentation and npm launcher checks passed; the terminal conversation integration passed in 1.546 seconds with simulated agent subprocesses and actual Node.js checks.
- The root README now focuses on installation and first use. Guides, branding, identity and historical validation records moved under docs without discarding them. Local file/heading links are checked, and installed-package coverage requires the exact guide bytes and rejects missing documentation. Packaging records its own six application builds, native self-test, installer and npm results in VALIDATION.md and validation.json. --skip-tests explicitly reuses the source suite recorded above.
- Native Windows/macOS CI for this source is checked after repository update. Cross-compilation alone is not a native-runtime result. The final package report records the actual CI outcome. No npm publication, deployment, new repository, authenticated inference, code signing or notarization is claimed.
- Continuation: use the latest public agentic-dev8k/buildheadway source; do not overwrite it with an older saved bundle. The documentation index is docs/README.md, harness instructions are docs/harness.md and continuation/validation records are this file. Checkpoint remains v12, with additive execution metadata. Preserve prior-brand settings, saved .forge goals, approved artwork and all budget/pricing/blocker/verification/senior gates.

# BuildHeadway 0.19.2 native harness validation — 2026-10-26

- Final repaired commit d640ed9870345afbe3e48da2708583952936b0d9 passed native Windows, macOS and Linux race/package checks plus the six-platform packaging job in https://github.com/agentic-dev8k/buildheadway/actions/runs/40380592954. Those are 0.19.2 results, not evidence for later runtime changes.

- The first public native CI run, https://github.com/agentic-dev8k/buildheadway/actions/runs/40368887903, passed Linux but failed Windows verification and a macOS agent-discovery assertion. The Windows resume fixture eventually reached the default ten-minute Go test timeout after repeated failed checks. Those failures are retained as evidence; native compatibility was not inferred from cross-compilation.
- Windows shell commands now preserve quoted checks through cmd.exe's own command-line parsing. Added native regressions for quoted file checks, actual failure propagation, a helper executable in a path with spaces, and arguments containing spaces, ampersands, an empty argument and a trailing backslash. A present-but-incompatible shell that masks failed checks must fail doctor diagnostics.
- Doctor executes positive and negative quoted-command probes in a removed temporary directory, with cancellation and three-second subprocess limits. Existing user-data preservation coverage remains in place. The macOS discovery regression now requires actual file identity rather than an exact casing spelling.
- Focused Linux race-enabled shell/doctor/discovery tests passed in 1.095 seconds. The full Linux x64 race suite passed in 113.268 seconds (114.993 seconds including the command), and go vet passed. Windows amd64 and macOS arm64 test executables cross-compiled after the repair; cross-compilation is not native execution. The POSIX terminal conversation workflow passed in 2.066 seconds using simulated agents and actual Node.js checks. An initial candidate built all six application targets and passed the native Linux self-test, installer checks with simulated HTTPS transport, and both real npm installation/package tests (26.441 seconds for npm tests). The packager explicitly reused the completed source suite through --skip-tests; it did not rerun Go tests. Subsequent native failures are recorded below; the final build records its own package and native-CI evidence.
- The next repaired native run, https://github.com/agentic-dev8k/buildheadway/actions/runs/40377440158, passed Windows quoted-executable/argument handling but exposed FINDSTR CRLF assumptions in fixtures and a Unix-separator assertion on Windows. The doctor probe now uses Windows text line endings; the shared worker check uses PowerShell to require the exact LF-only file bytes rather than relaxing content matching. Native CI is rerun after these corrections.
- Native run https://github.com/agentic-dev8k/buildheadway/actions/runs/40378554178 passed Linux and macOS. Windows completed the shell/argument/doctor regressions, but its full suite found Get-FileHash unavailable in the bundled-installer PowerShell session. The installer now streams SHA-256 through .NET; the native integration fixture deliberately shadows the utility cmdlet and still requires a corrupted binary to be rejected. The follow-up native matrix, including the installer regression, must pass before Windows validation is claimed; the final build report records that outcome.
- Native CI front-loads shell and discovery regressions, bounds its full test command to five minutes, and cancels superseded runs on the same ref. No model calls are part of the ordinary suite and no new authenticated live-model inference is claimed.

# BuildHeadway 0.19.1 branding validation — 2026-10-26

- Added the approved transparent logo, square icon and white-background logo under assets/brand. README and BRANDING.md reference local included files. Original image bytes are preserved.
- Packaging copies the three approved assets to the native distribution and source archive. npm's file list includes them and BRANDING.md. Installed-package coverage compares the exact image bytes against source, checks the README image reference, and confirms that missing/invalid branding fails the packaging check before restoring the asset.
- Runtime behavior is unchanged except the version constant. This branding patch does not rerun the complete Go race suite; the completed 0.19.0 runtime checks remain recorded below. The release builder runs six application builds, the native Linux self-test, installer fixture checks and actual npm install/launch/upgrade/integrity/uninstall checks. Its --skip-tests flag is explicit, not a claim that the Go suite ran again.
- No authenticated model inference, hosted native Windows/macOS runtime testing or npm publication is claimed for this patch. The public source repository is now [agentic-dev8k/buildheadway](https://github.com/agentic-dev8k/buildheadway); the retained workflows run native CI separately. Their results must be inspected before making a native CI success claim.
- Continuation: preserve the approved artwork and local README references in future releases. Keep the selected BuildHeadway name, earlier-brand compatibility and all existing harness gates. Fetch the latest repository before continuing; an older source bundle must not overwrite newer public commits.

# BuildHeadway 0.19.0 validation — 2026-10-26

- The user selected BuildHeadway, replacing the provisional Forge/Codentrail names. The command and npm package are `buildheadway`; distribution artifacts and current documentation use the selected name. No public npm publication is claimed.
- A real native install/uninstall regression first failed because the old installer receipt was overwritten. BuildHeadway now uses its own receipt filename and leaves older receipts and executables intact.
- New regression coverage exercises Codentrail/Forge environment aliases, explicit empty values, new-name precedence, all three project guidance/config names, malformed-new-config refusal, old config bytes staying intact, existing user directory precedence and no merging/deleting old directories. Existing harness regression coverage is retained.
- The initial full Linux x64 race-enabled Go suite passed in **99.185 seconds**. After the receipt fix, the final full race suite passed in **97.818 seconds**, and the focused receipt regression passed in **1.669 seconds**. Focused compatibility tests passed in **1.062 seconds**. Vet and the real POSIX terminal workflow passed. The latter used simulated Codex/OpenCode responses and actual Node.js checks; it exercised consultation, continuation, steering, hooks, acceptance, logs/routing, export and queue persistence with 10 senior calls and 3 worker calls. No new authenticated live-model inference was performed.
- Windows amd64 and macOS arm64 test executables cross-compiled successfully. They were not run natively. Packaging separately records six application builds, Linux self-test, installer transport fixtures with a real executable, and actual npm install/launch/upgrade/integrity/uninstall checks. --skip-tests reuses the completed source suite; the packaging test field means not rerun during packaging.
- GitHub's connector still returned no accessible repositories under agentic-dev8k and exposes no repository-creation tool. Manual sign-in was previously handed to the user; this iteration did not retry declined credentials or interfere with it. The intended destination is agentic-dev8k/buildheadway. No repository creation or push is claimed.
- Continuation: BuildHeadway is the selected name. Prefer the new buildheadway working source over older Forge/Codentrail folders. Checkpoint remains v12; preserve .forge saved runs and the earlier-name aliases along with pricing, explicit choices, blockers, budgets, verification and senior-approval gates. Actual Goal activation remains unverified; do not treat literal /goal text as activation.

# Codentrail 0.18.0 validation — 2026-10-26

- Renamed Forge to Codentrail with terminal command and npm package `codentrail`, coordinated native assets, launcher, installers and module path. No npm publication was performed.
- Final full Linux x64 race suite passed in **96.939 seconds**; the initial renamed suite passed in 99.715 seconds. Focused compatibility tests passed in **1.041 seconds**. Vet and Windows amd64/macOS arm64 test-executable compilation passed. Native execution on those operating systems is not claimed.
- Compatibility tests exercise FORGE_HOME fallback, explicit new-environment precedence, existing settings loading, legacy project config and guidance, new config precedence, retained .forge checkpoints, exact application-directory fallback and preserving old directories without moving or deleting data.
- The renamed executable passed the POSIX terminal workflow with simulated Codex/OpenCode replies and actual Node.js checks, including consultation, continuation, steering, hooks, acceptance, logs/routing, export and queues. Release packaging records six application builds, Linux self-test, installer transport fixtures and npm package checks separately; --skip-tests reuses the completed source suite.
- GitHub account access through the connector was verified. Repository creation is unavailable in that connector and no accessible repository was returned. Browser sign-in was declined, then handed to the user at their request. No repository or push is claimed. Do not retry automated credentials or disturb the manual sign-in. Resume the push after the user finishes and the repository is accessible.
- Continuation: Codentrail is the selected name. Keep .forge checkpoint storage, old aliases and all existing pricing, budget, blocker, verification and approval gates. Model inference tests remain simulated; no new authenticated live model calls occurred. Preserve future progress under this name.

# Release 0.17.0 validation — 2026-10-26

- Regression fixtures first reproduced oversized historical context against 0.16.0. They use a protocol-valid decision, a message below the existing 12,000-byte recording limit, and long retained planning summaries. The focused race-enabled tests passed in **2.170 seconds** after the implementation and final fixture refinement.
- Coverage checks historical sequence/reply/thread routing, explicit approval and veto flags, beginning/end diagnostics, Unicode validity, saved-state references, no mutation of full saved evidence, exact small messages, empty windows, and preservation of complete current council decisions and active routed answers.
- The full Linux x64 race-enabled Go suite passed in **98.560 seconds** and `go vet -buildvcs=false ./...` passed. The POSIX terminal workflow passed with simulated Codex/OpenCode model replies and actual Node.js checks: consultation, continuation, steering, hooks, acceptance, log/routing inspection, export and queue persistence. It used 10 senior calls and 3 worker calls.
- Windows amd64 and macOS arm64 test executables cross-compiled successfully; neither was run natively. Release packaging separately records all six application builds, Linux native self-test, installer checks with simulated HTTPS transport and a real executable, and actual npm launcher/install/upgrade/integrity/uninstall checks. Packaging uses --skip-tests to reuse the completed source suite; its test field means not rerun during packaging.
- No authenticated live model inference, native Windows/macOS/ARM execution, public publication or deployment was performed. Actual Codex Goal activation remains unverified: no hosted Goal lifecycle control was exposed and WINMIC was offline when checked this iteration.
- Continuation: checkpoint remains v12. Historical discussion and summaries are now bounded; current council decisions, active answers, requirements, task instructions and project context can still make the whole prompt large. Choose the next actual gap from current code. Preserve fresh price checks, explicit models, hard blockers, configured budgets, unchanged-source verification, requirement coverage and unanimous senior approval.

# Release 0.16.0 validation — 2026-10-26

- The full Linux x64 race-enabled Go suite passed in **96.886 seconds** after compilation; `go vet -buildvcs=false ./...` passed. Focused context-evidence tests passed with the race detector in **1.282 seconds**.
- New coverage checks a 30-worker batch, preservation of failure/model/status metadata, both beginning and ending diagnostics, valid Unicode boundaries, exact-size limits, saved-result references, independent handoff copies, and actual senior/consultation prompt construction. Preparing prompts leaves full saved worker reports unchanged.
- The real POSIX terminal workflow passed using simulated Codex/OpenCode replies and actual Node.js verification: team consultation, continuation, steering, hooks, acceptance, logs, routing, report export and queue persistence. It used 10 senior calls and 3 worker calls; original behavior tests remained unchanged.
- Windows amd64 and macOS arm64 test executables compiled successfully; they were not executed natively. Release packaging records the six application builds, native Linux self-test, installer and npm launcher/package checks separately. Packaging uses --skip-tests to reuse the passed source suite; its test field means not rerun during packaging.
- No authenticated live model inference, public publication or deployment was performed. Actual Codex Goal activation could not be verified: no hosted Goal control was exposed and the authorized WINMIC device remained offline. This does not block the completed hosted implementation and tests.
- Continuation: worker evidence is now bounded, but the entire prompt is not capped. Keep inspecting actual source for the next gap; preserve the full durable evidence and existing routing, pricing, budget, source-consistency and approval gates.

# Release 0.15.0 validation — 2026-10-25

- Two new real-subprocess regressions failed against 0.14.0 before the implementation: a check changed source and a later check restored it, and two after-check hooks did the same. The old harness returned a successful source snapshot in both cases. With the fix, the round stops at the first observed edit and saves invalidation evidence.
- The focused race-enabled regressions passed in **12.297 seconds**. They cover real check/hook edits, edits between commands, allowed before-check formatter changes, original exit-code retention, acceptance/hook refusal of invalidated evidence, checkpoint migration, three-round pausing without senior dispatch, and resume with fresh verification and approval.
- The complete Linux x64 race-enabled Go unit/integration suite passed in **97.640 seconds** after compilation. `go vet -buildvcs=false ./...` passed. No source-test failures remain.
- The real POSIX terminal workflow passed with simulated Codex/OpenCode subprocess replies and actual Node.js tests, including consultation, worker continuation, steering, hooks, acceptance evidence, logs, routing, export and queue persistence. Existing application tests remain unchanged.
- Windows amd64 and macOS arm64 test executables cross-compiled successfully. These were compile checks, not native runtime tests. The release builder separately records all six application binaries, native Linux self-test and installer verification, and actual npm launcher/install/log-inspection/upgrade/integrity/uninstall checks in `validation.json` and `VALIDATION.md`. Installed-package coverage also attempts the change/restore sequence and requires failure with `source_changed: true`, exit zero retained and the later command never executed.
- Packaging uses `--skip-tests` to reuse the completed source suite; its test field means not rerun during packaging. No new live premium or free-model inference, GitHub publication, npm publication or deployment is claimed. Native execution here is Linux x64; native Windows, macOS and ARM64 validation remains pending.

# Release 0.14.0 validation — 2026-10-25

- Linux x64: full race-enabled Go unit/integration suite passed in **102.781 seconds** after compilation. Final focused race-enabled log/evidence regressions passed in **4.975 seconds**, including the shorter display-excerpt refinement; `go vet -buildvcs=false ./...` passed.
- Real subprocess coverage confirms that check and hook logs retain more than 8 MiB on disk while saved output stays bounded and flagged. References survive checkpoint reload and acceptance summaries. Read-only log inspection is checked against unchanged checkpoint bytes, invalid paths, symlink escapes, invalid bounds, standalone checks and legacy v10 migration. New long-run listing and real large Git-diff tests passed with the race detector; the additional focused round took **1.459 seconds**.
- The POSIX terminal workflow passed with simulated senior/worker subprocess replies and actual Node.js behavior tests. It includes `/logs`, routed questions and replies, continuation, live steering, hooks, requirement coverage, routing inspection, export and a saved queue. The run used 10 senior calls and 3 worker calls; original behavior tests remained unchanged.
- npm launcher integrity tests passed (1 test, no failures). Release packaging separately records actual npm installation/launch/upgrade/log inspection/integrity/uninstall tests, native self-test, installer verification and all six platform builds in `validation.json` and `VALIDATION.md`. The packaging command reuses source tests with `--skip-tests`; its test field means not rerun during packaging, not a source-test failure.
- No new live model inference was performed. Native runtime execution here is Linux x64; Windows, macOS and ARM64 execution require native testing. Cross-compilation does not establish native behavior. No GitHub repository, public package publication, deployment or final name selection was performed.

# Release 0.13.0 validation — 2026-10-25

- Linux x64: the complete race-enabled Go unit/integration suite passed in **81.319 seconds** after compilation; `go vet -buildvcs=false ./...` passed.
- New real subprocess tests write more than 8 MiB, confirm full disk-log retention and bounded tail capture, reject oversized planner and worker replies containing a hidden early charge, and verify that output-limit errors block model failover.
- A real timed-out adapter emitting oversized output retains its output-limit blocker instead of being treated as an ordinary transient timeout. Exact-limit output and log-read failure boundaries are covered.
- The terminal workflow test uses simulated agent subprocesses and actual Node.js verification. Native package and platform-build results are recorded separately by the release builder.
- No new live premium or free-service inference was performed. Native execution is Linux x64 only; macOS/Windows and other architectures require native CI execution.

# Release 0.12.0 validation — 2026-10-24

- Linux x64: the complete Go unit/integration suite passed. The final race-enabled suite passed in **90.180 seconds** after compilation; `go vet -buildvcs=false ./...` passed.
- Routing coverage exercises failure memory across assignments and resume, cooldown expiration, explicit selection, fresh price verification before fallback, cancellation and steering during waits, exhausted call/runtime budgets, immediate authentication/quota/policy/cost blockers, bounded malformed planner replies, old-checkpoint migration, corrupt-health rejection, and read-only inspection.
- A worker failure after a real file edit returns to executed verification and unanimous senior review without dispatching a fallback worker. The fixture preserves the failed attempt and verifies the resulting file with an actual local command.
- The real POSIX terminal/PTY workflow passed using simulated Codex/OpenCode subprocess replies and actual Node.js tests. It exercised custom workflow discovery, live steering, an actual routed worker question and senior reply, continuation, hooks, acceptance evidence, `/routing`, report export, a saved queued goal, and clean shutdown. The run used **10 senior calls and 3 worker calls**, and the original tests stayed unchanged. Routing observations agree with the recorded worker-call ledger.
- This release made no new live model-inference calls. Deterministic agent fixtures validate controller behavior; they do not establish premium-account access or arbitrary application quality.
- Packaging, native self-test, installer/download verification and real npm package install/launch/upgrade/integrity/uninstall results are recorded separately by the release builder in `validation.json` and `VALIDATION.md`. Native runtime execution in this session is Linux x64; macOS, Windows and other architectures require their native CI runs.

# Release 0.11.0 validation — 2026-10-23

- Linux x64: Go unit/integration suite passed. The race-enabled suite passed in 46.572 seconds; `go vet -buildvcs=false ./...` passed.
- Acceptance regression coverage includes missing and unexecuted requirement coverage, failing checks, stale snapshots, retained project requirements, suggested-check replacement, checkpoint persistence, and automatic return to the senior team when coverage is missing.
- Verification consistency regression changes source after an initially passing command and requires the earlier evidence to be discarded.
- Native update integration installs and updates through the actual executable from an extracted release folder, preserves settings, checks the updated command, and rejects a corrupted update without damaging the installed app.
- POSIX internet-installer flow uses a simulated HTTPS transport and the real native executable. It verifies pinned release URLs, platform selection, manifest verification, paths with spaces, persistence of the update repository, rejection of corrupted downloads, and refusal to trust cwd assets when piped.
- Real terminal/PTY integration uses simulated Codex/OpenCode subprocess replies and actual Node.js behavior tests. It exercises a custom workflow, live steering, worker-to-senior questions and replies, continuation, hooks, acceptance display and JSON evidence, report export, queueing and clean exit. No new live-provider intelligence or quota claim is made by this fixture.
- macOS/Windows runtime validation and hosted GitHub Actions remain pending. The source contains native three-OS verification and a manually triggered draft-release workflow. No repository or registry publication was performed.

## Forge 0.10 extension verification completed on 22 October 2026

- The Go race-enabled regression suite passed in **41.135 seconds** after compilation; `go vet -buildvcs=false ./...` passed. New scenarios exercise concurrent steering writers, a direction arriving during implementation and during final council approval, stale dependent-task rejection, template precedence and persistent instructions, symlink containment, actual hook subprocesses and JSON input, failed gate repair, pinned hook commands on resume, timeout reporting, and re-verification after hook changes.
- `scripts/terminal_conversation_test.py --extensions` passed against the native Linux binary in a real POSIX terminal, with simulated Codex/OpenCode subprocess replies and actual Node.js tests. It discovered a project template, invoked `/repair`, accepted `/steer` while the first worker was held at an explicit test barrier, replanned before further implementation, routed a worker question to a senior, and continued from its answer. The result passed unchanged arithmetic tests, created the newly requested documentation, passed two configured hooks, obtained unanimous review, exported a report, and saved a queued goal. It used **10 senior calls and 3 worker calls**, with one durable applied direction.
- Release package tests exercise installed `workflows`, custom skill discovery, preview, steering inbox persistence, and hook-enabled `forge check`, alongside the existing installation, upgrade, tamper rejection, and uninstall coverage. The release builder records the results below.
- This update did not make new premium-account or free-service inference calls. The prior real OpenCode conversation test is recorded separately under 0.9. The new extension tests use deterministic agents to exercise timing and failures reproducibly.
- Native execution here is Linux amd64. macOS, Windows and ARM64 binaries are cross-compiled; native execution on those targets is not claimed. The source includes CI for the supported operating systems.

## Forge 0.9 conversation verification completed on 22 October 2026

Verification distinguishes real model inference from simulated adapter responses.

- The final Go race-enabled regression suite passed in **29.372 seconds** after compilation; `go vet -buildvcs=false ./...` also passed. Coverage includes senior-to-senior questions, nested free-peer advice, worker continuation from actual routed replies, interruption and restart at the pending recipient, cancellation while saving a handoff, corrupt-conversation rejection, stale-advice invalidation, replanning before dependent tasks, conversation limits, strict message parsing, verified-free advice, and the default team mode.
- Actual subprocess adapter fixtures cover Codex, Claude, OpenCode, Pi, Cline, and Gemini in ordinary planning, full-access planning, and restricted consultation modes. They check native arguments, stdin, and OpenCode permission configuration. This is protocol coverage; it is not premium-account inference.
- `scripts/terminal_conversation_test.py` passed against the compiled Linux binary in a real POSIX terminal. Simulated Codex/OpenCode subprocesses exchanged a worker question and a matched senior reply. Forge continued the assignment, ran actual Node.js tests, obtained unanimous review, displayed the discussion, exported a report, saved a default-team queued goal, and exited cleanly. The run used seven senior calls and two worker calls; the original tests were unchanged.
- A real OpenCode **1.19.3** conversation run passed in **353.32 seconds**, using the refreshed zero-price `opencode/deepseek-v4.1-flash-free` model for separate senior roles and implementation. A reviewer sent a suggestion to the lead, received its actual answer, and continued its critique. The worker then asked the lead about decimal behavior, received the routed answer, continued the same assignment, and repaired the actual Node.js fixture. The run used **9 senior calls**, **2 worker calls**, and **14 recorded discussion messages**. The original test file remained byte-for-byte unchanged; all four final verification commands passed and the senior review was unanimous. This run exercised the final conversation and adapter code, including native session-todo permissions.
- Six release binaries are built for Windows, macOS, and Linux on x64 and ARM64. Native execution in this session is Linux amd64 only. The release builder separately records package installation, upgrade, integrity rejection, and uninstall checks below.

`go test -buildvcs=false -run TestLiveFreeWorkerConsultationContinuesTask -v -timeout 14m` is an opt-in real-service test with `FORGE_LIVE_FREE_TESTS=1`. It requires an installed compatible OpenCode CLI, Node.js, network access, and a model passing the refreshed zero-price catalog check. It asserts an actual worker question and matched senior answer, continuation, unchanged original tests, passing verification, and unanimous review. Set `FORGE_LIVE_OUTPUT` to retain its workspace and checkpoints. Automated tests do not establish quality for arbitrary applications or future free-service capacity; premium-account inference and native macOS/Windows execution were not performed in this session.


## Forge 0.8 council verification performed on 21 October 2026

Execution host: Linux amd64. Windows/macOS and all ARM64 builds are cross-compiled; native runtime execution on those targets remains unverified here.

- The final Go race-enabled suite passed in **21.060 seconds** after compilation; `go vet -buildvcs=false ./...` also passed. New coverage includes a real failed check followed by repair, proposal/critique/synthesis handoffs, reviewer vetoes, partial planning recovery with a pinned team, changed-source vote invalidation, call budgets, free-worker gates, reported charges from attached free seniors, team persistence, figureout queue claims, and Gemini JSON/error parsing.
- Native subprocess fixtures exercise the actual argv/stdin/environment of all six adapters in ordinary and full-access planning modes. They verify that normal runs retain their previous permissions, full-access flags reach the native agent, and Codex receives its model reasoning setting. These are protocol tests, not premium model inference.
- A real OpenCode **1.19.3** free-service `figureout` run passed in **240.08 seconds** using a freshly verified zero-price `opencode/glm-5.3-flash-free` model. Two separately invoked senior roles on that connection exchanged proposals, critiques, and a final decision; an actual free worker repaired a failing Node.js addition fixture. It used **6 senior calls**, **1 worker call**, and saved **9 discussion messages**. The original test file was checked byte-for-byte and remained unchanged. All final checks passed and all senior votes approved completion.
- The native interactive terminal's existing initialization, memory, history, checks, queue, export, resume, and shutdown workflow passed after the council changes. A second real terminal session used simulated external CLI subprocesses to execute `/figureout`, display the senior discussion, run actual Node.js checks, require all six senior responses, export the report, preserve a queued figureout goal, and exit cleanly. The final npm package checks cover installed team inspection, detach, discussion inspection, figureout help and queue persistence in addition to the previous installation workflow.

The live run establishes that the coordination loop operates against an actual free service. It does not establish app quality for arbitrary goals, continued free capacity, or access to premium model IDs. Codex, Claude, Pi, Cline, and Gemini inference was not performed against premium accounts in this validation. Full access remains subject to native account, organization, and OS restrictions. Later refinements to readable discussion rendering and strict consensus counting were covered by the final source regression suite; the live call path was unchanged.

## Forge 0.7 project workflow verification performed on 20 October 2026

The Linux native suite passed with the Go race detector (**12.012 seconds** after compilation). `go vet` passed. New regression coverage includes:

- Strict project configuration, OS-specific checks, preserved initialization files, scoped context, and concurrent saved notes.
- Plan-only execution with no worker dispatch, saved-plan resume without duplicate planning, and pinned configuration across resumes.
- Worker-call and cumulative runtime budgets, including cancellation of an active model call and counting prior usage.
- Persistent queue claims, restart recovery with the same run ID, failure holding later goals, concurrent queue updates, cancellation, reordering, and exclusion of a second consumer.
- Standalone passing/failing/canceled checks that do not mutate the saved goal; Markdown/JSON exports, overwrite protection, history search, and read-only Git inspection.
- Terminal plan/check completion leaving queued work untouched.

A native interactive terminal session completed the offline repair loop, created project settings, saved and inspected a note, inspected tasks/history/configuration, exported a report, added a queued goal, ran standalone checks without additional model calls, resumed without repeating workers, and exited cleanly. A fresh command then read the still-pending queue from disk.

The real npm package tests also exercise the installed project setup, notes, queue, verification, reports, JSON export, and searchable history, in addition to installation, upgrades, integrity rejection, and removal. Their result is recorded in the release build validation.

A real OpenCode **1.19.3** integration using a refreshed zero-price `opencode/glm-5.3-flash-free` entry passed in **154.90 seconds**. Forge saved a plan with no implementation edits, no worker calls, and no harness checks; a new runner resumed that checkpoint, dispatched one implementation batch, ran the original tests, and obtained final review approval. The complete workflow used **two planner calls**. The original test file was checked byte-for-byte and remained unchanged.

Native runtime execution in this environment covers Linux amd64. The bundle contains cross-compiled Windows/macOS/Linux amd64 and arm64 executables. Windows and macOS runtime execution, signing, and public npm publication remain unclaimed.

## Forge 0.6 harness verification performed on 20 October 2026

Actual execution host: Linux amd64. Windows and macOS binaries are cross-compiled; native execution on those systems remains pending.

- The final source passed `go test -buildvcs=false -race ./...` in 5.206 seconds and `go vet -buildvcs=false ./...`.
- New regression tests cover a real subprocess exit immediately after a file edit, durable pre-worker checkpoints, cancellation recovery, stale review evidence, re-verification of completed sessions, retained checks and success criteria, task memory, state migration and validation, repeated no-progress recovery, ordered-task failure recovery, and held UI queues.
- The crash test terminates the controller with exit 73 before it can return the worker result. A separate runner loads the checkpoint, executes the actual verification command, and completes through review without replaying the worker.
- Source fingerprints detect same-size content changes even when modification timestamps are preserved. Runtime-state writes do not invalidate the fingerprint, and cancellation interrupts hashing.
- Check replacement tests reject ordinary failed tests, failed test collection, passing checks, and required checks. Only unavailable suggested commands qualify; invalid planner requests are corrected through the normal planner retry path.
- Package tests exercise the installed `forge status`, `forge tasks`, `forge history`, and `forge events` commands with JSON output, including preservation of project checkpoints across package reinstall. The release builder records the actual package test outcome below.

### Live service verification for the harness

A real OpenCode 1.19.3 integration test using the refreshed zero-price `opencode/glm-5.3-flash-free` catalog entry passed in **93.71 seconds**. It made **three planner calls** (including a retry) and completed **one implementation batch**.

The fixture began with a real failing Node.js addition test. The planner assigned a focused repair, a live worker changed subtraction to addition, Forge ran `npm test`, and a live reviewer inspected the actual files and approved completion. The test file remained byte-for-byte unchanged. Positive, negative, and decimal assertions passed. The two final failure-path refinements (replanning after worker failure and automatically correcting invalid check replacements) were subsequently covered by the final race-enabled source suite; they do not change the successful live path.

No paid inference or premium-account sign-in was performed. This is evidence for one actual free-service goal, not a guarantee of model quality, availability, or future pricing. The default no-progress threshold now pauses after three repeated unchanged verification rounds; productive cycles remain unlimited unless explicitly capped.

## Forge 0.5 source verification performed on 19 October 2026

- `go test -buildvcs=false -race -count=1 ./...` passed on Linux amd64 in 5.719 seconds after the terminal distribution changes, including standalone installer integrity checks and native install/upgrade/uninstall coverage.
- `go vet -buildvcs=false ./...` passed.
- The release builder can reuse completed source checks with `--skip-tests`; the generated report states whether that packaging run reran them.
- The release builder separately tests the actual npm package and records the result below. Those tests use real npm, the installed `forge` command, and actual native offline verification subprocesses.
- The agent execution loop is unchanged from the live-service validation below. No new premium inference or account sign-in is claimed for this packaging change.
- Native Windows and macOS execution and public npm publication remain pending. The included native CI runs the package tests on all three operating systems.

## Forge 0.4 validation performed on 19 October 2026

Actual execution host: Linux amd64. Native macOS and Windows execution remains pending; compiling their applications and test executables does not count as executing them.

- The local test suite passed with Go's race detector enabled, including real native install, upgrade, uninstall, paths containing spaces, packaged installer checksum rejection, process cancellation, checkpoint recovery, and repair after a real failed subprocess check.
- `go vet -buildvcs=false ./...` passed.
- Regression coverage includes the selected project versus an inherited `PWD`, wrapped planner JSON, recovery with another free planner, review command permissions, and a reported charge stopping automatic free planning.
- Windows amd64 and macOS arm64 test executables compiled successfully. The included GitHub Actions workflow runs the suite on native Windows, macOS, and Linux hosts when the source is uploaded to a repository; that workflow has not been run for this download.
- The offline sample uses simulated agents, writes actual files, and runs actual verification subprocesses. It proves the local orchestration loop, not live LLM quality.

### Real coding-agent probes

The five adapters were checked against the actual official CLI executables, using their help commands and capability checks without paid inference:

| Agent | Installed CLI version | Capability check |
| --- | --- | --- |
| Codex | 0.163.0 | Passed |
| Claude Code | 2.1.303 | Passed |
| OpenCode | 1.19.3 | Passed |
| Pi | 1.3.0 | Passed |
| Cline | 3.0.73 | Passed |

OpenCode's refreshed public model catalog returned ten entries with explicit zero pricing that the free-price parser accepted. This confirms catalog discovery, not availability of inference or a billing guarantee.

### Live planner-to-worker goal: passed

A real OpenCode 1.19.3 run used `opencode/glm-5.3-flash-free` for planning, implementation, and review. The test completed successfully in 365.61 seconds with four planner calls, including retries, and one implementation batch.

The fixture began with a broken `add(a,b)` implementation that subtracted its inputs. A real Node.js test subprocess failed before agent work. The planner inspected the actual files and assigned the fix; the free worker changed the implementation; Forge executed `npm test`; and the reviewer inspected the files, repeated the check, and approved completion. The original test file was checked byte-for-byte and remained unchanged. Final verification exited zero with the positive, negative, and decimal assertions passing.

The real integration test is included as `TestLiveFreeGoalPlansImplementsAndReviewsActualFiles`. It is opt-in, uses the refreshed zero-price catalog, and has a bounded test deadline. Production goals have no cycle or planner-call cap unless the user sets one.

The failed 0.3 probe was investigated rather than bypassed. Fixes include a suitable noninteractive planning configuration, explicit project selection, correct child `PWD`, valid JSON extraction from normal assistant explanations, and permissions for the exact checks Forge had already executed. Temporary endpoint-capacity errors occurred during validation and were retried.

No premium-account login or paid inference was performed. Codex, Claude, Pi, and Cline were checked with real CLI capability probes; live premium-account operation is not claimed. Model capacity and free offers can change, so one successful goal does not guarantee every model or task will succeed. Hard provider denials save the checkpoint and do not silently switch to paid workers.

### Remaining external release checks

The Windows and macOS application and test executables compile. Their native execution is still pending. The included CI workflow covers native install/upgrade/uninstall, offline goal verification, and a fresh private Windows Git Bash bootstrap. That workflow has not yet been executed on GitHub.

This candidate is unsigned. Code signing, macOS notarization, and native macOS/Windows execution remain outstanding release checks. They are not represented as passed by a Linux build.
