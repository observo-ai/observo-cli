package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/observo-ai/observo-cli/internal/state"
)

func resetRunFinishFlags() {
	rfProject = ""
	rfRunID = ""
	rfStatus = "auto"
	rfStateFile = state.DefaultPath
}

// seedStateFile drops a minimal state file in a fresh tempdir and returns
// the path. layers is optional.
func seedStateFile(t *testing.T, runID, projectID string, layers map[string]state.LayerState) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.json")
	if err := state.Save(path, &state.State{
		RunID:     runID,
		ProjectID: projectID,
		Layers:    layers,
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	return path
}

func TestRunFinish_AutoStatusFromStateLayers(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRootFlags()

	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"run": map[string]any{"id": "r1", "status": "failed"},
		})
	}))
	defer srv.Close()

	statePath := seedStateFile(t, "r1", "OB", map[string]state.LayerState{
		"frontend-unit": {Status: "passed"},
		"server-unit":   {Status: "failed"},
	})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "finish",
		"--state-file", statePath,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if gotMethod != "PATCH" {
		t.Errorf("method: %s", gotMethod)
	}
	if gotPath != "/api/projects/OB/runs/r1" {
		t.Errorf("path: %s", gotPath)
	}
	if !strings.Contains(gotBody, `"status":"failed"`) {
		t.Errorf("body should carry derived status=failed: %s", gotBody)
	}
}

func TestRunFinish_ExplicitStatusOverridesStateAuto(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRootFlags()

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"run": map[string]any{"id": "r1", "status": "aborted"},
		})
	}))
	defer srv.Close()

	statePath := seedStateFile(t, "r1", "OB", map[string]state.LayerState{
		"x": {Status: "passed"},
	})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "finish",
		"--state-file", statePath,
		"--status", "aborted",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(gotBody, `"status":"aborted"`) {
		t.Errorf("explicit status should win, body: %s", gotBody)
	}
}

func TestRunFinish_NoStateFileWithAutoErrors(t *testing.T) {
	// Post-fix: --status=auto with missing state must NOT silently
	// default to passed (was a "lost state file → green pipeline" trap).
	// Operators wanting the old behaviour pass --allow-missing-state.
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRootFlags()

	missingPath := filepath.Join(t.TempDir(), "missing.json")
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", "https://example",
		"run", "finish",
		"--project", "OB",
		"--run-id", "r1",
		"--state-file", missingPath,
		// status default = auto, no --allow-missing-state
	})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when --status=auto and state file missing")
	}
	if !strings.Contains(err.Error(), "--allow-missing-state") {
		t.Errorf("error should mention the opt-out flag; got: %v", err)
	}
}

func TestRunFinish_NoStateFileWithAllowMissingStateDefaultsToPassed(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRootFlags()

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{"run": map[string]any{"id": "r1", "status": "passed"}})
	}))
	defer srv.Close()

	missingPath := filepath.Join(t.TempDir(), "missing.json")
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "finish",
		"--project", "OB",
		"--run-id", "r1",
		"--state-file", missingPath,
		"--allow-missing-state",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(gotBody, `"status":"passed"`) {
		t.Errorf("--allow-missing-state with auto should default to passed: %s", gotBody)
	}
}

func TestRunFinish_NoStateAndNoFlagsErrors(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRootFlags()

	missingPath := filepath.Join(t.TempDir(), "missing.json")
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", "https://example",
		"run", "finish",
		"--state-file", missingPath,
		// no --project, no --run-id → should fail before hitting API
	})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when project + run-id + state all missing")
	}
}

func TestValidateFinishStatus(t *testing.T) {
	for _, s := range []string{"passed", "failed", "aborted", "skipped"} {
		if err := validateFinishStatus(s); err != nil {
			t.Errorf("%s should be valid: %v", s, err)
		}
	}
	for _, s := range []string{"", "auto", "running", "queued", "weird"} {
		if err := validateFinishStatus(s); err == nil {
			t.Errorf("%s should be invalid", s)
		}
	}
}
