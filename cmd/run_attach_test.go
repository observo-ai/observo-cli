package cmd

import (
	"bytes"
	"encoding/json"
	"io"
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
	raCase = ""
	raCaseCode = ""
	raStateFile = state.DefaultPath
	raExampleCells = ""
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

// OB-397: --case (alias --code) links the attachment to a specific run-case.
// The server resolves the short code to the per-case row via run_id, so the
// upload body must carry run_case_id.
func TestRunAttach_LinksToCaseViaCaseFlag(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetRootFlags()

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"attachment": map[string]any{"id": "att-9", "file_name": "trace.zip"},
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	file := filepath.Join(dir, "trace.zip")
	if err := os.WriteFile(file, []byte("PK\x03\x04zip"), 0o644); err != nil {
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
		"--project", "OB",
		"--run-id", "r1",
		"--case", "OB-76",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(gotBody, `"run_case_id":"OB-76"`) {
		t.Errorf("upload body missing run_case_id: %s", gotBody)
	}
}

// --case and --code are aliases; setting both to DIFFERENT values is a user
// error and must fail fast rather than silently picking one.
func TestRunAttach_CaseAndCodeConflict(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetRootFlags()

	dir := t.TempDir()
	file := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(file, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", "https://x",
		"run", "attach",
		"--file", file,
		"--project", "OB",
		"--run-id", "r1",
		"--case", "OB-76",
		"--code", "OB-77",
	})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when --case and --code differ")
	}
	if !strings.Contains(err.Error(), "both set and differ") {
		t.Errorf("unexpected error: %v", err)
	}
}

// OB-437: --example-cells JSON makes it into the upload POST body as
// `example_cells` so the server's UploadAttachment handler can disambiguate
// among parametrized example rows. Mirrors the wire-shape test pattern in
// run_case_test.go:247 (OB-405 step writeback).
func TestRunAttach_E2E_PassesExampleCellsInBody(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetRootFlags()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"attachment": map[string]any{"id": "att-1", "file_name": "trace.zip"},
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	file := filepath.Join(dir, "trace.zip")
	if err := os.WriteFile(file, []byte("PK\x03\x04 fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "attach",
		"--project", "OB",
		"--run-id", "r1",
		"--file", file,
		"--case", "OB-76",
		"--example-cells", `{"browser":"chromium"}`,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cells, ok := captured["example_cells"].(map[string]any)
	if !ok {
		t.Fatalf("expected example_cells in body, got: %v", captured)
	}
	if cells["browser"] != "chromium" {
		t.Errorf("expected browser=chromium, got %v", cells["browser"])
	}
	if got := captured["run_case_id"]; got != "OB-76" {
		t.Errorf("expected run_case_id=OB-76, got %v", got)
	}
}

// OB-437: --example-cells alone (no --case) is rejected CLI-side before any
// network call. The cells disambiguate AMONG example rows of a specific case,
// so the case identity is required to make the request meaningful.
func TestRunAttach_ExampleCellsWithoutCase_FailsBeforeHTTP(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetRootFlags()

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	file := filepath.Join(dir, "x.zip")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "attach",
		"--project", "OB",
		"--run-id", "r1",
		"--file", file,
		"--example-cells", `{"browser":"chromium"}`,
	})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when --example-cells is set without --case")
	}
	if called {
		t.Errorf("HTTP must NOT be called — guard is CLI-side")
	}
}

// OB-437: malformed --example-cells JSON aborts before HTTP. parseExampleCells
// already covers the JSON-parser path in run_case_test.go:371; this asserts
// the SAME guard fires on the attach subcommand (i.e. we routed through the
// shared helper, not a divergent local parser).
func TestRunAttach_MalformedExampleCells_FailsBeforeHTTP(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetRootFlags()

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	file := filepath.Join(dir, "x.zip")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "attach",
		"--project", "OB",
		"--run-id", "r1",
		"--file", file,
		"--case", "OB-76",
		"--example-cells", `not-json`,
	})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error on malformed --example-cells")
	}
	if called {
		t.Errorf("HTTP must NOT be called when --example-cells fails to parse")
	}
}
