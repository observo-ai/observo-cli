package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

// CaseStatus is the closed set of allowed run-case statuses.
// `flaky` is NOT a valid PATCH input — the server represents flake at
// the layer-aggregate level (PipelineLayer.Flaky). Reporters mapping
// from Playwright should fold passed-on-retry into `passed` per the
// OB-304 convention.
var caseStatuses = map[string]struct{}{
	"passed":  {},
	"failed":  {},
	"skipped": {},
	"blocked": {}, // timedOut, interrupted, infra problems
}

// IsValidCaseStatus exposes the enum to subcommands so they can fail
// fast at the CLI boundary instead of after the API round-trip.
func IsValidCaseStatus(s string) bool {
	_, ok := caseStatuses[s]
	return ok
}

// BatchAddCases attaches one or more test cases (by short code) to a
// run. Idempotent on the server side — calling twice is a no-op for
// already-attached cases, which lets the CLI safely call it before
// every PATCH without bookkeeping.
//
// We send even single-case attaches through batch_add so there's one
// code path covering both "first PATCH on this case" and "first PATCH
// on this run for this short code".
func (c *Client) BatchAddCases(ctx context.Context, runID string, shortCodes []string) error {
	if runID == "" {
		return errors.New("BatchAddCases: run_id required")
	}
	if len(shortCodes) == 0 {
		return errors.New("BatchAddCases: at least one short code required")
	}

	path := "/api/runs/" + url.PathEscape(runID) + "/cases:batch_add"
	body := map[string][]string{"test_case_ids": shortCodes}
	return c.DoJSON(ctx, "POST", path, body, nil)
}

// UpdateRunCase PATCHes a single run-case status by short code.
// On 404 (case not attached to run), caller should batch_add then retry
// — the wrapper `EnsureAndUpdateRunCase` below does that automatically.
func (c *Client) UpdateRunCase(ctx context.Context, runID, shortCode, status string) error {
	if runID == "" || shortCode == "" {
		return errors.New("UpdateRunCase: run_id and short_code required")
	}
	if !IsValidCaseStatus(status) {
		return fmt.Errorf("UpdateRunCase: invalid status %q (allowed: passed, failed, skipped, blocked)", status)
	}
	path := "/api/runs/" + url.PathEscape(runID) + "/cases/" + url.PathEscape(shortCode)
	body := map[string]string{"status": status}
	return c.DoJSON(ctx, "PATCH", path, body, nil)
}

// EnsureAndUpdateRunCase is the idempotent helper subcommands should
// call. It runs batch_add first (no-op when the case is already
// attached) then PATCHes status. Two round-trips per call — acceptable
// for v0.5.0; the optimization (skip batch_add when we know the case
// is attached) needs server-side guarantees we don't have today.
func (c *Client) EnsureAndUpdateRunCase(ctx context.Context, runID, shortCode, status string) error {
	if err := c.BatchAddCases(ctx, runID, []string{shortCode}); err != nil {
		return fmt.Errorf("ensure case %s on run: %w", shortCode, err)
	}
	return c.UpdateRunCase(ctx, runID, shortCode, status)
}

// UpdateRunCaseStepRequest mirrors the server's per-step PATCH body.
// Comment and FileURL are optional — most live-reporter flows only set
// Status. step_index is 1-based to match how operators count steps in
// the UI.
type UpdateRunCaseStepRequest struct {
	RunID     string `json:"-"`
	ShortCode string `json:"-"`
	StepIndex int    `json:"-"`

	Status  string `json:"status"`            // passed|failed|skipped|blocked
	Comment string `json:"comment,omitempty"` // free-form text, shown in dashboard
	FileURL string `json:"file_url,omitempty"` // attachment URL to surface inline
}

// UpdateRunCaseStep PATCHes a single step within a run-case by 1-based index.
// The parent case must already be attached to the run (typically because
// EnsureAndUpdateRunCase ran first for the same short code).
func (c *Client) UpdateRunCaseStep(ctx context.Context, req UpdateRunCaseStepRequest) error {
	if req.RunID == "" || req.ShortCode == "" {
		return errors.New("UpdateRunCaseStep: run_id and short_code required")
	}
	if req.StepIndex <= 0 {
		return fmt.Errorf("UpdateRunCaseStep: step_index must be >= 1 (1-based), got %d", req.StepIndex)
	}
	if !IsValidCaseStatus(req.Status) {
		return fmt.Errorf("UpdateRunCaseStep: invalid status %q", req.Status)
	}
	path := fmt.Sprintf("/api/runs/%s/cases/%s/steps/%d",
		url.PathEscape(req.RunID), url.PathEscape(req.ShortCode), req.StepIndex)
	return c.DoJSON(ctx, "PATCH", path, req, nil)
}
