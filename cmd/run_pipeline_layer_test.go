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
	"sync/atomic"
	"testing"

	"github.com/observo-ai/observo-cli/internal/state"
)

func resetPLFlags() {
	plProject = ""
	plRunID = ""
	plLayerID = ""
	plDisplayName = ""
	plFramework = ""
	plJunitFile = ""
	plLcovFile = ""
	plHtmlFile = ""
	plStateFile = state.DefaultPath
}

const junitWith3Cases = `<?xml version="1.0"?>
<testsuite name="x" tests="3">
  <testcase name="a" time="0.5"/>
  <testcase name="b" time="0.5"/>
  <testcase name="c" time="0.0"><failure/></testcase>
</testsuite>`

// fakeObservoServer routes the 3 endpoints `run pipeline-layer set` calls:
// upload junit, upload lcov, patch pipeline_layer.
// Counts hits per endpoint so tests can assert wiring.
type fakeObservoServer struct {
	uploads    atomic.Int32
	patches    atomic.Int32
	patchedReq atomic.Pointer[string] // captured last patch body
}

func (f *fakeObservoServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/attachments:upload"):
			// Post-fix: attachments are JSON+base64 (server gateway has
			// no multipart marshaler). Read JSON body, echo the filename
			// into the attachment id so tests can verify which file was
			// uploaded.
			f.uploads.Add(1)
			body, _ := io.ReadAll(r.Body)
			var req struct {
				FileName string `json:"file_name"`
				Content  string `json:"content"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatalf("upload body not JSON: %v\n%s", err, body)
			}
			if req.FileName == "" {
				t.Errorf("upload missing file_name")
			}
			if req.Content == "" {
				t.Errorf("upload missing content (base64)")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"attachment": map[string]any{"id": "att-" + req.FileName},
			})

		case strings.Contains(r.URL.Path, "/pipeline/layers/"):
			f.patches.Add(1)
			body, _ := io.ReadAll(r.Body)
			s := string(body)
			f.patchedReq.Store(&s)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"run": map[string]any{"id": "r1"},
			})

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}
}

func TestPipelineLayerSet_E2E_UploadsBothAttachmentsAndPatches(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetPLFlags()
	resetRootFlags()

	fk := &fakeObservoServer{}
	srv := httptest.NewServer(fk.handler(t))
	defer srv.Close()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "s.json")
	if err := state.Save(statePath, &state.State{RunID: "r1", ProjectID: "OB"}); err != nil {
		t.Fatal(err)
	}
	junitPath := filepath.Join(dir, "junit.xml")
	_ = os.WriteFile(junitPath, []byte(junitWith3Cases), 0o644)
	lcovPath := filepath.Join(dir, "cov.lcov")
	_ = os.WriteFile(lcovPath, []byte("TN:\nSF:foo.go\nDA:1,1\n"), 0o644)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "pipeline-layer", "set",
		"--layer-id", "frontend-unit",
		"--display-name", "Frontend (Vitest)",
		"--framework", "vitest",
		"--junit", junitPath,
		"--lcov", lcovPath,
		"--state-file", statePath,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if fk.uploads.Load() != 2 {
		t.Errorf("expected 2 uploads (junit + lcov), got %d", fk.uploads.Load())
	}
	if fk.patches.Load() != 1 {
		t.Errorf("expected 1 PATCH, got %d", fk.patches.Load())
	}

	patchBody := ""
	if p := fk.patchedReq.Load(); p != nil {
		patchBody = *p
	}
	for _, want := range []string{
		`"project_id":"OB"`,
		`"id":"frontend-unit"`,
		`"display_name":"Frontend (Vitest)"`,
		`"framework":"vitest"`,
		`"total":3`,
		`"passed":2`,
		`"failed":1`,
		`"junit_attachment_id":"att-junit.xml"`,
		`"lcov_attachment_id":"att-cov.lcov"`,
	} {
		if !strings.Contains(patchBody, want) {
			t.Errorf("PATCH body missing %q\nbody: %s", want, patchBody)
		}
	}

	// State file must have recorded the layer outcome.
	st, _ := state.Load(statePath)
	if st.Layers["frontend-unit"].Status != "failed" {
		t.Errorf("state layer outcome: %+v", st.Layers)
	}
}

func TestPipelineLayerSet_NoLcovFlag_OnlyUploadsJunit(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetPLFlags()
	resetRootFlags()

	fk := &fakeObservoServer{}
	srv := httptest.NewServer(fk.handler(t))
	defer srv.Close()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "s.json")
	_ = state.Save(statePath, &state.State{RunID: "r1", ProjectID: "OB"})
	junitPath := filepath.Join(dir, "junit.xml")
	_ = os.WriteFile(junitPath, []byte(junitWith3Cases), 0o644)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "pipeline-layer", "set",
		"--layer-id", "x",
		"--display-name", "X",
		"--junit", junitPath,
		"--state-file", statePath,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fk.uploads.Load() != 1 {
		t.Errorf("expected only 1 upload (junit), got %d", fk.uploads.Load())
	}
}

func TestPipelineLayerSet_LcovMissingOnDisk_WarnsAndContinues(t *testing.T) {
	resetRunCreateFlags()
	resetRunFinishFlags()
	resetRunAttachFlags()
	resetPLFlags()
	resetRootFlags()

	fk := &fakeObservoServer{}
	srv := httptest.NewServer(fk.handler(t))
	defer srv.Close()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "s.json")
	_ = state.Save(statePath, &state.State{RunID: "r1", ProjectID: "OB"})
	junitPath := filepath.Join(dir, "junit.xml")
	_ = os.WriteFile(junitPath, []byte(junitWith3Cases), 0o644)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "pipeline-layer", "set",
		"--layer-id", "x",
		"--display-name", "X",
		"--junit", junitPath,
		"--lcov", filepath.Join(dir, "nope.lcov"),
		"--state-file", statePath,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute should NOT fail on missing lcov: %v", err)
	}
	if fk.uploads.Load() != 1 {
		t.Errorf("expected 1 upload (junit only — lcov skipped), got %d", fk.uploads.Load())
	}
	if !strings.Contains(buf.String(), "nope.lcov not found") {
		t.Errorf("expected warning about missing lcov, got: %s", buf.String())
	}
}
