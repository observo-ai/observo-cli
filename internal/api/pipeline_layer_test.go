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

func TestPatchPipelineLayer_PathBodyShape(t *testing.T) {
	var gotPath, gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"run": Run{ID: "run-uuid"},
		})
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	_, err := c.PatchPipelineLayer(context.Background(), PatchPipelineLayerRequest{
		ProjectID: "OB",
		RunID:     "run-uuid",
		LayerID:   "frontend-unit",
		Layer: PipelineLayer{
			DisplayName: "Frontend (Vitest)",
			Framework:   "vitest",
			Total:       10, Passed: 9, Failed: 1, DurationMs: 1200,
			JunitAttachmentID: "junit-att-id",
			Coverage:          &LayerCoverage{LcovAttachmentID: "lcov-att-id"},
		},
	})
	if err != nil {
		t.Fatalf("PatchPipelineLayer: %v", err)
	}
	if gotMethod != "PATCH" {
		t.Errorf("method: %s", gotMethod)
	}
	if gotPath != "/api/runs/run-uuid/pipeline/layers/frontend-unit" {
		t.Errorf("path: %s", gotPath)
	}
	if !strings.Contains(gotBody, `"project_id":"OB"`) {
		t.Errorf("body missing project_id: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"id":"frontend-unit"`) {
		t.Errorf("body missing auto-filled layer.id: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"junit_attachment_id":"junit-att-id"`) {
		t.Errorf("body missing junit_attachment_id: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"lcov_attachment_id":"lcov-att-id"`) {
		t.Errorf("body missing nested coverage.lcov_attachment_id: %s", gotBody)
	}
}

func TestPatchPipelineLayer_RejectsPathBodyIDMismatch(t *testing.T) {
	c, _ := New(Options{BaseURL: "https://x", APIKey: "k"})
	_, err := c.PatchPipelineLayer(context.Background(), PatchPipelineLayerRequest{
		ProjectID: "OB", RunID: "r", LayerID: "from-path",
		Layer: PipelineLayer{ID: "from-body", DisplayName: "X"},
	})
	if err == nil {
		t.Fatal("expected error on layer_id mismatch")
	}
}

func TestPatchPipelineLayer_RequiresIDs(t *testing.T) {
	c, _ := New(Options{BaseURL: "https://x", APIKey: "k"})
	for _, tc := range []struct {
		name string
		req  PatchPipelineLayerRequest
	}{
		{"no project", PatchPipelineLayerRequest{RunID: "r", LayerID: "l", Layer: PipelineLayer{DisplayName: "x"}}},
		{"no run", PatchPipelineLayerRequest{ProjectID: "p", LayerID: "l", Layer: PipelineLayer{DisplayName: "x"}}},
		{"no layer", PatchPipelineLayerRequest{ProjectID: "p", RunID: "r", Layer: PipelineLayer{DisplayName: "x"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.PatchPipelineLayer(context.Background(), tc.req); err == nil {
				t.Errorf("expected error")
			}
		})
	}
}
