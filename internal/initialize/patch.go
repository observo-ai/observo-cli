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

// reportersArrayRegex matches the reporter: [...] array. Captures group 1 is
// the array body so we can detect whether Observo is already in there.
//
// We DON'T support reporter: 'single-string' form — for that shape we leave
// a TODO comment and skip patching. Customers using single-reporter configs
// can convert to array themselves.
var reportersArrayRegex = regexp.MustCompile(`reporter\s*:\s*\[([\s\S]*?)\]`)

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

	m := reportersArrayRegex.FindStringSubmatchIndex(original)
	if m == nil {
		return &PatchResult{
			Path: path, Original: original, NewContent: original,
			Changed: false,
			Reason: "reporter array not found — config uses single-reporter form? Add manually:\n" +
				"    reporter: [" + observoReporterEntry + " ['list']]",
		}, nil
	}

	// m[2..3] = inside-of-array text. Insert observo entry at the START of the
	// array body so it precedes user reporters (consistent positioning aids
	// future diff / re-patch detection).
	start := m[2]
	end := m[3]
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
