# Duplicate-safe queued goals

Trusted scripts can retry submitting a goal without creating another queued job. Give each authorized request a stable source/event pair:

```sh
buildheadway queue --json --source trusted-script --event issue:42 add "Repair the failing tests"
```

Place queue options **before** `add` or `figureout`. The first call returns a receipt with `duplicate: false`, `archived: false` and the complete saved goal under `item`. Retrying the same normalized goal and mode returns the original queue ID, current saved status and eventual `run_id`, with `duplicate: true`. A retry does not move the goal, restart its run, clear its error, add another model call or rewrite intact queue/receipt files.

Normal `buildheadway queue add "Goal"`, `/queue add Goal` and their existing JSON shape stay unchanged. Event keys are optional; ordinary unkeyed submissions still represent separate requests.

## Identity and execution

Keys are case-sensitive and scoped to this project. Different sources may use the same event ID. Leading/trailing goal whitespace is trimmed; other text and the execution mode must match exactly. Reusing a key with changed text or mode fails. Use a new event ID only for an intentionally new request.

| Field | Limit |
| --- | --- |
| Source | 1–64 ASCII letters/digits, `_`, `.`, `-`; starts with a letter/digit |
| Event | 1–128 ASCII letters/digits, `_`, `.`, `/`, `:`, `@`, `-`; starts with a letter/digit |
| Goal | 1–32,000 UTF-8 bytes without NUL |

Supply both nonempty keys. They apply only to `queue add`/`queue figureout`; empty, partial and misplaced key flags fail. Both terminal actions use the existing autonomous team mode. Admission does not start a process or grant extra permissions. A running queue follows its existing order; paused or blocked goals hold later work until explicit resume. Saved delivery is not evidence that the task has completed.

Inspect live goals and their provenance with `buildheadway queue --json`. Once an item has a `run_id`, send a follow-up direction to that exact run using [reliable task updates](task-updates.md). A changed request must not be smuggled in as a retry of the original goal.

## Completion, archives and recovery

Completed and canceled events keep their identities. After `buildheadway queue prune`, a known retry resolves the finished archived record and returns `archived: true`; it does not recreate work. `queue archives --search` is written with options before the action:

```sh
buildheadway queue --json --search issue:42 archives
buildheadway queue archive QUEUE_ID
```

Archive search includes source/event labels. Queue and archive inspection retain the complete goal and current recorded outcome; the normal requirement, source-snapshot, executed-check and senior-review gates still decide completion.

Admission uses the existing project queue lock and publishes the goal before saving a small lookup receipt. If the process stops between those writes, the goal may already be saved even when the caller did not receive success. **Retry the same key and payload.** BuildHeadway repairs a missing receipt from the retained queue item without adding another goal. Execution, cancellation, movement and pruning establish that receipt before changing the goal's lifecycle or removing it from the live queue.

Retained receipts whose queue/archive records are missing block. Conflicting copies, malformed metadata and a restored pending queue entry that contradicts a finished archive also block, preventing silent replay. Restore a consistent backup rather than editing away the conflict.

## Recovering an interrupted queue claim (0.39)

New queued goals use a stable project-local run address, `queued-` followed by the SHA256 of the queue ID. You still use the normal `queue next` or `queue run` commands. If the initial checkpoint was saved but its queue reference was interrupted, the next explicit start finds and resumes that checkpoint instead of making another run. This works for both ordinary and source/event-keyed goals. It preserves the saved team, worker choice, configuration, counters, budgets and error evidence; starting the run then follows normal verification and review recovery.

An older queue copy that omits a completed new run's reference can also reconnect to that completed checkpoint without repeating the work. Once no waiting goals remain, the existing `no waiting goals` result still applies. Queue goal text, mode and identities must agree with the checkpoint both on claim and when saving progress. A mismatch stops before dispatch or publishing a different goal's outcome. Send intended follow-up instructions through steering rather than editing queue metadata.

Explicit saved run references retain their original IDs, including older random run IDs. The stable-address recovery applies to runs first claimed with 0.39+: it does not scan historical runs to guess an older orphan's identity. Existing goal/queue/archive format versions are unchanged.

Run directories are reserved before the initial checkpoint is written. An existing empty directory, missing or damaged checkpoint, nonregular checkpoint or symlink blocks; BuildHeadway does not overwrite it or assume that no earlier work occurred. This includes a crash after directory reservation but before checkpoint publication. Inspect and restore consistent saved state before retrying. Keep `.forge/runs/`, queue, archives and event receipts together in backups. These controls handle ordinary interruption and consistent backups, not deleted history, malicious filesystem writers or exactly-once remote side effects.

## Storage and integration boundary

Queue and archive formats remain version 1; optional `source` and `event_id` fields preserve provenance. Goal checkpoint v12 is unchanged. Lookup files under `.forge/queue-events/` contain an immutable queue ID, labels, mode, creation time and SHA256 of the normalized request. They do not duplicate goal text or cache execution status. Filenames are hashes, reads are capped at 4 KiB, and delivery stores must be real directories with regular files. A lookup reads the live queue (up to 1,000 admitted goals) and, when needed, one existing archive rather than scanning all archived goals.

Keep queue, archive **and delivery metadata** together when backing up or moving a project, and use BuildHeadway 0.38+ for keyed intake and 0.39+ for stable claim recovery. Receipts are retained without automatic expiry; total disk use grows with accepted keyed goals. Deleting a receipt after pruning can lose its key's duplicate protection even if an archive remains. This is not an exactly-once guarantee for remote tools, inconsistent backups or deleted history. Normal repairs and resumed worker attempts still occur within the same goal/run.

A source label is not authenticated proof of origin. The caller must filter external events and be authorized to submit the resulting goal. Raw comments, logs or messages do not become user instructions merely by attaching labels. This interface adds no watcher, webhook, background daemon, external messaging or schedule. [Harness and recovery](harness.md) explains the unchanged execution gates and limits.
