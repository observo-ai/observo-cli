package api

import (
	"context"
	"errors"
	"net/url"
)

// CreateTestCaseRequest is the subset of the server's create-case body the
// CLI sends for JVM import. Enum fields carry the proto enum NAME string
// (grpc-gateway serializes enums by name), e.g. Layer="LAYER_API",
// Source="CASE_SOURCE_IMPORTED".
type CreateTestCaseRequest struct {
	SuiteID     string // when set → POST /api/suites/{suite_id}/cases
	ProjectID   string // else, for a root case → POST /api/projects/{project_id}/cases
	Name        string
	Description string
	Layer       string
	Priority    string
	Source      string
	Steps       []TestCaseStep
}

// createCaseBody is the JSON body. The scope id (suite_id / project_id) is
// bound to the URL path, so it is NEVER included here — grpc-gateway v2
// rejects a body field that duplicates a path parameter (see attachments.go).
type createCaseBody struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Layer       string           `json:"layer,omitempty"`
	Priority    string           `json:"priority,omitempty"`
	Source      string           `json:"source,omitempty"`
	Steps       []createStepBody `json:"steps,omitempty"`
}

type createStepBody struct {
	Action   string `json:"action,omitempty"`
	Data     string `json:"data,omitempty"`
	Expected string `json:"expected,omitempty"`
}

// CreateTestCase creates a single case under a suite (preferred) or a
// project root. Returns the created case incl. its server-assigned short
// code. Scope precedence: SuiteID wins over ProjectID.
func (c *Client) CreateTestCase(ctx context.Context, req CreateTestCaseRequest) (*TestCase, error) {
	if req.Name == "" {
		return nil, errors.New("CreateTestCase: name required")
	}
	var path string
	switch {
	case req.SuiteID != "":
		path = "/api/suites/" + url.PathEscape(req.SuiteID) + "/cases"
	case req.ProjectID != "":
		path = "/api/projects/" + url.PathEscape(req.ProjectID) + "/cases"
	default:
		return nil, errors.New("CreateTestCase: suite_id or project_id required")
	}

	body := createCaseBody{
		Name:        req.Name,
		Description: req.Description,
		Layer:       req.Layer,
		Priority:    req.Priority,
		Source:      req.Source,
	}
	for _, s := range req.Steps {
		body.Steps = append(body.Steps, createStepBody{Action: s.Action, Data: s.Data, Expected: s.Expected})
	}

	var resp getTestCaseResponse // {test_case: {...}} — same envelope as GetTestCase
	if err := c.DoJSON(ctx, "POST", path, body, &resp); err != nil {
		return nil, err
	}
	if resp.TestCase.ShortCode == "" {
		return nil, errors.New("CreateTestCase: response missing test_case.short_code")
	}
	return &resp.TestCase, nil
}
