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

// resetRunCreateFlags clears subcommand-scoped vars between tests so they
// don't leak across runs (cobra retains last-parsed values otherwise).
func resetRunCreateFlags() {
	rcProject = ""
	rcPlanKey = ""
	rcName = ""
	rcCommit = ""
	rcCommitURL = ""
	rcBranch = ""
	rcActor = ""
	rcCIRunURL = ""
	rcPrURL = ""
	rcSourceSystem = ""
	rcStateFile = state.DefaultPath
}

func TestBuildCreateRunRequest_FlagWinsOverGitHubEnv(t *testing.T) {
	t.Setenv("GITHUB_SHA", "envshaXXXXXXXXXXXXXXXXXXXXXXXXXXXXX")
	t.Setenv("GITHUB_REF_NAME", "env-branch")
	t.Setenv("GITHUB_ACTOR", "env-actor")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_REPOSITORY", "observo-ai/observo-cli")
	t.Setenv("GITHUB_RUN_ID", "999")

	req := buildCreateRunRequest("OB", "REGR", "explicit-name",
		"flagSHA1234567", "https://explicit.url", "flag-branch",
		"flag-actor", "https://flag.ci", "https://pr",
		"manual")
	if req.ProjectID != "OB" || req.PlanID != "REGR" || req.Name != "explicit-name" {
		t.Errorf("required fields: %+v", req)
	}
	if req.Pipeline.CommitSHA != "flagSHA1234567" {
		t.Errorf("flag commit lost: %q", req.Pipeline.CommitSHA)
	}
	if req.Pipeline.Branch != "flag-branch" {
		t.Errorf("flag branch lost: %q", req.Pipeline.Branch)
	}
	if req.Pipeline.SourceSystem != "manual" {
		t.Errorf("explicit source_system lost: %q", req.Pipeline.SourceSystem)
	}
	if req.Pipeline.CIRunURL != "https://flag.ci" {
		t.Errorf("flag ci_run_url lost: %q", req.Pipeline.CIRunURL)
	}
}

func TestBuildCreateRunRequest_GitHubEnvFillsBlanks(t *testing.T) {
	t.Setenv("GITHUB_SHA", "envSHA0123456")
	t.Setenv("GITHUB_REF_NAME", "main")
	t.Setenv("GITHUB_ACTOR", "ci-bot")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_REPOSITORY", "x/y")
	t.Setenv("GITHUB_RUN_ID", "42")

	req := buildCreateRunRequest("OB", "", "", "", "", "", "", "", "", "")

	if req.Pipeline.CommitSHA != "envSHA0123456" {
		t.Errorf("env SHA: %q", req.Pipeline.CommitSHA)
	}
	if req.Pipeline.Branch != "main" {
		t.Errorf("env branch: %q", req.Pipeline.Branch)
	}
	if req.Pipeline.Actor != "ci-bot" {
		t.Errorf("env actor: %q", req.Pipeline.Actor)
	}
	if req.Pipeline.CIRunURL != "https://github.com/x/y/actions/runs/42" {
		t.Errorf("derived ci_run_url: %q", req.Pipeline.CIRunURL)
	}
	if req.Pipeline.CommitURL != "https://github.com/x/y/commit/envSHA0123456" {
		t.Errorf("derived commit_url: %q", req.Pipeline.CommitURL)
	}
	if req.Pipeline.SourceSystem != "github_actions" {
		t.Errorf("source_system: %q", req.Pipeline.SourceSystem)
	}
	if req.Name != "Pipeline envSHA0" {
		t.Errorf("derived name: %q", req.Name)
	}
}

func TestBuildCreateRunRequest_FallsBackToManualSourceWhenNotOnActions(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("GITHUB_SHA", "")
	req := buildCreateRunRequest("OB", "", "", "", "", "", "", "", "", "")
	if req.Pipeline.SourceSystem != "manual" {
		t.Errorf("got %q want manual", req.Pipeline.SourceSystem)
	}
}

func TestRunCreate_EndToEndAgainstHTTPTest(t *testing.T) {
	resetRunCreateFlags()
	resetRootFlags()

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"run": map[string]any{"id": "the-run-id", "name": "Pipeline abc1234"},
		})
	}))
	defer srv.Close()

	stateDir := t.TempDir()
	statePath := filepath.Join(stateDir, "s.json")

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "create",
		"--project", "OB",
		"--name", "Pipeline abc1234",
		"--commit", "abc1234567",
		"--state-file", statePath,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(gotBody, `"project_id":"OB"`) {
		t.Errorf("body missing project_id: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"commit_sha":"abc1234567"`) {
		t.Errorf("body missing commit_sha: %s", gotBody)
	}
	if !strings.Contains(buf.String(), "the-run-id") {
		t.Errorf("stdout missing run id: %s", buf.String())
	}

	// State file must be written with the run id.
	st, err := state.Load(statePath)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	if st.RunID != "the-run-id" {
		t.Errorf("state run_id: got %q", st.RunID)
	}
	if st.ProjectID != "OB" {
		t.Errorf("state project_id: got %q", st.ProjectID)
	}
}

func TestRunURL_SwapsApiSubdomainForApp(t *testing.T) {
	if got := runURL("https://api.observoai.co", "uuid"); got != "https://app.observoai.co/run/uuid" {
		t.Errorf("got %q", got)
	}
	if got := runURL("http://api.localhost", "uuid"); got != "http://app.localhost/run/uuid" {
		t.Errorf("got %q", got)
	}
	if got := runURL("https://no-api-prefix.example", "uuid"); got != "" {
		t.Errorf("non-api base should yield empty, got %q", got)
	}
}

// helper used by run_finish_test.go too.
func resetRootFlags() {
	flagAPIKey = ""
	flagBaseURL = ""
	flagJSON = false
	flagVerbose = false
}

// Mute the linter — exported so test files in the package share helpers.
var _ = os.Stdout
