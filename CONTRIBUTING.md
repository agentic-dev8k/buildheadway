# Contributing to BuildHeadway

BuildHeadway is a Go terminal app requiring Go 1.25 or newer to build from source. Node.js/npm and Python are needed only to build and validate distribution packages; connected coding agents manage their own runtimes and accounts. Installed release packages need no Go compiler.

From a source checkout:

```sh
go test -buildvcs=false -race -timeout=300s ./...
go vet -buildvcs=false ./...
node --test npm/test/docs.test.cjs
node npm/check-docs.cjs
go build -buildvcs=false -trimpath -o buildheadway .
```

On macOS/Linux, test the actual terminal conversation with simulated agents and real Node.js checks:

```sh
python3 scripts/terminal_conversation_test.py /absolute/path/to/buildheadway
```

Build all six binaries and the installable package:

```sh
python3 release.py --output dist
```

The release command runs source tests, native smoke/installer checks and actual npm install/launch/upgrade/integrity/removal tests. Cross-compilation alone does not validate native execution on another OS. CI runs race and package tests on Windows, macOS, and Linux. Ordinary tests make no model calls; opt-in service tests and their requirements are described in [terminal usage](docs/usage.md#source-and-rebuilding).

Keep regression coverage for fresh zero-price dispatch, explicit model choices, budgets, provider blockers, task handoffs, saved-run compatibility, executed acceptance evidence, source consistency, and senior vetoes. Do not make a failing gate pass merely to finish a goal.

Keep the root README concise. Put user guides under `docs/` and maintainer records under `docs/maintainers/`; fix links and package coverage when moving them. Preserve the approved image bytes. See [publishing](docs/maintainers/publishing.md) for publication, which is separate from building a local package.
