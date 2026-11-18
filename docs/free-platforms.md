# Free platforms

OpenCode is the default zero-price pool. Full-access goals can also use Cline promotions and OpenRouter free variants through the installed Cline CLI. BuildHeadway checks the current provider catalog before dispatch and pins the selected provider and model. A known provider limit holds that platform out of the rest of the run. Automatic selection then checks the other enabled platforms. Explicit model choices pause at their provider limit.

## Inspect available platforms

```sh
buildheadway freebies platforms
buildheadway freebies platforms --check
buildheadway scan --models --agent opencode
```

The first command prints setup details. The second reads the official public Cline, OpenRouter, and Command Code catalogs and checks the local Codex sign-in type. Use `--json` for scripts. In the terminal app, use `/freebies platforms check` to refresh those checks. Catalog results describe published offers. Account eligibility and remaining quota are determined by the provider.

| Platform | How BuildHeadway uses it | Setup |
| --- | --- | --- |
| OpenCode | Default automatic pool | A compatible OpenCode CLI |
| Cline | Optional automatic pool from its official free promotion list | Cline CLI and native sign-in to the managed profile |
| OpenRouter | Optional automatic pool of tool-capable free variants with zero listed prices | Cline CLI and `OPENROUTER_API_KEY` |
| Command Code | Discovery of published free model IDs | Manage the account and background feature models in Command Code |
| Codex | Senior connection using your own account allowance | Sign in with ChatGPT through Codex |

Command Code lets separate models handle features such as title generation, compaction, and sub-agents. Its public model API lists IDs and supported endpoints. Use its model and account settings when choosing a free variant. See [Command Code models](https://commandcode.ai/models), [the model API](https://commandcode.ai/docs/provider) and [feature models](https://commandcode.ai/docs/settings).

## Enable Cline and OpenRouter

Install a compatible Cline CLI using `buildheadway setup --install cline` or the official [Cline instructions](https://docs.cline.bot/cli/cli-reference). To enable Cline's own promotions, sign in through the native CLI with the managed profile:

```sh
buildheadway freebies platforms login cline
```

To enable OpenRouter, set `OPENROUTER_API_KEY` in the environment for this terminal. Use a key you control. The key is passed to the native CLI via its environment. See [OpenRouter free models](https://openrouter.ai/collections/free-models/) and [Cline promotions](https://docs.cline.bot/getting-started/free-models).

Choose either platform or both and start BuildHeadway in the same terminal.

```sh
export BUILDHEADWAY_FREE_PLATFORMS=cline,openrouter
buildheadway
```

In PowerShell:

```powershell
$env:BUILDHEADWAY_FREE_PLATFORMS = 'cline,openrouter'
buildheadway
```

Enabled platforms may receive project context during eligible full-access goals. Workspace-mode goals and restricted consultations use OpenCode's free route. Seniors with required MCP servers use an adapter that supports those servers. `/models` lists eligible workers for full-access goals, including the platform in each ID. `/routing` shows decisions and provider limits.

For Codex, ChatGPT Free includes an account allowance that varies by plan. Choose Codex in `/connect` and use native ChatGPT sign-in. Use Codex to inspect the remaining allowance. See [Codex with your ChatGPT plan](https://help.openai.com/en/articles/11369540-using-codex-with-your-chatgpt-plan).
