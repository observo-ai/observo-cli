package api

import (
	"context"
	"errors"
	"net/url"
)

// Suite is the subset of the Observo TestSuite shape the CLI consumes.
// Observo suites are flat (no parent), so a suite name maps to a single
// Allure @Feature; there is no sub-suite / @Story level.
type Suite struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type getSuiteResponse struct {
	Suite Suite `json:"suite"`
}

// GetSuite resolves a suite by UUID within a project via
// GET /api/projects/{project_id}/suites/{id}.
//
// Route verified against the server (separate repo): proto
// service_observo.proto binds rpc GetTestSuite to
// `get: "/api/projects/{project_id}/suites/{id}"`; the handler authorizes
// via server.authorizeProjectAccessOrAPIKey(...), so the CLI's account key
// is accepted.
func (c *Client) GetSuite(ctx context.Context, projectID, suiteID string) (*Suite, error) {
	if projectID == "" {
		return nil, errors.New("GetSuite: project_id required")
	}
	if suiteID == "" {
		return nil, errors.New("GetSuite: suite_id required")
	}
	path := "/api/projects/" + url.PathEscape(projectID) + "/suites/" + url.PathEscape(suiteID)

	var resp getSuiteResponse
	if err := c.DoJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Suite.ID == "" {
		return nil, errors.New("GetSuite: response missing suite.id")
	}
	return &resp.Suite, nil
}
