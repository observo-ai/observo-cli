// Package doctor implements OB-524 — the `observo doctor` subcommand.
//
// doctor is a READ-ONLY setup diagnosis: it validates a repo's Observo
// integration and renders the OB-522 grounding ladder (L0–L3) with per-level
// ✅/❌ status and ONE exact fix for the first failing rung. It never mutates
// anything — every API call is a GET — and its exit code reflects health so it
// can double as a CI preflight step.
//
// The ladder definitions are the SAME as the Coverage-Truth Verdict's
// (OB-522): the canonical table lives in .github/OBSERVO_CI_SETUP.md ("Grounding
// levels (L0–L3)"), and both the verdict prompt and this command render from
// it. When the ladder changes, change it there and mirror it here — do not
// invent a parallel definition (that drift is exactly what OB-523 fixed for the
// project variable).
//
// The logic here is deliberately free of I/O beyond the injected Prober +
// pre-read workflow scan, so every level-detection branch is unit-testable
// without a network or a real checkout.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/observo-ai/observo-cli/internal/api"
)

// Grounding level numbers. Mirror OB-522 exactly (see the package doc).
const (
	LevelCodeOnly  = 0 // code-only:        diff + static test read (baseline)
	LevelProject   = 1 // project-grounded: managed cases, cross-PR memory, writeback
	LevelExecution = 2 // execution-verified: backend COVERED proven by a run's coverage
	LevelE2E       = 3 // e2e-traced:       UI behaviours verified via Playwright traces
	MaxLevel       = LevelE2E
)

// levelNames indexes the canonical short names by level number.
var levelNames = [MaxLevel + 1]string{
	LevelCodeOnly:  "code-only",
	LevelProject:   "project-grounded",
	LevelExecution: "execution-verified",
	LevelE2E:       "e2e-traced",
}

// Level is one rung of the ladder in the rendered report. Reached is
// cumulative: Reached at level N implies Reached at every lower level (each
// higher rung's condition includes the one below it), so the ladder never
// shows a satisfied rung above an unsatisfied one.
type Level struct {
	N       int    `json:"level"`
	Name    string `json:"name"`
	Reached bool   `json:"reached"`
	Detail  string `json:"detail"`        // one-line why (holds for both ✅ and ❌)
	Fix     string `json:"fix,omitempty"` // exact action to reach it (set only when !Reached)
}

// Report is the full diagnosis. Rendered as text or, with --json, verbatim.
type Report struct {
	Levels      []Level  `json:"levels"`
	Reached     int      `json:"reached_level"`       // highest contiguous reached rung
	FirstFail   int      `json:"first_failing_level"` // lowest unreached rung, -1 if fully grounded
	Fix         string   `json:"next_fix,omitempty"`  // fix for the first failing rung
	ProjectCode string   `json:"project_code,omitempty"`
	ProjectName string   `json:"project_name,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// Fully reports whether the repo reached the top of the ladder (L3).
func (r *Report) Fully() bool { return r.Reached >= MaxLevel }

// Prober is the read-only slice of the Observo API doctor needs. *api.Client
// satisfies it; tests pass a fake. Both calls are GETs.
type Prober interface {
	ListProjects(ctx context.Context) ([]api.Project, error)
	ListRuns(ctx context.Context, projectRef string) ([]api.Run, error)
}

// Options carries the pre-gathered, side-effect-free inputs. cmd/doctor.go
// reads the env + scans the workflows dir and hands the results in, keeping
// Diagnose pure.
type Options struct {
	// Workflow presence, scanned from .github/workflows/ by the caller.
	VerdictWorkflow  bool
	PipelineWorkflow bool

	// Project resolution candidates, in OB-523 precedence order. The caller
	// fills these from --project / $OBSERVO_PROJECT / $OBSERVO_PROJECT_CODE.
	ProjectFlag    string // --project (wins)
	EnvProject     string // $OBSERVO_PROJECT (canonical)
	EnvProjectCode string // $OBSERVO_PROJECT_CODE (deprecated fallback)
}

// Diagnose evaluates the grounding ladder against the repo + account and
// returns a Report. It performs only read-only GETs via prober and never
// returns a hard error for an unhealthy-but-reachable repo — the health is the
// Report. It returns an error only when it cannot form a verdict at all (which
// today it never does; the signature leaves room without forcing callers to
// change).
func Diagnose(ctx context.Context, prober Prober, opts Options) *Report {
	r := &Report{FirstFail: -1}
	levels := make([]Level, MaxLevel+1)
	for i := range levels {
		levels[i] = Level{N: i, Name: levelNames[i]}
	}

	// --- L0 code-only -------------------------------------------------------
	// OB-522 defines L0 as always-reachable (zero config). In a SETUP doctor,
	// the meaningful L0 question is "will this repo get a verdict at all?", so
	// we gate L0 on the Coverage-Truth Verdict workflow being present — a repo
	// with no verdict workflow gets no verdict, and reporting L0 ✅ there would
	// misrepresent setup health.
	if opts.VerdictWorkflow {
		levels[LevelCodeOnly].Reached = true
		levels[LevelCodeOnly].Detail = "Coverage-Truth Verdict workflow present — PRs get at least a code-only verdict"
	} else {
		levels[LevelCodeOnly].Detail = "no Coverage-Truth Verdict workflow found in .github/workflows/"
		levels[LevelCodeOnly].Fix = "Add the Coverage-Truth Verdict workflow: run `observo init`, or copy the workflow from .github/OBSERVO_CI_SETUP.md"
	}

	// --- L1 project-grounded ------------------------------------------------
	// Auth ping + project resolution share one ListProjects call: a 2xx means
	// the API key is valid, and the returned list resolves the project code
	// (and powers the sole-project fallback). We only attempt L1 once L0 holds,
	// so the reported ladder stays contiguous.
	projects, listErr := prober.ListProjects(ctx)
	apiKeyValid := listErr == nil
	code, name, resolveDetail, warn := resolveProject(opts, projects, listErr)
	if warn != "" {
		r.Warnings = append(r.Warnings, warn)
	}
	r.ProjectCode, r.ProjectName = code, name

	switch {
	case !levels[LevelCodeOnly].Reached:
		levels[LevelProject].Detail = "requires Level 0 (a verdict workflow) first"
		levels[LevelProject].Fix = levels[LevelCodeOnly].Fix
	case !apiKeyValid:
		levels[LevelProject].Detail = authFailureDetail(listErr)
		levels[LevelProject].Fix = authFailureFix(listErr)
	case code == "":
		levels[LevelProject].Detail = resolveDetail
		levels[LevelProject].Fix = "Set the repo variable OBSERVO_PROJECT to your Observo project code (Settings → Secrets and variables → Actions → Variables), or pass --project (see .github/OBSERVO_CI_SETUP.md)"
	default:
		levels[LevelProject].Reached = true
		levels[LevelProject].Detail = fmt.Sprintf("project resolved: %s (%s) — %s", code, name, resolveDetail)
	}

	// --- L2 execution-verified & L3 e2e-traced ------------------------------
	// Both read the latest CI run (the most recent run carrying pipeline
	// metadata). L2 needs coverage evidence on some layer; L3 needs a
	// Playwright layer. Only attempted once L1 holds.
	if levels[LevelProject].Reached {
		runs, runErr := prober.ListRuns(ctx, code)
		latest := latestCIRun(runs)
		switch {
		case runErr != nil:
			levels[LevelExecution].Detail = fmt.Sprintf("could not list runs for %s: %v", code, runErr)
			levels[LevelExecution].Fix = "Confirm the API key can read runs for this project, then re-run"
		case latest == nil:
			levels[LevelExecution].Detail = "no CI run with test layers found for this project yet"
			levels[LevelExecution].Fix = pipelineFix(opts.PipelineWorkflow)
		case !hasCoverage(latest):
			levels[LevelExecution].Detail = "latest CI run has no coverage evidence (no layer with a coverage LCOV attachment)"
			levels[LevelExecution].Fix = pipelineFix(opts.PipelineWorkflow)
		default:
			levels[LevelExecution].Reached = true
			levels[LevelExecution].Detail = "latest CI run attached coverage evidence — backend COVERED is proven by execution"
		}

		switch {
		case !levels[LevelExecution].Reached:
			levels[LevelE2E].Detail = "requires Level 2 (coverage evidence) first"
			levels[LevelE2E].Fix = levels[LevelExecution].Fix
		case !hasPlaywrightLayer(latest):
			levels[LevelE2E].Detail = "latest CI run has no Playwright layer — UI behaviours are not verified end-to-end"
			levels[LevelE2E].Fix = "Run Playwright in CI so its traces attach to the run (see .github/OBSERVO_CI_SETUP.md)"
		default:
			levels[LevelE2E].Reached = true
			levels[LevelE2E].Detail = "latest CI run has a Playwright layer — UI behaviours verified end-to-end"
		}
	} else {
		levels[LevelExecution].Detail = "requires Level 1 (project grounding) first"
		levels[LevelExecution].Fix = levels[LevelProject].Fix
		levels[LevelE2E].Detail = "requires Level 1 (project grounding) first"
		levels[LevelE2E].Fix = levels[LevelProject].Fix
	}

	// --- roll up: contiguous reached level + first failing rung -------------
	r.Levels = levels
	r.Reached = -1
	for i := 0; i <= MaxLevel; i++ {
		if !levels[i].Reached {
			break
		}
		r.Reached = i
	}
	if r.Reached < 0 {
		r.Reached = 0 // report "Level 0" as the floor even when L0 is unmet
		// (the ladder is 0-of-3); FirstFail below still points at L0.
	}
	for i := 0; i <= MaxLevel; i++ {
		if !levels[i].Reached {
			r.FirstFail = i
			r.Fix = levels[i].Fix
			break
		}
	}
	return r
}

// resolveProject picks the project code from the OB-523 precedence chain
// (--project → $OBSERVO_PROJECT → $OBSERVO_PROJECT_CODE → sole-project
// fallback) and validates it against the account's projects. It returns the
// resolved code + name (empty when unresolved), a one-line human detail, and a
// non-empty deprecation warning when the deprecated env var was used.
//
// projects is nil / listErr non-nil when the auth ping failed; in that case we
// can't validate, so we return empty (L1 fails on the auth branch upstream).
func resolveProject(opts Options, projects []api.Project, listErr error) (code, name, detail, warn string) {
	if listErr != nil {
		return "", "", "could not reach the Observo API to resolve the project", ""
	}

	candidate := strings.TrimSpace(opts.ProjectFlag)
	source := "--project"
	if candidate == "" {
		candidate = strings.TrimSpace(opts.EnvProject)
		source = "$OBSERVO_PROJECT"
	}
	if candidate == "" && strings.TrimSpace(opts.EnvProjectCode) != "" {
		candidate = strings.TrimSpace(opts.EnvProjectCode)
		source = "$OBSERVO_PROJECT_CODE"
		warn = "OBSERVO_PROJECT_CODE is deprecated — rename it to OBSERVO_PROJECT (see .github/OBSERVO_CI_SETUP.md)"
	}

	if candidate != "" {
		if p := findByCode(projects, candidate); p != nil {
			return p.Code, p.Name, "via " + source, warn
		}
		return "", "", fmt.Sprintf("project %q (from %s) not found in this account", candidate, source), warn
	}

	// No explicit code: sole-project fallback (same rule the verdict uses).
	switch len(projects) {
	case 0:
		return "", "", "no projects in this account", warn
	case 1:
		return projects[0].Code, projects[0].Name, "sole project in the account (no OBSERVO_PROJECT set)", warn
	default:
		return "", "", fmt.Sprintf("account has %d projects and no OBSERVO_PROJECT set — cannot pick one", len(projects)), warn
	}
}

// findByCode returns the project whose code matches (case-insensitively), or nil.
func findByCode(projects []api.Project, code string) *api.Project {
	want := strings.ToUpper(strings.TrimSpace(code))
	for i := range projects {
		if strings.ToUpper(projects[i].Code) == want {
			return &projects[i]
		}
	}
	return nil
}

// latestCIRun returns the most recent run carrying pipeline metadata (a CI
// run), or nil. Runs arrive latest-first from the server, so the first one
// with a non-nil pipeline is the most recent CI run.
func latestCIRun(runs []api.Run) *api.Run {
	for i := range runs {
		if runs[i].Pipeline != nil && len(runs[i].Pipeline.Layers) > 0 {
			return &runs[i]
		}
	}
	return nil
}

// hasCoverage reports whether any layer of the run carries a non-empty
// coverage LCOV attachment id (coverage.lcov_attachment_id) — the L2 signal.
func hasCoverage(run *api.Run) bool {
	if run == nil || run.Pipeline == nil {
		return false
	}
	for _, layer := range run.Pipeline.Layers {
		cov, ok := layer["coverage"].(map[string]any)
		if !ok {
			continue
		}
		if id, _ := cov["lcov_attachment_id"].(string); strings.TrimSpace(id) != "" {
			return true
		}
	}
	return false
}

// hasPlaywrightLayer reports whether any layer of the run has
// framework == "playwright" — the L3 signal.
func hasPlaywrightLayer(run *api.Run) bool {
	if run == nil || run.Pipeline == nil {
		return false
	}
	for _, layer := range run.Pipeline.Layers {
		if fw, _ := layer["framework"].(string); strings.EqualFold(strings.TrimSpace(fw), "playwright") {
			return true
		}
	}
	return false
}

// pipelineFix returns the L2 fix, tailored to whether a pipeline workflow was
// even found in the repo.
func pipelineFix(pipelinePresent bool) string {
	if pipelinePresent {
		return "Have the Observo pipeline attach coverage profiles + junit to the run for each commit (see .github/OBSERVO_CI_SETUP.md)"
	}
	return "Add the Observo pipeline workflow so CI creates a run and attaches coverage profiles + junit per commit (see .github/OBSERVO_CI_SETUP.md)"
}

// authFailureDetail turns a ListProjects error into a one-line L1 detail.
func authFailureDetail(err error) string {
	var herr *api.HTTPError
	if errors.As(err, &herr) && (herr.StatusCode == 401 || herr.StatusCode == 403) {
		return fmt.Sprintf("API key rejected (HTTP %d) — key is missing, invalid, or lacks access", herr.StatusCode)
	}
	return fmt.Sprintf("could not reach the Observo API: %v", err)
}

// authFailureFix turns a ListProjects error into the L1 fix hint.
func authFailureFix(err error) string {
	var herr *api.HTTPError
	if errors.As(err, &herr) && (herr.StatusCode == 401 || herr.StatusCode == 403) {
		return "Set a valid OBSERVO_API_KEY (create one at https://app.observoai.co/settings/api-keys) or pass --api-key"
	}
	return "Check network access to the Observo API (--base-url) and try again"
}
