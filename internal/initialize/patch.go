package initialize

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// PatchResult describes what PatchPlaywrightConfig will do (dry-run) or did
// (write). Either way, NewContent is the file post-patch.
type PatchResult struct {
	Path       string
	Original   string
	NewContent string
	Changed    bool
	Reason     string // explanation when Changed=false (already patched / unsupported shape)
}

// reporterImportLine is the import we add at the top of playwright.config.ts
// if not already present. Customers using ESM-style configs without explicit
// imports (Playwright lets you reference reporters by string) skip this.
const reporterImportLine = `// @observo:reporter — added by 'observo init'`

// observoReporterEntry is what we insert at the start of the reporters
// array. Uses the spread idiom rather than a ternary that emits `null`,
// because pre-1.38 Playwright throws on null entries when destructuring
// the reporter tuple. With spread, when CI is unset the array stays clean.
const observoReporterEntry = `...(process.env.CI ? [['@observo/playwright-reporter']] : []),`

// reporterKeyRegex finds the `reporter:` key + the opening `[` of the array.
// We use the regex ONLY to locate the start of the array body — the matching
// close-bracket is found by bracket-counting (findMatchingBracket) so that
// `]` characters inside string literals (e.g. `outputFolder: 'reports[v2]'`)
// or nested arrays/objects don't truncate the match.
//
// We DON'T support reporter: 'single-string' form — that shape returns a hint
// instead. Customers using a single-reporter literal can convert to array.
var reporterKeyRegex = regexp.MustCompile(`reporter\s*:\s*\[`)

// PatchPlaywrightConfig produces the post-patch content WITHOUT writing it.
// Caller invokes os.WriteFile only after confirming with the user (--print
// dry-run uses the same function to display the planned change). All edits
// are idempotent — running init twice never adds duplicate reporter entries.
func PatchPlaywrightConfig(path string) (*PatchResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	original := string(raw)

	// Mask comments BEFORE the idempotency check too — a developer who left
	// a previous trial of the reporter commented out (`// reporter: [['@observo/playwright-reporter']]`)
	// would otherwise make the raw-text Contains check return true, refuse
	// to patch, and print "no patch needed" forever even though the real
	// array still lacks the entry.
	searchSpace := maskComments(original)
	if strings.Contains(searchSpace, "@observo/playwright-reporter") {
		return &PatchResult{
			Path: path, Original: original, NewContent: original,
			Changed: false, Reason: "@observo/playwright-reporter already referenced (non-comment)",
		}, nil
	}

	// Search for the reporter key in the same comment-masked copy so a
	// commented-out `// reporter: ['list']` block can't hijack the match.
	keyMatch := reporterKeyRegex.FindStringIndex(searchSpace)
	if keyMatch == nil {
		return &PatchResult{
			Path: path, Original: original, NewContent: original,
			Changed: false,
			Reason: "reporter array not found — config uses single-reporter form? Add manually:\n" +
				"    reporter: [" + observoReporterEntry + " ['list']]",
		}, nil
	}

	// keyMatch[1] is index of the byte AFTER the opening `[`. Walk forward
	// counting brackets on the COMMENT-MASKED text — a `]` inside a commented
	// reporter entry (e.g. `// ['junit', { x: 'y' }],` line inside the array)
	// would otherwise close the depth count early and produce a truncated
	// patch. Offsets are identical between searchSpace and original because
	// maskComments preserves byte lengths.
	start := keyMatch[1]
	end, ok := findMatchingBracket(searchSpace, start)
	if !ok {
		return &PatchResult{
			Path: path, Original: original, NewContent: original,
			Changed: false,
			Reason:  "reporter array opening `[` found but no matching `]` — config syntax may be invalid; aborting patch",
		}, nil
	}
	inside := original[start:end]
	indent := detectIndent(inside)
	// Empty (no content lines) or bare-newline (first line had no leading
	// whitespace) → fall back to the reporter-key line's own indent + 2-space
	// offset so the inserted entry sits visually inside the array.
	if indent == "" || indent == "\n" {
		indent = "\n" + indentOfLineContaining(original, keyMatch[0]) + "  "
	}
	// Inserting a new line with a spread entry; for single-line original
	// arrays we also need to push the existing inline content onto a new
	// line by appending a trailing newline before the original `inside`.
	// Detect single-line: `inside` has no `\n`.
	separator := ""
	if !strings.Contains(inside, "\n") {
		separator = indent
	}
	patched := indent + observoReporterEntry + separator + strings.TrimLeft(inside, " \t")
	newContent := original[:start] + patched + original[end:]

	// Add a marker comment immediately above the reporters line for human
	// audit trail. Use the keyMatch offset directly — `strings.Index` would
	// match the FIRST occurrence of "reporter" anywhere in the file (e.g. a
	// variable `const myReporter` or a comment), putting the marker in the
	// wrong place. Our inserted entry was added AT keyMatch[1] (after the
	// opening `[`), so keyMatch[0] in `original` still points at the `r` of
	// `reporter` in `newContent` (the insertion is after that index).
	if !strings.Contains(newContent, reporterImportLine) {
		repIdx := keyMatch[0]
		// Walk back to the start of the line to insert above it. Reuse the
		// EXACT leading whitespace from the existing reporter line (could be
		// spaces, tabs, or mixed) instead of synthesizing spaces — tab-indented
		// configs would otherwise get a space-indented marker and trip
		// no-mixed-spaces-and-tabs lint rules.
		lineStart := strings.LastIndex(newContent[:repIdx], "\n") + 1
		marker := newContent[lineStart:repIdx] + reporterImportLine + "\n"
		newContent = newContent[:lineStart] + marker + newContent[lineStart:]
	}

	return &PatchResult{
		Path: path, Original: original, NewContent: newContent,
		Changed: true,
	}, nil
}

// maskComments returns a copy of `s` where every byte inside a single-line
// (`// ... \n`) or block (`/* ... */`) comment is replaced with a space.
// Lengths are preserved so byte offsets into the result are valid offsets
// into the original. Used to make regex searches comment-aware without
// rebuilding an offset map.
//
// Skips `//` and `/*` that appear inside string literals (single / double
// quoted, backtick template) — same string-handling shape as
// findMatchingBracket below so the two stay consistent.
func maskComments(s string) string {
	out := []byte(s)
	i := 0
	for i < len(s) {
		c := s[i]
		switch c {
		case '\'', '"', '`':
			quote := c
			i++
			for i < len(s) {
				if s[i] == '\\' && i+1 < len(s) {
					i += 2
					continue
				}
				if s[i] == quote {
					i++
					break
				}
				i++
			}
		case '/':
			if i+1 >= len(s) {
				i++
				continue
			}
			switch s[i+1] {
			case '/':
				// single-line comment — mask to end of line (preserve newline)
				start := i
				for i < len(s) && s[i] != '\n' {
					i++
				}
				for j := start; j < i; j++ {
					out[j] = ' '
				}
			case '*':
				// block comment — mask to `*/` (preserve newlines for line counting).
				// Unterminated block (EOF before */) masks through end of string,
				// not just up to len(s)-1 — otherwise the final byte could be a
				// reporter-array bracket the regex would still match.
				start := i
				i += 2
				terminated := false
				for i+1 < len(s) {
					if s[i] == '*' && s[i+1] == '/' {
						i += 2
						terminated = true
						break
					}
					i++
				}
				if !terminated {
					i = len(s)
				}
				for j := start; j < i; j++ {
					if out[j] != '\n' {
						out[j] = ' '
					}
				}
			default:
				i++
			}
		default:
			i++
		}
	}
	return string(out)
}

// findMatchingBracket walks `s` starting at `from` (which must be the index
// IMMEDIATELY AFTER an opening `[`) and returns the index of the matching
// close `]` plus true. It tracks nested `[`/`]` and skips `]` chars that
// appear inside single-quoted, double-quoted, or backtick-template string
// literals — the common JS shapes that trip a lazy regex like
// `outputFolder: 'reports[v2]'`.
//
// Returns (0, false) if the input runs out before the matching close is found.
// Comments and escape sequences are out of scope (cause: extremely rare
// inside a reporters array, and adding them would multiply complexity).
func findMatchingBracket(s string, from int) (int, bool) {
	depth := 1
	i := from
	for i < len(s) {
		c := s[i]
		switch c {
		case '\'', '"', '`':
			// Skip to matching unescaped quote.
			quote := c
			i++
			for i < len(s) {
				if s[i] == '\\' && i+1 < len(s) {
					i += 2
					continue
				}
				if s[i] == quote {
					i++
					break
				}
				i++
			}
		case '[':
			depth++
			i++
		case ']':
			depth--
			if depth == 0 {
				return i, true
			}
			i++
		default:
			i++
		}
	}
	return 0, false
}

// detectIndent picks up the leading whitespace of the FIRST non-empty line
// inside the reporter array so we add the new entry with matching indent.
// Best-effort; defaults to 4 spaces if we can't see one.
//
// Single-line reporter arrays (e.g. `reporter: [['list'], ['html']]` on one
// line) have no internal newline → `inside` is content-only with no
// indentation cues. Returning a bare `\n` would put the spread entry at
// column 0 with no newline before the closing `]`, visually breaking the
// surrounding indentation. The caller (PatchPlaywrightConfig) handles this
// by passing the reporter-key line's own indent as `outerIndent` for the
// fallback. detectIndentFallback below applies it.
func detectIndent(inside string) string {
	for _, ln := range strings.Split(inside, "\n") {
		trim := strings.TrimLeft(ln, " \t")
		if trim != "" {
			return "\n" + ln[:len(ln)-len(trim)]
		}
	}
	return "" // empty = caller should use outer-line indent + 2-space offset
}

// indentOfLineContaining returns the leading whitespace of the line that
// contains `offset` in `s`. Used as fallback for detectIndent when the
// reporter array is single-line.
func indentOfLineContaining(s string, offset int) string {
	if offset < 0 || offset > len(s) {
		return ""
	}
	lineStart := strings.LastIndex(s[:offset], "\n") + 1
	indent := ""
	for i := lineStart; i < len(s) && (s[i] == ' ' || s[i] == '\t'); i++ {
		indent += string(s[i])
	}
	return indent
}

// WritePatch persists a PatchResult.NewContent atomically (write to tmp,
// rename over original) so a failed write doesn't corrupt the user's config.
// Preserves the original file's mode bits — a script-like config (e.g.
// chmod +x because someone runs the file directly) keeps its perms.
func WritePatch(p *PatchResult) error {
	if !p.Changed {
		return nil
	}
	// Read original mode before overwrite so atomic rename doesn't strip it.
	mode := os.FileMode(0o644)
	if info, err := os.Stat(p.Path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp := p.Path + ".observo-tmp"
	if err := os.WriteFile(tmp, []byte(p.NewContent), mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, p.Path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename patch over original: %w", err)
	}
	return nil
}
