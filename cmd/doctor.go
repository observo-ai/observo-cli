package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/observo-ai/observo-cli/internal/api"
	"github.com/observo-ai/observo-cli/internal/config"
	"github.com/observo-ai/observo-cli/internal/doctor"
	"github.com/observo-ai/observo-cli/internal/output"

	"github.com/spf13/cobra"
)

// OB-524 — `observo doctor` subcommand.
//
// One-command setup diagnosis. Validates the repo's Observo integration and
// prints the OB-522 grounding ladder (L0–L3) with per-level ✅/❌ and ONE exact
// fix for the first failing rung. Read-only (every API call is a GET); the
// exit code reflects health so it can run as a CI preflight step. Customer-
// facing and project-agnostic — needs only repo context + an API key, not a
// local checkout of Observo itself.

var (
	doctorFlagProject  string
	doctorFlagMinLevel int
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose this repo's Observo integration (grounding ladder L0–L3)",
	Long: `Checks how much your repo is set up to be verified by Observo and prints the
grounding ladder — the same L0–L3 levels the Coverage-Truth Verdict reports:

  L0 code-only          a verdict runs on PRs (diff + static test read)
  L1 project-grounded   the project resolves → managed cases, cross-PR memory, writeback
  L2 execution-verified CI attaches coverage → backend COVERED proven by a real run
  L3 e2e-traced         Playwright traces attach → UI behaviours verified end-to-end

Each rung shows ✅/❌ and, for the FIRST failing one, the single exact fix.

doctor is READ-ONLY — it never changes your repo or your Observo data. The exit
code reflects health: 0 when fully grounded, non-zero otherwise, so you can run
it as a CI preflight step. Use --min-level to gate at a lower rung.

Project resolution (same order as the verdict): --project → $OBSERVO_PROJECT →
$OBSERVO_PROJECT_CODE (deprecated) → the sole project in the account.`,
	Args: cobra.NoArgs,
	RunE: doctorExec,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
	f := doctorCmd.Flags()
	f.StringVar(&doctorFlagProject, "project", "", "Observo project code to ground against (overrides $OBSERVO_PROJECT)")
	f.IntVar(&doctorFlagMinLevel, "min-level", doctor.MaxLevel,
		"exit 0 only when the repo reaches at least this grounding level (0–3; default 3 = fully grounded)")
}

func doctorExec(cmd *cobra.Command, _ []string) error {
	if doctorFlagMinLevel < 0 || doctorFlagMinLevel > doctor.MaxLevel {
		return fmt.Errorf("--min-level must be between 0 and %d", doctor.MaxLevel)
	}

	// NOTE: we intentionally do NOT call cfg.Validate() here — a missing API
	// key is itself a diagnosis (Level 1 fails with a fix), not a usage error.
	cfg := config.Resolve(flagAPIKey, flagBaseURL, flagJSON, flagVerbose)

	// Resolve the git root so the workflow scan looks under the repo's
	// top-level .github/workflows/ even when doctor is run from a subdirectory.
	// Fall back to the cwd when we're not in a git checkout — doctor should
	// still work (it just scans ./.github/workflows/).
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repoRoot := cwd
	if root, gerr := resolveGitRoot(cwd); gerr == nil {
		repoRoot = root
	}
	scan := doctor.ScanWorkflows(repoRoot)

	prober := buildProber(cfg)

	report := doctor.Diagnose(context.Background(), prober, doctor.Options{
		VerdictWorkflow:  scan.Verdict,
		PipelineWorkflow: scan.Pipeline,
		ProjectFlag:      doctorFlagProject,
		EnvProject:       strings.TrimSpace(os.Getenv("OBSERVO_PROJECT")),
		EnvProjectCode:   strings.TrimSpace(os.Getenv("OBSERVO_PROJECT_CODE")),
	})

	if cfg.JSON {
		p := output.New(true)
		p.Out = cmd.OutOrStdout()
		if rerr := p.Result(report, ""); rerr != nil {
			return rerr
		}
	} else if _, werr := fmt.Fprint(cmd.OutOrStdout(), renderReport(report, repoRoot)); werr != nil {
		return werr
	}

	// Exit code reflects health without looking like a command failure.
	code := 0
	if report.Reached < doctorFlagMinLevel {
		code = 1
	}
	exitOverride = &code
	return nil
}

// buildProber returns the read-only API prober doctor drives. When no API key
// is configured we return a stub that reports the key as rejected — so Level 1
// fails with the "set OBSERVO_API_KEY" fix instead of the command erroring out
// before it can render the ladder (L0 is still reportable from the local scan).
func buildProber(cfg *config.Config) doctor.Prober {
	if cfg.APIKey == "" {
		return missingKeyProber{}
	}
	client, err := api.New(api.Options{
		BaseURL:   cfg.BaseURL,
		APIKey:    cfg.APIKey,
		UserAgent: userAgent(),
		Verbose:   cfg.Verbose,
	})
	if err != nil {
		return missingKeyProber{}
	}
	return client
}

// missingKeyProber stands in when OBSERVO_API_KEY is unset. It returns a 401
// so the diagnosis flows down the standard auth-failure branch (which already
// phrases the fix as "key is missing, invalid, or lacks access").
type missingKeyProber struct{}

func (missingKeyProber) unauth() error {
	return &api.HTTPError{StatusCode: 401, URL: "", Body: "OBSERVO_API_KEY not set"}
}

func (m missingKeyProber) ListProjects(context.Context) ([]api.Project, error) {
	return nil, m.unauth()
}

func (m missingKeyProber) ListRuns(context.Context, string) ([]api.Run, error) {
	return nil, m.unauth()
}

// --- rendering --------------------------------------------------------------

// renderReport builds the human-readable ladder. The header line names the
// resolved project code when there is one, else falls back to repoRoot.
func renderReport(r *doctor.Report, repoRoot string) string {
	var b strings.Builder

	target := r.ProjectCode
	if target == "" {
		target = repoRoot
	}
	fmt.Fprintf(&b, "🩺 observo doctor — %s\n\n", target)

	fmt.Fprintf(&b, "🧭 Grounding: Level %d of %d (%s)\n\n", r.Reached, doctor.MaxLevel, r.Levels[r.Reached].Name)

	for i := 0; i <= doctor.MaxLevel; i++ {
		lv := r.Levels[i]
		mark := "⬜"
		switch {
		case lv.Reached:
			mark = "✅"
		case i == r.FirstFail:
			mark = "❌"
		}
		fmt.Fprintf(&b, "  %s L%d %-18s %s\n", mark, lv.N, lv.Name, lv.Detail)
	}

	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "\n⚠ %s", w)
	}
	if len(r.Warnings) > 0 {
		b.WriteString("\n")
	}

	if r.Fully() {
		fmt.Fprintf(&b, "\n🎉 Fully grounded — verdicts run at the highest fidelity.\n")
	} else {
		fmt.Fprintf(&b, "\n🔦 Next (to reach Level %d): %s\n", r.FirstFail, r.Fix)
	}
	return b.String()
}
