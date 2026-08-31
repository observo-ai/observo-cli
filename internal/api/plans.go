package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

// ErrPlanNotFound reports that a plan genuinely does not exist, as distinct
// from a lookup that failed to answer.
//
// Callers that create-if-absent MUST tell the two apart. GetPlanByKey resolves
// a key by listing, so a network blip, an exhausted retry or an auth hiccup all
// surface as an error too — and treating those as "absent" makes the next
// import create a duplicate plan. Only errors.Is(err, ErrPlanNotFound) means
// "safe to create".
var ErrPlanNotFound = errors.New("plan not found")

// ErrPlanCasesUnusable reports that the plan was read but its cases cannot be
// turned into short codes — the server sent rows without one.
//
// Distinct from a plan that is genuinely empty, and the distinction is the
// whole point (OB-852): a broken contract and an unattached plan used to
// produce the same output, while the caller's next move differs — attach
// cases, or go and fix the server. Callers that must tell them apart use
// errors.Is.
//
// One shape this does NOT catch, and cannot: a response with no `cases` key at
// all. The gateway omits an empty array (EmitUnpopulated is off), so an empty
// plan and a `cases` that moved or was renamed arrive identically on the wire,
// and nothing in the body says how many cases the plan holds. That is the
// shape the original bug took, so it is worth closing — it needs a count on
// the plan itself, which is a server change. Tracked separately; until then
// this guard covers rows-without-codes only. Tracked as OB-857.
var ErrPlanCasesUnusable = errors.New("plan cases carry no short code")

// Plan is the subset of the Observo Plan shape the CLI consumes.
//
// Cases is the snapshot at fetch time, populated by GetPlan from the response's
// top-level `cases` array — NOT from a `cases` field inside `plan`, which the
// server has never sent.
//
// Cases is trustworthy ONLY on a value returned by GetPlan or GetPlanByKey.
// ListPlans and CreatePlan hand back a Plan with Cases nil, because neither
// response carries cases at all (ListTestPlansResponse holds bare plan rows;
// CreateTestPlanResponse has no `cases` sibling). Empty there means "not
// asked for", not "no cases" — read the plan if you need them. Nothing reads
// Cases off those two paths today; this comment is here so nothing starts to.
type Plan struct {
	ID      string     `json:"id"`
	PlanKey string     `json:"plan_key"`
	Name    string     `json:"name,omitempty"`
	Cases   []PlanCase `json:"cases,omitempty"`
}

// PlanCase carries the minimum a CI consumer needs to filter tests.
//
// Short code only: this used to also carry a Title, which the server has never
// sent on a plan-case row (pb.TestPlanCase has no such field). A field that is
// always empty is the same trap as the one OB-852 is about, one size down, so
// it is gone rather than left to be read as "this case has no title".
type PlanCase struct {
	ShortCode string `json:"short_code"`
}

// planCaseWire is a plan-case row as GetTestPlan actually sends it — a sibling
// of `plan`, not a member of it. See testdata/plan_get_response.json, a
// verbatim capture of a real response body.
//
// TestCaseID is decoded but unused: it is what the row carried BEFORE OB-852
// added a short code, and naming it here keeps the wire shape legible next to
// the field that replaced it for this purpose.
type planCaseWire struct {
	TestCaseID string `json:"test_case_id"`
	ShortCode  string `json:"short_code"`
}

// getPlanResponse mirrors pb.GetTestPlanResponse: `plan` and `cases` are
// siblings. The CLI used to declare `cases` inside Plan and read plan.Cases,
// which is always nil against the real server — every plan looked empty, and
// `plan resolve --format grep` answered with its never-match sentinel. Nothing
// caught it because the test encoded the CLI's own Plan struct as the response
// body, so the client was only ever parsing a shape it had defined itself.
type getPlanResponse struct {
	Plan  Plan           `json:"plan"`
	Cases []planCaseWire `json:"cases"`
}

// planCasesFromWire projects the wire rows onto the shape the CLI consumes,
// refusing anything a short-code filter cannot be built from.
//
// Strict about a missing code on purpose: dropping the offending rows and
// keeping the rest would emit a filter that runs SOME of the plan while
// reporting the plan ran — the quiet under-run this ticket exists to remove. A
// plan with no rows at all is not an error; it is an empty plan.
func planCasesFromWire(rows []planCaseWire) ([]PlanCase, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]PlanCase, 0, len(rows))
	missing := 0
	for _, r := range rows {
		if r.ShortCode == "" {
			missing++
			continue
		}
		out = append(out, PlanCase{ShortCode: r.ShortCode})
	}
	if missing > 0 {
		return nil, fmt.Errorf("%w: %d of %d plan case(s) came back without one; "+
			"a filter built from the remaining %d would silently under-run the plan",
			ErrPlanCasesUnusable, missing, len(rows), len(out))
	}
	return out, nil
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
	cases, err := planCasesFromWire(resp.Cases)
	if err != nil {
		return nil, fmt.Errorf("GetPlan %s: %w", planID, err)
	}
	plan := resp.Plan
	plan.Cases = cases
	return &plan, nil
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
// the matched plan by UUID (ListTestPlans returns plan rows only — no
// `cases` sibling at all — so the second hop is required for consumers
// like --format=grep).
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
		p, err := c.GetPlan(ctx, projectID, planIDOrKey)
		if err != nil {
			var herr *HTTPError
			if errors.As(err, &herr) && herr.StatusCode == 404 {
				return nil, fmt.Errorf("%w: plan_id %s in project %s", ErrPlanNotFound, planIDOrKey, projectID)
			}
			return nil, err
		}
		return p, nil
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
	return nil, fmt.Errorf("%w: plan_key %q in project %s", ErrPlanNotFound, planIDOrKey, projectID)
}

type createPlanBody struct {
	Name        string   `json:"name"`
	PlanKey     string   `json:"plan_key,omitempty"`
	TestCaseIDs []string `json:"test_case_ids,omitempty"`
}

// CreatePlan creates a plan (optionally seeded with ordered case UUIDs) via
// POST /api/projects/{project_id}/plans. Used by `jvm import --chain=flat` to
// preserve an order-dependent chain's sequence as a plan.
//
// The returned Plan has Cases nil — CreateTestPlanResponse carries no cases,
// even when the create seeded some. Re-read with GetPlan to see them.
func (c *Client) CreatePlan(ctx context.Context, projectID, planKey, name string, caseIDs []string) (*Plan, error) {
	if projectID == "" {
		return nil, errors.New("CreatePlan: project_id required")
	}
	if name == "" {
		return nil, errors.New("CreatePlan: name required")
	}
	path := "/api/projects/" + url.PathEscape(projectID) + "/plans"
	body := createPlanBody{Name: name, PlanKey: planKey, TestCaseIDs: caseIDs}
	var resp getPlanResponse
	if err := c.DoJSON(ctx, "POST", path, body, &resp); err != nil {
		return nil, err
	}
	if resp.Plan.ID == "" {
		return nil, errors.New("CreatePlan: response missing plan.id")
	}
	return &resp.Plan, nil
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
