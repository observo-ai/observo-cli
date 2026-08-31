package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const validUUID = "11111111-2222-3333-4444-555555555555"

// serveFixture replies to every request with the named file from testdata.
//
// The fixtures are captured response BODIES, not values encoded from this
// package's own structs. That difference is the point of OB-852: the previous
// test encoded a Plan{Cases: ...} as the response, so it proved only that the
// client could parse a shape the client itself had defined, and stayed green
// for months while the real server sent something else.
func serveFixture(t *testing.T, name string, seen *string) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = r.Method + " " + r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}

// The cases a plan resolves to come from the response's TOP-LEVEL `cases`
// array. Fixture is a verbatim capture of a real GetTestPlan body.
func TestGetPlan_ReadsCasesFromCapturedServerResponse(t *testing.T) {
	var seen string
	srv := serveFixture(t, "plan_get_response.json", &seen)
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	plan, err := c.GetPlan(context.Background(), "OB", validUUID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if seen != "GET /api/projects/OB/plans/"+validUUID {
		t.Errorf("request: %s", seen)
	}
	// The plan object itself decodes too — plan_key is the field `plan resolve
	// --plan` matches on, and field naming against a real body is what this
	// whole fixture exists to pin.
	if plan.ID != "193e2b1e-22a5-4732-9579-d2938b2c06b3" {
		t.Errorf("plan.id: %q", plan.ID)
	}
	if plan.PlanKey != "REGR-MAIN-CI" {
		t.Errorf("plan.plan_key: %q", plan.PlanKey)
	}
	got := make([]string, 0, len(plan.Cases))
	for _, pc := range plan.Cases {
		got = append(got, pc.ShortCode)
	}
	want := []string{"JFHYPGZZ-1", "JFHYPGZZ-2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("short codes: got %v want %v", got, want)
	}
}

// The shape that shipped before OB-852: cases present, no short_code on any of
// them. It must be an error the caller can act on, NOT an empty case list —
// an empty list is indistinguishable from an unattached plan, and that
// collapse is what sent months of CI runs to a fallback tier in silence.
//
// Body is the real pre-fix production response with the identifiers replaced:
// the shape is what this pins, and the live tenant/plan/case UUIDs do not
// belong in a public repo's history.
func TestGetPlan_ResponseWithoutShortCodes_IsRejectedNotSilentlyEmpty(t *testing.T) {
	srv := serveFixture(t, "plan_get_response_pre_ob852.json", nil)
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	_, err := c.GetPlan(context.Background(), "OB", validUUID)
	if err == nil {
		t.Fatal("expected an error; a plan whose cases have no short code is not an empty plan")
	}
	if !errors.Is(err, ErrPlanCasesUnusable) {
		t.Errorf("want ErrPlanCasesUnusable, got %v", err)
	}
	if !strings.Contains(err.Error(), "3 of 3") {
		t.Errorf("error should say how many rows lacked a code; got: %v", err)
	}
}

// A plan with nothing attached is a legitimate answer, not a failure — the
// caller renders it as "no cases", which is a different instruction to the
// reader than "the server is broken".
func TestGetPlan_PlanWithNoCasesIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"plan":{"id":"` + validUUID + `","plan_key":"EMPTY"}}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	plan, err := c.GetPlan(context.Background(), "OB", validUUID)
	if err != nil {
		t.Fatalf("empty plan should not error: %v", err)
	}
	if len(plan.Cases) != 0 {
		t.Errorf("cases: %+v", plan.Cases)
	}
}

// One row missing a code fails the whole read rather than quietly resolving to
// the rest — a partial filter reports the plan ran while running less of it.
func TestGetPlan_PartiallyMissingShortCodes_Rejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"plan":{"id":"` + validUUID + `"},"cases":[` +
			`{"test_case_id":"a","short_code":"OB-50"},` +
			`{"test_case_id":"b"}]}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	_, err := c.GetPlan(context.Background(), "OB", validUUID)
	if !errors.Is(err, ErrPlanCasesUnusable) {
		t.Fatalf("want ErrPlanCasesUnusable, got %v", err)
	}
	if !strings.Contains(err.Error(), "1 of 2") {
		t.Errorf("error should name the shortfall; got: %v", err)
	}
}

func TestGetPlan_RequiresIDs(t *testing.T) {
	c, _ := New(Options{BaseURL: "https://x", APIKey: "k"})
	if _, err := c.GetPlan(context.Background(), "", "k"); err == nil {
		t.Error("expected err for empty project_id")
	}
	if _, err := c.GetPlan(context.Background(), "p", ""); err == nil {
		t.Error("expected err for empty plan_id")
	}
}

func TestGetPlan_ErrorsWhenServerOmitsPlanID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"plan":{}}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	_, err := c.GetPlan(context.Background(), "OB", validUUID)
	if err == nil {
		t.Fatal("expected error when server omits plan.id")
	}
	if !strings.Contains(err.Error(), "plan.id") {
		t.Errorf("error wording: %v", err)
	}
}

// GetPlanByKey is the user-facing resolver. UUID inputs take the direct
// path; keys go through list+filter+re-fetch.

func TestGetPlanByKey_UUIDInput_GoesDirectlyThroughGetPlan(t *testing.T) {
	var calls atomic.Int32
	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		lastPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plan": Plan{ID: validUUID, PlanKey: "X"},
		})
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	if _, err := c.GetPlanByKey(context.Background(), "OB", validUUID); err != nil {
		t.Fatalf("GetPlanByKey(UUID): %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("UUID input should be 1 call (direct GetPlan); got %d", calls.Load())
	}
	if !strings.HasSuffix(lastPath, "/plans/"+validUUID) {
		t.Errorf("expected direct GET, got path %s", lastPath)
	}
}

func TestGetPlanByKey_KeyInput_ListsThenFetches(t *testing.T) {
	var listCalls, getCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/plans"):
			listCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"plans": []Plan{
					{ID: "other-uuid-aaaa-bbbb-cccc-111111111111", PlanKey: "OTHER"},
					{ID: validUUID, PlanKey: "REGR-MAIN-CI"},
				},
			})
		default:
			getCalls.Add(1)
			// Server shape: `cases` is a sibling of `plan`, not a field in it.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"plan":  Plan{ID: validUUID, PlanKey: "REGR-MAIN-CI"},
				"cases": []planCaseWire{{TestCaseID: "c1", ShortCode: "OB-50"}},
			})
		}
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	plan, err := c.GetPlanByKey(context.Background(), "OB", "REGR-MAIN-CI")
	if err != nil {
		t.Fatalf("GetPlanByKey(key): %v", err)
	}
	if listCalls.Load() != 1 || getCalls.Load() != 1 {
		t.Errorf("expected 1 LIST + 1 GET; got list=%d get=%d", listCalls.Load(), getCalls.Load())
	}
	if plan.ID != validUUID || len(plan.Cases) != 1 {
		t.Errorf("resolved plan: %+v", plan)
	}
}

func TestGetPlanByKey_KeyNotFound_ReturnsClearError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plans": []Plan{{ID: validUUID, PlanKey: "OTHER"}},
		})
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	_, err := c.GetPlanByKey(context.Background(), "OB", "DOES-NOT-EXIST")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "DOES-NOT-EXIST") {
		t.Errorf("error should name the missing key; got: %v", err)
	}
}

func TestIsUUID(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{validUUID, true},
		{strings.ToUpper(validUUID), true},
		{"REGR-MAIN-CI", false},
		{"11111111-2222-3333-4444-55555555555", false},   // 11 chars in last group
		{"11111111-2222-3333-4444-5555555555550", false}, // 13 chars
		{"", false},
		{"not-a-uuid-at-all", false},
	} {
		if got := isUUID(tc.in); got != tc.want {
			t.Errorf("isUUID(%q): got %v want %v", tc.in, got, tc.want)
		}
	}
}

// The list hop is the first half of every `--plan REGR-MAIN-CI` invocation:
// GetPlanByKey resolves a key by listing and matching plan_key. It was the one
// step still tested only against a body this package encodes from its own
// structs — the anti-pattern OB-852 was, one hop up — so a rename of `plans`
// or `plan_key` would have gone the same way: no match, ErrPlanNotFound, and
// CI quietly back on the fallback tier.
func TestListPlans_ParsesCapturedServerResponse(t *testing.T) {
	srv := serveFixture(t, "plan_list_response.json", nil)
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	plans, err := c.ListPlans(context.Background(), "OB")
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	byKey := make(map[string]string, len(plans))
	for _, p := range plans {
		byKey[p.PlanKey] = p.ID
	}
	want := map[string]string{
		"REGR-MAIN-CI": "0c09f220-3e4c-4b0e-9755-ff29877284c4",
		"SMOKE":        "ced4c835-6da2-4931-9f2c-b14c23bedf60",
	}
	if !reflect.DeepEqual(byKey, want) {
		t.Errorf("plan_key -> id: got %v want %v", byKey, want)
	}
}

// And the key -> UUID resolution GetPlanByKey performs, driven end to end by
// two captured bodies: the real list response picks the UUID, the real get
// response turns it into short codes.
func TestGetPlanByKey_ResolvesThroughCapturedBodies(t *testing.T) {
	list, err := os.ReadFile(filepath.Join("testdata", "plan_list_response.json"))
	if err != nil {
		t.Fatalf("read list fixture: %v", err)
	}
	get, err := os.ReadFile(filepath.Join("testdata", "plan_get_response.json"))
	if err != nil {
		t.Fatalf("read get fixture: %v", err)
	}
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/plans") {
			_, _ = w.Write(list)
			return
		}
		gotPath = r.URL.Path
		_, _ = w.Write(get)
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	plan, err := c.GetPlanByKey(context.Background(), "OB", "REGR-MAIN-CI")
	if err != nil {
		t.Fatalf("GetPlanByKey: %v", err)
	}
	// The UUID it re-fetched must be the one the LIST body gave for that key,
	// not the id inside the get body — otherwise the match proves nothing.
	if !strings.HasSuffix(gotPath, "/plans/0c09f220-3e4c-4b0e-9755-ff29877284c4") {
		t.Errorf("re-fetched the wrong plan: %s", gotPath)
	}
	if len(plan.Cases) != 2 {
		t.Errorf("cases: %+v", plan.Cases)
	}
}
