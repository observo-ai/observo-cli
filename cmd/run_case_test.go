package cmd

import (
	"bytes"
	"encoding/json"
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
	rcssExampleCells = ""
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

// TestParseExampleCells covers the OB-406 JSON parser as a pure helper —
// validation errors must be loud and locale-stable so users see what to fix.
func TestParseExampleCells(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		want      map[string]string
		wantErr   bool
		errSubstr string
	}{
		{name: "empty -> nil (classic path)", in: "", want: nil},
		{name: "whitespace -> nil (classic path)", in: "   ", want: nil},
		{name: "single cell", in: `{"browser":"chromium"}`, want: map[string]string{"browser": "chromium"}},
		{name: "two cells", in: `{"browser":"firefox","locale":"de"}`, want: map[string]string{"browser": "firefox", "locale": "de"}},
		{name: "invalid JSON", in: `{browser:chromium}`, wantErr: true, errSubstr: "invalid JSON"},
		{name: "nested object rejected", in: `{"params":{"browser":"chromium"}}`, wantErr: true, errSubstr: "invalid JSON"},
		{name: "empty object rejected", in: `{}`, wantErr: true, errSubstr: "empty object"},
		{name: "empty value rejected", in: `{"browser":""}`, wantErr: true, errSubstr: "empty value"},
		// Whitespace-only key/value would reach the server as an unmatchable
		// param row (silent 404 or no-op) — catch it at the CLI like the empty case.
		{name: "whitespace-only value rejected", in: `{"browser":"   "}`, wantErr: true, errSubstr: "empty value"},
		{name: "whitespace-only key rejected", in: `{"   ":"chromium"}`, wantErr: true, errSubstr: "empty parameter name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseExampleCells(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tc.errSubstr)
				}
				if !strings.Contains(err.Error(), tc.errSubstr) {
					t.Fatalf("err = %v, want substring %q", err, tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len(got)=%d want=%d (got=%v)", len(got), len(tc.want), got)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("key %q: got %q want %q", k, got[k], v)
				}
			}
		})
	}
}

// TestRunCaseStepSet_ExampleCellsForwardedInBody is the OB-406 round-trip:
// --example-cells JSON makes it into the PATCH body as `example_cells`, so the
// server can resolve which parametrized example to update.
func TestRunCaseStepSet_ExampleCellsForwardedInBody(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetPLFlags()
	resetRunCaseFlags()
	resetRunCaseStepFlags()
	resetRootFlags()

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		"--step", "1",
		"--status", "passed",
		"--example-cells", `{"browser":"chromium"}`,
		"--state-file", statePath,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Assert by JSON parse, not substring — a `"example_cells":{"browser":"chromium"}`
	// substring would survive even if the field were nested wrong; round-tripping
	// proves the wire shape the server actually receives.
	var got struct {
		Status       string            `json:"status"`
		ExampleCells map[string]string `json:"example_cells"`
	}
	if err := json.Unmarshal([]byte(gotBody), &got); err != nil {
		t.Fatalf("body not valid JSON: %v\nbody=%s", err, gotBody)
	}
	if got.Status != "passed" {
		t.Errorf("status: %q want passed (body=%s)", got.Status, gotBody)
	}
	if got.ExampleCells["browser"] != "chromium" {
		t.Errorf("example_cells.browser = %q, want chromium (body=%s)", got.ExampleCells["browser"], gotBody)
	}
}

// TestRunCaseStepSet_ExampleCellsOmittedForClassic — without the flag, the body
// must NOT carry an `example_cells` field at all (omitempty), so the server
// treats it as a classic, non-parametrized write.
func TestRunCaseStepSet_ExampleCellsOmittedForClassic(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetPLFlags()
	resetRunCaseFlags()
	resetRunCaseStepFlags()
	resetRootFlags()

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	statePath := filepath.Join(t.TempDir(), "s.json")
	_ = state.Save(statePath, &state.State{RunID: "r1", ProjectID: "OB"})

	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "case", "step", "set",
		"--code", "OB-50",
		"--step", "1",
		"--status", "passed",
		"--state-file", statePath,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(gotBody, "example_cells") {
		t.Errorf("classic write must not emit example_cells; body=%s", gotBody)
	}
}

// TestRunCaseStepSet_RejectsMalformedExampleCells — bad JSON is a CLI error
// (rc != 0, no HTTP call).
func TestRunCaseStepSet_RejectsMalformedExampleCells(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetPLFlags()
	resetRunCaseFlags()
	resetRunCaseStepFlags()
	resetRootFlags()

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	statePath := filepath.Join(t.TempDir(), "s.json")
	_ = state.Save(statePath, &state.State{RunID: "r1", ProjectID: "OB"})

	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "case", "step", "set",
		"--code", "OB-50",
		"--step", "1",
		"--status", "passed",
		"--example-cells", `not-json`,
		"--state-file", statePath,
	})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error on malformed --example-cells")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("error = %v, want 'invalid JSON' substring", err)
	}
	if called {
		t.Errorf("HTTP must not be called when --example-cells fails to parse")
	}
}
