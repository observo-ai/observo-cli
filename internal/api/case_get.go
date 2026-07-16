package api

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// TestCase is the subset of the Observo TestCase shape the CLI consumes
// (currently for `jvm stub`). Field names mirror the server's snake_case
// JSON (grpc-gateway MarshalOptions.UseProtoNames=true).
type TestCase struct {
	ShortCode      string         `json:"short_code"`
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Layer          string         `json:"layer"` // "LAYER_API" | "LAYER_E2E" | "LAYER_UNIT" | ""
	SuiteID        string         `json:"suite_id"`
	ProjectID      string         `json:"project_id"`
	PreConditions  string         `json:"pre_conditions"`
	PostConditions string         `json:"post_conditions"`
	Steps          []TestCaseStep `json:"steps"`
}

// TestCaseStep mirrors the server's TestCaseStep proto (optional strings
// are omitted when unset).
type TestCaseStep struct {
	Action     string `json:"action"`
	Data       string `json:"data"`
	Expected   string `json:"expected"`
	StepNumber int    `json:"step_number"`
}

// LayerName strips the proto "LAYER_" prefix, yielding "API" / "E2E" /
// "UNIT" (or "" when unset). Tolerates an already-bare value.
func (tc TestCase) LayerName() string {
	return strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(tc.Layer)), "LAYER_")
}

type getTestCaseResponse struct {
	TestCase TestCase `json:"test_case"`
}

// GetTestCaseByCode resolves a test case by its compound short code
// (e.g. "PD-101") via GET /api/case/{compound_code}. The code encodes the
// project, so no project_id is needed.
//
// Route verified against the server (separate repo): proto
// service_observo.proto binds rpc GetTestCaseByCompoundCode to
// `get: "/api/case/{compound_code}"`; the handler
// gapi/rpc_get_test_case_by_compound_code.go authorizes via
// server.authorize(...) — the account-API-key path (not JWT-only
// authorizedUser) — so the CLI's account key is accepted. This is a
// deliberate top-level (non-project-scoped) route because the compound
// code is globally unique per account.
func (c *Client) GetTestCaseByCode(ctx context.Context, code string) (*TestCase, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, errors.New("GetTestCaseByCode: code required")
	}
	path := "/api/case/" + url.PathEscape(code)

	var resp getTestCaseResponse
	if err := c.DoJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.TestCase.ShortCode == "" {
		return nil, errors.New("GetTestCaseByCode: response missing test_case.short_code")
	}
	return &resp.TestCase, nil
}
