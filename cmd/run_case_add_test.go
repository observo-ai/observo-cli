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

func resetRunCaseAddFlags() {
	rcaProject = ""
	rcaRunID = ""
	rcaCodes = nil
	rcaStateFile = state.DefaultPath
}

func TestRunCaseAdd_BatchAddsByCode(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetPLFlags()
	resetRunCaseFlags()
	resetRunCaseStepFlags()
	resetRunCaseAddFlags()
	resetRootFlags()

	var batchCalls, otherCalls atomic.Int32
	var lastPath, lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":batch_add") {
			batchCalls.Add(1)
			lastPath = r.URL.Path
			b, _ := io.ReadAll(r.Body)
			lastBody = string(b)
		} else {
			otherCalls.Add(1)
		}
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
		"run", "case", "add",
		"--code", "OB-1,OB-2",
		"--state-file", statePath,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if batchCalls.Load() != 1 || otherCalls.Load() != 0 {
		t.Errorf("expected exactly 1 batch_add + 0 other; got batch=%d other=%d", batchCalls.Load(), otherCalls.Load())
	}
	if lastPath != "/api/runs/r1/cases:batch_add" {
		t.Errorf("path: %s", lastPath)
	}
	// OB-600: short codes must land in test_case_codes (not test_case_ids),
	// preserving order, so the server resolves them account/project-scoped.
	if !strings.Contains(lastBody, `"test_case_codes":["OB-1","OB-2"]`) {
		t.Errorf("body: %s", lastBody)
	}
}

func TestRunCaseAdd_RequiresCode(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetPLFlags()
	resetRunCaseFlags()
	resetRunCaseStepFlags()
	resetRunCaseAddFlags()
	resetRootFlags()

	statePath := filepath.Join(t.TempDir(), "s.json")
	_ = state.Save(statePath, &state.State{RunID: "r1", ProjectID: "OB"})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", "https://example",
		"run", "case", "add",
		"--state-file", statePath,
	})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error when --code is missing")
	}
}

func TestRunCaseAdd_RejectsEmptyCode(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetPLFlags()
	resetRunCaseFlags()
	resetRunCaseStepFlags()
	resetRunCaseAddFlags()
	resetRootFlags()

	statePath := filepath.Join(t.TempDir(), "s.json")
	_ = state.Save(statePath, &state.State{RunID: "r1", ProjectID: "OB"})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	// A "--code OB-1,,OB-2" typo yields an empty entry — reject it as a CLI
	// error rather than POST a blank code the server would 4xx on.
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", "https://example",
		"run", "case", "add",
		"--code", "OB-1,,OB-2",
		"--state-file", statePath,
	})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error for an empty --code entry")
	}
}
