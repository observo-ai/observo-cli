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
)

// seedRepo makes a temp dir the working directory (auto-restored) with the
// given workflow files under .github/workflows/, so doctor's scan + git-root
// fallback operate on controlled state instead of the observo-cli repo itself.
func seedRepo(t *testing.T, workflows map[string]string) {
	t.Helper()
	root := t.TempDir()
	if len(workflows) > 0 {
		dir := filepath.Join(root, ".github", "workflows")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		for name, body := range workflows {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
	}
	t.Chdir(root)
}

// resetDoctor clears command + process-exit state between runs.
func resetDoctor(t *testing.T) {
	t.Helper()
	doctorFlagProject = ""
	doctorFlagMinLevel = 3
	exitOverride = nil
	// Persistent root flags retain their value across Execute calls (cobra
	// idiom) — reset them so --api-key / --base-url from a prior test don't
	// leak into one that expects the no-key path.
	flagAPIKey = ""
	flagBaseURL = ""
	flagJSON = false
	flagVerbose = false
	// Doctor reads these from the environment — isolate the test from the
	// developer's shell.
	t.Setenv("OBSERVO_PROJECT", "")
	t.Setenv("OBSERVO_PROJECT_CODE", "")
}

// fullyGroundedServer answers ListProjects + ListRuns such that all four
// grounding levels are reached.
func fullyGroundedServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"projects": []map[string]any{{"id": "p1", "name": "Observo", "code": "OB"}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/OB/runs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"runs": []map[string]any{{
					"id": "r1",
					"pipeline": map[string]any{"layers": []map[string]any{{
						"framework": "playwright",
						"coverage":  map[string]any{"lcov_attachment_id": "att-1"},
					}}},
				}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestDoctor_FullyGrounded_Exit0(t *testing.T) {
	resetDoctor(t)
	seedRepo(t, map[string]string{
		"verdict.yml":  "env: { X: ${{ secrets.OBSERVO_MCP_API_KEY }} }\n",
		"pipeline.yml": "steps: [{ uses: observo-ai/setup@v1 }]\n",
	})
	srv := fullyGroundedServer(t)
	defer srv.Close()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"--api-key", "k", "--base-url", srv.URL, "doctor"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if exitOverride == nil || *exitOverride != 0 {
		t.Errorf("exit code = %v, want 0 (fully grounded)", exitOverride)
	}
	if out := buf.String(); !strings.Contains(out, "Level 3 of 3") {
		t.Errorf("output missing 'Level 3 of 3':\n%s", out)
	}
}

func TestDoctor_FullyGrounded_JSON(t *testing.T) {
	resetDoctor(t)
	seedRepo(t, map[string]string{
		"verdict.yml":  "OBSERVO_MCP_API_KEY\n",
		"pipeline.yml": "observo-ai/setup\n",
	})
	srv := fullyGroundedServer(t)
	defer srv.Close()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"--api-key", "k", "--base-url", srv.URL, "--json", "doctor"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var report struct {
		Reached   int    `json:"reached_level"`
		FirstFail int    `json:"first_failing_level"`
		Code      string `json:"project_code"`
	}
	if err := json.Unmarshal(buf.Bytes(), &report); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, buf.String())
	}
	if report.Reached != 3 || report.FirstFail != -1 || report.Code != "OB" {
		t.Errorf("report = %+v, want reached 3 / firstFail -1 / code OB", report)
	}
}

func TestDoctor_NoAPIKey_FailsAtL1_Exit1(t *testing.T) {
	resetDoctor(t)
	// Verdict workflow present → L0 reached; no API key → L1 fails.
	seedRepo(t, map[string]string{"verdict.yml": "OBSERVO_MCP_API_KEY\n"})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	// No --api-key and OBSERVO_API_KEY is not set in this process.
	t.Setenv("OBSERVO_API_KEY", "")
	rootCmd.SetArgs([]string{"doctor"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if exitOverride == nil || *exitOverride != 1 {
		t.Errorf("exit code = %v, want 1 (not fully grounded)", exitOverride)
	}
	if out := buf.String(); !strings.Contains(out, "OBSERVO_API_KEY") {
		t.Errorf("output should hint at OBSERVO_API_KEY:\n%s", out)
	}
}

func TestDoctor_MinLevelGate_L0ReachedExit0(t *testing.T) {
	resetDoctor(t)
	seedRepo(t, map[string]string{"verdict.yml": "OBSERVO_MCP_API_KEY\n"})
	t.Setenv("OBSERVO_API_KEY", "")

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	// L0 is reached (verdict workflow present); gate at 0 → healthy exit.
	rootCmd.SetArgs([]string{"doctor", "--min-level", "0"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if exitOverride == nil || *exitOverride != 0 {
		t.Errorf("exit code = %v, want 0 (reached L0 >= min-level 0)", exitOverride)
	}
}
