package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/observo-ai/observo-cli/internal/api"
	"github.com/observo-ai/observo-cli/internal/config"
	"github.com/observo-ai/observo-cli/internal/jvm"
	"github.com/observo-ai/observo-cli/internal/output"
	"github.com/observo-ai/observo-cli/internal/state"

	"github.com/spf13/cobra"
)

var (
	jiManifest  string
	jiFrom      string
	jiTestNG    string
	jiChain     string
	jiProject   string
	jiLayer     string
	jiPriority  string
	jiApply     bool
	jiAllowCI   bool
	jiStateFile string
)

var jvmImportCmd = &cobra.Command{
	Use:   "import [--manifest observo-link-manifest.json | --from allure-results] --project PD",
	Short: "Create/upsert Observo cases & suites from a JVM test run (dry-run by default)",
	Long: `Import a JVM suite into Observo: create suites (from @Feature) and cases
(from the manifest), modeling order-dependent chains.

Result source (one required):
  --manifest        a manifest from 'observo jvm manifest'
  --from/--testng-results  build the manifest inline

Chain modeling:
  --chain=steps (default)  a class → ONE case with N ordered steps
  --chain=flat             a class → N cases + a plan preserving order

Safety:
  dry-run by DEFAULT — prints the plan and calls no write API. Pass --apply
  to write. In CI (CI=true) --apply refuses without --allow-ci: import is a
  dev-machine operation (the tag write-back it will eventually do produces a
  local diff someone must commit).

Provenance: created cases are stamped source='imported'. Tests that already
carry an observo:<code> tag are matched to the existing case and skipped
(idempotent). Untracked tests get a NEW case; the tag is NOT yet written
back into the source (AST write-back is demand-gated) — the fqName→code map
is printed so you can add tags (or run 'observo jvm stub').

Examples:
  observo jvm import --from build/allure-results --testng-results build/testng-results.xml --project PD
  observo jvm import --manifest observo-link-manifest.json --chain=flat --project PD --apply`,
	Args: cobra.NoArgs,
	RunE: jvmImportExec,
}

func init() {
	jvmCmd.AddCommand(jvmImportCmd)
	f := jvmImportCmd.Flags()
	f.StringVar(&jiManifest, "manifest", "", "path to observo-link-manifest.json")
	f.StringVar(&jiFrom, "from", "", "Allure results dir (build manifest inline)")
	f.StringVar(&jiTestNG, "testng-results", "", "path to testng-results.xml")
	f.StringVar(&jiChain, "chain", jvm.ChainSteps, "chain modeling: steps | flat")
	f.StringVar(&jiProject, "project", "", "project UUID or short code (default: from state file)")
	f.StringVar(&jiLayer, "layer", "API", "layer for created cases: API | E2E | UNIT")
	f.StringVar(&jiPriority, "priority", "MEDIUM", "priority for created cases: HIGH | MEDIUM | LOW")
	f.BoolVar(&jiApply, "apply", false, "write to Observo (default: dry-run)")
	f.BoolVar(&jiAllowCI, "allow-ci", false, "permit --apply under CI=true (import is normally a dev-machine op)")
	f.StringVar(&jiStateFile, "state-file", state.DefaultPath, "where to read project when --project is unset")
}

type importSummaryJVM struct {
	Project       string   `json:"project"`
	Chain         string   `json:"chain"`
	SuitesCreated int      `json:"suites_created"`
	CasesCreated  int      `json:"cases_created"`
	CasesLinked   int      `json:"cases_linked"` // tracked → already existed, skipped
	PlansCreated  int      `json:"plans_created"`
	Untagged      []string `json:"untagged,omitempty"`    // fqName=code for standalone cases needing a source tag
	ChainCases    []string `json:"chain_cases,omitempty"` // steps-mode chain cases (re-create until write-back ships)
	DryRun        bool     `json:"dry_run,omitempty"`
	Errors        int      `json:"errors"`
}

func jvmImportExec(cmd *cobra.Command, args []string) error {
	layer, err := layerEnum(jiLayer)
	if err != nil {
		return err
	}
	priority, err := priorityEnum(jiPriority)
	if err != nil {
		return err
	}

	m, err := loadImportManifest()
	if err != nil {
		return err
	}
	plan, err := jvm.PlanImport(m, jvm.ImportOptions{ChainMode: jiChain})
	if err != nil {
		return err
	}

	project := resolveImportProject()
	if project == "" {
		return errors.New("no project: pass --project or run 'observo run create' first")
	}

	// CI-guard: --apply under CI needs an explicit override.
	if jiApply && isCI() && !jiAllowCI {
		return errors.New("refusing --apply under CI (CI=true): import is a dev-machine operation — pass --allow-ci to override")
	}

	stderr := cmd.ErrOrStderr()
	summary := importSummaryJVM{Project: project, Chain: jiChain, DryRun: !jiApply}

	if !jiApply {
		printImportPlan(stderr, plan)
		// Reflect the computed plan in the summary/JSON too, so `--json`
		// dry-run gives a scripted consumer real numbers, not zeros.
		// (SuitesCreated here is "suites to ensure" — dry-run makes no API
		// call, so it can't know which already exist.)
		summary.SuitesCreated = len(plan.Features)
		summary.PlansCreated = len(plan.Plans)
		for _, c := range plan.Cases {
			if c.Tracked {
				summary.CasesLinked++
			} else {
				summary.CasesCreated++
			}
		}
	} else {
		cfg := config.Resolve(flagAPIKey, flagBaseURL, flagJSON, flagVerbose)
		if err := cfg.Validate(); err != nil {
			return err
		}
		client, err := api.New(api.Options{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, UserAgent: userAgent(), Verbose: cfg.Verbose})
		if err != nil {
			return err
		}
		if err := executeImport(cmd.Context(), client, project, layer, priority, plan, &summary, stderr); err != nil {
			return err
		}
	}

	p := output.New(flagJSON)
	p.Out = cmd.OutOrStdout()
	return p.Result(importSummaryToMap(summary), importSummaryHuman(summary))
}

// executeImport applies the plan: ensure suites, create/skip cases, create
// flat-chain plans, and report untagged cases.
func executeImport(ctx context.Context, client *api.Client, project, layer, priority string,
	plan *jvm.ImportPlan, summary *importSummaryJVM, stderr io.Writer) error {

	suiteIDs, created, err := ensureSuites(ctx, client, project, plan.Features)
	if err != nil {
		return err
	}
	summary.SuitesCreated = created

	// fqName → created/linked case UUID, for resolving flat-chain plans.
	fqToID := map[string]string{}

	for i := range plan.Cases {
		pc := plan.Cases[i]

		// Tracked test → verify the case exists; skip (no duplicate).
		if pc.Tracked {
			tc, gErr := client.GetTestCaseByCode(ctx, pc.Code)
			if gErr == nil {
				fqToID[pc.FQName] = tc.ID
				summary.CasesLinked++
				fmt.Fprintf(stderr, "link: %s already exists (%s) — skipped\n", pc.Code, pc.FQName)
				continue
			}
			// Only a genuine 404 means the tag is stale (case deleted) → recreate.
			// A transient 5xx / timeout / 401 must NOT fall through to create,
			// or a backend hiccup silently duplicates every tagged case on
			// re-import. Mirror internal/api/cases.go's 404 discrimination.
			var herr *api.HTTPError
			if !(errors.As(gErr, &herr) && herr.StatusCode == 404) {
				fmt.Fprintf(stderr, "error: verify %s: %v (skipping to avoid a duplicate)\n", pc.Code, gErr)
				summary.Errors++
				continue
			}
			fmt.Fprintf(stderr, "warn: tagged %s not found (404) — re-creating\n", pc.Code)
		}

		req := api.CreateTestCaseRequest{
			Name:        pc.Name,
			Description: pc.Description,
			Layer:       layer,
			Priority:    priority,
			Source:      "CASE_SOURCE_IMPORTED",
		}
		if sid := suiteIDs[pc.Feature]; sid != "" {
			req.SuiteID = sid
		} else {
			req.ProjectID = project
		}
		for _, st := range pc.Steps {
			req.Steps = append(req.Steps, api.TestCaseStep{Action: st.Title})
		}

		tc, cErr := client.CreateTestCase(ctx, req)
		if cErr != nil {
			fmt.Fprintf(stderr, "error: create %q: %v\n", pc.Name, cErr)
			summary.Errors++
			continue
		}
		summary.CasesCreated++

		if len(pc.Steps) > 0 {
			// A steps-mode chain case is created new every run (no code to
			// match on until AST write-back ships), so per-method "add a tag"
			// advice would be misleading here — track it separately.
			summary.ChainCases = append(summary.ChainCases, tc.ShortCode+" ("+pc.Name+")")
			continue
		}
		if pc.FQName != "" {
			fqToID[pc.FQName] = tc.ID
			// Standalone case just minted a code — its source test needs the
			// tag added (untracked) or updated (stale-tracked re-created).
			summary.Untagged = append(summary.Untagged, pc.FQName+"="+tc.ShortCode)
		}
	}

	// Flat-chain plans, in chain order.
	for _, pp := range plan.Plans {
		// Idempotent: skip if a plan with this key already exists (re-import
		// must not create a duplicate plan — CreatePlan does no key check).
		existing, gErr := client.GetPlanByKey(ctx, project, pp.Key)
		if gErr == nil && existing != nil {
			fmt.Fprintf(stderr, "plan %s already exists — skipped\n", pp.Key)
			continue
		}
		// Only a genuine not-found may fall through to create. GetPlanByKey
		// resolves a key by listing, so a transient 5xx / timeout / auth hiccup
		// errors out too — and reading that as "absent" duplicates the plan on
		// every re-import. Same discipline as the tagged-case probe above.
		if gErr != nil && !errors.Is(gErr, api.ErrPlanNotFound) {
			fmt.Fprintf(stderr, "error: verify plan %s: %v (skipping to avoid a duplicate)\n", pp.Key, gErr)
			summary.Errors++
			continue
		}

		var ids []string
		missing := 0
		for _, fq := range pp.CaseFQNames {
			if id := fqToID[fq]; id != "" {
				ids = append(ids, id)
			} else {
				missing++
			}
		}
		// Surface an incomplete plan rather than silently dropping members
		// (a failed case create, or an entry with no fq_name).
		if missing > 0 {
			fmt.Fprintf(stderr, "warn: plan %s is missing %d/%d cases (create failed or no fq_name) — plan will be incomplete\n",
				pp.Key, missing, len(pp.CaseFQNames))
		}
		if len(ids) == 0 {
			continue
		}
		if _, err := client.CreatePlan(ctx, project, pp.Key, pp.Name, ids); err != nil {
			fmt.Fprintf(stderr, "error: create plan %s: %v\n", pp.Key, err)
			summary.Errors++
			continue
		}
		summary.PlansCreated++
	}

	if len(summary.Untagged) > 0 {
		fmt.Fprintf(stderr, "\nNOTE: %d standalone test(s) have no observo:<code> tag written back (AST write-back is demand-gated).\n"+
			"Add/update the tag manually or run 'observo jvm stub', else re-import will re-create them:\n", len(summary.Untagged))
		for _, u := range summary.Untagged {
			fmt.Fprintf(stderr, "  %s\n", u)
		}
	}
	if len(summary.ChainCases) > 0 {
		fmt.Fprintf(stderr, "\nNOTE: %d steps-mode chain case(s) are created new on every --apply (a class collapses to one\n"+
			"case with no per-run identity until AST write-back ships). Use --chain=flat for per-method\n"+
			"idempotent linking, or import chains once:\n", len(summary.ChainCases))
		for _, c := range summary.ChainCases {
			fmt.Fprintf(stderr, "  %s\n", c)
		}
	}
	return nil
}

// ensureSuites resolves each feature name to a suite id, reusing an existing
// suite by name (dedup) and creating any that are missing. Returns the
// name→id map and the count created.
func ensureSuites(ctx context.Context, client *api.Client, project string, features []string) (map[string]string, int, error) {
	existing, err := client.ListSuites(ctx, project)
	if err != nil {
		return nil, 0, fmt.Errorf("list suites: %w", err)
	}
	byName := map[string]string{}
	for _, s := range existing {
		if _, seen := byName[s.Name]; !seen { // first match wins on duplicate names
			byName[s.Name] = s.ID
		}
	}
	created := 0
	for _, f := range features {
		if _, ok := byName[f]; ok {
			continue
		}
		s, cErr := client.CreateSuite(ctx, project, f)
		if cErr != nil {
			return nil, created, fmt.Errorf("create suite %q: %w", f, cErr)
		}
		byName[f] = s.ID
		created++
	}
	return byName, created, nil
}

func loadImportManifest() (*jvm.LinkManifest, error) {
	switch {
	case jiManifest != "":
		return jvm.LoadManifest(jiManifest)
	case jiFrom != "" || jiTestNG != "":
		return jvm.BuildManifest(jvm.BuildOptions{AllureDir: jiFrom, TestNGResults: jiTestNG})
	default:
		return nil, errors.New("no result source: set --manifest, or --from / --testng-results")
	}
}

func resolveImportProject() string {
	if jiProject != "" {
		return jiProject
	}
	if st, err := state.Load(jiStateFile); err == nil {
		return st.ProjectID
	}
	return ""
}

// isCI reports whether we're running in a CI environment. Treats CI set to
// any value other than "" / "false" / "0" as true (GitHub/GitLab set "true").
func isCI() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("CI")))
	return v != "" && v != "false" && v != "0"
}

func layerEnum(s string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "API":
		return "LAYER_API", nil
	case "E2E":
		return "LAYER_E2E", nil
	case "UNIT":
		return "LAYER_UNIT", nil
	default:
		return "", fmt.Errorf("invalid --layer %q (want API|E2E|UNIT)", s)
	}
}

func priorityEnum(s string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "HIGH":
		return "PRIORITY_HIGH", nil
	case "MEDIUM":
		return "PRIORITY_MEDIUM", nil
	case "LOW":
		return "PRIORITY_LOW", nil
	default:
		return "", fmt.Errorf("invalid --priority %q (want HIGH|MEDIUM|LOW)", s)
	}
}

func printImportPlan(w io.Writer, plan *jvm.ImportPlan) {
	fmt.Fprintf(w, "dry-run — no writes. Plan (--apply to execute):\n")
	fmt.Fprintf(w, "  suites to ensure: %d %v\n", len(plan.Features), plan.Features)
	for _, c := range plan.Cases {
		verb := "create"
		if c.Tracked {
			verb = "link/skip"
		}
		suffix := ""
		if len(c.Steps) > 0 {
			suffix = fmt.Sprintf(" (%d steps)", len(c.Steps))
		}
		code := c.Code
		if code == "" {
			code = "new"
		}
		fmt.Fprintf(w, "  %s [%s] %q suite=%q%s\n", verb, code, c.Name, c.Feature, suffix)
	}
	for _, p := range plan.Plans {
		fmt.Fprintf(w, "  plan %s: %d cases in order\n", p.Key, len(p.CaseFQNames))
	}
}

func importSummaryToMap(s importSummaryJVM) map[string]any {
	return map[string]any{
		"project":        s.Project,
		"chain":          s.Chain,
		"suites_created": s.SuitesCreated,
		"cases_created":  s.CasesCreated,
		"cases_linked":   s.CasesLinked,
		"plans_created":  s.PlansCreated,
		"untagged":       s.Untagged,
		"chain_cases":    s.ChainCases,
		"dry_run":        s.DryRun,
		"errors":         s.Errors,
	}
}

func importSummaryHuman(s importSummaryJVM) string {
	prefix := "imported"
	if s.DryRun {
		prefix = "dry-run: would import"
	}
	return fmt.Sprintf("%s project=%s chain=%s suites+%d cases+%d linked=%d plans+%d untagged=%d errors=%d",
		prefix, s.Project, s.Chain, s.SuitesCreated, s.CasesCreated, s.CasesLinked,
		s.PlansCreated, len(s.Untagged), s.Errors)
}
