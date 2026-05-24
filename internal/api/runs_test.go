package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCreateRun_PostsJSONAndDecodesRunID(t *testing.T) {
	var gotPath, gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"run":{"id":"run-uuid","project_id":"OB","name":"Pipeline abc1234","status":"queued"}}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	run, err := c.CreateRun(context.Background(), CreateRunRequest{
		ProjectID:        "OB",
		Name:             "Pipeline abc1234",
		AutomationStatus: "automated",
		Pipeline: &Pipeline{
			Kind:      "pipeline",
			CommitSHA: "abc1234567",
			Branch:    "main",
		},
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if run.ID != "run-uuid" {
		t.Errorf("run.ID: got %q", run.ID)
	}
	if gotMethod != "POST" {
		t.Errorf("method: got %s", gotMethod)
	}
	if gotPath != "/api/projects/OB/runs" {
		t.Errorf("path: got %s", gotPath)
	}
	// Check the body includes pipeline + commit_sha snake_case.
	if !strings.Contains(gotBody, `"commit_sha":"abc1234567"`) {
		t.Errorf("body missing commit_sha: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"automation_status":"automated"`) {
		t.Errorf("body missing automation_status: %s", gotBody)
	}
}

func TestCreateRun_ErrorsOnMissingProjectID(t *testing.T) {
	c, _ := New(Options{BaseURL: "https://x", APIKey: "k"})
	_, err := c.CreateRun(context.Background(), CreateRunRequest{Name: "x"})
	if err == nil {
		t.Fatal("expected error for missing project_id")
	}
	if !strings.Contains(err.Error(), "project_id") {
		t.Errorf("error should mention project_id: %v", err)
	}
}

func TestCreateRun_ErrorsWhenServerOmitsRunID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"run":{}}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	_, err := c.CreateRun(context.Background(), CreateRunRequest{ProjectID: "OB"})
	if err == nil {
		t.Fatal("expected error when server omits run.id")
	}
}

func TestCreateRun_URLEncodesProjectID(t *testing.T) {
	var gotRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// r.URL.Path is decoded; r.URL.RawPath preserves the escaped form
		// (and is set only when the path actually contains escapes).
		gotRaw = r.URL.RawPath
		_, _ = w.Write([]byte(`{"run":{"id":"x"}}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	_, _ = c.CreateRun(context.Background(), CreateRunRequest{ProjectID: "weird code/with slash"})
	// PathEscape produces %20 for space and %2F for /. Without escaping
	// the path would be split into extra segments by the slash, breaking
	// the {project_id} param. Assert the raw path carries %2F.
	if !strings.Contains(gotRaw, "%2F") {
		t.Errorf("project_id slash not URL-encoded; rawpath=%q", gotRaw)
	}
	if !strings.Contains(gotRaw, "%20") {
		t.Errorf("project_id space not URL-encoded; rawpath=%q", gotRaw)
	}
}

func TestUpdateRun_PatchesStatus(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"run": Run{ID: "run-uuid", ProjectID: "OB", Status: "passed"},
		})
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	run, err := c.UpdateRun(context.Background(), UpdateRunRequest{
		ProjectID: "OB", RunID: "run-uuid", Status: "passed",
	})
	if err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}
	if run.Status != "passed" {
		t.Errorf("status: got %q", run.Status)
	}
	if gotMethod != "PATCH" {
		t.Errorf("method: got %s", gotMethod)
	}
	if gotPath != "/api/projects/OB/runs/run-uuid" {
		t.Errorf("path: got %s", gotPath)
	}
	if !strings.Contains(gotBody, `"status":"passed"`) {
		t.Errorf("body missing status: %s", gotBody)
	}
}

func TestUpdateRun_RequiresProjectAndRunID(t *testing.T) {
	c, _ := New(Options{BaseURL: "https://x", APIKey: "k"})
	if _, err := c.UpdateRun(context.Background(), UpdateRunRequest{RunID: "r"}); err == nil {
		t.Error("expected err for missing project_id")
	}
	if _, err := c.UpdateRun(context.Background(), UpdateRunRequest{ProjectID: "p"}); err == nil {
		t.Error("expected err for missing run_id")
	}
}
