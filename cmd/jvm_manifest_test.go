package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetJVMFlags clears the jvm-manifest package-level flags between runs
// (cobra retains StringVar values across Execute calls).
func resetJVMFlags() {
	jmFrom, jmTestNG, jmFramework, jmOut = "", "", "auto", "observo-link-manifest.json"
}

// writeAllureDir creates an Allure results dir with two tests: one tagged
// (JUnit5 @Tag) and one untracked.
func writeAllureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"a-result.json": `{"name":"health","fullName":"api.HealthTest.health","status":"passed",
			"start":0,"stop":10,
			"labels":[{"name":"tag","value":"observo:PD-200"},{"name":"feature","value":"Platform"},
			{"name":"testClass","value":"api.HealthTest"},{"name":"testMethod","value":"health"}]}`,
		"b-result.json": `{"name":"probe","fullName":"api.HealthTest.probe","status":"passed",
			"start":0,"stop":5,
			"labels":[{"name":"testClass","value":"api.HealthTest"},{"name":"testMethod","value":"probe"}]}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestJVMManifest_WritesFileWithTrackedAndUntracked(t *testing.T) {
	resetJVMFlags()
	allure := writeAllureDir(t)
	outPath := filepath.Join(t.TempDir(), "observo-link-manifest.json")

	out, err := executeRoot(t, "jvm", "manifest", "--from", allure, "-o", outPath)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 linked, 1 untracked") {
		t.Errorf("summary missing counts, got: %q", out)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Version int `json:"version"`
		Entries []struct {
			Code   *string `json:"code"`
			FQName string  `json:"fq_name"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("manifest not valid JSON: %v\n%s", err, data)
	}
	if m.Version != 1 || len(m.Entries) != 2 {
		t.Fatalf("want version 1 + 2 entries, got version %d / %d entries", m.Version, len(m.Entries))
	}
	// AC4: untracked entry serialized with code:null, present in file.
	if !strings.Contains(string(data), `"code": null`) {
		t.Errorf("untracked entry must serialize code:null:\n%s", data)
	}
}

func TestJVMManifest_DuplicateTagFailsLoud(t *testing.T) {
	resetJVMFlags()
	dir := t.TempDir()
	// Two tests, same observo:PD-1 tag → must fail with both locations.
	for _, f := range []struct{ name, fq string }{
		{"one-result.json", "a.A.one"},
		{"two-result.json", "b.B.two"},
	} {
		body := `{"name":"x","fullName":"` + f.fq + `","status":"passed",
			"labels":[{"name":"tag","value":"observo:PD-1"}]}`
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outPath := filepath.Join(t.TempDir(), "m.json")
	out, err := executeRoot(t, "jvm", "manifest", "--from", dir, "-o", outPath)
	if err == nil {
		t.Fatalf("expected duplicate-tag failure, got success:\n%s", out)
	}
	msg := err.Error()
	for _, want := range []string{"PD-1", "a.A#one", "b.B#two"} {
		if !strings.Contains(msg, want) {
			t.Errorf("duplicate error missing %q: %v", want, msg)
		}
	}
	// Must not have written a manifest on failure.
	if _, statErr := os.Stat(outPath); statErr == nil {
		t.Errorf("manifest should not be written when duplicate detection fails")
	}
}

func TestJVMManifest_NoSourceErrors(t *testing.T) {
	resetJVMFlags()
	out, err := executeRoot(t, "jvm", "manifest")
	if err == nil {
		t.Fatalf("expected error with no source, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "no report source") {
		t.Errorf("unexpected error: %v", err)
	}
}
