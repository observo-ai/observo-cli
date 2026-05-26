package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/observo-ai/observo-cli/internal/api"
	"github.com/observo-ai/observo-cli/internal/config"
	"github.com/observo-ai/observo-cli/internal/output"
	"github.com/observo-ai/observo-cli/internal/playwright"
	"github.com/observo-ai/observo-cli/internal/state"

	"github.com/spf13/cobra"
)

// Flags for `observo run import …`. Kept as package-level vars to
// match the existing cmd/* convention (run_create.go, run_attach.go).
var (
	riFrom        string
	riProject     string
	riRunID       string
	riResultsJSON string
	riSourceRoot  string
	riRedact      string
	riUploadPass  bool
	riDryRun      bool
	riStateFile   string
)

var runImportCmd = &cobra.Command{
	Use:   "import [results-dir]",
	Short: "Bulk-import a Playwright test-results directory into an Observo run",
	Long: `Parse a Playwright test-results directory + its JSON reporter output
(results.json) and upload every artifact to an Observo run: video.webm,
trace.zip, screenshot.png as-is, plus extracted console.json,
network.json, and failure.json per failed test.

Short-code resolution priority (first match wins):
  1. Test tag '@observo:OB-N'  (recommended — explicit, never wrong)
  2. 'OB-N' token in test or describe titles
  3. 'OB-N' token in attachment directory names (legacy bash-script
     compatibility)

Cases without a resolvable short code are skipped with a warning;
the import does not fail.

Coexistence: this is a POST-MORTEM importer — for live test-by-test
status updates during the run use the @observo/playwright-reporter npm
package or the existing reporter shim. Running both against the same
run is safe (last writer wins) but redundant.

When --project / --run are unset, they're read from the state file
written by 'run create'.

Examples:
  # CI-style: pre-create run, then bulk-import after npx playwright test
  observo run create --plan REGR --project OB
  npx playwright test
  observo run import ./test-results --run RUN-42

  # Inspect the plan without uploading
  observo run import ./test-results --run RUN-42 --dry-run

  # Extend redaction beyond the built-in Authorization/token defaults
  observo run import ./test-results --run RUN-42 --redact '(?i)session_id'`,
	Args: cobra.MaximumNArgs(1),
	RunE: runImportExec,
}

func init() {
	runCmd.AddCommand(runImportCmd)
	f := runImportCmd.Flags()
	f.StringVar(&riFrom, "from", "playwright", "source format (only 'playwright' supported in v0.7)")
	f.StringVar(&riProject, "project", "", "project UUID or short code (default: from state file)")
	f.StringVar(&riRunID, "run", "", "run UUID or short key (e.g. RUN-42); default: from state file")
	f.StringVar(&riResultsJSON, "results-json", "", "path to Playwright JSON reporter output (default: <results-dir>/results.json)")
	f.StringVar(&riSourceRoot, "source-root", "", "directory to resolve source_excerpt paths against (default: current working directory)")
	f.StringVar(&riRedact, "redact", "", "extra regex applied to network/failure bodies (combined with the built-in default)")
	f.BoolVar(&riUploadPass, "upload-passed", false, "also upload attachments for passing cases (default: failed/blocked only)")
	f.BoolVar(&riDryRun, "dry-run", false, "parse and print the plan; do not call the API")
	f.StringVar(&riStateFile, "state-file", state.DefaultPath, "where to read project/run from when flags are unset")
}

// importSummary is the JSON shape `--json` mode emits — picked up by CI
// scripts (and the dogfood replacement of upload-playwright-attachments.sh)
// for assertion-style verification.
type importSummary struct {
	Run            string `json:"run"`
	Project        string `json:"project,omitempty"`
	ResultsJSON    string `json:"results_json"`
	TotalSpecs     int    `json:"total_specs"`
	ResolvedCases  int    `json:"resolved_cases"`
	SkippedNoCode  int    `json:"skipped_no_code"`
	AttachmentsOK  int    `json:"attachments_ok"`
	AttachmentsErr int    `json:"attachments_err"`
	StepsPatched   int    `json:"steps_patched"`
	DryRun         bool   `json:"dry_run,omitempty"`
}

func runImportExec(cmd *cobra.Command, args []string) error {
	if !strings.EqualFold(riFrom, "playwright") {
		return fmt.Errorf("--from %q not supported (only 'playwright' in v0.7)", riFrom)
	}

	resultsDir := "."
	if len(args) == 1 {
		resultsDir = args[0]
	}

	resultsJSON := riResultsJSON
	if resultsJSON == "" {
		resultsJSON = filepath.Join(resultsDir, "results.json")
	}

	cfg := config.Resolve(flagAPIKey, flagBaseURL, flagJSON, flagVerbose)
	// In --dry-run we still want to surface config-shape mistakes (bad
	// base URL, malformed API key), but missing API key is fine — no
	// API calls happen. Skip the API-key portion of Validate for dry-run.
	if !riDryRun {
		if err := cfg.Validate(); err != nil {
			return err
		}
	}

	redact, err := playwright.NewRedactor(riRedact)
	if err != nil {
		return err
	}

	// Project / run resolution. Dry-run is lenient: missing values just
	// echo "" in the summary; useful when a developer wants to verify
	// the parser handles a test-results dir before wiring auth.
	projectID, runID := riProject, riRunID
	if !riDryRun && (projectID == "" || runID == "") {
		pid, rid, sErr := resolveProjectAndRun(projectID, runID, riStateFile)
		if sErr != nil {
			// The shared helper's error text references `--run-id`,
			// which is what `run attach` / `run case set` use. This
			// command uses `--run` — replace (not %w-wrap) the error
			// so users aren't told to pass a flag this subcommand
			// doesn't declare. We don't expose this error via
			// errors.Is anywhere, so dropping the chain is fine.
			return fmt.Errorf("missing --project or --run (and no state file from a prior `observo run create`)")
		}
		projectID, runID = pid, rid
	}

	// Parse Playwright JSON reporter output.
	fh, err := os.Open(resultsJSON)
	if err != nil {
		return fmt.Errorf("open results.json (%s): %w", resultsJSON, err)
	}
	defer fh.Close()
	results, err := playwright.ParseResults(fh)
	if err != nil {
		return err
	}

	sourceRoot := riSourceRoot
	if sourceRoot == "" {
		// Default to the rootDir Playwright recorded in its config.
		sourceRoot = results.Config.RootDir
	}
	if sourceRoot == "" {
		// Last-resort default: current working directory. This makes
		// failure.go's path-traversal guard always active — without a
		// non-empty root the guard short-circuits and a hostile
		// results.json with `location.file: "/etc/shadow"` would leak
		// up to 7 lines of that file into the uploaded failure.json.
		// Real risk on CI that processes external-PR test artifacts.
		// Errors here are tolerated: a Getwd failure leaves the guard
		// disabled, which is what the previous behaviour already was.
		if cwd, err := os.Getwd(); err == nil {
			sourceRoot = cwd
		}
	}

	// Build the API client only when not dry-running. Keeps dry-run
	// fully offline so it can run on a dev laptop without
	// OBSERVO_API_KEY in the env.
	var client *api.Client
	if !riDryRun {
		c, err := api.New(api.Options{
			BaseURL:   cfg.BaseURL,
			APIKey:    cfg.APIKey,
			UserAgent: userAgent(),
			Verbose:   cfg.Verbose,
		})
		if err != nil {
			return err
		}
		client = c
	}

	summary := importSummary{
		Run:         runID,
		Project:     projectID,
		ResultsJSON: resultsJSON,
		DryRun:      riDryRun,
	}

	stderr := cmd.ErrOrStderr()
	ctx := context.Background()
	results.IterTests(func(parents []string, spec *playwright.Spec) {
		summary.TotalSpecs++
		final := pickFinalResult(spec)
		titles := append(append([]string(nil), parents...), spec.Title)

		var attachmentPaths []string
		if final != nil {
			for _, a := range final.Attachments {
				if a.Path != "" {
					attachmentPaths = append(attachmentPaths, a.Path)
				}
			}
		}
		// titles is searched in order by ResolveShortCode (first match
		// wins). Build the resolver input innermost-first so the most
		// specific scope is tried before its ancestors:
		//
		//   spec.Title → innermost describe → … → outermost describe
		//
		// Playwright passes `parents` outermost-to-innermost via
		// walkSuite, so we reverse them before prepending spec.Title.
		// Two failure modes this prevents:
		//   1. `describe("OB-3 Auth", () => test("OB-7 login", ...))`
		//      — without spec.Title-first, OB-3 captures the test
		//      intended for OB-7.
		//   2. `describe("OB-3", () => describe("OB-5", () =>
		//      test("no-code")))` — without parents reversed, OB-3
		//      (outer) wins over OB-5 (inner, more specific).
		parentChain := titles[:len(titles)-1]
		titlesInnerFirst := make([]string, 0, len(titles))
		titlesInnerFirst = append(titlesInnerFirst, spec.Title)
		for i := len(parentChain) - 1; i >= 0; i-- {
			titlesInnerFirst = append(titlesInnerFirst, parentChain[i])
		}
		code := playwright.ResolveShortCode(spec.Tags, titlesInnerFirst, attachmentPaths)
		if code == "" {
			summary.SkippedNoCode++
			fmt.Fprintf(stderr, "skip: no OB-N short code resolved for %q\n",
				strings.Join(titles, " » "))
			return
		}
		summary.ResolvedCases++

		if final == nil {
			fmt.Fprintf(stderr, "%s: spec has no results (skipped test) — leaving case status untouched\n", code)
			return
		}
		status := playwright.MapStatus(final.Status)

		// PATCH case status first so subsequent attachment uploads can
		// scope to a run-case row that the server now knows about.
		if !riDryRun {
			if err := client.EnsureAndUpdateRunCase(ctx, runID, code, status); err != nil {
				fmt.Fprintf(stderr, "%s: PATCH case status: %v\n", code, err)
				// Continue to attachments anyway — server may have the
				// case already attached and reject is on something else.
			}
		} else {
			fmt.Fprintf(stderr, "would PATCH %s status=%s\n", code, status)
		}

		// Top-level test.step → run-case step PATCH (1-based). Mirrors
		// the live reporter rule: only category="test.step" maps;
		// expect/hook/fixture/pw:api don't.
		stepIdx := 0
		for _, step := range final.Steps {
			if step.Category != "test.step" {
				continue
			}
			stepIdx++
			stepStatus := "passed"
			var stepComment string
			if step.Error != nil {
				stepStatus = "failed"
				stepComment = step.Error.Message
			}
			if !riDryRun {
				if err := client.UpdateRunCaseStep(ctx, api.UpdateRunCaseStepRequest{
					RunID:     runID,
					ShortCode: code,
					StepIndex: stepIdx,
					Status:    stepStatus,
					Comment:   redact.Apply(stepComment),
				}); err != nil {
					fmt.Fprintf(stderr, "%s step %d: PATCH: %v\n", code, stepIdx, err)
					continue
				}
				// Counter reflects actual successful API calls only; in
				// dry-run no PATCH was made, so `steps_patched` stays
				// 0 — important for CI scripts that gate on summary
				// JSON before promoting a real import. (Same pattern
				// applies to AttachmentsOK below.)
				summary.StepsPatched++
			} else {
				fmt.Fprintf(stderr, "would PATCH %s step %d status=%s\n", code, stepIdx, stepStatus)
			}
		}

		// Attachments: skip passing cases unless --upload-passed.
		// `flaky` (passed-on-retry) is still passed for our purposes —
		// the user opted into uploading passed attachments explicitly.
		if status == "passed" && !riUploadPass {
			return
		}

		// 1. Direct artifact files (.webm / .zip / .png / etc.)
		for _, att := range final.Attachments {
			if att.Path == "" {
				// Playwright can emit small attachments (custom
				// test.info().attach() with a buffer payload) as
				// inline base64 in `body` with no `path`. v1 doesn't
				// decode+upload these — warn so the user knows a named
				// attachment was skipped rather than silently lost.
				if att.Body != "" {
					fmt.Fprintf(stderr,
						"%s: skipping inline attachment %q (no file path; v1 only uploads path-backed attachments)\n",
						code, att.Name)
				}
				continue
			}
			if !riDryRun {
				if _, err := client.UploadAttachment(ctx, api.UploadAttachmentRequest{
					ProjectID: projectID,
					RunID:     runID,
					RunCaseID: code,
					FilePath:  att.Path,
				}); err != nil {
					fmt.Fprintf(stderr, "%s: upload %s: %v\n", code, att.Name, err)
					summary.AttachmentsErr++
					continue
				}
				summary.AttachmentsOK++
			} else {
				fmt.Fprintf(stderr, "would upload %s for %s\n", att.Path, code)
			}
		}

		// 2. Extracted console.json + network.json from trace.zip (if any).
		//    Guard with len(...) > 0 so a trace without console events
		//    or without network requests doesn't upload an empty `[]`
		//    array — saves a round-trip, keeps AttachmentsOK honest, and
		//    avoids creating an attachment row the FE viewer would have
		//    to special-case as "no data" instead of "no attachment".
		if tracePath := pickTraceZip(final.Attachments); tracePath != "" {
			tr, err := playwright.ExtractTrace(tracePath, redact)
			if err != nil {
				// Soft failure — partial console/network may still be in tr.
				fmt.Fprintf(stderr, "%s: extract %s: %v (uploading whatever extracted)\n",
					code, tracePath, err)
			}
			if len(tr.Console) > 0 {
				if err := uploadExtractedJSON(ctx, client, stderr, projectID, runID, code,
					"console.json", tr.Console, riDryRun, &summary); err != nil {
					summary.AttachmentsErr++
				}
			}
			if len(tr.Network) > 0 {
				if err := uploadExtractedJSON(ctx, client, stderr, projectID, runID, code,
					"network.json", tr.Network, riDryRun, &summary); err != nil {
					summary.AttachmentsErr++
				}
			}
		}

		// 3. failure.json from errors[].
		if len(final.Errors) > 0 {
			failures := make([]playwright.Failure, 0, len(final.Errors))
			for _, e := range final.Errors {
				f := playwright.ExtractFailure(e, sourceRoot)
				// Both Message and Stack must run through redact —
				// HTTP-client libraries (axios / got / node-fetch) embed
				// the full request, including `Authorization: Bearer …`
				// headers, in the thrown error's stack frame. Redacting
				// only Message leaves secrets verbatim in failure.json.
				f.Message = redact.Apply(f.Message)
				f.Stack = redact.Apply(f.Stack)
				failures = append(failures, f)
			}
			if err := uploadExtractedJSON(ctx, client, stderr, projectID, runID, code,
				"failure.json", failures, riDryRun, &summary); err != nil {
				summary.AttachmentsErr++
			}
		}
	})

	p := output.New(cfg.JSON)
	p.Out = cmd.OutOrStdout()
	return p.Result(summaryToMap(summary), summaryHuman(summary))
}

// pickFinalResult selects which test attempt (and therefore which set
// of attachments + steps + errors) to upload for a spec.
//
// Single-project configs — the common case — have spec.Tests with one
// entry, and this just returns its FinalResult().
//
// Multi-project configs (chromium + webkit + firefox …) project the
// same spec across every project. spec.OK is the AND across them, so a
// chromium failure paired with a passing webkit must NOT report
// "passed" just because webkit was iterated last. We pick the result
// with the worst status across all projects so the dashboard surfaces
// the failure that needs attention and uploads the failing browser's
// artifacts. Tie-break: later project wins (preserves the old "last
// projection" behaviour when statuses match).
func pickFinalResult(spec *playwright.Spec) *playwright.TestResult {
	if len(spec.Tests) == 0 {
		return nil
	}
	var worst *playwright.TestResult
	worstRank := -1
	for i := range spec.Tests {
		r := spec.Tests[i].FinalResult()
		if r == nil {
			continue
		}
		if rank := statusSeverity(r.Status); rank >= worstRank {
			worstRank = rank
			worst = r
		}
	}
	return worst
}

// statusSeverity maps a Playwright TestResult.status to a rank — higher
// means "deserves more operator attention". Used by pickFinalResult to
// promote a failing browser over a passing one in multi-project specs.
func statusSeverity(s string) int {
	switch s {
	case "failed":
		return 4
	case "timedOut", "interrupted":
		return 3
	case "skipped":
		return 1
	case "passed":
		return 0
	}
	// Unknown statuses sit above passed but below the explicit failure
	// classes — defensively flag the test for review without overruling
	// a real failure.
	return 2
}

// pickTraceZip finds the Playwright trace.zip among a test's attachments.
// Two-pass selection: a named "trace" attachment always wins over any
// other .zip in the list. The fallback to .zip extension is for legacy
// configs where the attachment name was lost during file copy; it must
// NEVER override an explicitly named trace, because the test may have
// attached an unrelated `archive.zip` (custom test data, screenshots
// bundle, …) that would extract to empty console/network and the real
// trace would be silently ignored.
//
// Within each pass we take the LAST match — matches Playwright's "show
// me the final attempt" convention when retries produce multiple traces.
func pickTraceZip(atts []playwright.PWAttachment) string {
	var named, anyZip string
	for _, a := range atts {
		if a.Path == "" {
			continue
		}
		if strings.EqualFold(a.Name, "trace") {
			named = a.Path
			continue
		}
		if strings.HasSuffix(strings.ToLower(a.Path), ".zip") {
			anyZip = a.Path
		}
	}
	if named != "" {
		return named
	}
	return anyZip
}

// uploadExtractedJSON writes `payload` to a tempfile, then uploads it
// via the existing UploadAttachment path. We marshal-then-upload
// (instead of holding bytes in memory + base64-encoding ourselves) so
// the upload reuses all the retry / verbose / context-cancellation
// plumbing in internal/api/client.go without a parallel code path.
func uploadExtractedJSON(
	ctx context.Context,
	client *api.Client,
	stderr io.Writer,
	projectID, runID, code, fileName string,
	payload any,
	dryRun bool,
	summary *importSummary,
) error {
	var data []byte
	var err error
	switch v := payload.(type) {
	case []playwright.ConsoleEntry:
		data, err = playwright.MarshalConsole(v)
	case []playwright.NetworkEntry:
		data, err = playwright.MarshalNetwork(v)
	default:
		data, err = json.MarshalIndent(payload, "", "  ")
	}
	if err != nil {
		return fmt.Errorf("marshal %s: %w", fileName, err)
	}

	if dryRun {
		// Counter only reflects actual successful API calls — see
		// the step-PATCH loop above for rationale. Dry-run summary
		// will have AttachmentsOK=0 even when "would upload" lines
		// were printed.
		fmt.Fprintf(stderr, "would upload extracted %s for %s (%d bytes)\n", fileName, code, len(data))
		return nil
	}

	// Server keys attachments off the filename suffix for content-type
	// inference and the dashboard download UX, so the file we point
	// UploadAttachment at must have the meaningful basename (e.g.
	// "console.json", not "observo-console.json3829473").
	//
	// We get there via a per-invocation temp DIRECTORY rather than a
	// per-invocation temp FILE renamed to a fixed name at os.TempDir()
	// root — the rename-to-fixed-name approach races when two `run
	// import` processes (e.g. parallel sharded CI runners on one
	// machine) hit the same /tmp/console.json target. The second
	// Rename atomically overwrites the first process's file, and the
	// first process then uploads the second process's data.
	tmpDir, err := os.MkdirTemp("", "observo-import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	path := filepath.Join(tmpDir, fileName)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}

	_, uperr := client.UploadAttachment(ctx, api.UploadAttachmentRequest{
		ProjectID: projectID,
		RunID:     runID,
		RunCaseID: code,
		FilePath:  path,
	})
	if uperr != nil {
		fmt.Fprintf(stderr, "%s: upload %s: %v\n", code, fileName, uperr)
		return uperr
	}
	summary.AttachmentsOK++
	return nil
}

func summaryToMap(s importSummary) map[string]any {
	return map[string]any{
		"run":             s.Run,
		"project":         s.Project,
		"results_json":    s.ResultsJSON,
		"total_specs":     s.TotalSpecs,
		"resolved_cases":  s.ResolvedCases,
		"skipped_no_code": s.SkippedNoCode,
		"attachments_ok":  s.AttachmentsOK,
		"attachments_err": s.AttachmentsErr,
		"steps_patched":   s.StepsPatched,
		"dry_run":         s.DryRun,
	}
}

func summaryHuman(s importSummary) string {
	prefix := "imported"
	if s.DryRun {
		prefix = "dry-run: would import"
	}
	return fmt.Sprintf("%s run=%s specs=%d resolved=%d skipped=%d attachments=%d errors=%d steps=%d",
		prefix, s.Run, s.TotalSpecs, s.ResolvedCases, s.SkippedNoCode,
		s.AttachmentsOK, s.AttachmentsErr, s.StepsPatched)
}

