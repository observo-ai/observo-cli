package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/observo-ai/observo-cli/internal/api"
	"github.com/observo-ai/observo-cli/internal/config"
	"github.com/observo-ai/observo-cli/internal/output"
	"github.com/observo-ai/observo-cli/internal/state"

	"github.com/spf13/cobra"
)

var (
	rfProject           string
	rfRunID             string
	rfStatus            string
	rfStateFile         string
	rfAllowMissingState bool
)

var runFinishCmd = &cobra.Command{
	Use:   "finish",
	Short: "Close a TestRun with final status (status auto derives from layer outcomes in the state file)",
	Long: `Mark a TestRun finished. Status modes:
  --status auto       derive from layer outcomes recorded in the state file
                      (any layer failed → failed; any aborted → aborted;
                       all passed/skipped → passed; empty → passed)
  --status passed|failed|aborted|skipped
                      set explicitly; overrides the state file

When --run-id is unset, it's read from --state-file (default
.observo-pipeline-state.json). When --project is unset, same. So in the
common CI pattern (after 'run create' wrote the state file), this is
just:

  observo run finish --status auto`,
	Args: cobra.NoArgs,
	RunE: runFinishExec,
}

func init() {
	runCmd.AddCommand(runFinishCmd)
	f := runFinishCmd.Flags()
	f.StringVar(&rfProject, "project", "", "project UUID or short code (default: from state file)")
	f.StringVar(&rfRunID, "run-id", "", "run UUID (default: from state file)")
	f.StringVar(&rfStatus, "status", "auto", "auto | passed | failed | aborted | skipped")
	f.StringVar(&rfStateFile, "state-file", state.DefaultPath, "where to read run_id from")
	f.BoolVar(&rfAllowMissingState, "allow-missing-state", false,
		"with --status=auto and no state file, default to 'passed' instead of erroring (default false: missing state is an error so a lost-state-file CI pipeline doesn't silently report green)")
}

func runFinishExec(cmd *cobra.Command, _ []string) error {
	cfg := config.Resolve(flagAPIKey, flagBaseURL, flagJSON, flagVerbose)
	if err := cfg.Validate(); err != nil {
		return err
	}

	// Resolve project_id + run_id: explicit flags win; otherwise state file.
	st, stErr := state.Load(rfStateFile)
	projectID := rfProject
	runID := rfRunID
	status := rfStatus

	if projectID == "" {
		if stErr != nil {
			return fmt.Errorf("--project not set and state file unavailable: %w", stErr)
		}
		projectID = st.ProjectID
	}
	if runID == "" {
		if stErr != nil {
			return fmt.Errorf("--run-id not set and state file unavailable: %w", stErr)
		}
		runID = st.RunID
	}
	if status == "auto" {
		if errors.Is(stErr, state.ErrNotFound) {
			// Pre-fix this silently defaulted to "passed". That meant a
			// CI workflow whose state file got dropped between jobs
			// (artifact upload skipped on cancel, wrong working dir,
			// custom --state-file path forgotten) reported green for a
			// pipeline whose layers actually failed — the worst possible
			// failure mode for a status-push CLI.
			//
			// New default: missing state with --status=auto is an
			// error. Operators with a legitimate no-op pipeline (no
			// layers reported at all) can pass --allow-missing-state to
			// opt back in to the "passed" default.
			if !rfAllowMissingState {
				return fmt.Errorf("--status=auto requires a state file, none found at %s (run 'observo run create' first, or pass --status explicitly, or pass --allow-missing-state to default to 'passed')", rfStateFile)
			}
			status = "passed"
		} else if stErr != nil {
			return fmt.Errorf("read state for auto status: %w", stErr)
		} else {
			status = state.DeriveStatus(st)
		}
	}

	if err := validateFinishStatus(status); err != nil {
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

	run, err := client.UpdateRun(context.Background(), api.UpdateRunRequest{
		ProjectID: projectID,
		RunID:     runID,
		Status:    status,
	})
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}

	p := output.New(cfg.JSON)
	p.Out = cmd.OutOrStdout()
	return p.Result(map[string]any{
		"run_id":  run.ID,
		"status":  run.Status,
		"derived": rfStatus == "auto",
	}, fmt.Sprintf("run %s closed: %s", run.ID, run.Status))
}

// validateFinishStatus enforces the allowed status enum so the API
// doesn't reject our request late with a wrapped 400.
func validateFinishStatus(s string) error {
	switch s {
	case "passed", "failed", "aborted", "skipped":
		return nil
	default:
		return fmt.Errorf("invalid --status %q (allowed: auto, passed, failed, aborted, skipped)", s)
	}
}
