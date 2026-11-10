# Check your coding agents

BuildHeadway finds installed executables; the agent's model access still depends on its provider and account. Check installation, compatibility and current zero-price metadata separately:

```sh
buildheadway scan
buildheadway scan --check --agent cline
buildheadway scan --models --agent opencode
```

Add `--json` for scripts. `--agent NAME` selects one agent. Missing or incompatible explicitly selected agents exit nonzero. `--models` also checks compatibility and exits nonzero if the selected agent has no supported, verified free catalog. Without a selector, expected missing agents and detection-only entries do not fail the inventory; installed incompatible adapters and a failed OpenCode catalog do.

The plain scan reports paths and versions. `--check` probes required CLI controls without a model call. `--models` refreshes OpenCode's catalog and lists only tool-capable models with explicit zero input/output prices and no nonzero listed cache or other charges. Its `checked_at` is the time of this inspection. It does not test authentication, quota, latency, or successful inference. These diagnostics do not change project files, checkpoints, connections or model selection; the upstream CLI may maintain its own catalog cache.

Every implementation dispatch and retry refreshes pricing again. A past scan never authorizes a later model call. A free-looking name, installed CLI, subscription allowance, or rotating promotion is not zero-price proof.

## OpenCode

The supported adapter currently uses **OpenCode 1**, installed from the official `opencode-ai` package:

```sh
npm install --global opencode-ai
opencode --version
buildheadway scan --models --agent opencode
```

Both OpenCode 1 and OpenCode 2 provide an `opencode` command. OpenCode 2 (`@opencode/cli`) has a different interface and lacks the plugin-free controls required here. BuildHeadway detects it but blocks execution with the supported package named in the error. It does not substitute weaker controls. Check the printed executable path; use a supported installation on PATH or set `BUILDHEADWAY_AGENT_PATH` to its containing directory. See [installation and PATH troubleshooting](install.md).

After selecting your senior connection in `buildheadway`, give it a goal:

```sh
cd my-project
buildheadway figureout "Fix the failing tests and verify the repair"
```

Only fresh verified-zero-price workers are selected. Missing authentication, exhausted free usage, provider restrictions or reported charges stop the goal. BuildHeadway does not purchase credits or switch models to evade an account limit. OpenCode versions exposing error diagnostics are asked for error-level logs, allowing the harness to interrupt a session that remains open after a provider rejects inference. The original diagnostic stays in the saved invocation log.

## Cline and other agents

Install Cline's CLI separately from its editor extension:

```sh
npm install --global cline
cline auth
buildheadway scan --check --agent cline
```

Sign in through Cline's own terminal flow. Do not put passwords or keys in a BuildHeadway prompt. A compatible Cline CLI can be attached as a senior using an explicitly chosen provider/model; see [connecting seniors](usage.md#figureout-give-your-coding-team-a-goal). Cline 3's final `run_result` and older text events are supported. Failed or aborted final results cannot establish completion; authentication messages remain actionable blockers.

| Agent | Execution adapter | Automatic verified-free workers |
| --- | --- | --- |
| OpenCode 1 (`opencode-ai`) | Yes | Fresh supported catalog required |
| OpenCode 2 (`@opencode/cli`) | Incompatible; detected with an explanation | No |
| Cline CLI | Senior adapter; compatible CLI and provider access required | No Cline pricing verifier yet |
| Codex, Claude Code, Pi, Gemini CLI | Senior adapters; required controls and account access required | No automatic free-price verifier |
| Other inventory entries | Detection only | No |

Cline's free promotions rotate, require a Cline account, and have usage quotas. They are available through its own CLI/extension, not its API. BuildHeadway does not yet have a reliable fresh Cline pricing verifier, so it does not advertise Cline as a verified-free implementation worker. Local model runtimes and arbitrary OpenAI-compatible endpoints are also not automatically trusted as zero-priced workers.

## What was actually tested

On 10 November 2026, the retained BuildHeadway 0.43.0 package was downloaded, SHA256-verified and installed with npm offline and scripts disabled in an isolated Linux amd64 prefix. Official npm packages `opencode-ai@1.19.5` and `@opencode/cli@2.2.6` were downloaded and installed separately, retaining npm integrity metadata. Installed BuildHeadway detected OpenCode 1.19.5, passed its required controls and refreshed **12** qualifying zero-price entries. It independently detected OpenCode 2.2.6 and rejected the incompatible interface without inference.

A bounded real arithmetic-repair goal used automatic verified-free senior selection and fresh pricing. The provider returned **Free usage limit reached** on `opencode/beam-free`. BuildHeadway saved blocked status, one planner call, zero worker calls and auth/quota routing evidence. Existing failing tests and implementation bytes stayed unchanged. It did not rotate models to evade quota. No successful implementation or premium inference is claimed. These are Linux results; they do not establish native Windows/macOS model access or another user's quota.

Earlier real Linux checks on 3 November 2026 inspected Cline 3.0.74; its account-free probe returned `Unauthorized`. No new Cline authentication or inference was attempted on November 10. Fixture regressions replay provider failures and cost receipts through actual subprocesses; they are distinct from successful live inference. See the [validation record](maintainers/validation.md) for executed release checks and access limits.

Official OpenCode references rechecked 10 November 2026; Cline references last checked 3 November 2026: [OpenCode installation](https://opencode.ai/docs/), [OpenCode CLI](https://opencode.ai/docs/cli/), [Cline CLI](https://docs.cline.bot/usage/cli-overview), and [Cline free models](https://docs.cline.bot/getting-started/free-models).
