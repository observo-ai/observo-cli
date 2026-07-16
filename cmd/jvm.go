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

Subcommands:
  observo jvm manifest    build observo-link-manifest.json from run reports
  observo jvm import      create/upsert Observo cases & suites from a run
  observo jvm stub        generate Kotlin test skeletons from Observo cases
  observo jvm push        write run results + HTTP evidence back to a run

New suite (write tests against cases that already exist):
  observo jvm stub --cases PD-201,PD-202 --out src/test/kotlin/api/pd
  ./gradlew test
  observo jvm push --from allure-results --plan REGR-MAIN-CI

Existing suite (bring the code into Observo first):
  observo jvm import --from allure-results --project PD           # preview
  observo jvm import --from allure-results --project PD --apply

TestNG note: the groups join lives in testng-results.xml, which Allure does
not carry — pass --testng-results alongside --from, and enable TestNG's
default listeners so the file is written at all:

  useTestNG { useDefaultListeners(true) }`,
}

func init() {
	rootCmd.AddCommand(jvmCmd)
}
