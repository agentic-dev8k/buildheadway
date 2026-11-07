# Contributors and credits

BuildHeadway is maintained by [agentic-dev8k](https://github.com/agentic-dev8k). [Repository history](https://github.com/agentic-dev8k/buildheadway/graphs/contributors) records its commit contributors. Contributions, reviews, regression reports and tested improvements are welcome; see [Contributing](../CONTRIBUTING.md).

## See credits in the terminal

```sh
buildheadway about
buildheadway credits --agent claude
buildheadway about --json
```

Inside the app, use `/about`, `/credits` or `/credits claude`; both commands appear in Ctrl+P. Inspection works during a goal and preserves its connections, counters and cancellation state. Palette selection still requires explicit submission. CLI `--agent NAME` selects one execution adapter. Empty/unknown selectors and extra positional arguments fail clearly.

These commands describe the project and built-in adapter capabilities offline. They work without agent installation, model access, a valid project configuration or writable settings. They do not scan executables, sign in, contact providers or modify project/checkpoint files. Use [agent checks](agents.md) to inspect the current installation and compatibility.

## Agent projects

We acknowledge Claude Code and the other upstream coding-agent projects supported by BuildHeadway:

| Project | BuildHeadway role | Free-worker adapter | BuildHeadway MCP/browser profiles |
| --- | --- | --- | --- |
| [Claude Code](https://github.com/anthropics/claude-code) | Senior planning, review and consultation | No automatic price verifier | Full-access seniors |
| [Codex](https://github.com/openai/codex) | Senior planning, review and consultation | No automatic price verifier | Full-access seniors |
| [OpenCode](https://github.com/anomalyco/opencode) | Seniors and implementation workers through a compatible OpenCode 1 CLI | Fresh verified-zero-price catalog required | Full-access seniors |
| [Pi](https://github.com/earendil-works/pi) | Senior planning, review and consultation | No automatic price verifier | Not forwarded by BuildHeadway |
| [Cline](https://github.com/cline/cline) | Senior planning, review and consultation | No automatic Cline price verifier | Not forwarded by BuildHeadway |
| [Gemini CLI](https://github.com/google-gemini/gemini-cli) | Senior planning, review and consultation | No automatic price verifier | Not forwarded by BuildHeadway |

Capabilities require compatible CLIs and the agent's own account/provider access. Workers and restricted consultations retain their tool limits. Dispatch and retries still require fresh zero-price checks; installation, a free-looking name or a credit entry is not proof of inference or quota. See [MCP support](mcp.md) and [harness boundaries](harness.md).

To use Claude as an actual senior on your own machine, install/sign in through Claude's own flow, then run `buildheadway attach claude` and inspect `buildheadway team`. Existing goals preserve their saved team. A credit entry alone does not change your selected agents or model settings.

## Libraries and contribution records

The terminal uses [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Bubbles](https://github.com/charmbracelet/bubbles) and [Lip Gloss](https://github.com/charmbracelet/lipgloss). MCP uses the [official Go SDK](https://github.com/modelcontextprotocol/go-sdk). Release packages retain their dependency notices under `THIRD_PARTY_LICENSES`.

Credits recognize upstream tooling and its maintainers. They do not assert that an upstream project authored specific BuildHeadway commits or endorses it. AI-assisted contribution records should describe the tools actually used and the checks actually executed, according to repository policy. GitHub's contributor graph is derived from commit history and default-branch inclusion; see [GitHub's contributor documentation](https://docs.github.com/en/repositories/viewing-activity-and-data-for-your-repository/viewing-a-projects-contributors).
