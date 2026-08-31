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
                      @observo:(OB-50|OB-51|OB-52)(?![0-9])
                    Capture it FIRST and check the exit status — on failure
                    stdout is empty, and an empty --grep matches EVERY test:
                      g=$(observo plan resolve --plan REGR --project OB \
                            --format grep) || exit 1
                      npx playwright test --grep "$g"

  json              Full plan object via the global --json flag. Same as
                    --json with format=codes; included for symmetry.

Empty plan → empty output (codes) or a never-match sentinel (grep, so
Playwright skips everything rather than matching all). An empty plan is a
successful answer and exits 0.

Non-zero means the plan could not be resolved into codes: the API was
unreachable, the plan_key does not exist, or the server returned cases
without short codes (an Observo older than the one this CLI speaks to).`,
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
	// GetPlanByKey handles both UUID and plan_key inputs — UUIDs go
	// straight through, keys list+filter (the server-side GET requires
	// UUID only per OB-257; revisit if/when that changes).
	plan, err := client.GetPlanByKey(context.Background(), prProject, prPlan)
	if err != nil {
		return fmt.Errorf("resolve plan: %w", err)
	}

	// No empty-code filter here: GetPlan either returns every case with a code
	// or fails (api.ErrPlanCasesUnusable). Skipping blanks would re-introduce
	// exactly the quiet under-run that failing the read exists to prevent.
	codes := make([]string, 0, len(plan.Cases))
	for _, c := range plan.Cases {
		codes = append(codes, c.ShortCode)
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
// The trailing `(?![0-9])` is load-bearing. --grep is an unanchored
// RegExp.test against the test title, so a bare alternation lets a short code
// match a LONGER one that starts with it: `OB-1` matches `@observo:OB-171`.
// That is not theoretical — REGR-MAIN-CI carries OB-1 and OB-5 while the suite
// carries OB-171/172/173 and OB-50..OB-59, so without the boundary the plan
// tier runs specs nobody attached to the plan, and the run created FROM the
// plan has no case for their results to land on. The plan would report the
// cases it holds while a different set actually executed — the same lie as
// resolving to nothing, pointing the other way.
//
// This never fired before: plan.Cases was always nil (OB-852), so this
// function only ever returned the sentinel. The first real code reaching it is
// the first time the bug is reachable.
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
	return "@observo:(" + strings.Join(esc, "|") + ")(?![0-9])"
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
