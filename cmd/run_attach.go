package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/observo-ai/observo-cli/internal/api"
	"github.com/observo-ai/observo-cli/internal/config"
	"github.com/observo-ai/observo-cli/internal/output"
	"github.com/observo-ai/observo-cli/internal/state"

	"github.com/spf13/cobra"
)

var (
	raProject      string
	raRunID        string
	raFile         string
	raCase         string
	raCaseCode     string
	raStateFile    string
	raExampleCells string
)

var runAttachCmd = &cobra.Command{
	Use:   "attach",
	Short: "Upload an artifact (junit, lcov, html, etc.) and link it to the run or a run-case",
	Long: `Upload a local file to Observo as an attachment. The server returns an
attachment ID — useful when you want to splice it into
'observo run pipeline-layer set --junit-attachment-id ...' yourself.

By default the attachment is run-scoped. Pass --case (alias --code) with
a run-case short code to link it to a SPECIFIC case in the run instead —
e.g. a trace.zip / error-context.md for a failing E2E case (OB-397). The
case must already be attached to the run (--run-id resolves the per-case
row); otherwise the server returns 404.

Most CI flows should NOT call this directly — 'run pipeline-layer set'
runs the same upload internally and splices the IDs into the layer
record automatically.

When --project / --run-id are unset, they're read from the state file
written by 'run create'.`,
	Args: cobra.NoArgs,
	RunE: runAttachExec,
}

func init() {
	runCmd.AddCommand(runAttachCmd)
	f := runAttachCmd.Flags()
	f.StringVar(&raProject, "project", "", "project UUID or short code (default: from state file)")
	f.StringVar(&raRunID, "run-id", "", "run UUID (default: from state file)")
	f.StringVar(&raFile, "file", "", "local file path to upload (required)")
	f.StringVar(&raCase, "case", "", "run-case short code (e.g. OB-76) to link the attachment to; needs --run-id/state. Omit for a run-level attachment")
	f.StringVar(&raCaseCode, "code", "", "alias for --case (parity with 'run case set --code')")
	// OB-437: required to disambiguate which example row of a parametrized case
	// a case-level attachment belongs to. Shape matches `run case set` /
	// `run case step set` (OB-405 / OB-423): flat JSON object of "param":"value"
	// pairs. Ignored on a non-parametrized case (per the server's proto
	// contract); required when the case IS parametrized.
	f.StringVar(&raExampleCells, "example-cells", "", `JSON object of parameter values that pin a parametrized example row, e.g. '{"browser":"chromium"}'. Required when --case is a parametrized case`)
	f.StringVar(&raStateFile, "state-file", state.DefaultPath, "where to read run_id from")
	_ = runAttachCmd.MarkFlagRequired("file")
}

func runAttachExec(cmd *cobra.Command, _ []string) error {
	cfg := config.Resolve(flagAPIKey, flagBaseURL, flagJSON, flagVerbose)
	if err := cfg.Validate(); err != nil {
		return err
	}

	// --case is canonical; --code is an alias (parity with 'run case set').
	// Reconcile: prefer --case, fall back to --code, error if both differ.
	caseRef := raCase
	switch {
	case raCase != "" && raCaseCode != "" && raCase != raCaseCode:
		return fmt.Errorf("--case (%q) and --code (%q) both set and differ; pass only one", raCase, raCaseCode)
	case caseRef == "":
		caseRef = raCaseCode
	}

	cells, err := parseExampleCells(raExampleCells)
	if err != nil {
		return err
	}
	// --example-cells without --case is nonsensical: the cells disambiguate
	// AMONG example rows of a specific case, so the case identity is required.
	// Catch it CLI-side so the server doesn't have to spend a round-trip
	// returning InvalidArgument.
	if len(cells) > 0 && caseRef == "" {
		return fmt.Errorf("--example-cells requires --case to identify which parametrized case the row belongs to")
	}

	projectID, runID, err := resolveProjectAndRun(raProject, raRunID, raStateFile)
	if err != nil {
		return err
	}
	if _, err := os.Stat(raFile); err != nil {
		return fmt.Errorf("file %s: %w", raFile, err)
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

	att, err := client.UploadAttachment(context.Background(), api.UploadAttachmentRequest{
		ProjectID:    projectID,
		RunID:        runID,
		RunCaseID:    caseRef, // empty → run-scoped; short code → server resolves the per-case row via run_id
		ExampleCells: cells,
		FilePath:     raFile,
	})
	if err != nil {
		return fmt.Errorf("upload attachment: %w", err)
	}

	p := output.New(cfg.JSON)
	p.Out = cmd.OutOrStdout()
	res := map[string]any{
		"attachment_id": att.ID,
		"file_name":     att.FileName,
		"run_id":        runID,
	}
	if caseRef != "" {
		res["case"] = caseRef
	}
	return p.Result(res, att.ID)
}

// resolveProjectAndRun is the shared "flag → state file" lookup for any
// subcommand that needs both a project and a run. Returns descriptive
// errors when neither source provides the value.
func resolveProjectAndRun(projectFlag, runIDFlag, stateFile string) (string, string, error) {
	if projectFlag != "" && runIDFlag != "" {
		return projectFlag, runIDFlag, nil
	}
	st, err := state.Load(stateFile)
	if err != nil {
		// Caller's --project / --run-id flags weren't enough; state isn't there.
		return "", "", fmt.Errorf("--project and --run-id not both set; state file unavailable: %w", err)
	}
	pid := projectFlag
	if pid == "" {
		pid = st.ProjectID
	}
	rid := runIDFlag
	if rid == "" {
		rid = st.RunID
	}
	if pid == "" || rid == "" {
		return "", "", fmt.Errorf("missing project_id (%q) or run_id (%q) — pass --project / --run-id or run 'observo run create' first", pid, rid)
	}
	return pid, rid, nil
}
