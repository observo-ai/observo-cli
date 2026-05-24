package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/observo-ai/observo-cli/internal/state"
)

func resetRunCaseFlags() {
	rcsProject = ""
	rcsRunID = ""
	rcsCode = ""
	rcsStatus = ""
	rcsStateFile = state.DefaultPath
}

func resetRunCaseStepFlags() {
	rcssProject = ""
	rcssRunID = ""
	rcssCode = ""
	rcssStepIdx = 0
	rcssStatus = ""
	rcssComment = ""
	rcssFileURL = ""
	rcssStateFile = state.DefaultPath
}

func TestRunCaseSet_EnsureThenPatchE2E(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetPLFlags()
	resetRunCaseFlags()
	resetRunCaseStepFlags()
	resetRootFlags()

	var batchCalls, patchCalls atomic.Int32
	var lastPatchBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ":batch_add"):
			batchCalls.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			patchCalls.Add(1)
			b, _ := io.ReadAll(r.Body)
			lastPatchBody = string(b)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	statePath := filepath.Join(t.TempDir(), "s.json")
	_ = state.Save(statePath, &state.State{RunID: "r1", ProjectID: "OB"})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "case", "set",
		"--code", "OB-50",
		"--status", "failed",
		"--state-file", statePath,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Post-fix: batch_add removed from EnsureAndUpdateRunCase because the
	// server's batch_add requires UUID + JWT-only auth (incompatible with
	// short codes + API key the CLI sends). Expect ONLY a PATCH.
	if batchCalls.Load() != 0 || patchCalls.Load() != 1 {
		t.Errorf("expected 0 batch_add + 1 PATCH (batch_add removed); got batch=%d patch=%d", batchCalls.Load(), patchCalls.Load())
	}
	if !strings.Contains(lastPatchBody, `"status":"failed"`) {
		t.Errorf("PATCH body: %s", lastPatchBody)
	}
}

func TestRunCaseSet_RejectsInvalidStatus(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetPLFlags()
	resetRunCaseFlags()
	resetRunCaseStepFlags()
	resetRootFlags()

	statePath := filepath.Join(t.TempDir(), "s.json")
	_ = state.Save(statePath, &state.State{RunID: "r1", ProjectID: "OB"})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", "https://example",
		"run", "case", "set",
		"--code", "OB-50",
		"--status", "flaky", // not in allowed enum
		"--state-file", statePath,
	})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestRunCaseStepSet_PatchesByOneBasedIndex(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetPLFlags()
	resetRunCaseFlags()
	resetRunCaseStepFlags()
	resetRootFlags()

	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	statePath := filepath.Join(t.TempDir(), "s.json")
	_ = state.Save(statePath, &state.State{RunID: "r1", ProjectID: "OB"})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "case", "step", "set",
		"--code", "OB-50",
		"--step", "2",
		"--status", "passed",
		"--comment", "scroll into view",
		"--state-file", statePath,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotPath != "/api/runs/r1/cases/OB-50/steps/2" {
		t.Errorf("path: %s", gotPath)
	}
	if !strings.Contains(gotBody, `"status":"passed"`) || !strings.Contains(gotBody, `"comment":"scroll into view"`) {
		t.Errorf("body: %s", gotBody)
	}
}

func TestRunCaseStepSet_RejectsZeroOrNegativeStep(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetPLFlags()
	resetRunCaseFlags()
	resetRunCaseStepFlags()
	resetRootFlags()

	statePath := filepath.Join(t.TempDir(), "s.json")
	_ = state.Save(statePath, &state.State{RunID: "r1", ProjectID: "OB"})

	for _, step := range []string{"0", "-1"} {
		var buf bytes.Buffer
		rootCmd.SetOut(&buf)
		rootCmd.SetErr(&buf)
		rootCmd.SetArgs([]string{
			"--api-key", "k",
			"--base-url", "https://example",
			"run", "case", "step", "set",
			"--code", "OB-50",
			"--step", step,
			"--status", "passed",
			"--state-file", statePath,
		})
		if err := rootCmd.Execute(); err == nil {
			t.Errorf("step=%s should be rejected", step)
		}
		resetRunCaseStepFlags()
	}
}
