package cmd

import "github.com/spf13/cobra"

// jvmCmd is a pure container for the `jvm *` subcommand tree — the
// Kotlin/JVM ↔ Observo bridge (OB-542). Invoking `observo jvm` prints help.
var jvmCmd = &cobra.Command{
	Use:   "jvm",
	Short: "Bridge JVM test suites (Kotlin/Java · TestNG/JUnit5 · Allure) to Observo",
	Long: `Link JVM tests to Observo cases through the canonical observo:<code>
join key, expressed natively per framework — no Observo-specific
annotation, no new test-runtime dependency:

  JUnit5   @Tag("observo:PD-101")
  TestNG   @Test(groups = ["observo:PD-101"])
  Allure   @TmsLink("PD-101")                 (fallback join)

Subcommands (this release ships 'manifest'; import/stub/push follow in
OB-544 / OB-549 / OB-546):
  observo jvm manifest    build observo-link-manifest.json from run reports`,
}

func init() {
	rootCmd.AddCommand(jvmCmd)
}
