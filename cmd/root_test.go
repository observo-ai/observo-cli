package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// executeRoot runs the root cobra cmd with the given args and returns
// the captured stdout + error. Resets persistent flags between calls so
// tests don't pollute each other.
func executeRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	// Reset flag state — cobra holds values across Execute calls.
	flagAPIKey = ""
	flagBaseURL = ""
	flagJSON = false
	flagVerbose = false

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return buf.String(), err
}

func TestRootHelpListsSubcommands(t *testing.T) {
	out, err := executeRoot(t, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{"observo", "version", "--api-key", "--json", "--verbose"} {
		if !strings.Contains(out, want) {
			t.Errorf("--help missing %q in:\n%s", want, out)
		}
	}
}

func TestVersionSubcommandEmitsBuildInfo(t *testing.T) {
	out, err := executeRoot(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	// First line must start with "observo "; subsequent lines carry
	// commit/built/go/os fields. Don't assert exact version (it's "dev"
	// in tests).
	if !strings.HasPrefix(out, "observo ") {
		t.Errorf("version output: %q", out)
	}
	for _, want := range []string{"commit:", "built:", "go:", "os:"} {
		if !strings.Contains(out, want) {
			t.Errorf("version output missing %q in:\n%s", want, out)
		}
	}
}

func TestVersionFlagOnRootShortcuts(t *testing.T) {
	// `observo --version` should print just the version number (cobra's
	// default --version template). We override to "{{.Version}}\n" in
	// version.go init.
	out, err := executeRoot(t, "--version")
	if err != nil {
		t.Fatalf("--version: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Errorf("--version emitted nothing")
	}
}

func TestPersistentFlagsParsedAtRootLevel(t *testing.T) {
	_, err := executeRoot(t, "--api-key", "kkk", "--base-url", "https://example.com", "--json", "--verbose", "version")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if flagAPIKey != "kkk" {
		t.Errorf("flagAPIKey: got %q", flagAPIKey)
	}
	if flagBaseURL != "https://example.com" {
		t.Errorf("flagBaseURL: got %q", flagBaseURL)
	}
	if !flagJSON {
		t.Errorf("flagJSON: got false")
	}
	if !flagVerbose {
		t.Errorf("flagVerbose: got false")
	}
}

func TestUnknownSubcommandReturnsError(t *testing.T) {
	_, err := executeRoot(t, "nope-not-a-real-cmd")
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}
