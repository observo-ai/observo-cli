package jvm

// Merge enriches TestNG-derived entries with Allure metadata, matched by
// fq_name. This is the pilot client's shape: testng-results.xml carries
// the groups join + result + chain/order, while allure-testng records the
// human metadata (feature, story, description, friendly display name) that
// the XML omits.
//
// TestNG is the base (it owns the join the client uses). Allure fills only
// empty fields, so it never clobbers a value TestNG already knew. Entries
// present only in Allure (e.g. a JUnit5 suite built alongside) are
// appended as-is. Output order is not significant — Emit re-sorts.
func Merge(testng, allure []LinkEntry) []LinkEntry {
	out := make([]LinkEntry, len(testng))
	copy(out, testng)

	byFQ := make(map[string]int, len(out))
	for i := range out {
		if out[i].FQName == "" {
			continue // empty fq_name is not a join key (see collapseByFQName)
		}
		byFQ[out[i].FQName] = i
	}

	for _, a := range allure {
		if a.FQName == "" {
			out = append(out, a) // never coalesce keyless entries
			continue
		}
		i, ok := byFQ[a.FQName]
		if !ok {
			// Index the appended entry too, so a second Allure entry with
			// the same fq_name coalesces here instead of producing a
			// divergent duplicate row. (Allure input is already collapsed
			// upstream; this keeps Merge correct on its own.)
			byFQ[a.FQName] = len(out)
			out = append(out, a)
			continue
		}
		base := &out[i]

		// Prefer Allure's friendly name when TestNG only had the bare
		// method name (its default display).
		if a.DisplayName != "" && (base.DisplayName == "" || base.DisplayName == methodOf(base.FQName)) {
			base.DisplayName = a.DisplayName
		}
		if base.Description == "" {
			base.Description = a.Description
		}
		if base.Feature == "" {
			base.Feature = a.Feature
		}
		if base.Story == "" {
			base.Story = a.Story
		}
		// Only if TestNG somehow lacked the groups join but Allure had a
		// tag/@TmsLink for the same method do we adopt Allure's code.
		if !base.Tracked() && a.Tracked() {
			base.Code = a.Code
		}
	}
	return out
}
