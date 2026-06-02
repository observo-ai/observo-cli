package cmd

import (
	"context"
	"fmt"

	"github.com/observo-ai/observo-cli/internal/api"
	"github.com/observo-ai/observo-cli/internal/config"
	"github.com/observo-ai/observo-cli/internal/output"
	"github.com/observo-ai/observo-cli/internal/state"

	"github.com/spf13/cobra"
)

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
	rcssProject   string
	rcssRunID     string
	rcssCode      string
	rcssStepIdx   int
	rcssStatus    string
	rcssComment   string
	rcssFileURL   string
	rcssStateFile string
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
		RunID:     runID,
		ShortCode: rcssCode,
		StepIndex: rcssStepIdx,
		Status:    rcssStatus,
		Comment:   rcssComment,
		FileURL:   rcssFileURL,
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
