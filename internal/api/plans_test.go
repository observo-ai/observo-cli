package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const validUUID = "11111111-2222-3333-4444-555555555555"

func TestGetPlan_DirectFetchByUUID(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plan": Plan{
				ID: validUUID, PlanKey: "REGR-MAIN-CI", Name: "Main CI regression",
				Cases: []PlanCase{{ShortCode: "OB-50"}, {ShortCode: "OB-51"}},
			},
		})
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	plan, err := c.GetPlan(context.Background(), "OB", validUUID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if gotMethod != "GET" || gotPath != "/api/projects/OB/plans/"+validUUID {
		t.Errorf("method/path: %s %s", gotMethod, gotPath)
	}
	if plan.ID != validUUID || len(plan.Cases) != 2 {
		t.Errorf("plan: %+v", plan)
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
			_ = json.NewEncoder(w).Encode(map[string]any{
				"plan": Plan{
					ID: validUUID, PlanKey: "REGR-MAIN-CI",
					Cases: []PlanCase{{ShortCode: "OB-50"}},
				},
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
		{"11111111-2222-3333-4444-55555555555", false}, // 11 chars in last group
		{"11111111-2222-3333-4444-5555555555550", false}, // 13 chars
		{"", false},
		{"not-a-uuid-at-all", false},
	} {
		if got := isUUID(tc.in); got != tc.want {
			t.Errorf("isUUID(%q): got %v want %v", tc.in, got, tc.want)
		}
	}
}
