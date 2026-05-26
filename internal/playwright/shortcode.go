// Package playwright is the parser + extractor layer for the Playwright
// JSON reporter output, consumed by `observo run import --from playwright`.
//
// The package has no dependency on internal/api or cobra — the orchestrator
// (cmd/run_import.go) is the only file that wires data flow from this
// package into the HTTP client. This separation keeps the parser usable
// from the future OB-346 reporter wrapper too.
package playwright

import (
	"path/filepath"
	"regexp"
	"strings"
)

// observoTagRE matches the explicit annotation tag set by tests via
// `test('foo', { tag: '@observo:OB-7' }, ...)`. Mirrors the regex used
// by the existing live reporter (e2e/reporters/observo-reporter.ts:42)
// so both ingestion paths agree on the canonical form.
var observoTagRE = regexp.MustCompile(`^@observo:(OB-\d+)$`)

// codeInTextRE catches a free-form "OB-NNN" reference anywhere in a
// string. Used as the second-priority resolver against test / suite
// titles, then as the directory-name fallback for backward compat with
// `web-portal/scripts/upload-playwright-attachments.sh`.
var codeInTextRE = regexp.MustCompile(`\bOB-\d+\b`)

// ResolveShortCode finds the Observo case short code (e.g. "OB-7")
// associated with a Playwright test, scanning sources in priority order:
//
//  1. Explicit `@observo:OB-X` annotation tag — authoritative, never wrong.
//  2. First `OB-X` token in test titles (titles slice from spec title +
//     parent suite titles; caller controls order).
//  3. First `OB-X` token in attachment paths (Playwright's per-test
//     output dir name often encodes the short code, e.g.
//     `auth-login-OB-7-chromium/video.webm`) — preserves the bash
//     wedge's directory-matching behaviour for tests that haven't been
//     tagged yet.
//
// Returns "" when no source matched. Callers MUST treat that as a
// non-fatal skip (log + continue) — silently dropping an untagged test
// is the documented OB-347 behaviour.
func ResolveShortCode(tags []string, titles []string, attachmentPaths []string) string {
	for _, tag := range tags {
		if m := observoTagRE.FindStringSubmatch(strings.TrimSpace(tag)); m != nil {
			return m[1]
		}
	}
	for _, title := range titles {
		if code := codeInTextRE.FindString(title); code != "" {
			return code
		}
	}
	for _, p := range attachmentPaths {
		// Match on the directory component, not the file name itself —
		// `screenshot-OB-2.png` would be a false positive for OB-2 if a
		// test about OB-7 happened to use that screenshot name. The
		// directory name is what Playwright derives from the test title.
		if code := codeInTextRE.FindString(filepath.Dir(p)); code != "" {
			return code
		}
	}
	return ""
}
