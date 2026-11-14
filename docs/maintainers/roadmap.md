# Development roadmap

BuildHeadway should grow through features that users can inspect and rely on. Each iteration should complete one coherent change with behavioral tests and current-revision evidence. A larger feature set is useful when installation, recovery, verification and everyday terminal use remain dependable.

| Priority | Work | Acceptance evidence |
| --- | --- | --- |
| 1. Reliability | Finish saved-state recovery, watcher ownership, cancellation and cross-platform file/process handling. | Reproduce the failure, preserve existing evidence and budgets, run race regressions and execute native Windows/macOS/Linux CI. |
| 2. Discovery and setup | Make free-offer inspection useful, explain stale metadata and blockers, and keep reviewed installations predictable. | Read-only commands preserve bytes; setup refusal persists; discovery never installs announcement scripts or bypasses quota and pricing checks. |
| 3. Agent compatibility | Verify supported CLI controls and structured result protocols against pinned official releases; improve actionable diagnostics. | Separate installed, compatible, zero listed price and successful inference. Use protocol fixtures plus clearly identified optional live checks. |
| 4. Goals and queues | Improve failure search, progress summaries, durable routing, interruption and recovery for larger workloads. | Real subprocess tests cover interruption boundaries, duplicate-safe intake, read-only inspection and resumed work with executed acceptance. |
| 5. Extensions and usability | Improve reusable project templates, discoverable commands and bounded inspection of logs, prompts, offers and saved evidence. | Actual terminal workflows preserve drafts, active goals and selected models; fixtures exercise narrow terminals and Unicode paths/text. |
| 6. Distribution | Keep installation, upgrade, uninstall, artifacts, source and guides consistent across supported hosts. | Six builds, native-host installation checks, checksum/brand integrity, exact tested-source parity and accurate validation records. Publishing remains a separate authorized operation. |

## Current progress

- **0.47:** exclusive discovery ownership is acquired before changing preferences, and foreground cleanup retains ownership until its disabled state is durable.
- **0.48:** saved offers can be searched and filtered in the CLI or terminal app without refreshing discovery or changing stored evidence.

## Development loop

1. Inspect the latest tested branch, open reviews, CI failures and unfinished uploads. Recover missing work before adding another feature.
2. Choose a concrete user outcome or reproducible defect. Keep the change reviewable and retain meaningful existing assertions.
3. Run focused behavioral checks, the complete required race/static/documentation checks, actual terminal workflows and package validation appropriate to the change.
4. Push the tested source to a review branch, verify its exact files against local validation, and inspect native CI for the same revision.
5. Record actual evidence, remaining limits and the next priority. Distinguish fixture replies, cross-builds, native execution and authenticated live service tests.

Do not mark failed or unexecuted checks as passed. Preserve checkpoint compatibility, explicit model choices, freshly checked zero pricing, cumulative budgets, hard provider/tool blockers, executed acceptance, source consistency and senior review. Never rotate around account quotas or treat an announcement as permission to install software. Automated development may stop when authentication, budgets or the environment prevent safe progress; it must report that state accurately.

See [contributing](../../CONTRIBUTING.md), the [validation record](validation.md), [publishing](publishing.md) and the [harness](../harness.md).
