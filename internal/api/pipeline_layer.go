package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

// LayerCoverage carries the optional coverage attachment IDs spliced
// into pipeline.layers[i].coverage server-side.
type LayerCoverage struct {
	LcovAttachmentID string `json:"lcov_attachment_id,omitempty"`
	HtmlAttachmentID string `json:"html_attachment_id,omitempty"`
}

// PipelineLayer mirrors the server's PipelineLayer proto (snake_case
// JSON). Fields with zero values are omitted via omitempty so a partial
// patch (e.g. only setting totals + junit ID) doesn't send literal nulls.
type PipelineLayer struct {
	ID                string         `json:"id"`
	DisplayName       string         `json:"display_name"`
	Framework         string         `json:"framework,omitempty"`
	Total             int32          `json:"total,omitempty"`
	Passed            int32          `json:"passed,omitempty"`
	Failed            int32          `json:"failed,omitempty"`
	Flaky             int32          `json:"flaky,omitempty"`
	Skipped           int32          `json:"skipped,omitempty"`
	DurationMs        int64          `json:"duration_ms,omitempty"`
	JunitAttachmentID string         `json:"junit_attachment_id,omitempty"`
	Coverage          *LayerCoverage `json:"coverage,omitempty"`
}

// PatchPipelineLayerRequest mirrors the OB-A server RPC.
// project_id is needed for auth scoping (sent in body, not path —
// matches the proto binding `PATCH /api/runs/{run_id}/pipeline/layers/{layer_id}`).
type PatchPipelineLayerRequest struct {
	ProjectID string        `json:"project_id"`
	RunID     string        `json:"-"`
	LayerID   string        `json:"-"`
	Layer     PipelineLayer `json:"layer"`
}

// PatchPipelineLayerResponse mirrors the server envelope.
type PatchPipelineLayerResponse struct {
	Run Run `json:"run"`
}

// PatchPipelineLayer calls the OB-A endpoint that atomically upserts a
// single entry in pipeline.layers. Concurrent callers patching different
// layer_ids on the same run serialize at the SQL row level — see
// server/db/query/test_runs.sql:UpsertRunPipelineLayer.
func (c *Client) PatchPipelineLayer(ctx context.Context, req PatchPipelineLayerRequest) (*Run, error) {
	if req.ProjectID == "" || req.RunID == "" || req.LayerID == "" {
		return nil, errors.New("PatchPipelineLayer: project_id, run_id, layer_id all required")
	}
	if req.Layer.ID == "" {
		// Convenience: callers commonly pass LayerID separately + don't
		// duplicate it on Layer.ID. Auto-fill so the server's path-vs-body
		// equality check passes.
		req.Layer.ID = req.LayerID
	}
	if req.Layer.ID != req.LayerID {
		return nil, fmt.Errorf("PatchPipelineLayer: path layer_id %q != body layer.id %q", req.LayerID, req.Layer.ID)
	}

	path := "/api/runs/" + url.PathEscape(req.RunID) + "/pipeline/layers/" + url.PathEscape(req.LayerID)

	var resp PatchPipelineLayerResponse
	if err := c.DoJSON(ctx, "PATCH", path, req, &resp); err != nil {
		return nil, err
	}
	return &resp.Run, nil
}
