package cmd

import (
	"github.com/spf13/cobra"
)

// runCmd is a pure container for the `run *` subcommand tree.
// Doesn't do anything by itself — invoking `observo run` prints help.
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Manage Observo TestRuns (create / finish / case / attach / pipeline-layer)",
	Long: `Operations on Observo TestRuns. Typically called by CI pipelines:
  observo run create --project OB --plan REGR-MAIN-CI    # at start of pipeline
  observo run pipeline-layer set ... --run-id $RUN_ID    # per-layer (OB-D, planned)
  observo run case set ... --run-id $RUN_ID              # per-case live (OB-E, planned)
  observo run finish --run-id $RUN_ID --status auto      # at end of pipeline`,
}

func init() {
	rootCmd.AddCommand(runCmd)
}
