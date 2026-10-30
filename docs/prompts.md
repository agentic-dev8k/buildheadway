# Automatic instructions and handoffs

Start `buildheadway` in your project and describe the result you want. You do not need to select `/goal` or repeatedly type "continue". A normal request starts the same senior-team workflow; BuildHeadway generates the planning, implementation, consultation, repair and review instructions for each invocation.

For example:

```text
Build a task tracker with persistent storage, search and tests.
Preserve existing data and document how to start it.
```

The team works within that request. It stops when current executable evidence and senior review support completion, you pause it, a configured budget is reached, or a genuine blocker requires attention. Keep the app running. Finishing a task does not authorize unrelated new projects. These controls belong to BuildHeadway; they do not activate an attached agent's own Goal mode.

## What each agent receives

| Invocation | Instructions and evidence |
| --- | --- |
| Planning | Inspect the repository, identify the next coherent batch and write distinct, ordered tasks with an outcome, relevant files/interfaces, constraints, dependencies and verification expectations. |
| Implementation | A structured work order containing the exact current goal and steering, full assigned task, durable task key, host OS/shell, required and suggested checks, criteria and acceptance mappings. Previous attempts and logs help it choose the next unfinished action. |
| Consultation | A specific question and relevant evidence. The adviser recommends a next step using file inspection; it cannot execute implementation or approve the goal in this call. |
| Repair or continuation | Inspect current files, preserve partial work, assess actual teammate answers and change a failed approach. Bounded previous-report and retry excerpts retain both ends and identify truncation. |
| Review | Compare the complete request with current files, requirement coverage, check and hook evidence. Request a concrete repair or fresh verification before approving missing evidence. |

Reports are asked to use three concise sections inside the existing report field:

- **Changed:** the behavior and files changed.
- **Verification:** exact commands actually executed and their observed outcomes, plus anything not run and why.
- **Remaining:** unfinished work or a concrete blocker and recommended next step; "none" applies only to the assigned task.

An agent's report remains a claim to assess. BuildHeadway independently executes checks, enforces unchanged-source verification and obtains senior approval. Prompt wording cannot guarantee model obedience or prove that an unexecuted check passed. See [harness and recovery](harness.md) and [browser acceptance](browser.md).

## Keep your requirements intact

The instructions ask agents to preserve existing work, follow relevant repository guidance, distinguish assumptions from observations and stay within the user's scope. Work orders keep the original goal and applied steering instead of replacing them with a model-written paraphrase. JSON evidence sections keep questions, reports and retry errors separate from role instructions; this separation is not a complete prompt-injection security boundary.

Prompt generation does not shorten active requirements, raise the configured context allowance or bypass provider/tool blockers. Existing [prompt admission](harness.md#agent-context-admission-027) and evidence excerpt limits still apply. An oversized prompt pauses for explicit resolution; it does not silently discard requirements or select another model.

## Customize the team's guidance

Put project conventions and important constraints in `BUILDHEADWAY.md` or the supported guidance files. Use [skills and reusable prompts](extensions.md) for recurring tasks. Apply `/steer TEXT` to redirect an active request and inspect `/discussion`, `/tasks`, `/acceptance` and `/context` to see the team's work and evidence. These use the existing workflow; no new command or switch is needed for professional instructions.

Updated application code supplies these instructions to new invocations, including resumed goals. The saved goal, team, settings, checks and budgets stay pinned; checkpoint v12 and the agent response protocols are unchanged.

Use [`buildheadway inputs`](inputs.md) to inspect the actual saved prompt text for a dispatched agent call. This reads existing evidence and never invokes an agent or regenerates its instructions.
