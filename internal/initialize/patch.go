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

	if strings.Contains(original, "@observo/playwright-reporter") {
		return &PatchResult{
			Path: path, Original: original, NewContent: original,
			Changed: false, Reason: "@observo/playwright-reporter already referenced",
		}, nil
	}

	keyMatch := reporterKeyRegex.FindStringIndex(original)
	if keyMatch == nil {
		return &PatchResult{
			Path: path, Original: original, NewContent: original,
			Changed: false,
			Reason: "reporter array not found — config uses single-reporter form? Add manually:\n" +
				"    reporter: [" + observoReporterEntry + " ['list']]",
		}, nil
	}

	// keyMatch[1] is index of the byte AFTER the opening `[`. Walk forward
	// counting brackets to find the true close — handles nested arrays/objects
	// and string literals containing `]`. If we can't find a matching close
	// (truncated config / weird syntax), bail out cleanly.
	start := keyMatch[1]
	end, ok := findMatchingBracket(original, start)
	if !ok {
		return &PatchResult{
			Path: path, Original: original, NewContent: original,
			Changed: false,
			Reason: "reporter array opening `[` found but no matching `]` — config syntax may be invalid; aborting patch",
		}, nil
	}
	inside := original[start:end]
	indent := detectIndent(inside)
	patched := indent + observoReporterEntry + inside
	newContent := original[:start] + patched + original[end:]

	// Add a marker comment immediately above the reporters line for human
	// audit trail — helpful when someone asks "what did observo init do?"
	if !strings.Contains(newContent, reporterImportLine) {
		// Find the line containing "reporter:" and prepend a marker on the
		// previous line. This is a best-effort cosmetic touch — we don't
		// fail patch if line-based location heuristics shift.
		repIdx := strings.Index(newContent, "reporter")
		if repIdx > 0 {
			// Walk back to the start of the line to insert above it.
			lineStart := strings.LastIndex(newContent[:repIdx], "\n") + 1
			marker := strings.Repeat(" ", repIdx-lineStart) + reporterImportLine + "\n"
			newContent = newContent[:lineStart] + marker + newContent[lineStart:]
		}
	}

	return &PatchResult{
		Path: path, Original: original, NewContent: newContent,
		Changed: true,
	}, nil
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
func detectIndent(inside string) string {
	lines := strings.Split(inside, "\n")
	for _, ln := range lines {
		trim := strings.TrimLeft(ln, " \t")
		if trim != "" {
			indent := ln[:len(ln)-len(trim)]
			if !strings.HasSuffix(indent, "\n") {
				return "\n" + indent
			}
			return indent
		}
	}
	return "\n    "
}

// WritePatch persists a PatchResult.NewContent atomically (write to tmp,
// rename over original) so a failed write doesn't corrupt the user's config.
func WritePatch(p *PatchResult) error {
	if !p.Changed {
		return nil
	}
	tmp := p.Path + ".observo-tmp"
	if err := os.WriteFile(tmp, []byte(p.NewContent), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p.Path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename patch over original: %w", err)
	}
	return nil
}
