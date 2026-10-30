# Autonomous coding harness

[Browser acceptance checks](browser.md) execute actions and positive assertions with screenshot evidence under the existing source snapshots, coverage and senior review. Tool availability or a screenshot alone does not establish acceptance.

Give the app a goal. Senior agents plan and critique, verified free workers implement, the harness executes verification, and seniors review the actual result. Goals continue through repairs until completion, user interruption, configured budgets, or a real provider/access blocker.

## Free-model recovery

Version 0.12 saves model attempts, usable responses, failures and cooldowns with the goal. Worker and planner health are tracked separately: a malformed planning reply does not imply that the same model cannot implement code. Health records are observations, not test results or evidence of current pricing.

Before every automatic free call, including fallback and retry, BuildHeadway refreshes the zero-price catalog. Models in cooldown wait while ready alternatives can run. Among ready models, fewer consecutive failures take priority, preserving existing preferences and repair-cycle rotation for ties. Explicit model selection permits only the chosen model. Old pricing or a past successful response cannot authorize a call.

Transient failures begin with a 15-second cooldown; unusable replies and other execution failures begin with 30 seconds. Repeated failures double that model's cooldown up to five minutes. A usable response resets the consecutive-failure count and cooldown. If all eligible models are cooling down, BuildHeadway waits for the earliest retry and rechecks pricing. Waits poll for direction changes and respond to cancellation and the run's runtime budget. Exhausted call budgets pause before another wait or call. Unusable planner replies remain limited to two repair attempts per model per decision; unsuccessful worker batches return to the team for diagnosis.

Authentication, insufficient quota/credit, provider restrictions and reported nonzero costs are recorded as blockers and returned immediately. These do not trigger another model or an automatic timed retry. Resume still requires the underlying problem to be resolved.

If a failed worker changed files in the source snapshot, BuildHeadway stops that batch, runs checks, and supplies the partial-work report to the seniors before more implementation. Interruption and fatal blockers retain the existing uncertain-worker recovery path. Snapshot exclusions described below still apply.

Use `buildheadway routing [RUN_ID]`, `buildheadway routing --json`, or `/routing` to inspect saved observations and recent decisions without calling a model or refreshing a catalog. `buildheadway status` shows an active wait, and exported run reports include routing history. A usable response can still contain an incorrect implementation: acceptance checks, fresh source verification and unanimous senior review remain the completion gates.

## Durable pause control (0.21)

`buildheadway pause [RUN_ID]` lets another terminal request a pause without acquiring the running goal's project lock or rewriting its checkpoint. The request is pinned to one run. Duplicate requests coalesce; an explicit start/resume acknowledges the previous request. A paused queued goal keeps its identity and holds later goals until the user resumes the queue.

The controller checks before dispatch and completion and polls every 200 ms during agent calls, verification and retry waits. It cancels the existing execution context so local subprocesses use the same process-tree termination as Ctrl+C. Partial edits, task recovery and attempted-check diagnostics survive; resume verifies the current files and obtains fresh senior approval. Cancellation depends on the attached adapter honoring its context, and the requesting CLI does not claim that shutdown has already finished. Inspect `buildheadway status` for the saved outcome.

Completion and pause requests share the steering lock: a saved request prevents completion, or a completed checkpoint causes the pause command to refuse the request. Provider authentication/quota, cost, policy and output-limit errors retain precedence over concurrent pause cancellation and never become an automatic retry. Invalid or unsupported control files block instead of being silently ignored, including when discovered during a running call.

The separate `.forge/runs/RUN_ID/control.json` format is version 1, limited to 4,096 bytes and restricted to regular files with a matching run identity and valid request time. Only the controller writes `state.json`; request writers update the control file atomically. Checkpoint v12 and earlier-brand run locations remain unchanged. These controls coordinate a running local controller; they do not start a background service or suspend a remote provider independently of its adapter.

## Acceptance evidence

Version 0.11 makes acceptance coverage an executable gate for all new and resumed goals. Each retained success criterion must have at least one meaningful check. A senior can propose the mapping in its structured decision:

```json
{
  "done": false,
  "summary": "Implement persistence and test restart behavior",
  "tasks": [
    {"id": "persistence", "title": "Persist tasks", "instructions": "Save tasks and cover reopening the app with an integration test."}
  ],
  "checks": ["npm test"],
  "success_criteria": ["Tasks survive an application restart"],
  "acceptance": [
    {"criterion": "Tasks survive an application restart", "checks": ["npm test"]}
  ],
  "check_replacements": [],
  "messages": []
}
```

The harness runs these commands itself. A worker's statement that tests passed is not passing evidence. A successful unrelated build does not automatically cover every requirement: seniors must map coverage and review the tests' relevance.

Use `buildheadway acceptance`, `buildheadway acceptance --json`, or `/acceptance` to inspect each requirement. States are `unmapped`, `pending`, `failed`, `unverified`, and `passed`. Inspection compares the current source snapshot with the tested one; edits invalidate the displayed passing status. JSON includes actual check exit codes and bounded output excerpts. Full logs and reports remain in the saved run.

Completion requires every retained requirement to pass, every required and suggested check to have executed and passed, verification hooks to pass, and current source to match the checked snapshot. Figureout additionally requires unanimous senior review. New checks requested at review return to verification automatically.

Checks and acceptance mappings accumulate across batches and council members; a synthesis step cannot silently omit another senior's coverage request. An unavailable suggested command may be replaced using the existing explicit replacement protocol, and its acceptance mappings follow the replacement. A required check cannot be replaced by the planner.

## Pin project requirements

A project's `.buildheadway.json` can define mandatory acceptance checks:

```json
{
  "version": 1,
  "checks": ["npm run build"],
  "acceptance": [
    {"criterion": "Existing tasks reload after restarting", "checks": ["npm run test:restart"]},
    {"criterion": "Invalid input is rejected without data loss", "checks": ["npm run test:validation"]}
  ]
}
```

Use commands that exist in your project. This configuration is validated and pinned when a goal starts. Its acceptance commands become required checks. Resume retains the saved configuration; later edits affect new goals. Start a new goal when intentionally changing a pinned requirement. The agents still add coverage for other parts of the requested goal.

## Stable verification

Repeatable formatters belong in `before_check`. After those hooks, BuildHeadway pins one source snapshot for the round. Version 0.15 compares that snapshot immediately before and after each configured verification command and each `after_check` hook. A detected edit stops the round before the next command or hook. A later command cannot restore earlier files and conceal checks executed against a different version. All checks run again before approval; three continuously changing rounds pause for diagnosis.

Invalidated command results include `source_changed: true` in JSON. Their real exit codes, output excerpts and full log references remain intact: exit zero does not authorize completion when that evidence was invalidated. Acceptance becomes unverified, affected hook gates cannot pass, and status and Markdown reports identify the invalidation. Resume reruns verification and obtains fresh senior approval after the source is stable. Older checkpoints remain readable; migration does not invent historical invalidation flags.

These extra fingerprints add file-reading work proportional to the project size and number of verification commands. The boundary is each configured shell command or hook, not every command inside a compound shell expression. Edits made and completely reverted within one invocation may not be visible at its boundaries; this mechanism does not isolate processes or protect against malicious writers.

Generated and dependency directories such as `node_modules`, `dist`, `.git` and `.buildheadway` are excluded from source fingerprints. Checks run in the actual project, and symlink destinations are not traversed by fingerprinting. This is a consistency check for ordinary concurrent edits, not an isolation boundary against a malicious process.

## Shared instructions and recovery

The [automatic instructions guide](prompts.md) describes the stage-specific planning, work orders, consultations, repairs and review prompts. Workers receive the current goal/steering, task, host shell and required verification context. Handoff reports distinguish changes, executed checks and remaining work. Reports remain evidence to assess; the execution and approval gates below still decide completion.

All planners, reviewers and workers receive root `AGENTS.md`, `CLAUDE.md` and `BUILDHEADWAY.md` guidance, plus configured context files and saved project notes. Duplicate files are included once. Existing path containment, file type and size checks apply. Root guidance is refreshed on startup/resume; nested instructions remain available for the attached agent to inspect in its task's scope.

Checkpoint v12 stores verification invalidation and model health and cooldowns alongside acceptance mappings, the task ledger, messages, steering, hook results and executed checks. Older checkpoints remain readable and migrate under the run lock; inspection is read-only. Resuming clears an obsolete active-wait indicator but retains the models' retry times. Resuming repeats verification and obtains fresh approval. A legacy goal without acceptance mappings receives missing-coverage feedback and must establish coverage before completion.

Agents can route questions, challenges and suggestions to other seniors or verified free peers. Workers can hand off a question, receive the real answer, and continue the same task. These calls and replies remain checkpointed; communication limits stop circular discussion and return the team to concrete implementation. Worker ordering, model price checks, explicit budgets and interruption behavior remain enforced.

## Scope of the guarantee

The harness enforces that recorded requirements have executed passing checks and fresh review. It cannot prove that a test covers all behavior, that every requirement was inferred correctly, or that a passing application has no defects. Reviewers still inspect real files and test coverage. A goal that requires browser interaction, an external service or native OS behavior needs suitable project checks and access to that environment.

## Agent context admission (0.27)

Each complete prompt supplied by BuildHeadway has a default limit of 524,288 bytes (512 KiB). This includes planning, senior council steps, consultations, worker assignments, active answers, continuations and repair/retry text. A prompt over the limit pauses the goal before a model is called for that prompt. Initial admission also precedes catalog discovery and model call/health accounting; a rejected retry does not count as another attempt. Ordinary earlier attempts retain their actual results. The harness does not silently drop requirements, shorten active instructions or switch models to get past the limit.

The saved error identifies the prompt, its byte count and the allowed size. Inspect `buildheadway status`, saved tasks and logs; reduce project guidance/notes when appropriate or split the goal. To allow a larger prompt explicitly, use:

```sh
buildheadway resume --max-prompt-bytes 1048576 RUN_ID
```

Use `buildheadway context [RUN_ID]`, `buildheadway context --json [RUN_ID]` or `/context [RUN_ID]` to inspect the most recent 20 admission measurements. Each entry records the time, call label, actual bytes, applied limit and whether the prompt fits. The report also shows the pinned/default allowance, which can differ from an explicit invocation override. Status and Markdown exports include this information. Inspection works while the controller is busy, makes no model calls and leaves saved state unchanged. A fitting measurement is not proof that dispatch occurred or work passed: later pricing, access, budget or cancellation gates may still stop the call. Old goals without measurements report that absence; no history is invented. The added optional metadata keeps checkpoint v12 and does not retain a second copy of prompt text.

New goals can pin `max_prompt_bytes` in `.buildheadway.json`. Accepted nonzero values range from 16,384 to 8,388,608 bytes; zero or an omitted setting uses the default, including for older checkpoints. An explicit CLI flag overrides the pinned setting for that invocation only; `--max-prompt-bytes 0` selects the default rather than disabling admission. The UI and queued goals use the pinned project/default allowance. Project edits cannot silently raise a saved goal's allowance. Resume still runs its normal recovery, checks and fresh approval. Checkpoint v12 and the existing pricing, provider-blocker, budget, source-consistency and acceptance gates remain in force.

This is a byte limit on the prompt BuildHeadway supplies, not a token estimate or a promise that it fits a particular model. Attached agents may add their own instructions, tool results and history; their provider limits still apply. Admission does not bound prompt construction memory, full checkpoint/disk size or the attached agent's later context growth.

Acceptance requirements often share checks. Planning/review prompts retain every criterion, status and mapped command, but `acceptance_results[].evidence_sources` points to `state.json:check_results[INDEX]` rather than duplicating each diagnostic in every row. Read the shared `check_results` excerpts and referenced full logs for diagnosis. Inspection/export JSON and the saved full results keep their existing evidence; references do not create independent passing evidence, omit a veto or relax a completion gate.

## Interrupted verification evidence (0.20)

Commands and hooks save `duration_ms`, the exact process-log filename, bounded output and a `failure_kind` when they fail. Cancelling a running check retains its partial diagnostics before the run pauses. Cancelling before a command is attempted does not invent an execution result. `buildheadway check --json`, saved checkpoints, acceptance evidence, agent prompts, status and exports preserve these fields.

| Failure kind | Meaning | Recorded code |
| --- | --- | --- |
| `exit` | The child returned a real nonzero exit status | Actual exit status |
| `timeout` | The command exceeded its own time limit | 124 |
| `cancelled` | The upstream context stopped the command, including a run deadline | 130 |
| `execution` | The harness could not reliably start, wait for, or capture the command | -1 |

Codes 124, 130 and -1 are harness outcomes for those failures, not claims about the child's exit status. A command that itself exits 124 is an `exit` failure, not an inferred timeout. A log filename identifies the attempted log location; setup failures can prevent that file from being created, and log inspection reports that absence rather than inventing output. Durations measure the invocation on this host; older records omit them.

Every failure blocks passing acceptance, hook gates and completion. Resume reruns checks against current source and obtains fresh senior review. Saved failure metadata cannot be turned into passing evidence merely by changing its exit code to zero. The fields are additive to checkpoint v12; historical executions keep their recorded outputs without guessed failure classifications. These diagnostics do not automatically retry authentication, pricing, quota or policy blockers.

## Large command output

Version 0.13 retains complete subprocess logs on disk but captures at most the last 8 MiB in memory. Verification still uses the actual command exit code, and reports retain bounded excerpts. Oversized model replies and model catalogs are rejected rather than parsed from a tail. Codex last-message files have the same size limit. An output-limit blocker requires inspecting the saved log and reducing the assignment before resume; it does not silently switch models because cost or completion evidence may have been omitted. These limits bound captured output, not disk log size or the model provider's own memory use.


## Inspect saved logs

Version 0.14 links each executed verification command and hook to its full process log. New JSON results include `log` (a filename relative to the saved run directory) and `output_truncated` when the recorded output is only an excerpt. Acceptance evidence and exported reports keep these references. Prompt excerpts keep the filename and truncation marker; seniors, workers and consultation recipients receive `log_directory` so they can inspect earlier diagnostics without putting whole logs into every prompt. These references help investigation; they do not replace executed passing checks or current-source review.

```sh
buildheadway logs
buildheadway logs --run RUN_ID
buildheadway logs --bytes 64000 LOG_NAME.log
buildheadway logs --json LOG_NAME.log
buildheadway logs --last-check
buildheadway logs --last-check LOG_NAME.log
```

Put flags before the filename. `buildheadway logs` lists process logs for the latest goal; `--run` selects another saved goal. `--last-check` selects the latest standalone `buildheadway check` report, including hook logs. `/logs` and `/logs LOG_NAME.log` offer the same default-goal inspection inside the terminal app. Inspection never invokes a model, reruns commands or rewrites checkpoints, and works while a goal is running. A running log is read as it exists at inspection time; this is not live following.

A selected log returns its last 16,000 bytes by default, with original size, byte offset and explicit truncation. `--bytes` accepts 1 through 1,048,576. Open the listed on-disk file directly for larger output. Directory listing is limited to 1,000 logs; runs beyond that can still inspect a specific filename from saved check or hook evidence. Only regular `.log` files in the selected log directory are accepted, not prompts, arbitrary paths or symlink files. Terminal display sanitizes control sequences; JSON preserves raw log text. Logs can contain project or tool output, so inspect them before sharing.

Checkpoint v11 retains new references through resume. Migrating an older run preserves its existing output and exit codes without guessing which log belongs to a result. Older logs remain listable when present. Missing/deleted logs are reported as an inspection error, not regenerated or treated as new passing evidence.

Git status/diff evidence now captures at most 14,000 bytes per command in memory and marks truncation or command failure. This improves bounded diagnosis; it does not bound every part of the prompt or total disk usage.


## Bounded worker evidence in agent context

Version 0.16 projects worker results into prompt-only evidence. Each report is limited to 3,000 UTF-8 bytes and each error to 1,000 bytes, preserving a beginning and ending excerpt with an omission marker. Task/model identifiers, success/failure, communication status, truncation flags and a `state.json:worker_results[index]` reference remain visible for every worker in the batch. Structured replies are not duplicated wholesale inside that projection; actual questions and answers remain in the existing discussion and dialogue context.

Consultation and worker-continuation handoff reports use the same 3,000-byte excerpt. The prompt identifies the full saved state file, and the continuation names `worker_session.report`. Agents can inspect full saved evidence when an excerpt is insufficient. Saved reports, handoffs, checkpoints and exports are not shortened by preparing a prompt. These bounds apply to worker evidence, not the entire prompt, number of accumulated requirements, disk usage or the provider's context window. Full reports are evidence, not proof of passing verification; the existing executed-check, source-consistency and senior-review gates still decide completion. No checkpoint migration is needed for these prompt-only projections.

### Bounded historical team context (0.17)

Historical discussion windows now contain prompt-only projections rather than full nested decisions. Every selected message retains its sequence, reply link, thread, time, cycle, sender, recipient and kind. Text is limited to 3,000 UTF-8 bytes, and a decision summary to 1,000 bytes, using beginning/end excerpts and explicit truncation flags. A decision's approval or veto is retained even when false. The source points to `state.json:discussion[index]` within the supplied saved-run directory. The six most recent planning-history summaries are each limited to 1,500 bytes, with truncation and `state.json:history[index]` references.

Full historical decisions and summaries remain in saved state and exports. Current-round council decisions and active routed questions/answers are never replaced by these projections; these determine the actual work and must remain complete. This reduces historical duplication, not the entire provider context: active decisions, requirements, task instructions, project context and message counts can still make prompts large. Historical excerpts cannot satisfy acceptance, replace executed checks, or override a current veto. No checkpoint migration is needed.


## Verification shell diagnostics (0.19.2)

`buildheadway doctor` runs a quoted file-content check that must pass, then changes the isolated fixture and requires the same check to fail. The probe verifies quoted filenames, argument quotes, redirection and failure propagation. It uses a temporary directory that is removed afterwards, with a three-second timeout for each subprocess and the caller's cancellation context. No project command, model request or saved configuration change is involved. An executable merely being present on PATH is no longer enough to report the shell as usable.

Windows checks retain the original command text for `cmd.exe /D /S /C` rather than applying ordinary executable argument escaping. Native Windows regressions execute quoted file checks and a helper whose path contains spaces; they also require wrong content to remain a failed check. This follows [Go's documented command-line quoting distinction for cmd.exe](https://pkg.go.dev/os/exec). Native macOS agent-discovery coverage compares the actual file identity so case-insensitive filename aliases do not produce a false failure.

## Historical task handoffs (0.23)

Planning and worker prompts include recent task evidence with the task key, status, model, attempts and timestamps. Long historical instructions, reports and errors retain both their beginning and end within the existing 2,000/1,500/1,000-byte limits. Truncation flags and `state.json:task_history[INDEX]` references identify omitted material. UTF-8 boundaries are preserved. Current task assignments, requirements, active consultation answers and acceptance evidence are not shortened by this projection.

Use `buildheadway tasks` to find a durable key, then `buildheadway tasks --task 1:0` to read its full instructions, report and error. Add `--json` before the key flag for automation, and an optional run ID after the flags to inspect an earlier goal. The key identifies a ledger entry, even when several cycles reuse the same model task ID. Inspection does not call a model, acquire the controller lock, migrate the checkpoint on disk or imply that a task report passed acceptance. Full saved task evidence remains unchanged.

## Page through long verification logs (0.24)

```sh
buildheadway logs --offset 0 --bytes 16000 LOG_NAME
buildheadway logs --json --offset 16000 --bytes 16000 LOG_NAME
buildheadway logs --last-check --offset 0 LOG_NAME
```

Choose an actual filename from `buildheadway logs`. The default remains a tail read; an explicit nonnegative `--offset` reads from that byte position. JSON supplies `offset`, `next_offset`, `more` and the observed total `bytes`. Continue from `next_offset` while `more` is true. Each page is capped at 1 MiB, including when a log is much larger than captured worker/check output. EOF returns an empty page; offsets beyond the observed size fail. Each call observes the current file size, so an actively growing log can change between calls. Byte boundaries can split multibyte text; saved log bytes remain unchanged.

Prompt-only check and hook excerpts now retain both ends of captured output within 2,500 bytes, preserve UTF-8 boundaries, and retain existing truncation, exit/failure, source-change, duration and log-reference fields. This exposes an early diagnosis alongside the final outcome. Capture itself can already be truncated, so the beginning of a captured excerpt is not necessarily the beginning of the full log; use `--offset 0` to inspect that. Inspection does not rerun checks, write checkpoints or establish passing acceptance. Full logs, original records, budgets and completion gates are unchanged.

## Queue history and capacity (0.25)

The active queue retains up to 1,000 entries, including finished history. `buildheadway queue prune` (or `/queue prune`) archives completed and canceled entries, then removes those entries from the active queue so new goals can be added. Pending, running and blocked entries keep their order and fields. This never cancels a goal, changes a budget, resumes a blocked provider or edits a run checkpoint. It can run while a controller owns the project lock; normal queue locking coordinates writers.

Each removed record is first atomically saved under `.forge/queue-archive/QUEUE_ID.json` in an independent version-1 wrapper. Its complete queued goal, mode, status, error, times and run reference are retained. All archives must succeed before the smaller queue is published. After interruption, matching per-goal files are reused, and conflicts block pruning instead of overwriting history. A failed attempt may leave some archives beside an unchanged queue; retry after resolving the error. An archive is historical metadata, not passing acceptance or permission to rerun a goal.

Use `buildheadway queue --json prune` for counts and archived IDs, then `buildheadway queue archive QUEUE_ID` (or `/queue archive QUEUE_ID`) for inspection. Add `--json` before `archive` to retrieve the complete record. Archive files are limited to 1 MiB each; an oversized or conflicting record leaves queue entries intact. The no-op case leaves the queue file unchanged. Run checkpoints, full logs and their verification/senior-approval gates are preserved; this feature does not delete old runs or log files. Atomic archives and queue publication protect ordinary interruption/retry, not malicious concurrent filesystem writers.

## Find archived goals (0.26)

```sh
buildheadway queue archives
buildheadway queue --json --search "task tracker" --limit 20 archives
buildheadway queue --json --after QUEUE_ID --limit 20 archives
```

Search is literal and case-insensitive across the full archived goal, queue ID and finished status. Flags go before `archives`. Results are sorted lexicographically by queue ID; the default page is 20 entries and the accepted range is 1–100. JSON supplies `more` and `next_after`. Repeat the same search with `--after NEXT_AFTER` while `more` is true. Each goal summary is limited to 240 UTF-8 bytes, preserves its beginning/end and marks truncation. Use `queue archive QUEUE_ID` for the full saved record.

`/queue archives` and `/queue archives SEARCH` show the first matching page in the app. Use the CLI for later pages. The command scans directory batches and retains at most the requested page plus one matching summary. Searching can read all eligible archives; CLI scans stop on Ctrl+C or a 30-second deadline, and in-app scans have a 10-second deadline. Timeouts/cancellation are errors, not partial success. Invalid records encountered after the cursor fail clearly; unrelated filenames and symlink/non-regular entries are excluded. New archives can appear during a scan, so restart without a cursor to include newly added IDs that sort earlier.

Discovery takes no controller/queue lock, makes no model calls and does not rewrite archives, queues or checkpoints. It is navigation evidence and never marks a goal complete, bypasses a provider blocker or authorizes requeueing.
