package cmd

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// versionCmd preserves the v0.1.0 stub binary's UX (observo --version,
// observo version) so anyone relying on the output format from v0.1.0
// doesn't break.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print observo version, commit, build date",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return writeVersion(cmd.OutOrStdout())
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
	// Also wire --version on the root cmd so `observo --version` works
	// without a subcommand (idiomatic for CLIs; matches the v0.1.0 stub).
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate("{{.Version}}\n")
}

// writeVersion emits the multi-line build-info block. Factored out so the
// version subcommand and a future `--version-verbose` flag share format.
func writeVersion(w io.Writer) error {
	v := Version
	if v == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
	}
	_, err := fmt.Fprintf(w,
		"observo %s\n  commit: %s\n  built:  %s\n  go:     %s\n  os:     %s/%s\n",
		v, Commit, Date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return err
}
