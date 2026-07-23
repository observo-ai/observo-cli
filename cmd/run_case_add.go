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

// `run case add` — pre-attach cases to a run by short code.
//
// OB-600: the Prove loop pre-creates a plan-less run and attaches all its
// gap cases UP FRONT (fixed membership, crosses terminal exactly once),
// then runs the specs so every reporter writeback finds its case already
// attached — instead of self-creating a plan-less run and 404-ing on the
// first by-code writeback because the case was never attached.
//
// Wraps POST /api/runs/{run_id}/cases:batch_add with the server's
// `test_case_codes` field (account + private-project scoped), which now
// accepts account-scoped API keys.

var (
	rcaProject   string
	rcaRunID     string
	rcaCodes     []string
	rcaStateFile string
)

var runCaseAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Attach one or more test cases to a run by short code",
	Long: `Attach cases to a run by their @observo short code (e.g. OB-50) BEFORE any
per-case writeback — batch_add with the server's test_case_codes field.

Typical use is pre-attaching a plan-less run's cases up front so later
'run case set' / 'run case step set' writebacks find each case already
attached (the Prove loop). Idempotent: attaching an already-attached case
is a no-op.

When --project / --run-id are unset, they're read from the state file
written by 'run create'.`,
	Args: cobra.NoArgs,
	RunE: runCaseAddExec,
}

func init() {
	runCaseCmd.AddCommand(runCaseAddCmd)

	f := runCaseAddCmd.Flags()
	f.StringVar(&rcaProject, "project", "", "project UUID or short code (default: from state file)")
	f.StringVar(&rcaRunID, "run-id", "", "run UUID (default: from state file)")
	f.StringSliceVar(&rcaCodes, "code", nil, "test case short code(s), e.g. --code OB-1,OB-2 (repeatable; required)")
	f.StringVar(&rcaStateFile, "state-file", state.DefaultPath, "where to read run_id from")
	_ = runCaseAddCmd.MarkFlagRequired("code")
}

func runCaseAddExec(cmd *cobra.Command, _ []string) error {
	cfg := config.Resolve(flagAPIKey, flagBaseURL, flagJSON, flagVerbose)
	if err := cfg.Validate(); err != nil {
		return err
	}

	// Reject an empty --code entry early (StringSliceVar keeps ""), so a
	// "--code OB-1,,OB-2" typo is a clear CLI error, not a 4xx from the API.
	for _, c := range rcaCodes {
		if c == "" {
			return fmt.Errorf("--code: empty short code not allowed")
		}
	}

	_, runID, err := resolveProjectAndRun(rcaProject, rcaRunID, rcaStateFile)
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
	if err := client.BatchAddCases(context.Background(), runID, rcaCodes); err != nil {
		return fmt.Errorf("attach run-cases: %w", err)
	}

	p := output.New(cfg.JSON)
	p.Out = cmd.OutOrStdout()
	return p.Result(
		map[string]any{"run_id": runID, "codes": rcaCodes},
		fmt.Sprintf("attached %d case(s) to run", len(rcaCodes)),
	)
}
