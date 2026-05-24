package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetPlan_ResolvesByKey(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plan": Plan{
				ID: "plan-uuid", PlanKey: "REGR-MAIN-CI", Name: "Main CI regression",
				Cases: []PlanCase{
					{ShortCode: "OB-50", Title: "Login happy path"},
					{ShortCode: "OB-51", Title: "Password reset"},
				},
			},
		})
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	plan, err := c.GetPlan(context.Background(), "OB", "REGR-MAIN-CI")
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if gotMethod != "GET" || gotPath != "/api/projects/OB/plans/REGR-MAIN-CI" {
		t.Errorf("method/path: %s %s", gotMethod, gotPath)
	}
	if plan.ID != "plan-uuid" || plan.PlanKey != "REGR-MAIN-CI" {
		t.Errorf("plan metadata: %+v", plan)
	}
	if len(plan.Cases) != 2 || plan.Cases[0].ShortCode != "OB-50" {
		t.Errorf("cases: %+v", plan.Cases)
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
	_, err := c.GetPlan(context.Background(), "OB", "REGR")
	if err == nil {
		t.Fatal("expected error when server omits plan.id")
	}
	if !strings.Contains(err.Error(), "plan.id") {
		t.Errorf("error wording: %v", err)
	}
}
