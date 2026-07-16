package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/observo-ai/observo-cli/internal/api"
	"github.com/observo-ai/observo-cli/internal/config"
	"github.com/observo-ai/observo-cli/internal/jvm"
	"github.com/observo-ai/observo-cli/internal/output"

	"github.com/spf13/cobra"
)

var (
	jsCases     string
	jsOut       string
	jsFramework string
	jsPackage   string
	jsForce     bool
	jsDryRun    bool
)

var jvmStubCmd = &cobra.Command{
	Use:   "stub --cases PD-101,PD-102 --out src/test/kotlin/api/pd",
	Short: "Generate Kotlin test skeletons from Observo cases (reverse of import)",
	Long: `Fetch Observo cases by short code and generate compilable Kotlin test
skeletons with the observo:<code> join tag and Allure metadata already
wired — the developer fills in only the body.

Cases sharing an Observo suite are grouped into one class (suite → class,
case → @Test method, suite name → @Feature). The join tag is native per
framework: TestNG @Test(groups=[…]), JUnit5 @Test + @Tag(…).

The Kotlin package is taken from --package, else inferred from the --out
path (the segment after a kotlin/ or java/ source root).

Existing files are never overwritten without --force.

Examples:
  observo jvm stub --cases PD-201,PD-202 --out src/test/kotlin/api/pd --framework testng
  observo jvm stub --cases PD-9 --out gen --package api.gen --dry-run`,
	Args: cobra.NoArgs,
	RunE: jvmStubExec,
}

func init() {
	jvmCmd.AddCommand(jvmStubCmd)
	f := jvmStubCmd.Flags()
	f.StringVar(&jsCases, "cases", "", "comma-separated Observo short codes (e.g. PD-101,PD-102)")
	f.StringVar(&jsOut, "out", "", "output directory for generated .kt files")
	f.StringVar(&jsFramework, "framework", jvm.FrameworkTestNG, "testng | junit5")
	f.StringVar(&jsPackage, "package", "", "Kotlin package (default: inferred from --out)")
	f.BoolVar(&jsForce, "force", false, "overwrite existing files")
	f.BoolVar(&jsDryRun, "dry-run", false, "print what would be generated; write nothing")
}

// stubSummary is the --json shape.
type stubSummary struct {
	Framework    string   `json:"framework"`
	Package      string   `json:"package,omitempty"`
	Cases        int      `json:"cases"`
	FilesWritten []string `json:"files_written"`
	FilesSkipped []string `json:"files_skipped"`
	DryRun       bool     `json:"dry_run,omitempty"`
}

func jvmStubExec(cmd *cobra.Command, args []string) error {
	codes := splitCodes(jsCases)
	if len(codes) == 0 {
		return fmt.Errorf("--cases is required (comma-separated short codes)")
	}
	if jsOut == "" {
		return fmt.Errorf("--out is required")
	}
	if jsFramework != jvm.FrameworkTestNG && jsFramework != jvm.FrameworkJUnit5 {
		return fmt.Errorf("--framework %q not supported (want %s|%s)", jsFramework, jvm.FrameworkTestNG, jvm.FrameworkJUnit5)
	}

	pkg := jsPackage
	if pkg == "" {
		pkg = inferPackage(jsOut)
	}

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

	ctx := context.Background()
	classes, err := buildStubClasses(ctx, client, codes, pkg, cmd.ErrOrStderr())
	if err != nil {
		return err
	}

	// --dry-run must not touch the filesystem — guard the mkdir itself,
	// not just its error (the WriteFile below is guarded the same way).
	if !jsDryRun {
		if err := os.MkdirAll(jsOut, 0o755); err != nil {
			return fmt.Errorf("create out dir %s: %w", jsOut, err)
		}
	}

	stderr := cmd.ErrOrStderr()
	summary := stubSummary{Framework: jsFramework, Package: pkg, Cases: len(codes), DryRun: jsDryRun}
	for _, cls := range classes {
		content, err := jvm.RenderKotlin(cls, jsFramework)
		if err != nil {
			return fmt.Errorf("render %s: %w", cls.ClassName, err)
		}
		path := filepath.Join(jsOut, cls.ClassName+".kt")

		// Overwrite guard (AC3): never clobber an existing file silently.
		if !jsForce {
			if _, statErr := os.Stat(path); statErr == nil {
				fmt.Fprintf(stderr, "skip: %s exists (use --force to overwrite)\n", path)
				summary.FilesSkipped = append(summary.FilesSkipped, path)
				continue
			}
		}

		if jsDryRun {
			fmt.Fprintf(stderr, "would write %s (%d cases)\n", path, len(cls.Cases))
			summary.FilesWritten = append(summary.FilesWritten, path)
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		summary.FilesWritten = append(summary.FilesWritten, path)
	}

	p := output.New(cfg.JSON)
	p.Out = cmd.OutOrStdout()
	return p.Result(stubSummaryToMap(summary), stubSummaryHuman(summary))
}

// buildStubClasses fetches each case, resolves its suite name (cached),
// and groups cases sharing a suite into one class. Suite order follows the
// first case that referenced it, so output is deterministic.
func buildStubClasses(ctx context.Context, client *api.Client, codes []string, pkg string, stderr io.Writer) ([]jvm.StubClass, error) {
	suiteNames := map[string]string{} // suite_id → name (cache)
	bySuite := map[string]*jvm.StubClass{}
	var order []string // suite keys in first-seen order

	for _, code := range codes {
		tc, err := client.GetTestCaseByCode(ctx, code)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", code, err)
		}

		feature := ""
		if tc.SuiteID != "" {
			name, ok := suiteNames[tc.SuiteID]
			if !ok {
				s, sErr := client.GetSuite(ctx, tc.ProjectID, tc.SuiteID)
				if sErr != nil {
					// A missing/inaccessible suite must not sink the whole
					// run — the case still stubs, just without @Feature. But
					// this swallows ANY error (401/500/network), not only
					// not-found, so surface it rather than degrade silently.
					fmt.Fprintf(stderr, "warning: suite %s (case %s) unreadable: %v — stubbing without @Feature\n",
						tc.SuiteID, code, sErr)
					name = ""
				} else {
					name = s.Name
				}
				suiteNames[tc.SuiteID] = name
			}
			feature = name
		}

		key := tc.SuiteID // "" groups all suite-less cases together
		cls, ok := bySuite[key]
		if !ok {
			cls = &jvm.StubClass{
				Package:   pkg,
				ClassName: jvm.ClassNameFromFeature(feature),
				Feature:   feature,
			}
			bySuite[key] = cls
			order = append(order, key)
		}
		cls.Cases = append(cls.Cases, jvm.StubCase{
			Code:        tc.ShortCode,
			Name:        tc.Name,
			Description: tc.Description,
			Layer:       tc.LayerName(),
			Steps:       stepLines(tc.Steps),
		})
	}

	// Disambiguate two distinct suites that map to the same class name
	// (e.g. "PD Wallet" and "PD-Wallet" both → PDWalletTest) so their
	// generated files don't collide and overwrite each other.
	used := map[string]bool{}
	out := make([]jvm.StubClass, 0, len(order))
	for _, k := range order {
		cls := *bySuite[k] // copy out of the map
		name := cls.ClassName
		base := strings.TrimSuffix(cls.ClassName, "Test")
		for n := 2; used[name]; n++ {
			name = base + fmt.Sprintf("%d", n) + "Test"
			// base can be "" (suite literally named "Test") → "2Test",
			// an invalid leading-digit identifier. Guard it, same as
			// pascalCase does for the initial name.
			if name[0] >= '0' && name[0] <= '9' {
				name = "_" + name
			}
		}
		used[name] = true
		cls.ClassName = name
		out = append(out, cls)
	}
	return out, nil
}

// stepLines renders case steps into single-line descriptions ("action →
// expected"), skipping empty steps.
func stepLines(steps []api.TestCaseStep) []string {
	var out []string
	for _, s := range steps {
		parts := []string{}
		if s.Action != "" {
			parts = append(parts, s.Action)
		}
		if s.Expected != "" {
			parts = append(parts, "→ "+s.Expected)
		}
		line := strings.Join(parts, " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// splitCodes parses a comma-separated code list, trimming blanks and
// dropping duplicates (first occurrence wins). Deduping here means a
// repeated `--cases PD-1,PD-1` doesn't emit two @Test methods bound to the
// same observo:<code> join tag.
func splitCodes(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(s, ",") {
		c := strings.TrimSpace(p)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

// inferPackage derives a Kotlin package from an output path by taking the
// path segments after a "kotlin" or "java" source-root segment. Returns ""
// when no such root is present (caller emits a package-less file).
func inferPackage(outDir string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(outDir)), "/")
	// Match the LAST kotlin/java segment — that's the source root. Matching
	// the first would mis-parse paths like /opt/java/proj/src/main/kotlin/…
	// into "proj.src.main.kotlin.…".
	for i := len(parts) - 1; i >= 0; i-- {
		if (parts[i] == "kotlin" || parts[i] == "java") && i+1 < len(parts) {
			return strings.Join(parts[i+1:], ".")
		}
	}
	return ""
}

func stubSummaryToMap(s stubSummary) map[string]any {
	return map[string]any{
		"framework":     s.Framework,
		"package":       s.Package,
		"cases":         s.Cases,
		"files_written": s.FilesWritten,
		"files_skipped": s.FilesSkipped,
		"dry_run":       s.DryRun,
	}
}

func stubSummaryHuman(s stubSummary) string {
	prefix := "stubbed"
	if s.DryRun {
		prefix = "dry-run: would stub"
	}
	return fmt.Sprintf("%s %d cases → %d file(s) written, %d skipped (framework=%s)",
		prefix, s.Cases, len(s.FilesWritten), len(s.FilesSkipped), s.Framework)
}
