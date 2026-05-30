package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/observo-ai/observo-cli/internal/initialize"

	"github.com/spf13/cobra"
)

// OB-356 — `observo init` subcommand.
//
// Interactive bootstrap that wires a customer's repo to Observo in one
// command. Detects the Playwright config, patches it with the Observo
// reporter, generates a GitHub Actions workflow that uses
// observo-ai/setup@v1 for OIDC auth, and (optionally) installs the npm
// package and creates a git branch + commit.
//
// Plan creation in Observo (POST /api/plans) is intentionally NOT done
// here yet — the customer creates the plan in the dashboard, copies the
// plan_key, and either accepts the auto-derived default at the prompt or
// types theirs. Wiring the API call comes once the OAuth-from-CLI flow
// ships; for v1 we keep `init` zero-secret-prompt and the customer
// supplies the plan name in the prompt.

var (
	initFlagPlan       string
	initFlagFramework  string
	initFlagYes        bool
	initFlagPrint      bool
	initFlagNoCommit   bool
	initFlagNoInstall  bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Bootstrap a repo for Observo CI integration (Playwright + GitHub)",
	Long: `Detects your test framework, patches the config to enable the Observo
reporter, generates a GitHub Actions workflow that authenticates via OIDC,
and (optionally) commits the result as a PR-ready branch.

Use --print for a dry-run that shows every planned change without writing
anything. Use --yes to accept all defaults non-interactively (useful in CI
preview workflows).`,
	Args: cobra.NoArgs,
	RunE: initExec,
}

func init() {
	rootCmd.AddCommand(initCmd)
	f := initCmd.Flags()
	f.StringVar(&initFlagPlan, "plan", "", "Observo plan key (default: auto-derived from repo name)")
	f.StringVar(&initFlagFramework, "framework", "playwright", "test framework (only 'playwright' supported in v1)")
	f.BoolVarP(&initFlagYes, "yes", "y", false, "accept all defaults non-interactively")
	f.BoolVar(&initFlagPrint, "print", false, "dry-run: print planned changes without writing files or running commands")
	f.BoolVar(&initFlagNoCommit, "no-commit", false, "skip the git branch + commit step")
	f.BoolVar(&initFlagNoInstall, "no-install", false, "skip 'npm install' for the reporter package")
}

func initExec(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	in := cmd.InOrStdin()

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	// 1) Detect repo. Failure is fatal — we don't support init outside a git checkout.
	repo, err := initialize.DetectRepo(cwd)
	if err != nil {
		return fmt.Errorf("observo init must run inside a git repository: %w", err)
	}
	fmt.Fprintf(out, "✓ Detected repo: %s/%s on %s\n", repo.Owner, repo.Name, repo.Host)

	// 2) Detect framework. Only Playwright in v1.
	if initFlagFramework != "playwright" {
		return fmt.Errorf("framework %q not supported in v1 — only 'playwright'. File issue at https://github.com/observo-ai/observo-cli/issues if you need it", initFlagFramework)
	}
	pwCfg, err := initialize.FindPlaywrightConfig(cwd)
	if err != nil {
		return fmt.Errorf("playwright not detected: %w", err)
	}
	fmt.Fprintf(out, "✓ Detected framework: Playwright at %s (%d spec files)\n", pwCfg.ConfigPath, len(pwCfg.SpecFiles))

	// 3) Plan name. Flag wins; else prompt; else derive from repo name.
	//    Validate the final value — auto-derived keys are already sanitized,
	//    but --plan flag and prompted values can carry chars that confuse the
	//    YAML scalar embed (`: `, leading `*` / `&`) or backend plan_key
	//    constraints. Stop early with a clear error before we render anything.
	planKey := initFlagPlan
	if planKey == "" {
		planKey = repo.DefaultPlan
		if !initFlagYes {
			planKey = promptDefault(in, out, fmt.Sprintf("Plan key (in Observo dashboard)? [%s]", repo.DefaultPlan), repo.DefaultPlan)
		}
	}
	// Normalize to uppercase before validation. Both prompted input and
	// --plan flag arrive verbatim; ValidatePlanKey requires uppercase. Without
	// this, a user shown `Plan key? [MY-APP-E2E]` who types `my-app-e2e` would
	// get "invalid plan key" and have to restart the whole flow.
	planKey = strings.ToUpper(planKey)
	if err := initialize.ValidatePlanKey(planKey); err != nil {
		return fmt.Errorf("invalid plan key: %w", err)
	}
	fmt.Fprintf(out, "✓ Plan key: %s\n", planKey)

	// 4) Resolve the git root — the workflow file MUST land under the repo's
	//    top-level .github/workflows/ to be picked up by GitHub Actions.
	//    `cwd` may be a subdirectory (e.g. user runs `observo init` from
	//    monorepos/frontend/) in which case `cwd/.github/workflows/` is the
	//    wrong place.
	gitRoot, err := resolveGitRoot(cwd)
	if err != nil {
		return fmt.Errorf("resolve git root: %w", err)
	}

	// 5) Detect existing Playwright workflow under the actual git root + warn.
	//    Capture I/O errors instead of swallowing — permissions / fs errors
	//    on .github/workflows/ should surface, not vanish the dup-warning.
	existing, dwErr := initialize.DetectExistingPlaywrightWorkflow(gitRoot)
	if dwErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "⚠ Could not scan %s/.github/workflows/ for existing Playwright workflows: %v (proceeding without dup-warning)\n", gitRoot, dwErr)
	} else if existing != "" {
		fmt.Fprintf(out, "ⓘ Note: %s already runs Playwright. 'observo init' will create a SECOND workflow.\n"+
			"  Consider patching the existing one instead — see https://observoai.co/docs/connect-github-actions\n",
			existing)
	}

	// 6) Plan the patch to playwright.config.ts.
	patch, err := initialize.PatchPlaywrightConfig(filepath.Join(cwd, pwCfg.ConfigPath))
	if err != nil {
		return fmt.Errorf("read playwright config: %w", err)
	}
	if patch.Changed {
		fmt.Fprintf(out, "✓ Will patch %s (adds @observo/playwright-reporter line)\n", pwCfg.ConfigPath)
	} else {
		fmt.Fprintf(out, "ⓘ %s — no patch needed: %s\n", pwCfg.ConfigPath, patch.Reason)
	}

	// 7) Plan the workflow file — anchored at git root, NOT process cwd.
	wfDest := initialize.WorkflowFileTarget(gitRoot)
	wfBody := initialize.RenderWorkflow(initialize.WorkflowSpec{PlanKey: planKey})
	wfWillWrite := !initialize.FileExists(wfDest)
	if wfWillWrite {
		fmt.Fprintf(out, "✓ Will create %s (%d bytes)\n", relTo(cwd, wfDest), len(wfBody))
	} else {
		fmt.Fprintf(out, "⚠ Skipping %s — file already exists. Patch manually or remove and re-run.\n", relTo(cwd, wfDest))
	}

	// 7) Plan dependency install + commit.
	if !initFlagNoInstall {
		fmt.Fprintln(out, "✓ Will run: npm install -D @observo/playwright-reporter")
	} else {
		fmt.Fprintln(out, "⊘ Skipping npm install (--no-install)")
	}
	if !initFlagNoCommit {
		fmt.Fprintln(out, "✓ Will create branch chore/observo-init and commit changes")
	} else {
		fmt.Fprintln(out, "⊘ Skipping git branch + commit (--no-commit)")
	}

	// 8) Dry-run? Exit here — never touch the filesystem.
	if initFlagPrint {
		fmt.Fprintln(out, "\n(dry-run: nothing was written. Re-run without --print to apply.)")
		return nil
	}

	// 9) Final confirmation prompt unless --yes.
	if !initFlagYes {
		if !promptYesNo(in, out, "Apply these changes?", true) {
			fmt.Fprintln(out, "aborted")
			return nil
		}
	}

	// 10) Execute the plan.
	if patch.Changed {
		if err := initialize.WritePatch(patch); err != nil {
			return fmt.Errorf("write patched config: %w", err)
		}
		fmt.Fprintf(out, "  ✓ Patched %s\n", pwCfg.ConfigPath)
	}
	if wfWillWrite {
		if err := initialize.WriteWorkflow(wfDest, wfBody); err != nil {
			return fmt.Errorf("write workflow: %w", err)
		}
		fmt.Fprintf(out, "  ✓ Wrote %s\n", relTo(cwd, wfDest))
	}
	if !initFlagNoInstall {
		// npm install runs in the directory that owns playwright.config.ts,
		// not in process cwd. Monorepo with web-portal/playwright.config.ts
		// has its own web-portal/package.json — installing at repo root
		// would silently drop the dep into the wrong package.json (or fail
		// if no root package.json exists). filepath.Dir handles the
		// repo-root case too (Dir("playwright.config.ts") == ".").
		pwDir := filepath.Join(cwd, filepath.Dir(pwCfg.ConfigPath))
		if err := runShellIn(cmd, pwDir, "npm", "install", "-D", "@observo/playwright-reporter"); err != nil {
			return fmt.Errorf("npm install: %w", err)
		}
		fmt.Fprintln(out, "  ✓ Installed @observo/playwright-reporter")
	}
	if !initFlagNoCommit {
		// Build path list from what we actually changed. Package files are
		// added ONLY when at least one other change is happening — otherwise
		// a re-run on an already-configured repo (config patched, workflow
		// present) would commit unchanged package.json + lock and fail with
		// "nothing to commit".
		var addPaths []string
		if patch.Changed {
			addPaths = append(addPaths, pwCfg.ConfigPath)
		}
		if wfWillWrite {
			addPaths = append(addPaths, relTo(cwd, wfDest))
		}
		hasOtherChanges := len(addPaths) > 0
		if !initFlagNoInstall && hasOtherChanges {
			// package.json + lock live next to the playwright config (monorepo case).
			pkgDir := filepath.Dir(pwCfg.ConfigPath)
			addPaths = append(addPaths,
				filepath.Join(pkgDir, "package.json"),
				filepath.Join(pkgDir, "package-lock.json"),
			)
		}
		if len(addPaths) == 0 {
			// Nothing changed — skip the git operations entirely so we don't
			// switch the user's working branch as a side-effect of a no-op.
			fmt.Fprintln(out, "  ⊘ Nothing to commit (config already had reporter + workflow exists)")
		} else {
			if err := commitChanges(cmd, cwd, addPaths); err != nil {
				return fmt.Errorf("commit: %w", err)
			}
			fmt.Fprintln(out, "  ✓ Committed on branch chore/observo-init")
		}
	}

	// 11) Next-step hint.
	fmt.Fprintf(out, `
Next:
  1. Make sure the Observo plan %q exists in the dashboard:
     https://app.observoai.co/plans
  2. Install the Observo GitHub App on your org (if not already):
     https://github.com/apps/observo → /install/complete
  3. Push the branch and open a PR.
`, planKey)
	return nil
}

// --- prompts ----------------------------------------------------------------

func promptDefault(in io.Reader, out io.Writer, prompt, def string) string {
	fmt.Fprintf(out, "  %s ", prompt)
	r := bufio.NewReader(in)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func promptYesNo(in io.Reader, out io.Writer, prompt string, defaultYes bool) bool {
	hint := "[Y/n]"
	if !defaultYes {
		hint = "[y/N]"
	}
	fmt.Fprintf(out, "  %s %s ", prompt, hint)
	r := bufio.NewReader(in)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
}

// --- side-effects -----------------------------------------------------------

// runShellIn runs a shell command with explicit cwd. Used for npm install
// which must target the package.json directory (the playwright project
// root, which in monorepos is NOT process cwd). Pass "" for `dir` to inherit
// process cwd.
func runShellIn(cmd *cobra.Command, dir string, name string, args ...string) error {
	c := exec.Command(name, args...)
	if dir != "" {
		c.Dir = dir
	}
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	return c.Run()
}

// resolveGitRoot returns the absolute path of the git working-tree root by
// running `git -C cwd rev-parse --show-toplevel`. Returns an error if cwd
// is not inside a git checkout — caller already requires the repo, so this
// is a sanity confirmation rather than a hot path.
func resolveGitRoot(cwd string) (string, error) {
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func commitChanges(cmd *cobra.Command, cwd string, addPaths []string) error {
	// Filter missing paths FIRST so we never switch the user's branch
	// (via `git checkout -B`) only to discover there's nothing to stage.
	// Earlier order swapped: an empty `existing` after a missing-files
	// pass left the user on chore/observo-init with the only feedback
	// being a "no paths" error message.
	var existing []string
	for _, p := range addPaths {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, abs)
		}
		if _, err := os.Stat(abs); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "  ⚠ %s missing: %v (skipped)\n", p, err)
			continue
		}
		existing = append(existing, p)
	}
	if len(existing) == 0 {
		return fmt.Errorf("no paths to commit (all candidate files missing on disk)")
	}
	if err := runGit(cmd, cwd, "checkout", "-B", "chore/observo-init"); err != nil {
		return fmt.Errorf("git checkout: %w", err)
	}
	// `git commit --only -- <paths>` commits ONLY the listed paths even if
	// the user had other unrelated changes pre-staged (`git add` before
	// running `observo init`). Without --only those foreign staged changes
	// would silently land in the chore/observo-init branch.
	args := append([]string{"commit", "--only", "-m", "chore: integrate Observo CI (observo init)", "--"}, existing...)
	if err := runGit(cmd, cwd, args...); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}

func runGit(cmd *cobra.Command, cwd string, args ...string) error {
	full := append([]string{"-C", cwd}, args...)
	c := exec.Command("git", full...)
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	return c.Run()
}

// --- helpers ----------------------------------------------------------------

func relTo(cwd, abs string) string {
	if r, err := filepath.Rel(cwd, abs); err == nil {
		return r
	}
	return abs
}
