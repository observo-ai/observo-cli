package cmd

import (
	"fmt"
	"os"

	"github.com/observo-ai/observo-cli/internal/jvm"
	"github.com/observo-ai/observo-cli/internal/output"

	"github.com/spf13/cobra"
)

// Flags for `observo jvm manifest …`, kept as package-level vars per the
// existing cmd/* convention.
var (
	jmFrom      string
	jmTestNG    string
	jmFramework string
	jmOut       string
)

var jvmManifestCmd = &cobra.Command{
	Use:   "manifest",
	Short: "Build observo-link-manifest.json from JVM run reports",
	Long: `Build the observo-link-manifest.json artifact — the single source of
truth that links each JVM test to an Observo case, consumed by
'observo jvm import/stub/push' and the coverage verdict.

Two report sources (at least one required):
  --from            Allure results dir (feature/story/description/display,
                    plus @Tag join for JUnit5 and @TmsLink fallback join)
  --testng-results  testng-results.xml (the @Test(groups=…) join source for
                    TestNG suites; allure-testng does NOT emit groups)

When both are given they are merged: TestNG owns the join + result +
chain/order, Allure fills in the human metadata. This is the pilot
client's shape (TestNG + Allure).

Untracked tests (no join key) are kept with "code": null — visible as
untracked, never silently dropped. Two tests sharing one code fail loud.

Examples:
  # TestNG + Allure client (typical)
  observo jvm manifest --from build/allure-results \
      --testng-results build/testng-results.xml -o observo-link-manifest.json

  # Allure-only (JUnit5 @Tag or @TmsLink)
  observo jvm manifest --from build/allure-results

  # Inspect on stdout
  observo jvm manifest --from build/allure-results -o -`,
	Args: cobra.NoArgs,
	RunE: jvmManifestExec,
}

func init() {
	jvmCmd.AddCommand(jvmManifestCmd)
	f := jvmManifestCmd.Flags()
	f.StringVar(&jmFrom, "from", "", "Allure results directory (*-result.json)")
	f.StringVar(&jmTestNG, "testng-results", "", "path to testng-results.xml (join source for TestNG groups)")
	f.StringVar(&jmFramework, "framework", "auto", "testng | junit5 | playwright | auto")
	f.StringVarP(&jmOut, "out", "o", "observo-link-manifest.json", "output path ('-' for stdout)")
}

// manifestSummary is the `--json` shape, useful for CI assertions.
type manifestSummary struct {
	Output    string `json:"output"`
	Framework string `json:"framework,omitempty"`
	Total     int    `json:"total"`
	Tracked   int    `json:"tracked"`
	Untracked int    `json:"untracked"`
}

func jvmManifestExec(cmd *cobra.Command, args []string) error {
	m, err := jvm.BuildManifest(jvm.BuildOptions{
		AllureDir:     jmFrom,
		TestNGResults: jmTestNG,
		Framework:     jmFramework,
	})
	if err != nil {
		return err
	}

	// Write the manifest. stdout ("-") keeps the JSON on stdout and pushes
	// the summary to stderr so a piped manifest stays clean.
	toStdout := jmOut == "-"
	out := os.Stdout
	if !toStdout {
		out, err = os.Create(jmOut)
		if err != nil {
			return fmt.Errorf("create %s: %w", jmOut, err)
		}
		defer out.Close()
	}
	if err := m.Emit(out); err != nil {
		return err
	}

	tracked := 0
	for _, e := range m.Entries {
		if e.Tracked() {
			tracked++
		}
	}
	sum := manifestSummary{
		Output:    jmOut,
		Framework: m.Framework,
		Total:     len(m.Entries),
		Tracked:   tracked,
		Untracked: len(m.Entries) - tracked,
	}

	p := output.New(flagJSON)
	if toStdout {
		// Manifest already occupies stdout; route the summary to stderr.
		p.Out = cmd.ErrOrStderr()
	} else {
		p.Out = cmd.OutOrStdout()
	}
	return p.Result(summaryToMapManifest(sum), summaryHumanManifest(sum))
}

func summaryToMapManifest(s manifestSummary) map[string]any {
	return map[string]any{
		"output":    s.Output,
		"framework": s.Framework,
		"total":     s.Total,
		"tracked":   s.Tracked,
		"untracked": s.Untracked,
	}
}

func summaryHumanManifest(s manifestSummary) string {
	return fmt.Sprintf("wrote %s (%s): %d tests — %d linked, %d untracked",
		s.Output, s.Framework, s.Total, s.Tracked, s.Untracked)
}
