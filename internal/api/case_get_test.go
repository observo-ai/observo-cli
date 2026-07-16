package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetTestCaseByCode(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		// Server emits proto-name JSON: layer as "LAYER_API", snake_case.
		_, _ = w.Write([]byte(`{"test_case":{
			"short_code":"PD-101","name":"trader creates a wallet",
			"description":"Wallet is created on first login","layer":"LAYER_API",
			"suite_id":"s-uuid","project_id":"p-uuid",
			"steps":[{"action":"POST /wallet","expected":"201","step_number":1}]}}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	tc, err := c.GetTestCaseByCode(context.Background(), "PD-101")
	if err != nil {
		t.Fatalf("GetTestCaseByCode: %v", err)
	}
	if gotMethod != "GET" || gotPath != "/api/case/PD-101" {
		t.Errorf("method/path: %s %s", gotMethod, gotPath)
	}
	if tc.Name != "trader creates a wallet" || tc.LayerName() != "API" {
		t.Errorf("case: %+v (layer %q)", tc, tc.LayerName())
	}
	if len(tc.Steps) != 1 || tc.Steps[0].Action != "POST /wallet" {
		t.Errorf("steps: %+v", tc.Steps)
	}
}

func TestGetTestCaseByCode_RequiresCode(t *testing.T) {
	c, _ := New(Options{BaseURL: "https://x", APIKey: "k"})
	if _, err := c.GetTestCaseByCode(context.Background(), "  "); err == nil {
		t.Error("expected err for empty code")
	}
}

func TestLayerName(t *testing.T) {
	cases := map[string]string{
		"LAYER_API": "API", "LAYER_E2E": "E2E", "LAYER_UNIT": "UNIT",
		"API": "API", "": "", "layer_api": "API",
	}
	for in, want := range cases {
		if got := (TestCase{Layer: in}).LayerName(); got != want {
			t.Errorf("LayerName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGetSuite(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"suite":{"id":"s-uuid","name":"PD Wallet"}}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	s, err := c.GetSuite(context.Background(), "p-uuid", "s-uuid")
	if err != nil {
		t.Fatalf("GetSuite: %v", err)
	}
	if gotPath != "/api/projects/p-uuid/suites/s-uuid" {
		t.Errorf("path: %s", gotPath)
	}
	if s.Name != "PD Wallet" {
		t.Errorf("suite: %+v", s)
	}
}

func TestGetSuite_RequiresIDs(t *testing.T) {
	c, _ := New(Options{BaseURL: "https://x", APIKey: "k"})
	if _, err := c.GetSuite(context.Background(), "", "s"); err == nil {
		t.Error("expected err for empty project_id")
	}
	if _, err := c.GetSuite(context.Background(), "p", ""); err == nil {
		t.Error("expected err for empty suite_id")
	}
}
