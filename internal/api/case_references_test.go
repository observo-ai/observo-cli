package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// OB-551: SetTestCaseReferences PUTs to /api/cases/{code}/references with the
// proto enum NAME strings and source=imported (replace-by-source on the server).
func TestSetTestCaseReferences_RequestShape(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"references":[]}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	err := c.SetTestCaseReferences(context.Background(), "DEMO-12", []CaseReferenceInput{
		{Kind: "ticket", Value: "PD-742", Label: "PD-742", URL: "https://jira/PD-742"},
		{Kind: "link", Value: "https://docs/x", Label: "doc"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if gotPath != "/api/cases/DEMO-12/references" {
		t.Errorf("path = %s", gotPath)
	}
	if gotBody["source"] != "CASE_REFERENCE_SOURCE_IMPORTED" {
		t.Errorf("source = %v, want CASE_REFERENCE_SOURCE_IMPORTED", gotBody["source"])
	}
	refs, ok := gotBody["references"].([]any)
	if !ok || len(refs) != 2 {
		t.Fatalf("references = %v", gotBody["references"])
	}
	first := refs[0].(map[string]any)
	if first["kind"] != "CASE_REFERENCE_KIND_TICKET" || first["value"] != "PD-742" || first["url"] != "https://jira/PD-742" {
		t.Errorf("first ref wrong: %v", first)
	}
	second := refs[1].(map[string]any)
	if second["kind"] != "CASE_REFERENCE_KIND_LINK" {
		t.Errorf("second ref kind wrong: %v", second)
	}
	// A link ref with no url must omit the url field (omitempty), not send "".
	if _, present := second["url"]; present {
		t.Errorf("empty url must be omitted: %v", second)
	}
}
