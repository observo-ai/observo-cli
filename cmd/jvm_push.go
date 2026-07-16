package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/observo-ai/observo-cli/internal/api"
	"github.com/observo-ai/observo-cli/internal/config"
	"github.com/observo-ai/observo-cli/internal/jvm"
	"github.com/observo-ai/observo-cli/internal/output"
	"github.com/observo-ai/observo-cli/internal/state"

	"github.com/spf13/cobra"
)

var (
	jpManifest  string
	jpFrom      string
	jpTestNG    string
	jpRun       string
	jpPlan      string
	jpProject   string
	jpStateFile string
	jpDryRun    bool
)

var jvmPushCmd = &cobra.Command{
	Use:   "push --run RUN-42 [--manifest observo-link-manifest.json | --from allure-results]",
	Short: "Write JVM run results + HTTP-traffic evidence back to an Observo run",
	Long: `Push a JVM test run into Observo: set pass/fail/skip on each linked case
and attach execution-evidence (captured HTTP traffic + the junit/testng
report) so the coverage verdict can tell "covered" from "hollow".

Result source (one required):
  --manifest        a manifest from 'observo jvm manifest'
  --from            Allure results dir (built inline; also the HTTP-evidence
                    source) and/or --testng-results testng-results.xml

Run target (one required):
  --run RUN-42      an existing run
  --plan KEY        create a run from a plan (plan_key or UUID)

HTTP-traffic evidence (method·path·status) is extracted from allure-okhttp3
attachments in --from and attached per case as http-evidence.json, stamped
with the push time. FAIL/SKIP are never reported as PASS.

Examples:
  ./gradlew test
  observo jvm push --from build/allure-results --testng-results build/testng-results.xml --run RUN-42
  observo jvm push --manifest observo-link-manifest.json --plan REGR-MAIN-CI --project PD`,
	Args: cobra.NoArgs,
	RunE: jvmPushExec,
}

func init() {
	jvmCmd.AddCommand(jvmPushCmd)
	f := jvmPushCmd.Flags()
	f.StringVar(&jpManifest, "manifest", "", "path to observo-link-manifest.json")
	f.StringVar(&jpFrom, "from", "", "Allure results dir (build manifest inline + HTTP evidence source)")
	f.StringVar(&jpTestNG, "testng-results", "", "path to testng-results.xml (join + result source)")
	f.StringVar(&jpRun, "run", "", "existing run key/UUID (e.g. RUN-42)")
	f.StringVar(&jpPlan, "plan", "", "plan key/UUID to create a run from")
	f.StringVar(&jpProject, "project", "", "project UUID or short code (default: from state file)")
	f.StringVar(&jpStateFile, "state-file", state.DefaultPath, "where to read project/run when flags are unset")
	f.BoolVar(&jpDryRun, "dry-run", false, "print what would be pushed; call no write APIs")
}

type pushSummary struct {
	Run           string `json:"run"`
	Project       string `json:"project,omitempty"`
	CasesUpdated  int    `json:"cases_updated"`
	CasesSkipped  int    `json:"cases_skipped"` // untracked or no result
	EvidenceCases int    `json:"evidence_cases"`
	Errors        int    `json:"errors"`
	DryRun        bool   `json:"dry_run,omitempty"`
}

func jvmPushExec(cmd *cobra.Command, args []string) error {
	// 1. Resolve the result source.
	m, err := loadPushManifest()
	if err != nil {
		return err
	}

	// 2. HTTP evidence (best-effort) from the allure dir, when given.
	var httpEv map[string][]jvm.HTTPExchange
	if jpFrom != "" {
		if ev, evErr := jvm.ExtractHTTPEvidence(jpFrom); evErr == nil {
			httpEv = ev
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: HTTP evidence extraction failed: %v\n", evErr)
		}
	}

	cfg := config.Resolve(flagAPIKey, flagBaseURL, flagJSON, flagVerbose)
	if !jpDryRun {
		if err := cfg.Validate(); err != nil {
			return err
		}
	}

	// 3. Resolve project + run target.
	project := resolvePushProject()
	// Evidence/report uploads (UploadAttachment) require a project, unlike
	// status writeback (run-scoped path). Fail loud up front — otherwise the
	// documented `--from … --run` example silently uploads zero evidence
	// while exiting 0. Checked before dry-run previews too, so the preview
	// is honest. Status-only push (--manifest with no --from/--testng-results)
	// needs no project.
	if project == "" && (jpFrom != "" || jpTestNG != "") {
		return fmt.Errorf("attaching evidence requires a project — pass --project or run 'observo run create' first (status-only push works with --manifest alone)")
	}
	var client *api.Client
	if !jpDryRun {
		c, cErr := api.New(api.Options{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, UserAgent: userAgent(), Verbose: cfg.Verbose})
		if cErr != nil {
			return cErr
		}
		client = c
	}

	ctx := context.Background()
	runID, err := resolvePushRun(ctx, client, project)
	if err != nil {
		return err
	}

	stderr := cmd.ErrOrStderr()
	summary := pushSummary{Run: runID, Project: project, DryRun: jpDryRun}
	capturedAt := time.Now().UTC().Format(time.RFC3339)

	for _, e := range m.Entries {
		if !e.Tracked() {
			summary.CasesSkipped++
			fmt.Fprintf(stderr, "skip: untracked test %s (no observo:<code>)\n", e.FQName)
			continue
		}
		if e.Result == nil {
			summary.CasesSkipped++
			fmt.Fprintf(stderr, "skip: %s has no result (manifest not from a run)\n", *e.Code)
			continue
		}
		status, ok := runCaseStatus(e.Result.Status)
		if !ok {
			summary.CasesSkipped++
			fmt.Fprintf(stderr, "skip: %s unknown result status %q\n", *e.Code, e.Result.Status)
			continue
		}

		if jpDryRun {
			fmt.Fprintf(stderr, "would set %s = %s\n", *e.Code, status)
			summary.CasesUpdated++
		} else {
			if err := client.EnsureAndUpdateRunCase(ctx, runID, *e.Code, status, ""); err != nil {
				fmt.Fprintf(stderr, "%s: set status: %v\n", *e.Code, err)
				summary.Errors++
				continue
			}
			summary.CasesUpdated++
		}

		// Per-case HTTP-traffic evidence.
		if ex := httpEv[e.FQName]; len(ex) > 0 {
			if err := uploadHTTPEvidence(ctx, client, stderr, project, runID, *e.Code, ex, capturedAt, jpDryRun); err != nil {
				summary.Errors++
			} else {
				summary.EvidenceCases++
			}
		}
	}

	// Run-level evidence: the testng-results.xml report, when provided.
	if jpTestNG != "" {
		if err := uploadRunReport(ctx, client, stderr, project, runID, jpTestNG, jpDryRun); err != nil {
			summary.Errors++
		}
	}

	p := output.New(cfg.JSON)
	p.Out = cmd.OutOrStdout()
	return p.Result(pushSummaryToMap(summary), pushSummaryHuman(summary))
}

// loadPushManifest resolves the result source: an explicit --manifest, or
// an inline build from --from / --testng-results.
func loadPushManifest() (*jvm.LinkManifest, error) {
	switch {
	case jpManifest != "":
		return jvm.LoadManifest(jpManifest)
	case jpFrom != "" || jpTestNG != "":
		return jvm.BuildManifest(jvm.BuildOptions{AllureDir: jpFrom, TestNGResults: jpTestNG})
	default:
		return nil, fmt.Errorf("no result source: set --manifest, or --from / --testng-results")
	}
}

// resolvePushProject reads --project, falling back to the state file.
func resolvePushProject() string {
	if jpProject != "" {
		return jpProject
	}
	if st, err := state.Load(jpStateFile); err == nil {
		return st.ProjectID
	}
	return ""
}

// resolvePushRun resolves the run target: --plan creates a run (needs a
// project), --run uses an existing one. Exactly one must be set.
func resolvePushRun(ctx context.Context, client *api.Client, project string) (string, error) {
	switch {
	case jpPlan != "" && jpRun != "":
		return "", fmt.Errorf("--plan and --run are mutually exclusive")
	case jpPlan != "":
		if project == "" {
			return "", fmt.Errorf("--plan requires --project (or a state file)")
		}
		if jpDryRun {
			return "(dry-run: new run from plan " + jpPlan + ")", nil
		}
		run, err := client.CreateRun(ctx, api.CreateRunRequest{ProjectID: project, PlanID: jpPlan})
		if err != nil {
			return "", fmt.Errorf("create run from plan %s: %w", jpPlan, err)
		}
		if run.RunKey != "" {
			return run.RunKey, nil
		}
		return run.ID, nil
	case jpRun != "":
		return jpRun, nil
	default:
		// Fall back to the run recorded by `observo run create` — the
		// --state-file help promises project/run resolution from state.
		if st, err := state.Load(jpStateFile); err == nil && st.RunID != "" {
			return st.RunID, nil
		}
		return "", fmt.Errorf("no run target: set --run or --plan (or run 'observo run create' first)")
	}
}

// runCaseStatus maps a manifest Result status to the server's run-case
// status enum. Unknown input returns ok=false so the case is skipped
// rather than mis-reported — FAIL/SKIP must never surface as passed.
func runCaseStatus(s string) (string, bool) {
	switch s {
	case jvm.StatusPass:
		return "passed", true
	case jvm.StatusFail:
		return "failed", true
	case jvm.StatusSkip:
		return "skipped", true
	default:
		return "", false
	}
}

type httpEvidenceDoc struct {
	Code       string             `json:"code"`
	CapturedAt string             `json:"captured_at"`
	Exchanges  []jvm.HTTPExchange `json:"exchanges"`
}

// uploadHTTPEvidence writes the per-case HTTP exchanges to a temp
// http-evidence.json (stamped with capturedAt) and attaches it to the run
// case.
func uploadHTTPEvidence(ctx context.Context, client *api.Client, stderr io.Writer,
	project, runID, code string, ex []jvm.HTTPExchange, capturedAt string, dryRun bool) error {
	doc := httpEvidenceDoc{Code: code, CapturedAt: capturedAt, Exchanges: ex}
	if dryRun {
		fmt.Fprintf(stderr, "would attach http-evidence.json for %s (%d exchanges)\n", code, len(ex))
		return nil
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	path, cleanup, err := writeTemp("http-evidence.json", data)
	if err != nil {
		fmt.Fprintf(stderr, "%s: stage evidence: %v\n", code, err)
		return err
	}
	defer cleanup()
	if _, err := client.UploadAttachment(ctx, api.UploadAttachmentRequest{
		ProjectID: project, RunID: runID, RunCaseID: code, FilePath: path,
	}); err != nil {
		fmt.Fprintf(stderr, "%s: upload evidence: %v\n", code, err)
		return err
	}
	return nil
}

// uploadRunReport attaches the testng-results.xml report at run scope.
func uploadRunReport(ctx context.Context, client *api.Client, stderr io.Writer,
	project, runID, reportPath string, dryRun bool) error {
	if dryRun {
		fmt.Fprintf(stderr, "would attach run report %s\n", reportPath)
		return nil
	}
	if _, err := client.UploadAttachment(ctx, api.UploadAttachmentRequest{
		ProjectID: project, RunID: runID, FilePath: reportPath,
	}); err != nil {
		fmt.Fprintf(stderr, "upload run report %s: %v\n", reportPath, err)
		return err
	}
	return nil
}

// writeTemp writes data to a temp file with the given basename (so the
// server keys content-type off the meaningful name) and returns its path +
// a cleanup func. Mirrors run_import.go's per-invocation temp-dir approach.
func writeTemp(name string, data []byte) (string, func(), error) {
	dir, err := os.MkdirTemp("", "observo-jvm-push-")
	if err != nil {
		return "", func() {}, err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		os.RemoveAll(dir)
		return "", func() {}, err
	}
	return path, func() { os.RemoveAll(dir) }, nil
}

func pushSummaryToMap(s pushSummary) map[string]any {
	return map[string]any{
		"run":            s.Run,
		"project":        s.Project,
		"cases_updated":  s.CasesUpdated,
		"cases_skipped":  s.CasesSkipped,
		"evidence_cases": s.EvidenceCases,
		"errors":         s.Errors,
		"dry_run":        s.DryRun,
	}
}

func pushSummaryHuman(s pushSummary) string {
	prefix := "pushed"
	if s.DryRun {
		prefix = "dry-run: would push"
	}
	return fmt.Sprintf("%s run=%s updated=%d skipped=%d evidence=%d errors=%d",
		prefix, s.Run, s.CasesUpdated, s.CasesSkipped, s.EvidenceCases, s.Errors)
}
