package playwright

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FailureLocation is the source position of the failing assertion, lifted
// straight from Playwright `testResult.errors[].location`. file is the
// path Playwright recorded — may be absolute (workspace-anchored in CI)
// or relative to the project root depending on the runner config.
type FailureLocation struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// Failure is the structured shape uploaded as `failure.json` per failed
// case. v1 ships fields that Playwright surfaces directly + a
// best-effort `source_excerpt` lifted from disk. The four heuristic
// fields (Matcher / Expected / Actual / Diff) are placeholders for a
// follow-up: Playwright embeds them inside `message` as free text, and
// the parser is brittle across matcher libraries. v1.1 (separate
// ticket) will add a recognizer where it's safe.
//
// FE failure viewer (OB-350) renders Message + Stack + SourceExcerpt —
// that covers ~80% of the debugging UX without the brittle parsing.
type Failure struct {
	Message       string           `json:"message"`
	Stack         string           `json:"stack,omitempty"`
	Location      *FailureLocation `json:"location,omitempty"`
	SourceExcerpt string           `json:"source_excerpt,omitempty"`

	// v1.1 placeholders — always null in v1 output. Kept in the schema
	// so FE viewers can be coded against the final shape today and the
	// extractor lights them up later without a wire-contract bump.
	Matcher  *string `json:"matcher,omitempty"`
	Expected *string `json:"expected,omitempty"`
	Actual   *string `json:"actual,omitempty"`
	Diff     *string `json:"diff,omitempty"`
}

// PWError mirrors the subset of `testResult.errors[]` we consume. Kept
// minimal so callers in cmd/run_import.go can pass through whatever
// shape `encoding/json` gave them — they don't need the full Playwright
// types package.
type PWError struct {
	Message  string `json:"message,omitempty"`
	Stack    string `json:"stack,omitempty"`
	Value    string `json:"value,omitempty"`
	Location *struct {
		File   string `json:"file"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	} `json:"location,omitempty"`
}

// sourceExcerptRadius is the number of context lines pulled either side
// of the failing line. 3 keeps the JSON small (<1KB typical) and is
// enough to spot the failing expression + the surrounding setup.
const sourceExcerptRadius = 3

// ExtractFailure builds a Failure from one Playwright error. sourceRoot
// is the directory excerpts are resolved relative to (typically the
// invocation cwd or the --source-root flag). When the source file is
// unreadable — missing, outside the project tree, binary, permission
// denied — SourceExcerpt is left empty rather than erroring. That keeps
// `observo run import` working in sandboxed CI runners where the test
// source isn't co-located with the results dir.
func ExtractFailure(err PWError, sourceRoot string) Failure {
	f := Failure{
		Message: err.Message,
		Stack:   err.Stack,
	}
	if err.Location != nil && err.Location.File != "" {
		f.Location = &FailureLocation{
			File:   err.Location.File,
			Line:   err.Location.Line,
			Column: err.Location.Column,
		}
		f.SourceExcerpt = readSourceExcerpt(sourceRoot, err.Location.File, err.Location.Line)
	}
	// Fallback to `value` field when message is empty — Playwright
	// sometimes splits a structured error into `value` (JSON-stringified
	// matcher output) without setting message. Either populates the FE
	// viewer.
	if f.Message == "" {
		f.Message = err.Value
	}
	return f
}

// readSourceExcerpt returns line `n` with `sourceExcerptRadius` lines on
// either side, line-numbered, ready to render in a code block. Returns
// "" silently on any I/O error — see ExtractFailure for rationale.
func readSourceExcerpt(root, file string, line int) string {
	if line <= 0 {
		return ""
	}
	// Fail-safe: if the caller couldn't determine a source root (rare —
	// orchestrator falls back to os.Getwd(), which itself can fail when
	// the cwd was deleted under a long-running CI runner), refuse to
	// open ANY path rather than running unguarded. Without root the
	// traversal check below is skipped, and an absolute path like
	// `/etc/shadow` in a hostile `results.json` would otherwise reach
	// `os.Open` unconditionally.
	if root == "" {
		return ""
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, file)
	}
	// Reject paths that escape the source root after Join — defence
	// against a malicious results.json pointing at /etc/shadow on a
	// shared CI runner.
	//
	// Resolve symlinks BEFORE the Rel check: a lexical `filepath.Rel`
	// happily accepts `<root>/link` where `link → /etc/shadow`, because
	// the rel string ("link") has no `..` prefix. EvalSymlinks collapses
	// the link to its real target so the Rel check sees the actual
	// destination, and the guard rejects symlinks that escape. Missing
	// files (typical for tests in CI sandboxes where the source isn't
	// co-located) cause EvalSymlinks to error — fall through to "" via
	// the os.Open failure, no special-case needed.
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	path = realPath
	fh, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer fh.Close()

	from := line - sourceExcerptRadius
	if from < 1 {
		from = 1
	}
	to := line + sourceExcerptRadius

	var out strings.Builder
	scanner := bufio.NewScanner(fh)
	// Bump the line buffer cap so tests with long minified lines don't
	// silently truncate. 1MB / line is well past any real test file.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; scanner.Scan() && n <= to; n++ {
		if n >= from {
			marker := "  "
			if n == line {
				marker = "→ "
			}
			fmt.Fprintf(&out, "%s%4d  %s\n", marker, n, scanner.Text())
		}
	}
	return strings.TrimRight(out.String(), "\n")
}
