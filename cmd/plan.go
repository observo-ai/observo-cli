package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/observo-ai/observo-cli/internal/api"
	"github.com/observo-ai/observo-cli/internal/config"

	"github.com/spf13/cobra"
)

// planCmd groups plan-* subcommands. Today only `resolve`; future
// stories may add `add-case`, `remove-case`, `list`.
var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Operations on Observo Test Plans (resolve plan → short codes)",
}

var (
	prProject string
	prPlan    string
	prFormat  string
)

// planResolveCmd: read a plan by key, list its cases, emit short codes
// in one of three formats useful for CI piping.
var planResolveCmd = &cobra.Command{
	Use:   "resolve",
	Short: "Read a plan and emit its case short codes (codes / grep / json)",
	Long: `Fetch a plan by --plan (key or UUID) and emit the attached case
short codes in the requested format.

Formats:
  codes  (default)  Newline-delimited short codes, one per line.
                    Suitable for bash piping:
                      observo plan resolve --plan REGR --project OB | xargs ...

  grep              Playwright-friendly --grep regex:
                      @observo:(OB-50|OB-51|OB-52)
                    Pipe directly into the Playwright invocation:
                      npx playwright test --grep "$(observo plan resolve \
                        --plan REGR --project OB --format grep)"

  json              Full plan object via the global --json flag. Same as
                    --json with format=codes; included for symmetry.

Empty plan → empty output (codes) or 'NONE_MATCH_REGEX' sentinel (grep,
so Playwright skips everything rather than matching all). Caller should
check exit code (0 = found, non-zero = error contacting API).`,
	Args: cobra.NoArgs,
	RunE: planResolveExec,
}

// neverMatchRegex is what we emit when a plan is empty in --format=grep
// mode. Playwright's --grep takes a regex; if we emit "" it matches all
// tests, which is the OPPOSITE of caller intent. This sentinel matches
// nothing (literal $$ is impossible inside a normal test name).
const neverMatchRegex = "NEVER_MATCH_OBSERVO_PLAN_EMPTY_$$"

func init() {
	rootCmd.AddCommand(planCmd)
	planCmd.AddCommand(planResolveCmd)

	f := planResolveCmd.Flags()
	f.StringVar(&prProject, "project", "", "project UUID or short code (required)")
	f.StringVar(&prPlan, "plan", "", "plan key or UUID (required)")
	f.StringVar(&prFormat, "format", "codes", "codes | grep | json")
	_ = planResolveCmd.MarkFlagRequired("project")
	_ = planResolveCmd.MarkFlagRequired("plan")
}

func planResolveExec(cmd *cobra.Command, _ []string) error {
	cfg := config.Resolve(flagAPIKey, flagBaseURL, flagJSON, flagVerbose)
	if err := cfg.Validate(); err != nil {
		return err
	}
	switch prFormat {
	case "codes", "grep", "json":
	default:
		return fmt.Errorf("invalid --format %q (allowed: codes, grep, json)", prFormat)
	}

	client, err := api.New(api.Options{
		BaseURL:   cfg.BaseURL,
		APIKey:    cfg.APIKey,
		UserAgent: userAgent(),
		Verbose:   cfg.Verbose,
	})
	if err != nil {
		return err
	}
	plan, err := client.GetPlan(context.Background(), prProject, prPlan)
	if err != nil {
		return fmt.Errorf("resolve plan: %w", err)
	}

	codes := make([]string, 0, len(plan.Cases))
	for _, c := range plan.Cases {
		if c.ShortCode != "" {
			codes = append(codes, c.ShortCode)
		}
	}

	out := cmd.OutOrStdout()
	switch prFormat {
	case "json":
		// Use global --json shape — encode the plan via output.Printer.
		// Bypass: just write the raw plan struct so callers get full data.
		// (Format-json is a superset of --json; both invoke the same path.)
		return writeJSON(out, plan)
	case "grep":
		fmt.Fprintln(out, buildGrepRegex(codes))
	default: // codes
		for _, c := range codes {
			fmt.Fprintln(out, c)
		}
	}
	return nil
}

// buildGrepRegex composes a Playwright-compatible --grep alternation.
// Each short code becomes a regex literal (only `-` and `.` need escape
// in the basic Playwright dialect; short codes are typically [A-Z]+-[0-9]+
// so no escaping needed in practice — but we escape defensively).
//
// Empty input returns the neverMatchRegex sentinel so an empty plan
// causes Playwright to run zero tests rather than every test.
func buildGrepRegex(codes []string) string {
	if len(codes) == 0 {
		return neverMatchRegex
	}
	esc := make([]string, len(codes))
	for i, c := range codes {
		esc[i] = regexEscapeShortCode(c)
	}
	return "@observo:(" + strings.Join(esc, "|") + ")"
}

// regexEscapeShortCode escapes the characters that could appear in
// Observo short codes (project_code-int) and have regex meaning. project
// codes are uppercase ASCII letters; ints are digits — no metachars in
// the wild. Defensive escape only for `.` and `-` (no-op in character
// classes but harmless outside).
func regexEscapeShortCode(s string) string {
	r := strings.NewReplacer(".", `\.`, "-", `\-`)
	return r.Replace(s)
}

// writeJSON is the only place we emit raw JSON outside the output.Printer —
// here the user explicitly asked for full plan via --format=json.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
