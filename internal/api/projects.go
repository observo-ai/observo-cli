package api

import "context"

// Project is the subset of the Observo Project shape the CLI needs. Mirrors
// the wire JSON (snake_case) rather than importing server's pb/ types — the
// CLI is a separate go module and should not couple to server's protobuf deps.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Code string `json:"code"` // short code, e.g. "OB"
}

// listProjectsResponse mirrors the server's ListProjects envelope.
type listProjectsResponse struct {
	Projects []Project `json:"projects"`
}

// ListProjects calls GET /api/projects. The endpoint accepts account-scoped
// API-key auth (server: authorize → same projects the key's creator sees), so
// it doubles as an auth ping: a 2xx means the key is valid, a 401/403 surfaces
// as an *HTTPError the caller can classify.
//
// Read-only. Used by `observo doctor` to (1) validate the API key, (2) resolve
// a project short code → project, and (3) apply the sole-project fallback when
// no project variable is set (mirrors the verdict's OB-523 resolution order).
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var resp listProjectsResponse
	if err := c.DoJSON(ctx, "GET", "/api/projects", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Projects, nil
}
