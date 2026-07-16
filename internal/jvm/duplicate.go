package jvm

import (
	"fmt"
	"sort"
	"strings"
)

// DuplicateError reports two or more tests that resolved to the SAME
// Observo short code — almost always a copy-paste bug (a @Test duplicated
// with its join tag). Letting "last write win" would attach the case to an
// arbitrary test and hide the other, so OB-543 AC2 requires failing loud
// with every offending location.
type DuplicateError struct {
	// Collisions maps a short code to the sorted fq_names that claim it.
	Collisions map[string][]string
}

func (e *DuplicateError) Error() string {
	codes := make([]string, 0, len(e.Collisions))
	for c := range e.Collisions {
		codes = append(codes, c)
	}
	sort.Strings(codes)

	var b strings.Builder
	b.WriteString("duplicate observo join tag(s) — each short code must map to exactly one test:")
	for _, c := range codes {
		fmt.Fprintf(&b, "\n  %s claimed by:", c)
		for _, fq := range e.Collisions[c] {
			fmt.Fprintf(&b, "\n    - %s", fq)
		}
	}
	return b.String()
}

// DetectDuplicates returns a *DuplicateError if any short code is claimed
// by more than one tracked entry. Untracked entries (code=null) are
// ignored — many tests legitimately share "no code". Returns nil when
// every resolved code is unique.
func DetectDuplicates(entries []LinkEntry) error {
	byCode := map[string][]string{}
	for _, e := range entries {
		if !e.Tracked() {
			continue
		}
		byCode[*e.Code] = append(byCode[*e.Code], e.FQName)
	}

	collisions := map[string][]string{}
	for code, fqs := range byCode {
		if len(fqs) > 1 {
			sort.Strings(fqs)
			collisions[code] = fqs
		}
	}
	if len(collisions) == 0 {
		return nil
	}
	return &DuplicateError{Collisions: collisions}
}
