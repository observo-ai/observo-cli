// Package cmd hosts the cobra command tree. main.go calls Execute().
package cmd

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

// Build-info vars are set by GoReleaser via -ldflags on release builds.
// Local `go build` and `go run` leave them as their zero values; the
// version command falls back to runtime/debug.ReadBuildInfo() so a
// `go install github.com/observo-ai/observo-cli@vX.Y.Z` still prints a
// useful version.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Globally-shared flags populated by cobra before any subcommand runs.
// Subcommands read these via package-level vars to keep their signatures
// small (cobra idiom).
var (
	flagAPIKey  string
	flagBaseURL string
	flagJSON    bool
	flagVerbose bool
)

// userAgent is sent on every HTTP request; helps server-side telemetry
// attribute load to CLI vs SDK callers.
func userAgent() string {
	return fmt.Sprintf("observo-cli/%s (%s; %s/%s)", Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

var rootCmd = &cobra.Command{
	Use:   "observo",
	Short: "CLI for pushing CI test runs, coverage, and live test status to Observo",
	Long: `observo — pushes CI test runs, coverage, and live test status to Observo.

Designed for CI orchestrators that want to create a TestRun at the start
of a pipeline, attach per-layer JUnit + LCOV artifacts as each test layer
finishes, and PATCH per-case status live (so the run page updates in
real time during a merge).

AUTH
  Account-scoped API key, read from OBSERVO_API_KEY env or --api-key.
  Create one at https://app.observoai.co/settings/api-keys.

DOCS
  https://github.com/observo-ai/observo-cli`,
	// Disable the default cobra completion subcommand until we wire it.
	CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	// Don't auto-show usage on runtime errors — they're not usage problems.
	SilenceUsage: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagAPIKey, "api-key", "",
		"Observo API key (overrides $OBSERVO_API_KEY)")
	rootCmd.PersistentFlags().StringVar(&flagBaseURL, "base-url", "",
		"Observo API base URL (overrides $OBSERVO_BASE_URL; default https://api.observoai.co)")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false,
		"emit machine-readable JSON instead of human text")
	rootCmd.PersistentFlags().BoolVar(&flagVerbose, "verbose", false,
		"log HTTP requests/responses to stderr")
}

// Execute runs the root command. Returns the exit code so main.go can
// `os.Exit(cmd.Execute())` without importing os in two places.
func Execute() int {
	if err := rootCmd.Execute(); err != nil {
		// Cobra already printed err to stderr; exit non-zero so CI scripts
		// can detect failure without parsing output.
		_ = err
		return 1
	}
	return 0
}

// stdout indirection so tests can capture output of subcommands that
// don't take an io.Writer parameter. Defaults to os.Stdout.
var stdout = func() *os.File { return os.Stdout }
