# observo

CLI for pushing CI test runs, coverage, and live test status to [Observo](https://observoai.co).

[![Release](https://img.shields.io/github/v/release/observo-ai/observo-cli)](https://github.com/observo-ai/observo-cli/releases)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue)](LICENSE)

> **v0.2.0 (OB-B) ships the CLI foundation:** cobra command tree, `OBSERVO_API_KEY` env wiring, retry-aware HTTP client, `--json` output mode. The functional subcommands (`run create`, `run case set`, `run attach`, `run pipeline-layer set`, `plan resolve`) land per-subcommand in v0.3.0..v0.7.0 (OB-C..F). See [OB-336](https://dineviser.atlassian.net/browse/OB-336).

## Install

| Channel | Command |
|---|---|
| **curl** | `curl -fsSL https://cli.observoai.co/install \| bash` |
| **Homebrew** | `brew install observo-ai/tap/observo` |
| **npm** | `npm install -g @observo-ai/cli` |
| **Docker** | `docker run --rm ghcr.io/observo-ai/observo-cli:latest --version` |
| **Go** | `go install github.com/observo-ai/observo-cli@latest` |
| **Binary** | Download for your OS/arch from [Releases](https://github.com/observo-ai/observo-cli/releases) |

Verify:

```bash
observo --version
```

### Pinned version

The curl installer accepts `--version`:

```bash
curl -fsSL https://cli.observoai.co/install | bash -s -- --version v0.1.0
```

Other channels follow each tool's native pinning (`brew install observo-ai/tap/observo@0.1.0`, `npm install -g @observo-ai/cli@0.1.0`, `ghcr.io/observo-ai/observo-cli:v0.1.0`).

## Usage (preview)

```text
observo <command> [global flags]

COMMANDS (v0.2.0):
  version      Print version, commit, build date
  help         Show this help

COMMANDS (planned v0.3.0..v0.7.0, OB-C..F):
  run create               Create a TestRun from a regression plan
  run finish               Mark a run passed/failed/aborted
  run case set             PATCH a run-case status by short code
  run case step set        PATCH a single step's status (live e2e reporter)
  run attach               Upload an artifact (junit, lcov, html) to a run
  run pipeline-layer set   Set aggregate stats + attachment IDs for a CI layer
  plan resolve             Read a plan → emit short codes or Playwright --grep regex

GLOBAL FLAGS:
  --api-key string    Observo API key (overrides $OBSERVO_API_KEY)
  --base-url string   Observo API base URL (overrides $OBSERVO_BASE_URL)
  --json              emit machine-readable JSON instead of human text
  --verbose           log HTTP requests/responses to stderr

ENV:
  OBSERVO_API_KEY   Account-scoped API key (required for any HTTP subcommand).
                    Create one at https://app.observoai.co/settings/api-keys.
  OBSERVO_BASE_URL  API base URL (default: https://api.observoai.co).
                    Override for self-hosted / staging.
```

## Development

```bash
go mod tidy
go build -o observo .
./observo --version
```

Release dry-run (requires [GoReleaser](https://goreleaser.com)):

```bash
goreleaser release --snapshot --clean --skip=publish
```

Cutting a release:

```bash
git tag v0.X.Y
git push origin v0.X.Y
# .github/workflows/release.yml takes it from here.
```

## License

[Apache 2.0](LICENSE)
