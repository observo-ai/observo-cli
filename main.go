// Package main is the entrypoint for the `observo` CLI.
//
// v0.2.0 (OB-B) replaces v0.1.0's stub switch-on-os.Args with cobra +
// internal/api HTTP client foundation. The wire surface (run create,
// run case set, run attach, run pipeline-layer set, plan resolve)
// lands per-subcommand in OB-C..F.
package main

import (
	"os"

	"github.com/observo-ai/observo-cli/cmd"
)

// Build-info vars, set by GoReleaser via -ldflags on release builds.
// We forward them into cmd/ so root.go owns the user-visible
// formatting in one place.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// SetBuildInfo updates the package vars AND rootCmd.Version so cobra's
	// --version flag prints the ldflags-injected value, not the "dev" stub.
	cmd.SetBuildInfo(version, commit, date)
	os.Exit(cmd.Execute())
}
