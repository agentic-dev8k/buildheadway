# Free platforms

OpenCode is the default zero-price pool. Full-access goals can also use Cline promotions, OpenRouter free variants, keyless Kilo Gateway offers, and local models in Ollama, LM Studio, llama.cpp or SGLang. BuildHeadway checks independent platforms concurrently and combines the catalogs in OpenCode-first order, followed by the order in `BUILDHEADWAY_FREE_PLATFORMS`. It refreshes eligibility before dispatch and pins the selected provider and model. A known provider limit holds that platform out of the rest of the run. Automatic selection then checks the other enabled platforms. Explicit model choices pause at their provider limit.

## Inspect available platforms

```sh
buildheadway freebies platforms
buildheadway freebies platforms --check
buildheadway scan --models --agent opencode
```

The first command prints setup details. The second reads the official public Cline, OpenRouter, Kilo, and Command Code catalogs, checks local model readiness, and checks the local Codex sign-in type. Use `--json` for scripts or `/freebies platforms check` in the terminal app. Providers determine hosted eligibility and quota.

| Platform | How BuildHeadway uses it | Setup |
| --- | --- | --- |
| OpenCode | Default automatic pool | A compatible OpenCode CLI |
| Cline | Optional automatic pool from its official free promotion list | Cline CLI and native sign-in to the managed profile |
| OpenRouter | Optional automatic pool of tool-capable free variants with zero listed prices | Cline CLI and `OPENROUTER_API_KEY` |
| Kilo Gateway | Optional keyless pool of tool-capable free variants and Kilo Auto Free | Native Kilo CLI |
| Command Code hosted | Discovery of published free model IDs | Manage offers in the native Command Code CLI |
| Ollama through Command Code | Optional automatic pool of installed, tool-capable local models | Ollama running on loopback and a compatible native Command Code CLI with headless sign-in |
| Ollama through Pi | Optional keyless pool of running, tool-capable local models | Pi and Ollama on `127.0.0.1:11434` |
| LM Studio through Pi | Optional pool of loaded tool-capable models | Pi and the local LM Studio server on `127.0.0.1:1234` |
| llama.cpp through Pi | Optional loaded model with a tool-capable chat template | Pi and a single-model `llama-server` on `127.0.0.1:8080` |
| SGLang through Pi | Optional served model with a configured tool-call parser | Pi and a recent SGLang server on `127.0.0.1:30000` |
| Codex | Senior connection using your own account allowance | Sign in with ChatGPT through Codex |

The Command Code public model API lists hosted IDs and supported endpoints. The automatic Command Code route uses the local Ollama catalog below. See [Command Code models](https://commandcode.ai/models) and [the model API](https://commandcode.ai/docs/provider).

## Enable Kilo Gateway

Install the native Kilo CLI and enable its route in the same terminal.

```sh
npm install --global @kilocode/cli
buildheadway freebies platforms --check
export BUILDHEADWAY_FREE_PLATFORMS=kilo
buildheadway
```

In PowerShell use `$env:BUILDHEADWAY_FREE_PLATFORMS = 'kilo'`.

BuildHeadway uses an isolated Kilo profile and verifies tool support and zero pricing against the live Gateway catalog. Eligible models appear with a `kilo/` prefix in `/models`. Kilo Gateway allows anonymous free requests and applies quota per IP address. See [Kilo Gateway authentication](https://kilo.ai/docs/gateway/authentication) and [models and providers](https://kilo.ai/docs/gateway/models-and-providers).

## Enable Ollama through Command Code

Install [Ollama](https://ollama.com/download) and the native Command Code CLI. Start Ollama on `127.0.0.1:11434` and install a tool-capable model sized for your machine. Configure [Ollama's local-only setting](https://docs.ollama.com/faq#how-do-i-disable-ollama-s-cloud-features) if you also want to turn off the daemon's cloud features.

```sh
npm install --global command-code
command-code login
buildheadway freebies platforms setup ollama
buildheadway freebies platforms --check
export BUILDHEADWAY_FREE_PLATFORMS=ollama
buildheadway
```

In PowerShell use `$env:BUILDHEADWAY_FREE_PLATFORMS = 'ollama'`.

Command Code's headless CLI requires an existing native login. BuildHeadway launches it in local-only mode with the selected installed model and a keyless loopback endpoint. Setup adds the `buildheadway-ollama` provider to `~/.commandcode/providers.json` and preserves other providers. Run setup again after installing new models. Any Command Code feature model overrides must also resolve to verified models in that local provider. See [Command Code headless authentication](https://commandcode.ai/docs/headless) and [Ollama BYOK configuration](https://commandcode.ai/docs/byok).

Eligible models appear as `ollama/<model>` in `/models`. Ollama uses your hardware and has no hosted inference quota. BuildHeadway checks installed model metadata before dispatch and verifies the model recorded by the native CLI.

## Enable local servers through Pi

Install a compatible Pi CLI with `buildheadway setup --install pi`. These routes speak OpenAI-compatible Chat Completions over loopback and use a temporary Pi profile.

- For **LM Studio**, load a tool-capable model and start its local API server on port 1234 in its default keyless mode. [LM Studio server setup](https://lmstudio.ai/docs/developer/rest/quickstart)
- For **llama.cpp**, use a model with a tool-capable chat template. Run `llama-server -m /path/to/model.gguf --host 127.0.0.1 --port 8080 --jinja`. [llama.cpp server guide](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)
- For **Ollama**, start Ollama in local-only mode and run a local model that supports tools, for example with `ollama run qwen3:8b`. Keep it loaded while using BuildHeadway. This route requires no account and only offers models reported as running with a known context length. [Running Ollama models](https://docs.ollama.com/api/ps) · [Ollama local-only mode](https://docs.ollama.com/faq#how-do-i-disable-ollama-s-cloud-features)
- For **SGLang**, start a keyless loopback server with a tool-capable model, a served name, and its matching `--tool-call-parser`. BuildHeadway needs `/model_info` to report the served name and parser, and `/v1/models` to report the model's context length. [SGLang server arguments](https://docs.sglang.io/docs/advanced_features/server_arguments) · [SGLang tool parsers](https://docs.sglang.io/docs/advanced_features/tool_parser)

```sh
python -m sglang.launch_server --model-path Qwen/Qwen3-8B --served-model-name qwen3-8b \
  --tool-call-parser qwen25 --host 127.0.0.1 --port 30000
```

In the terminal running BuildHeadway, select the routes for the servers you have started.

```sh
export BUILDHEADWAY_FREE_PLATFORMS=ollama-pi,sglang
buildheadway freebies platforms --check
buildheadway
```

In PowerShell use `$env:BUILDHEADWAY_FREE_PLATFORMS = 'ollama-pi,sglang'`. The four Pi routes are `lmstudio`, `llamacpp`, `ollama-pi` and `sglang`. Models appear in `/models` as `<route>/<model>`. BuildHeadway cross-checks each loaded model against the server's OpenAI-compatible model list and restricts inference to the selected model on loopback.

If a local server is temporarily unavailable or returns an incomplete reply, BuildHeadway checks the eligible backups. Worker edits made before a failure enter verification before any further assignment. A local redirect or keyless access refusal pauses the goal so you can repair the server configuration. `/routing` shows model health and cooldowns.

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
