package jvm

import (
	"encoding/json"
	"io"
	"sort"
)

// ManifestVersion is stamped into every emitted manifest. Bump it when a
// field's meaning changes so downstream consumers (import/stub/push/
// verdict) can refuse an incompatible artifact instead of misreading it.
const ManifestVersion = 1

// Canonical result statuses used in a LinkEntry.Result. Kept framework-
// agnostic (Allure "broken" and TestNG "FAIL" both collapse to FAIL) so
// consumers switch on one small set.
const (
	StatusPass = "PASS"
	StatusFail = "FAIL"
	StatusSkip = "SKIP"
)

// LinkManifest is the run artifact that links JVM tests to Observo cases.
// It is the single source of truth shared by import (OB-544), push
// (OB-546), the runtime listener (OB-547) and the coverage verdict
// (OB-550).
type LinkManifest struct {
	Version   int         `json:"version"`
	Framework string      `json:"framework,omitempty"` // testng | junit5 | playwright
	Entries   []LinkEntry `json:"entries"`
}

// LinkEntry is one test ↔ case link. Field provenance is documented in
// the PRD Data Model. Optional fields are pointers / omitempty so a
// manifest built from a static report (no runtime listener) stays small
// and honest about what it could not determine.
type LinkEntry struct {
	// Code is the canonical Observo short code, or nil for an untracked
	// test (no join tag). Marshalled as JSON `null` — never omitted — so
	// an untracked test stays visible in the artifact rather than being
	// silently dropped (OB-543 AC4).
	Code *string `json:"code"`

	FQName      string `json:"fq_name"`               // api.pd.PdBackendE2ETest#traderCreatesWallet
	DisplayName string `json:"display_name"`          // human title (Allure name / backtick fun-name)
	Description string `json:"description,omitempty"` // @Description
	Feature     string `json:"feature,omitempty"`     // @Feature → suite
	Story       string `json:"story,omitempty"`       // @Story → sub-suite

	Order   *int   `json:"order,omitempty"`    // 1-based position within an ordered chain
	ChainID string `json:"chain_id,omitempty"` // shared id for an order-dependent class chain

	Result    *Result `json:"result,omitempty"`     // present only when built from a real run
	SourceRef string  `json:"source_ref,omitempty"` // file:line — supplied by the listener (OB-547)

	// References are durable case references extracted from the report (OB-551):
	// ticket-refs (Allure @Issue) and links (@Link). Empty when the test carries
	// none — nothing is invented. Import attaches them to the case via
	// SetTestCaseReferences.
	References []Reference `json:"references,omitempty"`
}

// Reference kinds. "rationale" exists server-side but the report path never
// emits it (the WHY prose lives in Description); import only produces ticket/link.
const (
	RefKindTicket = "ticket"
	RefKindLink   = "link"
)

// Reference is one external reference on a test — a ticket-ref or a link.
type Reference struct {
	Kind  string `json:"kind"`            // ticket | link
	Value string `json:"value"`           // ticket key (PD-742) or url
	Label string `json:"label,omitempty"` // display label
	URL   string `json:"url,omitempty"`   // resolved URL when the report carries one
}

// Result is the per-test outcome, present only when the manifest was built
// from an actual run (Allure results / testng-results.xml), not a static
// scan.
type Result struct {
	Status     string `json:"status"`      // PASS | FAIL | SKIP
	DurationMs int64  `json:"duration_ms"` // 0 when the source omits timing
}

// Tracked reports whether the entry resolved to an Observo short code.
func (e LinkEntry) Tracked() bool { return e.Code != nil && *e.Code != "" }

// Emit writes the manifest as indented JSON with entries in a stable
// order (sorted by fq_name) so repeated builds of the same run produce
// byte-identical output — needed for golden tests and for diffing a
// committed manifest. Sorting is done in place; callers do not rely on
// insertion order after Emit.
func (m *LinkManifest) Emit(w io.Writer) error {
	sort.SliceStable(m.Entries, func(i, j int) bool {
		return m.Entries[i].FQName < m.Entries[j].FQName
	})
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}

// strPtr is a helper for building entries with a resolved code.
func strPtr(s string) *string { return &s }

// intPtr is a helper for building entries with an order.
func intPtr(i int) *int { return &i }
