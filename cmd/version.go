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
	// Cobra only registers the `--version` flag when rootCmd.Version is
	// non-empty. Seed it from the package var (defaults to "dev") so the
	// flag exists from program start. main.go's SetBuildInfo() overwrites
	// rootCmd.Version with the ldflags value before Execute() runs, so
	// release builds print the real version, not "dev".
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate("{{.Version}}\n")
}

// SetBuildInfo updates the build-info package vars + propagates Version
// onto rootCmd so cobra's --version flag prints the right value. main.go
// must call this BEFORE Execute(); test code may also use it to assert
// release-build behaviour deterministically.
//
// Pre-fix: rootCmd.Version was assigned in init() from the package var
// Version="dev". main.go's later `cmd.Version = ldflagsVersion` updated
// the package var but NOT rootCmd.Version — so brew/npm release builds
// printed "dev" on `observo --version`. The `observo version`
// subcommand was fine because it read the package var at run time.
func SetBuildInfo(version, commit, date string) {
	Version = version
	Commit = commit
	Date = date
	rootCmd.Version = version
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
