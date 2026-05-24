package api

import (
	"context"
	"fmt"
	"net/url"
)

// Run is the subset of the Observo TestRun shape the CLI needs.
// We deliberately mirror the wire JSON (snake_case) rather than importing
// server's pb/ types — the CLI is a separate go module and should not
// couple to server's protobuf deps.
type Run struct {
	ID               string    `json:"id"`
	ProjectID        string    `json:"project_id"`
	Name             string    `json:"name"`
	Status           string    `json:"status"`
	AutomationStatus string    `json:"automation_status"`
	RunKey           string    `json:"run_key"`
	Pipeline         *Pipeline `json:"pipeline,omitempty"`
}

// Pipeline carries the optional pipeline metadata attached to a run.
// Fields not used by the CLI today are omitted; server tolerates
// unknown-field-drop on read.
type Pipeline struct {
	Kind         string           `json:"kind,omitempty"`
	SourceSystem string           `json:"source_system,omitempty"`
	CommitSHA    string           `json:"commit_sha,omitempty"`
	CommitURL    string           `json:"commit_url,omitempty"`
	Branch       string           `json:"branch,omitempty"`
	PrURL        string           `json:"pr_url,omitempty"`
	Actor        string           `json:"actor,omitempty"`
	CIRunURL     string           `json:"ci_run_url,omitempty"`
	Layers       []map[string]any `json:"layers,omitempty"` // opaque — OB-D builds these
}

// CreateRunRequest mirrors the server's POST /api/projects/{id}/runs body.
// All fields are optional except project_id; the server fills defaults.
type CreateRunRequest struct {
	ProjectID        string    `json:"project_id"`
	Name             string    `json:"name,omitempty"`
	PlanID           string    `json:"plan_id,omitempty"` // accepts plan_key or UUID server-side
	AutomationStatus string    `json:"automation_status,omitempty"`
	Pipeline         *Pipeline `json:"pipeline,omitempty"`
}

// CreateRunResponse mirrors the server's create-run response envelope.
type CreateRunResponse struct {
	Run Run `json:"run"`
}

// CreateRun calls POST /api/projects/{project_id}/runs.
// project_id may be a UUID or short code (e.g. "OB") — the server resolves
// both at the URL path. We URL-encode defensively so future formats work.
func (c *Client) CreateRun(ctx context.Context, req CreateRunRequest) (*Run, error) {
	if req.ProjectID == "" {
		return nil, fmt.Errorf("CreateRun: project_id required")
	}
	path := "/api/projects/" + url.PathEscape(req.ProjectID) + "/runs"

	var resp CreateRunResponse
	if err := c.DoJSON(ctx, "POST", path, req, &resp); err != nil {
		return nil, err
	}
	if resp.Run.ID == "" {
		return nil, fmt.Errorf("CreateRun: response missing run.id")
	}
	return &resp.Run, nil
}

// UpdateRunRequest mirrors the server's PATCH /api/projects/{id}/runs/{id} body.
// We only expose status today; OB-D will extend if needed.
type UpdateRunRequest struct {
	ProjectID string `json:"-"` // path scope, not body
	RunID     string `json:"-"`
	Status    string `json:"status,omitempty"` // passed|failed|aborted|skipped
}

// UpdateRunResponse mirrors the server's update-run response envelope.
type UpdateRunResponse struct {
	Run Run `json:"run"`
}

// UpdateRun calls PATCH /api/projects/{project_id}/runs/{run_id}.
// Used by `run finish` to close out a run with final status.
func (c *Client) UpdateRun(ctx context.Context, req UpdateRunRequest) (*Run, error) {
	if req.ProjectID == "" || req.RunID == "" {
		return nil, fmt.Errorf("UpdateRun: project_id and run_id required")
	}
	path := "/api/projects/" + url.PathEscape(req.ProjectID) + "/runs/" + url.PathEscape(req.RunID)

	var resp UpdateRunResponse
	if err := c.DoJSON(ctx, "PATCH", path, req, &resp); err != nil {
		return nil, err
	}
	return &resp.Run, nil
}
