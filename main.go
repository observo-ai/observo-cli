// Package main is the entrypoint for the `observo` CLI.
//
// v0.1.0 is a stub release that proves the distribution pipeline
// (GitHub Releases, Homebrew, npm, GHCR, curl-bash, go install) is wired
// end-to-end before any real subcommands land. Real CLI surface ships in
// v0.2.0 per OB-336 (run create/finish, run case set, run attach,
// run pipeline-layer set, plan resolve).
package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
)

// Set by GoReleaser via -ldflags on release builds. Local `go run` and
// dev builds fall back to "dev" + runtime/debug.ReadBuildInfo() so an
// install via `go install github.com/observo-ai/observo-cli@vX.Y.Z`
// still surfaces the module version.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}
	switch os.Args[1] {
	case "--version", "-v", "version":
		printVersion()
	case "--help", "-h", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "observo: unknown command %q\n", os.Args[1])
		fmt.Fprintln(os.Stderr, "Subcommands ship in v0.2.0 (OB-336). For now, only --version / --help are wired.")
		os.Exit(2)
	}
}

func printVersion() {
	v := version
	if v == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
	}
	fmt.Printf("observo %s\n  commit: %s\n  built:  %s\n  go:     %s\n  os:     %s/%s\n",
		v, commit, date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func printUsage() {
	fmt.Println(`observo — CLI for pushing CI test runs, coverage, and live test status to Observo.

USAGE:
  observo <command> [flags]

COMMANDS (v0.1.0):
  version      Print version, commit, build date
  help         Show this help

COMMANDS (planned v0.2.0):
  run create        Create a TestRun from a regression plan
  run finish        Mark a run passed/failed/aborted
  run case set      PATCH a run-case status by short code
  run attach        Upload an artifact (junit, lcov, html) to a run
  run pipeline-layer set
                    Set aggregate stats + attachment IDs for a CI layer
  plan resolve      Read a plan and emit short codes or Playwright --grep regex

ENV:
  OBSERVO_API_KEY   Account-scoped API key (required for v0.2.0 subcommands)
  OBSERVO_BASE_URL  API base URL (default: https://api.observoai.co)

DOCS:
  https://github.com/observo-ai/observo-cli
`)
}
