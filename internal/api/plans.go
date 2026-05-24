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

type getPlanResponse struct {
	Plan Plan `json:"plan"`
}

type listPlansResponse struct {
	Plans []Plan `json:"plans"`
}

// GetPlan resolves a plan by UUID within a project. The server's
// GetTestPlan handler validates with val.ValidateUuid — a plan_key
// here returns 400 InvalidArgument. Callers with a human-readable
// plan_key should use GetPlanByKey, which lists + filters until the
// server learns to resolve keys (OB-257 limitation surfaced in operator
// notes: "get_plan / update_plan / delete_plan / clone_plan currently
// accept ONLY plan UUID").
func (c *Client) GetPlan(ctx context.Context, projectID, planID string) (*Plan, error) {
	if projectID == "" {
		return nil, errors.New("GetPlan: project_id required")
	}
	if planID == "" {
		return nil, errors.New("GetPlan: plan_id required")
	}
	path := fmt.Sprintf("/api/projects/%s/plans/%s",
		url.PathEscape(projectID), url.PathEscape(planID))

	var resp getPlanResponse
	if err := c.DoJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Plan.ID == "" {
		return nil, errors.New("GetPlan: response missing plan.id")
	}
	return &resp.Plan, nil
}

// ListPlans returns all plans in a project. Used by GetPlanByKey to
// resolve plan_key → UUID. No pagination today — the server returns
// the full list. If projects grow to hundreds of plans, switch to a
// server-side filter param.
func (c *Client) ListPlans(ctx context.Context, projectID string) ([]Plan, error) {
	if projectID == "" {
		return nil, errors.New("ListPlans: project_id required")
	}
	path := "/api/projects/" + url.PathEscape(projectID) + "/plans"
	var resp listPlansResponse
	if err := c.DoJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Plans, nil
}

// GetPlanByKey is the human-friendly resolver `plan resolve` uses. If
// the input parses as a UUID, it goes directly through GetPlan;
// otherwise it lists project plans, filters by plan_key, and re-fetches
// the matched plan by UUID (the list response isn't guaranteed to
// include the full Cases array, so the second hop is required for
// consumers like --format=grep).
//
// Two round-trips for keys, one for UUIDs. Acceptable until the server
// accepts plan_key on GET.
func (c *Client) GetPlanByKey(ctx context.Context, projectID, planIDOrKey string) (*Plan, error) {
	if projectID == "" {
		return nil, errors.New("GetPlanByKey: project_id required")
	}
	if planIDOrKey == "" {
		return nil, errors.New("GetPlanByKey: plan_id or plan_key required")
	}
	if isUUID(planIDOrKey) {
		return c.GetPlan(ctx, projectID, planIDOrKey)
	}
	plans, err := c.ListPlans(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list plans (for key resolution): %w", err)
	}
	for _, p := range plans {
		if p.PlanKey == planIDOrKey {
			return c.GetPlan(ctx, projectID, p.ID)
		}
	}
	return nil, fmt.Errorf("no plan with plan_key %q found in project %s", planIDOrKey, projectID)
}

// isUUID is a cheap shape check (8-4-4-4-12 hex with dashes). We don't
// import google/uuid here to keep this package free of cross-module
// deps.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}
