package initialize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRemoteURL_HTTPS(t *testing.T) {
	r, err := parseRemoteURL("https://github.com/observo-ai/example-app.git")
	if err != nil {
		t.Fatalf("parse https: %v", err)
	}
	if r.Host != "github.com" || r.Owner != "observo-ai" || r.Name != "example-app" {
		t.Errorf("unexpected: %+v", r)
	}
	if r.DefaultPlan != "EXAMPLE-APP-E2E" {
		t.Errorf("plan default: %s", r.DefaultPlan)
	}
}

func TestParseRemoteURL_SSH(t *testing.T) {
	r, err := parseRemoteURL("git@github.com:observo-ai/example-app.git")
	if err != nil {
		t.Fatalf("parse ssh: %v", err)
	}
	if r.Host != "github.com" || r.Owner != "observo-ai" || r.Name != "example-app" {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestParseRemoteURL_GitLab(t *testing.T) {
	r, err := parseRemoteURL("git@gitlab.com:acme/foo.git")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Host != "gitlab.com" || r.Owner != "acme" || r.Name != "foo" {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestParseRemoteURL_SelfHostedGitLab(t *testing.T) {
	r, err := parseRemoteURL("git@gitlab.acme.internal:team/svc.git")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Host != "gitlab.acme.internal" {
		t.Errorf("host: %s", r.Host)
	}
}

func TestParseRemoteURL_Rejects(t *testing.T) {
	cases := []string{"", "not a url", "https://github.com/", "git@github.com"}
	for _, c := range cases {
		if _, err := parseRemoteURL(c); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestDerivePlanName(t *testing.T) {
	cases := map[string]string{
		"example-app":           "EXAMPLE-APP-E2E",
		"foo":                   "FOO-E2E",
		"weird.name_123":        "WEIRD-NAME_123-E2E",
		"already-e2e":           "ALREADY-E2E",
		"":                      "E2E",
	}
	for in, want := range cases {
		if got := derivePlanName(in); got != want {
			t.Errorf("derivePlanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFindPlaywrightConfig(t *testing.T) {
	dir := t.TempDir()
	// Monorepo layout — config under web-portal/.
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "web-portal", "tests", "e2e"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "web-portal", "playwright.config.ts"), []byte("export default {}"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "web-portal", "tests", "e2e", "login.spec.ts"), []byte("test('x', () => {})"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "web-portal", "tests", "e2e", "billing.spec.ts"), []byte("test('y', () => {})"), 0o644))
	must(os.MkdirAll(filepath.Join(dir, "node_modules", "junk"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "node_modules", "junk", "ignored.spec.ts"), []byte(""), 0o644))

	cfg, err := FindPlaywrightConfig(dir)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if cfg.ConfigPath != filepath.Join("web-portal", "playwright.config.ts") {
		t.Errorf("config path: %s", cfg.ConfigPath)
	}
	if len(cfg.SpecFiles) != 2 {
		t.Errorf("expected 2 spec files (skipping node_modules), got %d: %v", len(cfg.SpecFiles), cfg.SpecFiles)
	}
}

func TestFindPlaywrightConfig_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := FindPlaywrightConfig(dir)
	if err == nil {
		t.Fatal("expected error when no config exists")
	}
}

func TestPatchPlaywrightConfig_ArrayForm(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "playwright.config.ts")
	original := `import { defineConfig } from '@playwright/test';
export default defineConfig({
  reporter: [
    ['list'],
    ['html', { open: 'never' }],
  ],
});
`
	if err := os.WriteFile(cfg, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := PatchPlaywrightConfig(cfg)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected change, got %s", res.Reason)
	}
	if !strings.Contains(res.NewContent, "@observo/playwright-reporter") {
		t.Errorf("reporter not added:\n%s", res.NewContent)
	}
	// Idempotency: re-patch should be a no-op.
	if err := os.WriteFile(cfg, []byte(res.NewContent), 0o644); err != nil {
		t.Fatal(err)
	}
	res2, _ := PatchPlaywrightConfig(cfg)
	if res2.Changed {
		t.Errorf("re-patch should be no-op:\n%s", res2.NewContent)
	}
}

func TestPatchPlaywrightConfig_NonArrayForm(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "playwright.config.ts")
	original := `export default defineConfig({ reporter: 'list' });`
	if err := os.WriteFile(cfg, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := PatchPlaywrightConfig(cfg)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if res.Changed {
		t.Errorf("should NOT patch single-string form, expected manual instruction in Reason")
	}
	if !strings.Contains(res.Reason, "reporter array not found") {
		t.Errorf("Reason should explain: %s", res.Reason)
	}
}

func TestRenderWorkflow(t *testing.T) {
	out := RenderWorkflow(WorkflowSpec{PlanKey: "MY-PLAN-E2E"})
	// plan: '...' (quoted) — single-quote wrapping guards against YAML scalar
	// ambiguity for any plan_key, even though derivePlanName sanitizes the
	// auto-derived form.
	for _, want := range []string{"observo-ai/setup@v1", "plan: 'MY-PLAN-E2E'", "id-token: write", "node-version: '20'"} {
		if !strings.Contains(out, want) {
			t.Errorf("workflow missing %q in:\n%s", want, out)
		}
	}
}

func TestValidatePlanKey(t *testing.T) {
	good := []string{"E2E", "EXAMPLE-APP-E2E", "REGRESSION_2026_05", "X", "0", "ABC123"}
	for _, k := range good {
		if err := ValidatePlanKey(k); err != nil {
			t.Errorf("ValidatePlanKey(%q) = %v, want nil", k, err)
		}
	}
	bad := []string{
		"",                  // empty
		"lowercase",         // lowercase not allowed (would survive but PRD says uppercase)
		"with space",        // space
		"with:colon",        // YAML mapping ambiguity
		"with'quote",        // would break single-quoted YAML embed
		"*alias",            // YAML alias syntax
		"&anchor",           // YAML anchor syntax
		"-leading-dash",     // starts with dash → flag confusion in shell
		"plan/with/slash",   // path-like
	}
	for _, k := range bad {
		if err := ValidatePlanKey(k); err == nil {
			t.Errorf("ValidatePlanKey(%q) = nil, want error", k)
		}
	}
}

func TestPatchPlaywrightConfig_SpreadIdiom(t *testing.T) {
	// Make sure the patched output uses the spread idiom (no bare `null`)
	// so pre-Playwright-1.38 versions don't choke on a null reporter entry
	// during local runs.
	dir := t.TempDir()
	cfg := filepath.Join(dir, "playwright.config.ts")
	original := `export default defineConfig({
  reporter: [
    ['list'],
  ],
});
`
	if err := os.WriteFile(cfg, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := PatchPlaywrightConfig(cfg)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected change")
	}
	if strings.Contains(res.NewContent, ": null,") {
		t.Errorf("patched content still contains bare 'null' entry — pre-1.38 Playwright would throw:\n%s", res.NewContent)
	}
	if !strings.Contains(res.NewContent, "...(process.env.CI") {
		t.Errorf("patched content missing spread idiom:\n%s", res.NewContent)
	}
}

func TestPatchPlaywrightConfig_HintHasNoDuplicateReporter(t *testing.T) {
	// Single-string reporter form → we return a Reason with a manual snippet.
	// Snippet must mention @observo/playwright-reporter exactly ONCE (earlier
	// version concatenated the entry constant with another literal copy).
	dir := t.TempDir()
	cfg := filepath.Join(dir, "playwright.config.ts")
	if err := os.WriteFile(cfg, []byte(`export default defineConfig({ reporter: 'list' });`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := PatchPlaywrightConfig(cfg)
	count := strings.Count(res.Reason, "@observo/playwright-reporter")
	if count != 1 {
		t.Errorf("Reason mentions @observo/playwright-reporter %d times, want exactly 1:\n%s", count, res.Reason)
	}
}

func TestWriteWorkflow_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, ".github", "workflows", "observo.yml")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("pre-existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteWorkflow(dest, "new content")
	if err == nil {
		t.Fatal("expected refuse-overwrite error")
	}
	// Existing content untouched.
	got, _ := os.ReadFile(dest)
	if string(got) != "pre-existing" {
		t.Errorf("file was clobbered: %s", got)
	}
}

func TestDetectExistingPlaywrightWorkflow(t *testing.T) {
	dir := t.TempDir()
	wfDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	must := func(name, body string) {
		if err := os.WriteFile(filepath.Join(wfDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("lint.yml", "name: Lint\non: pull_request\njobs:\n  lint:\n    runs-on: ubuntu-latest")
	must("e2e.yml", "name: E2E\non: push\njobs:\n  test:\n    steps:\n      - run: npx playwright test")

	got, err := DetectExistingPlaywrightWorkflow(dir)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !strings.HasSuffix(got, "e2e.yml") {
		t.Errorf("expected e2e.yml, got %s", got)
	}
}
