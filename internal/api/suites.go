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

type createSuiteBody struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// CreateSuite creates a suite under a project via
// POST /api/projects/{project_id}/suites.
func (c *Client) CreateSuite(ctx context.Context, projectID, name string) (*Suite, error) {
	if projectID == "" {
		return nil, errors.New("CreateSuite: project_id required")
	}
	if name == "" {
		return nil, errors.New("CreateSuite: name required")
	}
	path := "/api/projects/" + url.PathEscape(projectID) + "/suites"
	var resp getSuiteResponse
	if err := c.DoJSON(ctx, "POST", path, createSuiteBody{Name: name}, &resp); err != nil {
		return nil, err
	}
	if resp.Suite.ID == "" {
		return nil, errors.New("CreateSuite: response missing suite.id")
	}
	return &resp.Suite, nil
}

// suiteTreeNode mirrors the server's TestSuiteTree ({suite, children[]}) —
// ListTestSuites returns a nested tree, which we flatten.
type suiteTreeNode struct {
	Suite    Suite           `json:"suite"`
	Children []suiteTreeNode `json:"children"`
}

type listSuitesResponse struct {
	Suites []suiteTreeNode `json:"suites"`
}

// ListSuites returns every suite in a project as a flat slice (the server
// returns a tree; we flatten depth-first). Used to dedup — reuse an existing
// suite by name instead of creating a duplicate on re-import.
func (c *Client) ListSuites(ctx context.Context, projectID string) ([]Suite, error) {
	if projectID == "" {
		return nil, errors.New("ListSuites: project_id required")
	}
	path := "/api/projects/" + url.PathEscape(projectID) + "/suites"
	var resp listSuitesResponse
	if err := c.DoJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	var out []Suite
	var walk func(n suiteTreeNode)
	walk = func(n suiteTreeNode) {
		if n.Suite.ID != "" {
			out = append(out, n.Suite)
		}
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	for _, n := range resp.Suites {
		walk(n)
	}
	return out, nil
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
