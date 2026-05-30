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

func TestFindPlaywrightConfig_DotPrefixedDirectoriesAreScanned(t *testing.T) {
	// Regression: the walk used to skip ALL dot-prefixed dirs ("HasPrefix .")
	// which excluded `.apps/web/` and similar legitimate convention dirs.
	// Now only an explicit allowlist is skipped — playwright.config.ts under
	// a non-noise dot-dir must be found.
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, ".apps", "web"), 0o755))
	must(os.WriteFile(filepath.Join(dir, ".apps", "web", "playwright.config.ts"), []byte("export default {}"), 0o644))
	// Negative control: noise dirs are still skipped.
	must(os.MkdirAll(filepath.Join(dir, ".next", "junk"), 0o755))
	must(os.WriteFile(filepath.Join(dir, ".next", "junk", "playwright.config.ts"), []byte("export default {}"), 0o644))

	cfg, err := FindPlaywrightConfig(dir)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	want := filepath.Join(".apps", "web", "playwright.config.ts")
	if cfg.ConfigPath != want {
		t.Errorf("config = %q, want %q (should descend into .apps/, skip .next/)", cfg.ConfigPath, want)
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
	for _, want := range []string{
		"observo-ai/setup@v1",
		"plan: 'MY-PLAN-E2E'",
		"id-token: write",
		"node-version: '20'",
		// finalize step must fail loud, not silently:
		// - `-f` makes curl exit non-zero on HTTP errors
		// - explicit env-var guard before the call
		// - `|| exit 1` so a 401/5xx isn't swallowed
		"curl -fsS",
		`if [ -z "$OBSERVO_RUN_KEY" ]`,
		"|| exit 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("workflow missing %q in:\n%s", want, out)
		}
	}
}

func TestPatchPlaywrightConfig_CommentedOutReporterIgnored(t *testing.T) {
	// Regression: previously the regex matched the FIRST `reporter:` token in
	// the file, including inside `// ...` comments. The comment-out version
	// would hijack the patch, leave the real array untouched, and on the
	// next run the idempotency check would find @observo/playwright-reporter
	// in the comment body and refuse to do anything.
	dir := t.TempDir()
	cfg := filepath.Join(dir, "playwright.config.ts")
	original := `import { defineConfig } from '@playwright/test';
// reporter: ['list']  // ← old config kept as reference, must not match
/* reporter: [ ['junit'] ] */
export default defineConfig({
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
		t.Fatalf("expected Changed=true, got Reason: %s", res.Reason)
	}
	// The REAL reporter array (inside defineConfig) must have been touched —
	// look for the spread idiom in the area after `defineConfig`.
	idx := strings.Index(res.NewContent, "defineConfig")
	if idx < 0 {
		t.Fatalf("defineConfig missing")
	}
	if !strings.Contains(res.NewContent[idx:], "...(process.env.CI") {
		t.Errorf("real reporter array not patched (spread missing in defineConfig block):\n%s", res.NewContent)
	}
	// Original comments must still be present unchanged — masking is only for
	// search, not output.
	if !strings.Contains(res.NewContent, "// reporter: ['list']") {
		t.Errorf("comment-out line was modified — masking must only affect SEARCH, not OUTPUT")
	}
}

func TestMaskComments(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		want     string
	}{
		{"plain", "abc", "abc"},
		{"single-line comment", "abc // hi", "abc      "},
		{"block comment", "ab /* hi */ cd", "ab          cd"},
		{"block with newlines preserved", "a /* x\ny */ b", "a     \n     b"},
		{"slash inside string", `"a/b/c"`, `"a/b/c"`},
		{"comment marker inside string", `"//not a comment"`, `"//not a comment"`},
		{"backtick string", "`/* not */`", "`/* not */`"},
		{"escaped quote then comment", `"x\"y" // c`, `"x\"y"     `},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := maskComments(c.in)
			if got != c.want {
				t.Errorf("maskComments(%q) = %q, want %q", c.in, got, c.want)
			}
			if len(got) != len(c.in) {
				t.Errorf("length mismatch: %d vs %d (must preserve offsets)", len(got), len(c.in))
			}
		})
	}
}

func TestPatchPlaywrightConfig_MarkerPlacedAtRealReporter(t *testing.T) {
	// Earlier `strings.Index(newContent, "reporter")` matched the FIRST
	// occurrence anywhere — a variable like `const myReporter = ...` or
	// even a comment would shift the audit marker far above the real array.
	// Now we use the keyMatch offset directly.
	dir := t.TempDir()
	cfg := filepath.Join(dir, "playwright.config.ts")
	original := `const myReporter = 'distraction'; // first 'reporter' token in file
export default defineConfig({
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
		t.Fatal("expected Changed=true")
	}
	// Marker must appear AFTER `defineConfig(` (i.e. right above the real
	// reporter line), NOT before `const myReporter`.
	markerIdx := strings.Index(res.NewContent, "@observo:reporter")
	defineIdx := strings.Index(res.NewContent, "defineConfig")
	if markerIdx < 0 {
		t.Fatal("audit marker missing")
	}
	if markerIdx < defineIdx {
		t.Errorf("audit marker placed before defineConfig (= above wrong 'reporter' occurrence):\n%s", res.NewContent)
	}
}

func TestPatchPlaywrightConfig_CommentedBracketDoesNotTruncate(t *testing.T) {
	// Regression: findMatchingBracket previously walked the raw `original`,
	// so a commented-out reporter entry containing a `]` (e.g. the line
	// below) closed the depth count early. The patch output then had a
	// truncated reporter array and broke the surrounding code.
	dir := t.TempDir()
	cfg := filepath.Join(dir, "playwright.config.ts")
	original := `export default defineConfig({
  reporter: [
    ['list'],
    // ['junit', { outputFile: 'old.xml' }],
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
		t.Fatalf("expected change: %s", res.Reason)
	}
	// All three reporter entries must still appear after the patch — html line
	// is what gets dropped if the comment-line `]` closes the array early.
	for _, want := range []string{"['list'],", "['html', { open: 'never' }],", "// ['junit'"} {
		if !strings.Contains(res.NewContent, want) {
			t.Errorf("content missing %q (likely truncated by comment-bracket bug):\n%s", want, res.NewContent)
		}
	}
	// Outer `]` of reporter array + closing `}` of defineConfig must still
	// frame the right region — `}); ` close-up survives intact.
	if !strings.Contains(res.NewContent, "],\n});") {
		t.Errorf("outer reporter close + defineConfig close mangled:\n%s", res.NewContent)
	}
}

func TestPatchPlaywrightConfig_TabIndentPreserved(t *testing.T) {
	// Tab-indented config (Prettier `useTabs: true`) — the audit marker
	// must reuse the same tab indentation, not synthesize spaces. Mixed
	// indentation would trip ESLint `no-mixed-spaces-and-tabs` on the
	// next lint-staged run.
	dir := t.TempDir()
	cfg := filepath.Join(dir, "playwright.config.ts")
	original := "export default defineConfig({\n\treporter: [\n\t\t['list'],\n\t],\n});\n"
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
	// The marker line should begin with a TAB (same as the reporter: line),
	// not a space. Search for the marker and confirm preceding indent.
	mkIdx := strings.Index(res.NewContent, reporterImportLine)
	if mkIdx < 0 {
		t.Fatal("marker missing")
	}
	lineStart := strings.LastIndex(res.NewContent[:mkIdx], "\n") + 1
	indent := res.NewContent[lineStart:mkIdx]
	if !strings.HasPrefix(indent, "\t") {
		t.Errorf("marker indent = %q, want tab-prefixed (matching reporter line)", indent)
	}
	if strings.Contains(indent, " ") {
		t.Errorf("marker indent mixes spaces with tabs: %q", indent)
	}
}

func TestMaskComments_UnterminatedBlock(t *testing.T) {
	// Block comment that runs to EOF — the entire tail must be masked,
	// including the very last byte (earlier off-by-one let the final byte
	// slip through, which could be a `[` the regex would still match).
	in := "code /* never closed [reporter:["
	got := maskComments(in)
	if len(got) != len(in) {
		t.Fatalf("length changed: %d vs %d", len(got), len(in))
	}
	// Everything from the `/*` onward must be spaces.
	tail := got[5:] // after "code "
	for i, c := range tail {
		if c != ' ' {
			t.Errorf("unmasked byte at index %d in tail: %q (full tail: %q)", i, string(c), tail)
		}
	}
}

func TestWriteWorkflow_ErrorMentionsValidRecovery(t *testing.T) {
	// The "file already exists" error must NOT reference --force (no such flag).
	dir := t.TempDir()
	dest := filepath.Join(dir, ".github", "workflows", "observo.yml")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("pre"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteWorkflow(dest, "new")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if strings.Contains(msg, "--force") {
		t.Errorf("error message references non-existent --force flag: %q", msg)
	}
	if !strings.Contains(msg, "remove the file") {
		t.Errorf("error message should mention valid recovery (remove file): %q", msg)
	}
}

func TestWritePatch_PreservesMode(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "playwright.config.ts")
	original := `export default defineConfig({ reporter: [['list']] });`
	if err := os.WriteFile(cfg, []byte(original), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := PatchPlaywrightConfig(cfg)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if err := WritePatch(res); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := info.Mode().Perm()
	if got != 0o755 {
		t.Errorf("mode = %o, want 0755 (original mode must survive atomic rename)", got)
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

func TestPatchPlaywrightConfig_BracketInStringLiteral(t *testing.T) {
	// Regression: lazy regex `[\s\S]*?\]` stopped at the FIRST `]` after
	// the opening — for a config where an option value contains `]`
	// (e.g. outputFolder with `[v2]` in the path), the patched output
	// dropped everything after that inner `]`, producing invalid TS.
	// Bracket-counting parser must skip `]` inside string literals.
	dir := t.TempDir()
	cfg := filepath.Join(dir, "playwright.config.ts")
	original := `import { defineConfig } from '@playwright/test';
export default defineConfig({
  reporter: [
    ['list'],
    ['html', { outputFolder: 'reports[v2]', open: 'never' }],
    ['junit', { outputFile: "out[snapshot].xml" }],
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
		t.Fatalf("expected change: %s", res.Reason)
	}
	// The patched content MUST still contain the html reporter line intact
	// — if the lazy regex bug returns, the close `}` of the html entry gets
	// dropped and this assertion catches it.
	if !strings.Contains(res.NewContent, "outputFolder: 'reports[v2]', open: 'never'") {
		t.Errorf("html reporter line truncated by bracket-counting bug:\n%s", res.NewContent)
	}
	if !strings.Contains(res.NewContent, "outputFile: \"out[snapshot].xml\"") {
		t.Errorf("junit reporter line truncated:\n%s", res.NewContent)
	}
	// And the closing `]` of the outer reporter array must still be present
	// in the post-patch text.
	if !strings.Contains(res.NewContent, "],\n});") {
		t.Errorf("outer array close `]` missing or moved:\n%s", res.NewContent)
	}
}

func TestFindMatchingBracket(t *testing.T) {
	cases := []struct {
		name string
		s    string
		from int // index AFTER opening `[`
		want int
		ok   bool
	}{
		{"simple", "[abc]", 1, 4, true},
		{"nested", "[a[b]c]", 1, 6, true},
		{"deeply nested", "[[[]]]", 1, 5, true},
		{"single-quoted bracket inside", "['x]y']", 1, 6, true},
		{"double-quoted bracket inside", "[\"x]y\"]", 1, 6, true},
		{"backtick bracket inside", "[`x]y`]", 1, 6, true},
		{"escaped quote inside string", "['it\\'s]ok']", 1, 11, true},
		{"unterminated", "[abc", 1, 0, false},
		{"unterminated nested", "[a[b", 1, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := findMatchingBracket(c.s, c.from)
			if ok != c.ok || got != c.want {
				t.Errorf("findMatchingBracket(%q, %d) = (%d, %v), want (%d, %v)", c.s, c.from, got, ok, c.want, c.ok)
			}
		})
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
