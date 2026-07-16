package jvm

// statusRank orders result statuses for aggregation across repeated
// invocations of one logical test (data-driven / parametrized / retried):
// any failure dominates, and a run that executed (PASS) beats a pure skip.
func statusRank(s string) int {
	switch s {
	case StatusFail:
		return 3
	case StatusPass:
		return 2
	case StatusSkip:
		return 1
	default:
		return 0
	}
}

// aggregateResult folds b into a, returning the dominant status and the
// summed duration. Either operand may be nil.
func aggregateResult(a, b *Result) *Result {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	out := &Result{Status: a.Status, DurationMs: a.DurationMs + b.DurationMs}
	if statusRank(b.Status) > statusRank(a.Status) {
		out.Status = b.Status
	}
	return out
}

// collapseByFQName merges entries that share an fq_name into a single
// logical test. A JUnit5 @ParameterizedTest, a data-driven TestNG method,
// or a retried test emits one report record per invocation — all with the
// same class#method identity. They are ONE Observo case, not N colliding
// ones, so collapsing them here keeps DetectDuplicates (which is about
// two DIFFERENT tests claiming one code) honest and stops a mainstream
// framework feature from failing the whole build.
//
// Result is aggregated (any FAIL wins, durations summed); every other
// field takes the first non-empty value across the group. First-occurrence
// order is preserved.
func collapseByFQName(entries []LinkEntry) []LinkEntry {
	idx := make(map[string]int, len(entries))
	out := make([]LinkEntry, 0, len(entries))
	for _, e := range entries {
		// An entry with no derivable fq_name is not a "same test" key —
		// two distinct tests that both fail to yield an fq_name must stay
		// separate, never merge into one (AC4: never silently drop a test).
		if e.FQName == "" {
			out = append(out, e)
			continue
		}
		i, ok := idx[e.FQName]
		if !ok {
			idx[e.FQName] = len(out)
			out = append(out, e)
			continue
		}
		mergeInto(&out[i], e)
	}
	return out
}

// mergeInto folds a repeat invocation e into the retained base entry.
func mergeInto(base *LinkEntry, e LinkEntry) {
	if base.Code == nil && e.Code != nil {
		base.Code = e.Code
	}
	if base.DisplayName == "" {
		base.DisplayName = e.DisplayName
	}
	if base.Description == "" {
		base.Description = e.Description
	}
	if base.Feature == "" {
		base.Feature = e.Feature
	}
	if base.Story == "" {
		base.Story = e.Story
	}
	if base.Order == nil {
		base.Order = e.Order
	}
	if base.ChainID == "" {
		base.ChainID = e.ChainID
	}
	if base.SourceRef == "" {
		base.SourceRef = e.SourceRef
	}
	base.Result = aggregateResult(base.Result, e.Result)
}
