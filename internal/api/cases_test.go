package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBatchAddCases_PostsArrayShape(t *testing.T) {
	var gotPath, gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	if err := c.BatchAddCases(context.Background(), "r1", []string{"OB-50", "OB-51"}); err != nil {
		t.Fatalf("BatchAddCases: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/api/runs/r1/cases:batch_add" {
		t.Errorf("method/path: %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, `"test_case_ids":["OB-50","OB-51"]`) {
		t.Errorf("body: %s", gotBody)
	}
}

func TestBatchAddCases_RequiresRunIDAndCodes(t *testing.T) {
	c, _ := New(Options{BaseURL: "https://x", APIKey: "k"})
	if err := c.BatchAddCases(context.Background(), "", []string{"OB-1"}); err == nil {
		t.Error("expected err for empty run_id")
	}
	if err := c.BatchAddCases(context.Background(), "r1", nil); err == nil {
		t.Error("expected err for empty codes")
	}
}

func TestUpdateRunCase_PatchesStatus(t *testing.T) {
	var gotPath, gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	if err := c.UpdateRunCase(context.Background(), "r1", "OB-50", "failed"); err != nil {
		t.Fatalf("UpdateRunCase: %v", err)
	}
	if gotMethod != "PATCH" || gotPath != "/api/runs/r1/cases/OB-50" {
		t.Errorf("method/path: %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, `"status":"failed"`) {
		t.Errorf("body: %s", gotBody)
	}
}

func TestUpdateRunCase_RejectsInvalidStatus(t *testing.T) {
	c, _ := New(Options{BaseURL: "https://x", APIKey: "k"})
	for _, s := range []string{"", "auto", "flaky", "PASSED", "weird"} {
		if err := c.UpdateRunCase(context.Background(), "r1", "OB-50", s); err == nil {
			t.Errorf("status %q should be rejected", s)
		}
	}
	for _, s := range []string{"passed", "failed", "skipped", "blocked"} {
		if !IsValidCaseStatus(s) {
			t.Errorf("status %q should be valid", s)
		}
	}
}

func TestEnsureAndUpdateRunCase_CallsBatchAddThenPatch(t *testing.T) {
	var batchAdds, patches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ":batch_add"):
			batchAdds.Add(1)
		default:
			patches.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	if err := c.EnsureAndUpdateRunCase(context.Background(), "r1", "OB-50", "passed"); err != nil {
		t.Fatalf("EnsureAndUpdateRunCase: %v", err)
	}
	if batchAdds.Load() != 1 {
		t.Errorf("batch_add calls: got %d, want 1", batchAdds.Load())
	}
	if patches.Load() != 1 {
		t.Errorf("PATCH calls: got %d, want 1", patches.Load())
	}
}

func TestEnsureAndUpdateRunCase_BatchAddFailureStopsPatch(t *testing.T) {
	var patches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":batch_add") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"no such case"}`))
			return
		}
		patches.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	c.InitialWait = 1 * time.Millisecond
	c.MaxWait = 2 * time.Millisecond

	err := c.EnsureAndUpdateRunCase(context.Background(), "r1", "MISSING-99", "passed")
	if err == nil {
		t.Fatal("expected err to surface from batch_add 4xx")
	}
	if patches.Load() != 0 {
		t.Errorf("PATCH should NOT run after batch_add fails; calls=%d", patches.Load())
	}
}

func TestUpdateRunCaseStep_PatchesByOneBasedIndex(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	err := c.UpdateRunCaseStep(context.Background(), UpdateRunCaseStepRequest{
		RunID: "r1", ShortCode: "OB-50", StepIndex: 3,
		Status: "failed", Comment: "selector timed out",
	})
	if err != nil {
		t.Fatalf("UpdateRunCaseStep: %v", err)
	}
	if gotPath != "/api/runs/r1/cases/OB-50/steps/3" {
		t.Errorf("path: %s", gotPath)
	}
	if !strings.Contains(gotBody, `"status":"failed"`) || !strings.Contains(gotBody, `"comment":"selector timed out"`) {
		t.Errorf("body: %s", gotBody)
	}
}

func TestUpdateRunCaseStep_RequiresPositiveStepIndex(t *testing.T) {
	c, _ := New(Options{BaseURL: "https://x", APIKey: "k"})
	for _, idx := range []int{0, -1, -100} {
		err := c.UpdateRunCaseStep(context.Background(), UpdateRunCaseStepRequest{
			RunID: "r1", ShortCode: "OB-50", StepIndex: idx, Status: "passed",
		})
		if err == nil {
			t.Errorf("step_index %d should be rejected", idx)
		}
	}
}
