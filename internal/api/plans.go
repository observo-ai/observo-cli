package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

// Plan is the subset of the Observo Plan shape the CLI consumes.
// Cases is the snapshot at fetch time — server returns the same short
// codes that `create run --plan KEY` would seed into a new run.
type Plan struct {
	ID      string     `json:"id"`
	PlanKey string     `json:"plan_key"`
	Name    string     `json:"name,omitempty"`
	Cases   []PlanCase `json:"cases,omitempty"`
}

// PlanCase carries the minimum a CI consumer needs to filter tests.
// Title is included for nicer CLI output; consumers that only need the
// grep regex ignore it.
type PlanCase struct {
	ShortCode string `json:"short_code"`
	Title     string `json:"title,omitempty"`
}

// getPlanResponse mirrors the server envelope.
type getPlanResponse struct {
	Plan Plan `json:"plan"`
}

// GetPlan resolves a plan by ID or plan_key within a project, returning
// the plan + attached cases.
//
// Server endpoint: GET /api/projects/{project_id}/plans/{plan_id_or_key}.
// The server accepts both UUID and plan_key (same convention as
// `POST /runs` with plan_id; per OB-257 plans management).
func (c *Client) GetPlan(ctx context.Context, projectID, planIDOrKey string) (*Plan, error) {
	if projectID == "" {
		return nil, errors.New("GetPlan: project_id required")
	}
	if planIDOrKey == "" {
		return nil, errors.New("GetPlan: plan_id or plan_key required")
	}
	path := fmt.Sprintf("/api/projects/%s/plans/%s",
		url.PathEscape(projectID), url.PathEscape(planIDOrKey))

	var resp getPlanResponse
	if err := c.DoJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Plan.ID == "" {
		return nil, errors.New("GetPlan: response missing plan.id")
	}
	return &resp.Plan, nil
}
