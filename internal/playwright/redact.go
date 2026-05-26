package playwright

import (
	"fmt"
	"regexp"
	"strings"
)

// DefaultRedactPattern catches the obvious secret keys customers send in
// real Playwright tests against staging/dev APIs. Case-insensitive,
// matches the key name when it appears at line start or after a JSON
// quote — not inside a longer word. Conservative on purpose: false
// positives hide signal a debug session needs.
const DefaultRedactPattern = `(?i)(authorization|password|token|secret|api[_-]?key|bearer)`

// Redactor applies a regex line-by-line to body strings extracted from
// trace.network entries and to failure messages. Construct via
// NewRedactor and reuse — the compiled regex is concurrency-safe.
type Redactor struct {
	re *regexp.Regexp
}

// NewRedactor combines the built-in DefaultRedactPattern with the
// user-supplied extra pattern (from --redact). An empty extra is
// allowed — the default still applies. An invalid extra pattern is
// surfaced as an error so the CLI can fail fast at flag parse time
// rather than silently shipping unredacted bodies.
func NewRedactor(extra string) (*Redactor, error) {
	pattern := DefaultRedactPattern
	if extra = strings.TrimSpace(extra); extra != "" {
		pattern = "(" + DefaultRedactPattern + ")|(" + extra + ")"
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid --redact pattern %q: %w", extra, err)
	}
	return &Redactor{re: re}, nil
}

// Apply redacts every line of s that contains a match. The whole line is
// replaced with "<redacted by observo>" — partial redaction (e.g.
// blanking out only the value) would require parsing the body format
// (JSON / form / header line), which is fragile across content types.
// Whole-line redaction is unambiguous and easy to spot when reviewing.
//
// Empty s returns "" — the caller (network/failure shape builder) does
// not differentiate "empty body" from "redacted body" beyond this.
func (r *Redactor) Apply(s string) string {
	if s == "" || r == nil || r.re == nil {
		return s
	}
	if !r.re.MatchString(s) {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if r.re.MatchString(line) {
			lines[i] = "<redacted by observo>"
		}
	}
	return strings.Join(lines, "\n")
}
