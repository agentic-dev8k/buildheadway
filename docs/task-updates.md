# Reliable task updates

Give a running team a direction from another terminal with `buildheadway steer`. For a trusted script that may retry delivery, add a stable source/event key and an explicit saved run ID:

```sh
buildheadway steer --project ./my-app --run RUN_ID --source ci --event workflow/42:failed --json "Investigate the failed integration test and retain all required checks."
```

This single-line command works in POSIX shells, PowerShell and Windows Command Prompt. Replace the project, run ID, key and direction with your own values. Put options before the direction; use `--` before text that starts with a hyphen. Find saved run IDs with `buildheadway history` or `buildheadway status`.

## Delivery and retries

The first successful delivery saves one update and prints a receipt:

```json
{
  "run_id": "RUN_ID",
  "duplicate": false,
  "status": "waiting",
  "message": {
    "id": "steer-EXAMPLE",
    "text": "Investigate the failed integration test and retain all required checks.",
    "created_at": "2026-11-01T00:00:00Z",
    "source": "ci",
    "event_id": "workflow/42:failed"
  }
}
```

If a caller loses this acknowledgement, retry the same command. The receipt keeps the original message ID and creation time, sets `duplicate` to `true`, and reports the current `waiting` or `applied` status. Concurrent senders share the same admission lock. A retry adds no update, rewrites neither the inbox nor the controller checkpoint, and consumes no model call. Reusing a key with different text fails; choose a new event ID for a new direction. Leading and trailing whitespace is trimmed before comparison.

Keys are scoped to one project and run, then to the exact case-sensitive source/event pair. `--source` and `--event` must be supplied together. Keyed delivery requires an actual `--run ID`; `latest` is rejected because a later task could become the latest run before a retry arrives. The source is a caller-supplied label, not authenticated proof of origin.

| Field | Allowed values |
| --- | --- |
| `source` | 1–64 ASCII letters/digits, `_`, `.`, `-`; starts with a letter/digit |
| `event` | 1–128 ASCII letters/digits, `_`, `.`, `/`, `:`, `@`, `-`; starts with a letter/digit |
| Direction | Nonempty UTF-8 text, at most 8,000 bytes, without NUL |

## Inspect and recover

```sh
buildheadway steering --project ./my-app RUN_ID
buildheadway steering --project ./my-app --json RUN_ID
```

JSON includes `run_id`, a `messages` array of receipts, and the existing formatted `status` string. Inspection receipts set `duplicate` to `false`; that field describes an individual submission result, not whether a message has ever been retried. Each message retains its source/event reference in saved state, prompts and exported reports.

The active controller applies all waiting directions at its next agent boundary. It invalidates stale plans, consultations and approvals, preserves required acceptance checks, and verifies partial implementation before continuing. Updates to paused or blocked goals wait for explicit resume. Receipts distinguish saved delivery from application; delivery does not mean the requested work has completed.

Applied event keys remain in checkpoint v12. If an older inbox backup is restored, keys already recorded as applied are still acknowledged without applying them again. Conflicting copies block instead of silently replacing accepted directions. A retry of an already accepted event may be acknowledged after completion; a new event cannot reopen a completed goal.

The existing limits remain: 64 distinct directions per run and 128 KiB of total direction text. Retries do not consume additional slots. Keys last as long as the run's retained inbox/checkpoint; deleting all those records also deletes deduplication history. Use BuildHeadway 0.36 or newer for every controller and sender participating in this workflow. Ordinary `/steer TEXT` and `buildheadway steer TEXT` retain their existing behavior.

## Integration boundary

This is a local, deterministic intake interface for directions the caller is authorized to send. A trusted integration can explicitly filter an event, select its owning run and deliver an approved direction. Raw comments or CI output must be evaluated before promoting them to user directions; source labels do not make untrusted text authoritative.

BuildHeadway does not install a webhook, monitor accounts, send messages, run an always-online daemon, restart paused work, or add parallel writers to one project through this interface. Separate project runs keep their existing isolated state; the project execution lock and model, pricing, budget, verification and senior-review gates remain in force. [Steering and hooks](extensions.md#redirect-an-active-team) describe application timing; [harness and recovery](harness.md) describe completion gates.
