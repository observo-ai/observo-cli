package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCreateTestCase_SuiteScopedWithSourceAndSteps(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"test_case":{"id":"c-uuid","short_code":"PD-101","name":"one"}}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	tc, err := c.CreateTestCase(context.Background(), CreateTestCaseRequest{
		SuiteID: "s1", Name: "one", Layer: "LAYER_API", Priority: "PRIORITY_MEDIUM",
		Source: "CASE_SOURCE_IMPORTED",
		Steps:  []TestCaseStep{{Action: "POST /wallet", Expected: "201"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/suites/s1/cases" {
		t.Errorf("path = %s", gotPath)
	}
	if gotBody["source"] != "CASE_SOURCE_IMPORTED" || gotBody["layer"] != "LAYER_API" {
		t.Errorf("body missing source/layer: %v", gotBody)
	}
	// Scope id must NOT be in the body (it's a path param).
	if _, bad := gotBody["suite_id"]; bad {
		t.Errorf("suite_id must not be in body: %v", gotBody)
	}
	if tc.ID != "c-uuid" || tc.ShortCode != "PD-101" {
		t.Errorf("case: %+v", tc)
	}
}

func TestCreateTestCase_RootScopedWhenNoSuite(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"test_case":{"id":"c","short_code":"PD-1","name":"n"}}`))
	}))
	defer srv.Close()
	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k"})
	if _, err := c.CreateTestCase(context.Background(), CreateTestCaseRequest{
		ProjectID: "PD", Name: "n", Layer: "LAYER_E2E",
	}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/projects/PD/cases" {
		t.Errorf("root path = %s", gotPath)
	}
}

func TestCreateTestCase_RequiresNameAndScope(t *testing.T) {
	c, _ := New(Options{BaseURL: "https://x", APIKey: "k"})
	if _, err := c.CreateTestCase(context.Background(), CreateTestCaseRequest{SuiteID: "s"}); err == nil {
		t.Error("expected error for missing name")
	}
	if _, err := c.CreateTestCase(context.Background(), CreateTestCaseRequest{Name: "n"}); err == nil {
		t.Error("expected error for missing scope")
	}
}

func TestCreateSuiteAndListSuites_FlattensTree(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST":
			w.Write([]byte(`{"suite":{"id":"s-new","name":"PD Wallet"}}`))
		case r.Method == "GET":
			// Nested tree: root → child.
			w.Write([]byte(`{"suites":[{"suite":{"id":"root","name":"Root"},"children":[{"suite":{"id":"s1","name":"PD Wallet"},"children":[]}]}]}`))
		}
	}))
	defer srv.Close()
	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k"})

	s, err := c.CreateSuite(context.Background(), "PD", "PD Wallet")
	if err != nil || s.ID != "s-new" {
		t.Fatalf("CreateSuite: %v %+v", err, s)
	}

	list, err := c.ListSuites(context.Background(), "PD")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 flattened suites (root + child), got %d: %+v", len(list), list)
	}
	found := false
	for _, x := range list {
		if x.ID == "s1" && x.Name == "PD Wallet" {
			found = true
		}
	}
	if !found {
		t.Errorf("nested child suite not flattened: %+v", list)
	}
}

func TestCreatePlan_WithOrderedCaseIDs(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"plan":{"id":"p1","plan_key":"PD-CHAIN"}}`))
	}))
	defer srv.Close()
	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k"})
	p, err := c.CreatePlan(context.Background(), "PD", "PD-CHAIN", "PD chain", []string{"c1", "c2"})
	if err != nil || p.PlanKey != "PD-CHAIN" {
		t.Fatalf("CreatePlan: %v %+v", err, p)
	}
	ids, _ := gotBody["test_case_ids"].([]any)
	if len(ids) != 2 || ids[0] != "c1" || ids[1] != "c2" {
		t.Errorf("case id order not preserved: %v", gotBody["test_case_ids"])
	}
}
