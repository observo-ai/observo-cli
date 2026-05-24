package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/observo-ai/observo-cli/internal/api"
	"github.com/observo-ai/observo-cli/internal/config"
	"github.com/observo-ai/observo-cli/internal/output"
	"github.com/observo-ai/observo-cli/internal/state"

	"github.com/spf13/cobra"
)

// run-create flags. Most have GitHub Actions env-var fallbacks so the
// command is single-line in a typical workflow:
//
//	observo run create --project OB --plan REGR-MAIN-CI
//
// — everything else (commit, branch, actor, ci_run_url) picked up from
// the runner env automatically.
var (
	rcProject      string
	rcPlanKey      string
	rcName         string
	rcCommit       string
	rcCommitURL    string
	rcBranch       string
	rcActor        string
	rcCIRunURL     string
	rcPrURL        string
	rcSourceSystem string
	rcStateFile    string
)

var runCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a TestRun (optionally seeded from a plan); write state file for follow-up commands",
	Long: `Create a TestRun in the given project. When --plan is set, the run is
seeded with cases snapshotted from that plan (server-side).

On success, writes a state file (default: .observo-pipeline-state.json
in the CWD) holding run_id + project_id so subsequent CLI calls in the
same CI job — particularly 'observo run finish --status auto' — can
operate without rediscovering the run.

Environment-variable defaults (GitHub Actions):
  --commit       $GITHUB_SHA
  --commit-url   $GITHUB_SERVER_URL/$GITHUB_REPOSITORY/commit/$GITHUB_SHA
  --branch       $GITHUB_REF_NAME
  --actor        $GITHUB_ACTOR
  --ci-run-url   $GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID

The default --source-system is "github_actions" when GITHUB_ACTIONS=true,
else "manual".`,
	Args: cobra.NoArgs,
	RunE: runCreateExec,
}

func init() {
	runCmd.AddCommand(runCreateCmd)

	f := runCreateCmd.Flags()
	f.StringVar(&rcProject, "project", "", "project UUID or short code (required)")
	f.StringVar(&rcPlanKey, "plan", "", "plan key or UUID to seed cases from (optional)")
	f.StringVar(&rcName, "name", "", "human-readable run name (default: \"Pipeline <short_sha>\")")
	f.StringVar(&rcCommit, "commit", "", "commit SHA (default: $GITHUB_SHA)")
	f.StringVar(&rcCommitURL, "commit-url", "", "commit URL (default: derived from GitHub env)")
	f.StringVar(&rcBranch, "branch", "", "git branch (default: $GITHUB_REF_NAME)")
	f.StringVar(&rcActor, "actor", "", "who triggered the run (default: $GITHUB_ACTOR)")
	f.StringVar(&rcCIRunURL, "ci-run-url", "", "link back to the CI run (default: derived from GitHub env)")
	f.StringVar(&rcPrURL, "pr-url", "", "PR URL when triggered by a PR (optional)")
	f.StringVar(&rcSourceSystem, "source-system", "", "source system tag (default: github_actions or manual)")
	f.StringVar(&rcStateFile, "state-file", state.DefaultPath, "where to persist run_id for subsequent commands")

	_ = runCreateCmd.MarkFlagRequired("project")
}

func runCreateExec(cmd *cobra.Command, _ []string) error {
	cfg := config.Resolve(flagAPIKey, flagBaseURL, flagJSON, flagVerbose)
	if err := cfg.Validate(); err != nil {
		return err
	}

	client, err := api.New(api.Options{
		BaseURL:   cfg.BaseURL,
		APIKey:    cfg.APIKey,
		UserAgent: userAgent(),
		Verbose:   cfg.Verbose,
	})
	if err != nil {
		return err
	}

	// Compose request — fall back to GH env vars where flags unset.
	req := buildCreateRunRequest(rcProject, rcPlanKey, rcName,
		rcCommit, rcCommitURL, rcBranch, rcActor, rcCIRunURL, rcPrURL, rcSourceSystem)

	ctx := context.Background()
	run, err := client.CreateRun(ctx, req)
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}

	// Persist state so `run finish` (etc.) can find this run.
	st := &state.State{
		RunID:     run.ID,
		RunURL:    runURL(cfg.BaseURL, run.ID),
		ProjectID: rcProject,
	}
	if err := state.Save(rcStateFile, st); err != nil {
		// Hard error: caller needs the state file for follow-up commands.
		// A successful API call without a usable state file leaves the
		// pipeline in an awkward "run exists but I can't find it" spot.
		return fmt.Errorf("save state file %s: %w", rcStateFile, err)
	}

	p := output.New(cfg.JSON)
	p.Out = cmd.OutOrStdout()
	return p.Result(map[string]any{
		"run_id":     run.ID,
		"run_url":    st.RunURL,
		"name":       run.Name,
		"state_file": rcStateFile,
	}, run.ID)
}

// buildCreateRunRequest is exported for testing — pure function, no I/O.
func buildCreateRunRequest(project, plan, name, commit, commitURL, branch, actor, ciRunURL, prURL, sourceSystem string) api.CreateRunRequest {
	if commit == "" {
		commit = os.Getenv("GITHUB_SHA")
	}
	if branch == "" {
		branch = os.Getenv("GITHUB_REF_NAME")
	}
	if actor == "" {
		actor = os.Getenv("GITHUB_ACTOR")
	}
	if ciRunURL == "" {
		ciRunURL = derivedCIRunURL()
	}
	if commitURL == "" {
		commitURL = derivedCommitURL(commit)
	}
	if sourceSystem == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			sourceSystem = "github_actions"
		} else {
			sourceSystem = "manual"
		}
	}
	if name == "" && commit != "" {
		name = "Pipeline " + shortSHA(commit)
	}

	req := api.CreateRunRequest{
		ProjectID:        project,
		Name:             name,
		PlanID:           plan,
		AutomationStatus: "automated",
		Pipeline: &api.Pipeline{
			Kind:         "pipeline",
			SourceSystem: sourceSystem,
			CommitSHA:    commit,
			CommitURL:    commitURL,
			Branch:       branch,
			PrURL:        prURL,
			Actor:        actor,
			CIRunURL:     ciRunURL,
		},
	}
	return req
}

func derivedCIRunURL() string {
	srv := os.Getenv("GITHUB_SERVER_URL")
	repo := os.Getenv("GITHUB_REPOSITORY")
	run := os.Getenv("GITHUB_RUN_ID")
	if srv == "" || repo == "" || run == "" {
		return ""
	}
	return srv + "/" + repo + "/actions/runs/" + run
}

func derivedCommitURL(commit string) string {
	if commit == "" {
		return ""
	}
	srv := os.Getenv("GITHUB_SERVER_URL")
	repo := os.Getenv("GITHUB_REPOSITORY")
	if srv == "" || repo == "" {
		return ""
	}
	return srv + "/" + repo + "/commit/" + commit
}

func shortSHA(s string) string {
	if len(s) >= 7 {
		return s[:7]
	}
	return s
}

// runURL guesses the dashboard URL by swapping the api. subdomain for app.
// Mirrors the heuristic in observo-ai/upload-pipeline-action's publish.sh.
// Returns "" when baseURL doesn't fit the expected shape — caller treats
// empty as "no dashboard URL available", state file omits it.
func runURL(baseURL, runID string) string {
	if runID == "" {
		return ""
	}
	dashboard := strings.Replace(baseURL, "https://api.", "https://app.", 1)
	if dashboard == baseURL {
		dashboard = strings.Replace(baseURL, "http://api.", "http://app.", 1)
	}
	if dashboard == baseURL {
		return ""
	}
	return dashboard + "/run/" + runID
}
