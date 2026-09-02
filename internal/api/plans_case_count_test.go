package api

// OB-857: the shape OB-852 could not close. The gateway omits an empty `cases`
// array (EmitUnpopulated is off), so a plan nobody has attached anything to and
// a response whose array was renamed, moved or re-nested arrive as the same
// bytes — no key — and the CLI answered "empty plan" to both. That is the shape
// the original incident actually took: CI reported the regression plan empty,
// ran a smaller tier, stayed green, and sent the reader to the dashboard to
// attach cases that were already attached.
//
// TestPlan.case_count is the server's half of the fix. These tests pin the
// client's: a positive count that the rows do not match is a broken contract,
// while zero stays a claim about nothing.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The count decodes off a real body and agrees with the cases beside it, so the
// guard is silent on the ordinary response. Fixture is the verbatim OB-852
// capture with the one field this change adds.
func TestGetPlan_CaseCountFromServerAgreesWithTheCases(t *testing.T) {
	srv := serveFixture(t, "plan_get_response_with_count.json", nil)
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	plan, err := c.GetPlan(context.Background(), "OB", validUUID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if plan.CaseCount != 2 {
		t.Errorf("case_count decoded as %d, want 2 — check the JSON field name", plan.CaseCount)
	}
	if len(plan.Cases) != 2 {
		t.Fatalf("cases: %+v", plan.Cases)
	}
}

// The bug this ticket is about: the plan says it holds cases and the array is
// absent. Reporting that as an empty plan is what sent the reader to attach
// cases that were already attached.
func TestGetPlan_PositiveCountWithNoCases_IsRejectedNotReportedEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"plan":{"id":"` + validUUID + `","plan_key":"REGR-MAIN-CI","case_count":35}}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	_, err := c.GetPlan(context.Background(), "OB", validUUID)
	if err == nil {
		t.Fatal("expected an error; a plan that reports 35 cases and sends none is not an empty plan")
	}
	if !errors.Is(err, ErrPlanCasesUnusable) {
		t.Errorf("want ErrPlanCasesUnusable, got %v", err)
	}
	if !strings.Contains(err.Error(), "35") {
		t.Errorf("the error should name the count the plan reported; got: %v", err)
	}
}

// A short array is the same class one size down — rows dropped somewhere
// between the query and the wire — and would otherwise resolve to a filter that
// runs part of the plan while reporting the plan ran.
func TestGetPlan_CountHigherThanTheRowsSent_IsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"plan":{"id":"` + validUUID + `","case_count":3},"cases":[` +
			`{"test_case_id":"a","short_code":"OB-50"}]}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	_, err := c.GetPlan(context.Background(), "OB", validUUID)
	if !errors.Is(err, ErrPlanCasesUnusable) {
		t.Fatalf("want ErrPlanCasesUnusable, got %v", err)
	}
	if !strings.Contains(err.Error(), "3") || !strings.Contains(err.Error(), "1") {
		t.Errorf("the error should name both numbers; got: %v", err)
	}
}

// A server older than OB-857 sends no case_count at all. It decodes as zero
// beside a full `cases` array, and rejecting that would break the CLI against
// every server deployed before this shipped — so zero means "no claim", never
// "empty".
func TestGetPlan_ServerWithoutCaseCount_StillReadsItsCases(t *testing.T) {
	srv := serveFixture(t, "plan_get_response.json", nil)
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	plan, err := c.GetPlan(context.Background(), "OB", validUUID)
	if err != nil {
		t.Fatalf("a response with no case_count must still resolve: %v", err)
	}
	if plan.CaseCount != 0 {
		t.Errorf("case_count should be 0 when absent, got %d", plan.CaseCount)
	}
	if len(plan.Cases) != 2 {
		t.Errorf("cases: %+v", plan.Cases)
	}
}

// And the genuinely empty plan keeps working: no count, no cases, no error.
// This is the shape that stays ambiguous by design — it is the one a client
// should keep treating as empty.
func TestGetPlan_EmptyPlanWithNoCountIsStillNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"plan":{"id":"` + validUUID + `","plan_key":"EMPTY"}}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	plan, err := c.GetPlan(context.Background(), "OB", validUUID)
	if err != nil {
		t.Fatalf("empty plan should not error: %v", err)
	}
	if len(plan.Cases) != 0 || plan.CaseCount != 0 {
		t.Errorf("plan: %+v", plan)
	}
}

// The guard itself, at its edges. A negative count is nonsense the server
// cannot send today, and it must not become a way to fail a good response.
func TestCheckPlanCaseCount(t *testing.T) {
	for _, tc := range []struct {
		name          string
		reported, got int
		wantErr       bool
	}{
		{"no claim, no rows", 0, 0, false},
		{"no claim, rows present (pre-OB-857 server)", 0, 7, false},
		{"claim matches", 3, 3, false},
		{"claim with nothing behind it", 35, 0, true},
		{"claim higher than the rows", 3, 1, true},
		{"more rows than claimed", 1, 3, true},
		{"negative claim is not a failure", -1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkPlanCaseCount(tc.reported, tc.got)
			if tc.wantErr && err == nil {
				t.Fatalf("checkPlanCaseCount(%d, %d) = nil, want an error", tc.reported, tc.got)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("checkPlanCaseCount(%d, %d) = %v, want nil", tc.reported, tc.got, err)
			}
		})
	}
}
