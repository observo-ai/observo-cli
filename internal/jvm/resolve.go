// Package jvm is the parser + resolver layer for the Kotlin/JVM ↔ Observo
// bridge (OB-542). It turns a test run's reports (Allure results,
// testng-results.xml) into a single observo-link-manifest.json, the shared
// artifact consumed by `observo jvm import/stub/push` and the coverage
// verdict.
//
// The package has no dependency on internal/api or cobra — the CLI
// orchestrator (cmd/jvm_*.go) is the only wiring from here into HTTP.
// This mirrors internal/playwright so the two ingestion paths stay
// symmetric and independently testable.
package jvm

import (
	"regexp"
	"strings"
)

// codeRE matches a canonical Observo short code: an uppercase project
// prefix, a dash, and digits (e.g. "PD-101", "OB-543").
//
// Deliberately NOT hard-coded to "OB" — the bridge is cross-project and
// the pilot client uses "PD" codes. Any [A-Z][A-Z0-9]* prefix resolves.
// (internal/playwright/shortcode.go hard-codes OB because that path only
// ever ingests Observo's own e2e suite; this one must not.)
var codeRE = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)

// tagRE matches the canonical join tag in its two written forms:
//
//	observo:PD-101   — JUnit5 @Tag value / TestNG groups entry
//	@observo:PD-101  — Playwright title-tag (carries the @ sigil)
//
// The single sub-match is the bare code. Case-sensitive on purpose: the
// sigil and keyword are fixed strings our tooling emits, not user prose,
// so a stray "Observo:" in a comment must not resolve.
var tagRE = regexp.MustCompile(`^@?observo:([A-Z][A-Z0-9]*-[0-9]+)$`)

// FromTag resolves the canonical short code from an authoritative join
// tag: a JUnit5 @Tag("observo:<code>"), a TestNG groups entry
// "observo:<code>", or a Playwright title-tag "@observo:<code>". The
// leading @ is optional so all three frameworks feed one parser (OB-543
// AC1 — cross-language parity).
//
// Returns ("", false) for any string that isn't an observo join tag, so
// callers can distinguish "no link" from a real match.
func FromTag(raw string) (string, bool) {
	m := tagRE.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// FromTmsLink resolves a short code from an Allure @TmsLink value — the
// FALLBACK join source for suites that already track cases through a TMS
// integration instead of a native tag. Unlike FromTag it accepts a bare
// "<code>" (that's all @TmsLink("PD-101") carries) but also tolerates a
// redundant observo: prefix.
//
// A bare code is far more ambiguous than a prefixed tag, so this is
// applied ONLY to @TmsLink values — never scanned across free-form titles
// or paths. That title/path scan is exactly how the Playwright resolver
// produces false positives; we do not repeat it here.
func FromTmsLink(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if code, ok := FromTag(raw); ok {
		return code, true
	}
	if codeRE.MatchString(raw) {
		return raw, true
	}
	return "", false
}

// Resolve returns the canonical short code for a single test, preferring
// authoritative tags over the Allure @TmsLink fallback. tags are the
// JUnit5/TestNG/Playwright join tags; tmsLinks are Allure TMS link names.
//
// Returns "" when the test carries no resolvable link. Callers MUST
// record that as an untracked (code=null) manifest entry, never drop it
// (OB-543 AC4).
func Resolve(tags, tmsLinks []string) string {
	for _, t := range tags {
		if code, ok := FromTag(t); ok {
			return code
		}
	}
	for _, l := range tmsLinks {
		if code, ok := FromTmsLink(l); ok {
			return code
		}
	}
	return ""
}
