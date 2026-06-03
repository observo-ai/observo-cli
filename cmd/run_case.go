package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/observo-ai/observo-cli/internal/api"
	"github.com/observo-ai/observo-cli/internal/config"
	"github.com/observo-ai/observo-cli/internal/output"
	"github.com/observo-ai/observo-cli/internal/state"

	"github.com/spf13/cobra"
)

// parseExampleCells normalizes the --example-cells flag value. An empty/whitespace
// string returns (nil, nil) — the classic, non-parametrized path. A non-empty
// value MUST be a flat JSON object of string-keyed string values; nested objects,
// arrays, or numbers are rejected loudly so the user sees a clear error instead
// of a silent body shape the server then 4xx's. Keys/values cannot be empty —
// they wouldn't match any example row's recorded param_values.
func parseExampleCells(raw string) (map[string]string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	var cells map[string]string
	if err := json.Unmarshal([]byte(s), &cells); err != nil {
		return nil, fmt.Errorf(`--example-cells: invalid JSON (must be a flat object of "name":"value" pairs, e.g. '{"browser":"chromium"}'): %w`, err)
	}
	if len(cells) == 0 {
		return nil, fmt.Errorf(`--example-cells: empty object — omit the flag for a non-parametrized case`)
	}
	for k, v := range cells {
		if strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf(`--example-cells: empty parameter name not allowed`)
		}
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf(`--example-cells: empty value for parameter %q not allowed`, k)
		}
	}
	return cells, nil
}

// runCaseCmd is the parent of `set` and `step` (which is itself a parent
// of `set` — nested subcommand for per-step PATCH).
var runCaseCmd = &cobra.Command{
	Use:   "case",
	Short: "Per-case operations on a run (set status, drill into steps)",
}

// runCaseSetCmd: PATCH /api/runs/{run_id}/cases/{short_code} with status.
var (
	rcsProject   string
	rcsRunID     string
	rcsCode      string
	rcsStatus    string
	rcsComment   string
	rcsStateFile string
)

var runCaseSetCmd = &cobra.Command{
	Use:   "set",
	Short: "PATCH a run-case status by short code (auto-attaches the case to the run if needed)",
	Long: `PATCH the status of one test case attached to a run.

Idempotent: the first call for a given short code automatically
batch_adds the case to the run before PATCHing; subsequent calls are
just PATCHes. Safe to call repeatedly from a live test reporter as
each Playwright/Vitest case finishes.

When --project / --run-id are unset, they're read from the state file
written by 'run create'.

Status values: passed | failed | skipped | blocked
('blocked' covers timeouts, interrupts, and infra failures —
Playwright timedOut/interrupted should map to 'blocked'.
Flaky-on-retry should map to 'passed'; flake is tracked at the
layer-aggregate level, not per case.)`,
	Args: cobra.NoArgs,
	RunE: runCaseSetExec,
}

func init() {
	runCmd.AddCommand(runCaseCmd)
	runCaseCmd.AddCommand(runCaseSetCmd)

	f := runCaseSetCmd.Flags()
	f.StringVar(&rcsProject, "project", "", "project UUID or short code (default: from state file)")
	f.StringVar(&rcsRunID, "run-id", "", "run UUID (default: from state file)")
	f.StringVar(&rcsCode, "code", "", "test case short code, e.g. OB-50 (required)")
	f.StringVar(&rcsStatus, "status", "", "passed | failed | skipped | blocked (required)")
	f.StringVar(&rcsComment, "comment", "", "free-form case-level note shown in the dashboard (e.g. the failure reason); omit to preserve any existing comment")
	f.StringVar(&rcsStateFile, "state-file", state.DefaultPath, "where to read run_id from")
	_ = runCaseSetCmd.MarkFlagRequired("code")
	_ = runCaseSetCmd.MarkFlagRequired("status")
}

func runCaseSetExec(cmd *cobra.Command, _ []string) error {
	cfg := config.Resolve(flagAPIKey, flagBaseURL, flagJSON, flagVerbose)
	if err := cfg.Validate(); err != nil {
		return err
	}
	if !api.IsValidCaseStatus(rcsStatus) {
		return fmt.Errorf("invalid --status %q (allowed: passed, failed, skipped, blocked)", rcsStatus)
	}

	_, runID, err := resolveProjectAndRun(rcsProject, rcsRunID, rcsStateFile)
	if err != nil {
		return err
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
	if err := client.EnsureAndUpdateRunCase(context.Background(), runID, rcsCode, rcsStatus, rcsComment); err != nil {
		return fmt.Errorf("update run-case: %w", err)
	}

	p := output.New(cfg.JSON)
	p.Out = cmd.OutOrStdout()
	res := map[string]any{
		"run_id": runID,
		"code":   rcsCode,
		"status": rcsStatus,
	}
	if rcsComment != "" {
		res["comment"] = rcsComment
	}
	return p.Result(res, fmt.Sprintf("case %s: %s", rcsCode, rcsStatus))
}

// `run case step set` — separate sub-tree because it's a different
// resource (per-step) and has its own flags. Living under `run case
// step` keeps the help discoverable next to `run case set`.

var runCaseStepCmd = &cobra.Command{
	Use:   "step",
	Short: "Per-step operations on a run-case",
}

var (
	rcssProject      string
	rcssRunID        string
	rcssCode         string
	rcssStepIdx      int
	rcssStatus       string
	rcssComment      string
	rcssFileURL      string
	rcssExampleCells string // OB-406: JSON object of param-name -> value, e.g. {"browser":"chromium"}
	rcssStateFile    string
)

var runCaseStepSetCmd = &cobra.Command{
	Use:   "set",
	Short: "PATCH a single step status within a run-case (1-based index)",
	Long: `PATCH /api/runs/{run_id}/cases/{short_code}/steps/{step_index}.

The parent case must already be attached to the run — typically because
'run case set' was called for the same short code first, or because the
case was pre-attached via batch_add. If the case isn't attached, the
server returns 404 and this command surfaces the error.

Status values: same as 'run case set' — passed | failed | skipped | blocked.
--step is 1-based to match the dashboard's display.`,
	Args: cobra.NoArgs,
	RunE: runCaseStepSetExec,
}

func init() {
	runCaseCmd.AddCommand(runCaseStepCmd)
	runCaseStepCmd.AddCommand(runCaseStepSetCmd)

	f := runCaseStepSetCmd.Flags()
	f.StringVar(&rcssProject, "project", "", "project UUID or short code (default: from state file)")
	f.StringVar(&rcssRunID, "run-id", "", "run UUID (default: from state file)")
	f.StringVar(&rcssCode, "code", "", "test case short code, e.g. OB-50 (required)")
	f.IntVar(&rcssStepIdx, "step", 0, "1-based step index within the case (required, >= 1)")
	f.StringVar(&rcssStatus, "status", "", "passed | failed | skipped | blocked (required)")
	f.StringVar(&rcssComment, "comment", "", "free-form text shown next to the step in the dashboard")
	f.StringVar(&rcssFileURL, "file-url", "", "optional attachment URL to surface inline for the step")
	f.StringVar(&rcssExampleCells, "example-cells", "", `JSON object of parameter values for a parametrized example, e.g. '{"browser":"chromium"}'. Targets that one example only; omit for non-parametrized cases`)
	f.StringVar(&rcssStateFile, "state-file", state.DefaultPath, "where to read run_id from")
	_ = runCaseStepSetCmd.MarkFlagRequired("code")
	_ = runCaseStepSetCmd.MarkFlagRequired("step")
	_ = runCaseStepSetCmd.MarkFlagRequired("status")
}

func runCaseStepSetExec(cmd *cobra.Command, _ []string) error {
	cfg := config.Resolve(flagAPIKey, flagBaseURL, flagJSON, flagVerbose)
	if err := cfg.Validate(); err != nil {
		return err
	}
	if !api.IsValidCaseStatus(rcssStatus) {
		return fmt.Errorf("invalid --status %q (allowed: passed, failed, skipped, blocked)", rcssStatus)
	}
	if rcssStepIdx <= 0 {
		return fmt.Errorf("--step must be >= 1 (1-based), got %d", rcssStepIdx)
	}

	// OB-406: parse --example-cells once here so a malformed JSON shape is a
	// CLI error (clear, fixable by the caller), not a 4xx surfaced from the API.
	exampleCells, err := parseExampleCells(rcssExampleCells)
	if err != nil {
		return err
	}

	_, runID, err := resolveProjectAndRun(rcssProject, rcssRunID, rcssStateFile)
	if err != nil {
		return err
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
	if err := client.UpdateRunCaseStep(context.Background(), api.UpdateRunCaseStepRequest{
		RunID:        runID,
		ShortCode:    rcssCode,
		StepIndex:    rcssStepIdx,
		Status:       rcssStatus,
		Comment:      rcssComment,
		FileURL:      rcssFileURL,
		ExampleCells: exampleCells,
	}); err != nil {
		return fmt.Errorf("update run-case-step: %w", err)
	}

	p := output.New(cfg.JSON)
	p.Out = cmd.OutOrStdout()
	return p.Result(map[string]any{
		"run_id": runID,
		"code":   rcssCode,
		"step":   rcssStepIdx,
		"status": rcssStatus,
	}, fmt.Sprintf("case %s step %d: %s", rcssCode, rcssStepIdx, rcssStatus))
}
