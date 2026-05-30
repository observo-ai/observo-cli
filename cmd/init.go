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
	if err := initialize.ValidatePlanKey(planKey); err != nil {
		return fmt.Errorf("invalid plan key: %w", err)
	}
	fmt.Fprintf(out, "✓ Plan key: %s\n", planKey)

	// 4) Detect existing Playwright workflow + warn the customer.
	if existing, _ := initialize.DetectExistingPlaywrightWorkflow(cwd); existing != "" {
		fmt.Fprintf(out, "ⓘ Note: %s already runs Playwright. 'observo init' will create a SECOND workflow.\n"+
			"  Consider patching the existing one instead — see https://observo.ai/docs/connect-github-actions\n",
			existing)
	}

	// 5) Plan the patch to playwright.config.ts.
	patch, err := initialize.PatchPlaywrightConfig(filepath.Join(cwd, pwCfg.ConfigPath))
	if err != nil {
		return fmt.Errorf("read playwright config: %w", err)
	}
	if patch.Changed {
		fmt.Fprintf(out, "✓ Will patch %s (adds @observo/playwright-reporter line)\n", pwCfg.ConfigPath)
	} else {
		fmt.Fprintf(out, "ⓘ %s — no patch needed: %s\n", pwCfg.ConfigPath, patch.Reason)
	}

	// 6) Plan the workflow file.
	wfDest := initialize.WorkflowFileTarget(cwd)
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
		if err := runShell(cmd, "npm", "install", "-D", "@observo/playwright-reporter"); err != nil {
			return fmt.Errorf("npm install: %w", err)
		}
		fmt.Fprintln(out, "  ✓ Installed @observo/playwright-reporter")
	}
	if !initFlagNoCommit {
		// Build the path list from what we actually changed — hardcoding
		// "playwright.config.ts" breaks for monorepos where the config lives
		// in a subdirectory like web-portal/playwright.config.ts, because
		// `git add <missing-path>` errors and was previously silently swallowed.
		var addPaths []string
		if patch.Changed {
			addPaths = append(addPaths, pwCfg.ConfigPath)
		}
		if wfWillWrite {
			addPaths = append(addPaths, relTo(cwd, wfDest))
		}
		if !initFlagNoInstall {
			addPaths = append(addPaths, "package.json", "package-lock.json")
		}
		if err := commitChanges(cmd, cwd, addPaths); err != nil {
			return fmt.Errorf("commit: %w", err)
		}
		fmt.Fprintln(out, "  ✓ Committed on branch chore/observo-init")
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

func runShell(cmd *cobra.Command, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	return c.Run()
}

func commitChanges(cmd *cobra.Command, cwd string, addPaths []string) error {
	// Branch checkout + commit are fatal; `git add` of an optional path
	// (package-lock.json may not exist if npm install was skipped) is
	// tolerated so a missing companion file doesn't abort the whole step.
	if err := runGit(cmd, cwd, "checkout", "-B", "chore/observo-init"); err != nil {
		return fmt.Errorf("git checkout: %w", err)
	}
	for _, p := range addPaths {
		if err := runGit(cmd, cwd, "add", p); err != nil {
			// Non-fatal: log to stderr but keep going. Common case is
			// package-lock.json absent when --no-install was used; the
			// other intended files still get staged.
			fmt.Fprintf(cmd.ErrOrStderr(), "  ⚠ git add %s: %v (skipped)\n", p, err)
		}
	}
	if err := runGit(cmd, cwd, "commit", "-m", "chore: integrate Observo CI (observo init)"); err != nil {
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
