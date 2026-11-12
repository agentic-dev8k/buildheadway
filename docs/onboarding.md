# Setup and free-offer discovery

Open `buildheadway` in your project on Windows, macOS or Linux. The first unconnected launch inspects installed agents, required CLI controls, Node.js/npm/Git and OpenCode's fresh model catalog. It does not run inference. Choose a senior such as Codex, Claude Code, OpenCode, Pi or Cline. Compatible existing tools use their own accounts. Before adding or repairing a missing/incompatible senior or the OpenCode worker, the app shows the fixed official package names and asks for approval. Private Node.js or a Windows shell may also be required. Resize to at least 50×20 to approve a readable installation review.

Choose **Skip installation; watch free offers** to save a persistent refusal and start discovery without downloading agents. Later headless setup and forced repairs honor that refusal. Existing installations remain usable; explicitly approving one platform does not approve every other platform. `/connect` lets you review again. The refusal covers agent installation and its runtime prerequisites, not unrelated explicit browser setup or application updates. Existing headless workflows without a saved refusal retain their previous automatic setup behavior.

For scripts or a second terminal:

```sh
buildheadway setup --json
buildheadway setup --decline-install
buildheadway setup --install opencode
```

Inspection is read-only for connections and projects. `--install` accepts supported agent identifiers, never URLs or package names from an announcement. `attach` can prepare a missing OpenCode worker in existing headless workflows; use `setup --decline-install` first if you want to prohibit automatic downloads.

## Free discovery without X

```sh
buildheadway freebies check
buildheadway freebies status --json
buildheadway freebies watch --interval 30m
```

Discovery prefers the installed compatible OpenCode adapter's freshly refreshed zero-price catalog. Without one, it reads [models.dev's public JSON catalog](https://github.com/anomalyco/models.dev), keeping tool-capable entries with explicitly zero input, output and every listed cost component. Public catalog entries are labeled `catalog_only`; native listings are `listed_zero_price`. Neither proves authentication, available account quota or successful inference. The app does not inspect arbitrary personal files or access saved provider secrets to find offers.

`watch` runs in the foreground until Ctrl+C or `freebies disable` from another terminal. `/freebies watch` runs beside an active coding goal in the terminal app; `/freebies stop` stops only discovery. `freebies enable` opts into discovery on later app launches, without creating a daemon or scheduled task. Intervals are bounded to 15 minutes–24 hours. The app must remain open. One watcher and one publishing check run per user data directory; overlapping checks refuse instead of duplicating network traffic. Network requests are bounded, cancellable and reject redirects. External disable takes effect after an in-flight check; Ctrl+C cancels its context.

Reports retain at most 100 entries, including up to 20 deduplicated X announcements with their original observation times, including when a post is received again. Catalog prices are replaced each check rather than retained as fresh evidence. Terminal output shows at most 20 dated entries, reserving up to ten places for announcements so a large catalog cannot hide them. Remaining places show catalog entries, then any additional announcements. JSON returns the complete bounded report in its original order. `freebies status` performs no network/model call and does not rewrite saved state.

## Optional X search is metered

X's [official API pricing](https://docs.x.com/x-api/getting-started/pricing) is usage based. BuildHeadway cannot offer universally free X search, access private account benefits or promise unlimited free inference. No X credential or paid request is needed for the default catalog path. Cline's [free-model promotions](https://docs.cline.bot/getting-started/free-models) rotate and have their own account and quota limits; Cline is not an automatically verified zero-price implementation worker.

If you deliberately enable the official [recent-search API](https://docs.x.com/x-api/posts/search-recent-posts), place your bearer token in an environment variable using your terminal's environment settings, then authorize a small number of attempts:

```sh
buildheadway freebies x-enable --token-env X_BEARER_TOKEN --requests 5 --acknowledge-metered
buildheadway freebies check
buildheadway freebies x-disable
```

The token value is read only at the network boundary and never saved; configuration keeps its variable name. Search is a fixed coding/AI-offer query sampling at most ten recent results per request, not an exhaustive archive: further result pages are not fetched, and the recent-search window does not cover an indefinite offline period. Attempts are persisted **before** HTTP, shared across restarts and concurrent callers, and never refunded after ambiguous failures. `--requests` adds to this lifetime allowance rather than resetting usage. This caps attempts, **not dollar spending**: configure actual billing limits in your X account. Configuration itself sends no request.

A successful page's cursor and its announcements are saved together. Failed validation or publication cannot advance the cursor alone. Received post IDs must be newer than the requested cursor, and a nonempty page must identify its newest received post; decimal IDs are compared exactly. This guards received-page consistency, not completeness of X's search results.

If another terminal disables or reauthorizes X while a request is pending, the result is marked `superseded`. Its reserved attempt stays counted and received announcements can remain unverified history, but its old cursor and account blocker cannot replace the newer settings. An explicit `freebies check` exits unsuccessfully for this status; inspect settings and run another check if wanted. Changing only the discovery watch interval does not invalidate a response. A hard blocker received under unchanged authorization is saved immediately, even if cancellation or a later report save fails.

Authentication/payment/permission/quota responses (401/402/403/429) latch a blocker. Watch checks can continue reading free catalogs but will not retry X until an explicit `x-enable` adds an allowance and acknowledges metering again. Missing credentials do not consume an attempt. Malformed, partial or oversized replies are not successful searches. No live authenticated X request is part of the account-free regression suite.

## Offers never become executable instructions

X posts are labeled `unverified_announcement` and link only to their X status page. Their URLs, commands and claims are never followed, installed, executed, added to agent prompts or used to change selected models. Discovery cannot resume a blocked goal, rotate around account quotas or supply acceptance evidence. Review offers independently and approve only a supported installation you want.

Every free dispatch/retry still refreshes prices independently. Explicit model choices, cumulative budgets, authentication/quota/payment/policy/cost/output/MCP blockers, executed meaningful acceptance checks, unchanged verification snapshots and fresh senior approval remain required. Checkpoint v12 is unchanged. See [agent compatibility](agents.md), the [harness](harness.md) and [terminal usage](usage.md).
