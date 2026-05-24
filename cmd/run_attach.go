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
	raProject   string
	raRunID     string
	raFile      string
	raStateFile string
)

var runAttachCmd = &cobra.Command{
	Use:   "attach",
	Short: "Upload an artifact (junit, lcov, html, etc.) and link it to the run",
	Long: `Upload a local file to Observo as a run-scoped attachment. The server
returns an attachment ID — useful when you want to splice it into
'observo run pipeline-layer set --junit-attachment-id ...' yourself.

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
	f.StringVar(&raStateFile, "state-file", state.DefaultPath, "where to read run_id from")
	_ = runAttachCmd.MarkFlagRequired("file")
}

func runAttachExec(cmd *cobra.Command, _ []string) error {
	cfg := config.Resolve(flagAPIKey, flagBaseURL, flagJSON, flagVerbose)
	if err := cfg.Validate(); err != nil {
		return err
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
		ProjectID: projectID,
		RunID:     runID,
		FilePath:  raFile,
	})
	if err != nil {
		return fmt.Errorf("upload attachment: %w", err)
	}

	p := output.New(cfg.JSON)
	p.Out = cmd.OutOrStdout()
	return p.Result(map[string]any{
		"attachment_id": att.ID,
		"file_name":     att.FileName,
		"run_id":        runID,
	}, att.ID)
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
