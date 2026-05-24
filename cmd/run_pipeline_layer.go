package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/observo-ai/observo-cli/internal/api"
	"github.com/observo-ai/observo-cli/internal/config"
	"github.com/observo-ai/observo-cli/internal/junit"
	"github.com/observo-ai/observo-cli/internal/output"
	"github.com/observo-ai/observo-cli/internal/state"

	"github.com/spf13/cobra"
)

// pipeline-layer is a non-leaf subcommand grouping current `set` and
// future variants (e.g. `clear`, `inspect`). Cobra emits help when
// invoked without a leaf.
var pipelineLayerCmd = &cobra.Command{
	Use:   "pipeline-layer",
	Short: "Manage entries in TestRun.pipeline.layers",
}

var (
	plProject     string
	plRunID       string
	plLayerID     string
	plDisplayName string
	plFramework   string
	plJunitFile   string
	plLcovFile    string
	plHtmlFile    string
	plStateFile   string
)

var pipelineLayerSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Upload junit+lcov+html for a layer and PATCH its aggregates atomically",
	Long: `End-to-end one-shot for a single CI layer (frontend-unit, server-unit,
mcp-contract, etc.):

  1. Parses --junit XML to derive total/passed/failed/skipped/duration_ms.
  2. Uploads --junit / --lcov / --html as run-scoped attachments
     (when each flag is set + file exists).
  3. PATCHes /api/runs/{run_id}/pipeline/layers/{layer_id} via the
     OB-A atomic upsert RPC, splicing the attachment IDs into the
     layer record. Concurrent layer-job patches don't clobber each
     other (server-side row lock + jsonb_set CASE).
  4. Records this layer's outcome (passed/failed/skipped derived from
     junit) in the state file so 'run finish --status auto' can roll
     up correctly.

When --project / --run-id are unset, they're read from the state file
written by 'run create'.

Typical CI usage (after the tests + coverage have produced files):

  observo run pipeline-layer set \
    --layer-id server-unit \
    --display-name "Backend Unit (Go)" \
    --framework go \
    --junit server/junit-unit.xml \
    --lcov  server/server-unit-lcov.info`,
	Args: cobra.NoArgs,
	RunE: pipelineLayerSetExec,
}

func init() {
	runCmd.AddCommand(pipelineLayerCmd)
	pipelineLayerCmd.AddCommand(pipelineLayerSetCmd)

	f := pipelineLayerSetCmd.Flags()
	f.StringVar(&plProject, "project", "", "project UUID or short code (default: from state file)")
	f.StringVar(&plRunID, "run-id", "", "run UUID (default: from state file)")
	f.StringVar(&plLayerID, "layer-id", "", "layer id, e.g. \"frontend-unit\" (required)")
	f.StringVar(&plDisplayName, "display-name", "", "human label, e.g. \"Frontend (Vitest)\" (required)")
	f.StringVar(&plFramework, "framework", "", "framework hint for the dashboard icon (vitest|go|playwright|...)")
	f.StringVar(&plJunitFile, "junit", "", "path to JUnit XML (required — used for both aggregates and attachment)")
	f.StringVar(&plLcovFile, "lcov", "", "path to LCOV file to attach (optional)")
	f.StringVar(&plHtmlFile, "html", "", "path to HTML coverage report to attach (optional)")
	f.StringVar(&plStateFile, "state-file", state.DefaultPath, "where to read run_id from + record layer outcome")
	_ = pipelineLayerSetCmd.MarkFlagRequired("layer-id")
	_ = pipelineLayerSetCmd.MarkFlagRequired("display-name")
	_ = pipelineLayerSetCmd.MarkFlagRequired("junit")
}

func pipelineLayerSetExec(cmd *cobra.Command, _ []string) error {
	cfg := config.Resolve(flagAPIKey, flagBaseURL, flagJSON, flagVerbose)
	if err := cfg.Validate(); err != nil {
		return err
	}

	projectID, runID, err := resolveProjectAndRun(plProject, plRunID, plStateFile)
	if err != nil {
		return err
	}

	// 1. Parse junit for aggregates.
	junitFile, err := os.Open(plJunitFile)
	if err != nil {
		return fmt.Errorf("open junit %s: %w", plJunitFile, err)
	}
	agg, err := junit.Parse(junitFile)
	junitFile.Close()
	if err != nil {
		return fmt.Errorf("parse junit %s: %w", plJunitFile, err)
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
	ctx := context.Background()

	// 2. Upload attachments (junit required, lcov/html optional).
	junitAtt, err := client.UploadAttachment(ctx, api.UploadAttachmentRequest{
		ProjectID: projectID, RunID: runID, FilePath: plJunitFile,
	})
	if err != nil {
		return fmt.Errorf("upload junit: %w", err)
	}

	var coverage *api.LayerCoverage
	if plLcovFile != "" {
		if _, err := os.Stat(plLcovFile); err == nil {
			lcovAtt, err := client.UploadAttachment(ctx, api.UploadAttachmentRequest{
				ProjectID: projectID, RunID: runID, FilePath: plLcovFile,
			})
			if err != nil {
				return fmt.Errorf("upload lcov: %w", err)
			}
			coverage = &api.LayerCoverage{LcovAttachmentID: lcovAtt.ID}
		} else {
			cmd.PrintErrf("warning: --lcov %s not found, skipping\n", plLcovFile)
		}
	}
	if plHtmlFile != "" {
		if _, err := os.Stat(plHtmlFile); err == nil {
			htmlAtt, err := client.UploadAttachment(ctx, api.UploadAttachmentRequest{
				ProjectID: projectID, RunID: runID, FilePath: plHtmlFile,
			})
			if err != nil {
				return fmt.Errorf("upload html: %w", err)
			}
			if coverage == nil {
				coverage = &api.LayerCoverage{}
			}
			coverage.HtmlAttachmentID = htmlAtt.ID
		} else {
			cmd.PrintErrf("warning: --html %s not found, skipping\n", plHtmlFile)
		}
	}

	// 3. PATCH pipeline_layer with aggregates + attachment IDs.
	run, err := client.PatchPipelineLayer(ctx, api.PatchPipelineLayerRequest{
		ProjectID: projectID,
		RunID:     runID,
		LayerID:   plLayerID,
		Layer: api.PipelineLayer{
			ID:                plLayerID,
			DisplayName:       plDisplayName,
			Framework:         plFramework,
			Total:             int32(agg.Total),
			Passed:            int32(agg.Passed),
			Failed:            int32(agg.Failed),
			Skipped:           int32(agg.Skipped),
			DurationMs:        agg.DurationMs,
			JunitAttachmentID: junitAtt.ID,
			Coverage:          coverage,
		},
	})
	if err != nil {
		return fmt.Errorf("patch pipeline layer: %w", err)
	}

	// 4. Record outcome in state file for `run finish --status auto`.
	if err := state.RecordLayer(plStateFile, plLayerID, agg.Outcome()); err != nil {
		// Soft warn — the API write succeeded; state failure shouldn't
		// fail the CI step. `run finish --status auto` will fall back
		// to "passed" if the state file is incomplete.
		cmd.PrintErrf("warning: failed to record layer outcome in state file: %v\n", err)
	}

	p := output.New(cfg.JSON)
	p.Out = cmd.OutOrStdout()
	return p.Result(map[string]any{
		"run_id":              run.ID,
		"layer_id":            plLayerID,
		"outcome":             agg.Outcome(),
		"total":               agg.Total,
		"passed":              agg.Passed,
		"failed":              agg.Failed,
		"skipped":             agg.Skipped,
		"duration_ms":         agg.DurationMs,
		"junit_attachment_id": junitAtt.ID,
	}, fmt.Sprintf("layer %s: %s (%d total, %d failed, %d skipped) — junit_attachment=%s",
		plLayerID, agg.Outcome(), agg.Total, agg.Failed, agg.Skipped, junitAtt.ID))
}
