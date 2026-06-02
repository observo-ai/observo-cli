# observo

CLI for pushing CI test runs, coverage, and live test status to [Observo](https://observoai.co).

[![Release](https://img.shields.io/github/v/release/observo-ai/observo-cli)](https://github.com/observo-ai/observo-cli/releases)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue)](LICENSE)

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
curl -fsSL https://cli.observoai.co/install | bash -s -- --version v0.7.1
```

Other channels follow each tool's native pinning (`brew install observo-ai/tap/observo@0.7.1`, `npm install -g @observo-ai/cli@0.7.1`, `ghcr.io/observo-ai/observo-cli:v0.7.1`).

## Usage

```text
observo <command> [global flags]

COMMANDS:
  version                  Print version, commit, build date
  help                     Show this help
  run create               Create a TestRun from a regression plan
  run finish               Mark a run passed/failed/aborted
  run case set             PATCH a run-case status (+ optional --comment) by short code
  run case step set        Update a single step's status within a run case
  run attach               Upload an artifact (junit, lcov, html, ...) to a run
                           or a specific run-case (--case / --code)
  run pipeline-layer set   Set aggregate stats + attachment IDs for a CI layer
  run import --from playwright <dir>
                           Bulk post-mortem import of a Playwright results dir
                           (cases, steps, video/trace/screenshot, plus extracted
                           console.json / network.json / failure.json). See
                           "Importing Playwright runs" below.
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

## Importing Playwright runs

After `npx playwright test` finishes, `observo run import --from playwright <dir>`
walks the test-results directory, parses `results.json` (the Playwright JSON
reporter), and uploads everything per run-case to Observo. Designed as a
post-mortem complement to the live `@observo/playwright-reporter` — either
works in isolation; both against the same run is safe (last writer wins).

### Prerequisites

Enable Playwright's JSON reporter so a parseable `results.json` is written:

```ts
// playwright.config.ts
export default defineConfig({
  reporter: [
    ['list'],
    ['json', { outputFile: 'playwright-report/results.json' }],
  ],
});
```

### Usage

Replace `WEB` with your own project code (the short prefix shown in
your Observo dashboard, e.g. `WEB`, `MYAPP`, `CHECKOUT`):

```bash
# 1. Pre-create the run (carries CI metadata: commit, branch, actor, …).
observo run create --project WEB --plan REGRESSION

# 2. Run Playwright.
npx playwright test

# 3. Bulk-import everything into the run created in step 1.
observo run import --from playwright ./test-results --run "$RUN_KEY"
```

### Short-code resolution

Each Playwright test maps to one Observo case by its short code (e.g.
`WEB-7`, where `WEB` is your project prefix). The importer looks at three
sources, in order, and stops at the first match:

1. **Explicit tag** `@observo:WEB-7` (recommended — unambiguous):
   ```ts
   test('login redirect', { tag: ['@observo:WEB-7'] }, async ({ page }) => { … });
   ```
2. **Code in test or describe title** (e.g. `test('WEB-7 redirect after submit', …)`).
3. **Code in the attachment directory name** (last-resort fallback for
   suites that don't tag yet — Playwright's auto-generated dir name often
   carries the code, e.g. `login-WEB-7-chromium/`).

Tests with no resolvable code are skipped with a warning; the import does
not fail.

### What gets uploaded per failed case

| Artifact            | Source                                                       |
|---------------------|--------------------------------------------------------------|
| `video.webm`        | `result.attachments[]` with name `video`                     |
| `trace.zip`         | `result.attachments[]` with name `trace`                     |
| `screenshot.png`    | `result.attachments[]` (any `image/*` attachment)             |
| `console.json`      | Extracted from `trace.zip` (browser console events)          |
| `network.json`      | Extracted from `trace.zip` (request/response pairs)          |
| `failure.json`      | Shaped from `result.errors[]` + source excerpt               |

`console.json` and `network.json` are machine-readable, so downstream
tools (dashboards, AI failure-analysis agents) can consume them
without binary parsing.

Passed cases skip attachment upload by default — pass `--upload-passed` to
include them.

### Redaction

The default `--redact` regex matches `Authorization`, `password`, `token`,
`api_key`, `bearer`, `secret`. Whole lines containing any match are replaced
with `<redacted by observo>` in `network.json` bodies and `failure.json`
messages. Extend with `--redact '(?i)session_id'` (combined with the default,
not replacing it).

### Dry-run

```bash
observo run import --from playwright ./test-results --run RUN-42 --dry-run
```

Parses the directory and prints the plan (which short codes resolve, which
attachments would upload) without making API calls. No `OBSERVO_API_KEY`
required.

### Extract-only mode

When your CI already runs the Observo Playwright reporter to stream live
status + raw artifacts during the test run, use `--extract-only` to add
just the extracted `console.json` / `network.json` / `failure.json`
artifacts afterwards — without re-updating case status or re-uploading
the video / trace / screenshot files the reporter already pushed.

```bash
observo run import --from playwright ./test-results --run RUN-42 --extract-only
```

Skipped in this mode: case status updates, per-step status updates, raw
artifact uploads.

Still uploaded: the extracted `console.json`, `network.json` (from
`trace.zip`), and `failure.json` (from the test's error objects).

Running the default import alongside the live reporter would create
duplicate attachments in the dashboard; `--extract-only` is the clean
composition.

## Contributing

Local build:

```bash
go mod tidy
go build -o observo .
./observo --version
```

Issues and PRs welcome at <https://github.com/observo-ai/observo-cli>.

## License

[Apache 2.0](LICENSE)
