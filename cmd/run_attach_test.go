package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/observo-ai/observo-cli/internal/state"
)

func resetRunAttachFlags() {
	raProject = ""
	raRunID = ""
	raFile = ""
	raStateFile = state.DefaultPath
}

func TestRunAttach_E2E_ReadsRunIDFromStateFile(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetRootFlags()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"attachment": map[string]any{"id": "att-1", "file_name": "x.lcov"},
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "s.json")
	if err := state.Save(statePath, &state.State{
		RunID: "r1", ProjectID: "OB",
	}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "x.lcov")
	if err := os.WriteFile(file, []byte("TN:\nSF:foo.go\nDA:1,1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "attach",
		"--file", file,
		"--state-file", statePath,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(buf.String(), "att-1") {
		t.Errorf("stdout missing attachment id: %s", buf.String())
	}
}

func TestRunAttach_FailsWhenFileMissing(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetRootFlags()

	statePath := filepath.Join(t.TempDir(), "s.json")
	_ = state.Save(statePath, &state.State{RunID: "r1", ProjectID: "OB"})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", "https://example",
		"run", "attach",
		"--file", filepath.Join(t.TempDir(), "does-not-exist.txt"),
		"--state-file", statePath,
	})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestResolveProjectAndRun_FlagWinsOverState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "s.json")
	_ = state.Save(statePath, &state.State{RunID: "from-state", ProjectID: "from-state"})

	p, r, err := resolveProjectAndRun("flag-proj", "flag-run", statePath)
	if err != nil {
		t.Fatal(err)
	}
	if p != "flag-proj" || r != "flag-run" {
		t.Errorf("flag should win: %q %q", p, r)
	}
}

func TestResolveProjectAndRun_StateFillsBlanks(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "s.json")
	_ = state.Save(statePath, &state.State{RunID: "rid", ProjectID: "pid"})

	p, r, err := resolveProjectAndRun("", "", statePath)
	if err != nil {
		t.Fatal(err)
	}
	if p != "pid" || r != "rid" {
		t.Errorf("state should fill: %q %q", p, r)
	}
}

func TestResolveProjectAndRun_ErrorWhenAllMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	_, _, err := resolveProjectAndRun("", "", missing)
	if err == nil {
		t.Fatal("expected error when nothing set")
	}
}
