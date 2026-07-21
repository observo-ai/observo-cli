package jvm

import (
	"strings"
	"testing"
)

// OB-551: Allure link extraction into references. @Issue → ticket, @Link
// (custom) → link, @TmsLink stays code-resolution-only (never a reference).
func TestToEntry_ExtractsReferences(t *testing.T) {
	const result = `{
      "name": "trader creates a wallet",
      "fullName": "api.pd.WalletTest.createsWallet",
      "status": "passed",
      "labels": [
        {"name": "testClass", "value": "api.pd.WalletTest"},
        {"name": "testMethod", "value": "createsWallet"},
        {"name": "tag", "value": "observo:PD-101"}
      ],
      "links": [
        {"name": "PD-742", "type": "issue", "url": "https://jira/PD-742"},
        {"name": "design doc", "type": "custom", "url": "https://docs/x"},
        {"name": "TMS-9", "type": "tms"}
      ]
    }`

	e, err := decodeAllure(strings.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}

	if !e.Tracked() || *e.Code != "PD-101" {
		t.Fatalf("want code PD-101 from the @Tag, got %v", e.Code)
	}
	if len(e.References) != 2 {
		t.Fatalf("want 2 references (issue+custom, tms excluded), got %d: %+v", len(e.References), e.References)
	}

	ticket := e.References[0]
	if ticket.Kind != RefKindTicket || ticket.Value != "PD-742" || ticket.URL != "https://jira/PD-742" {
		t.Errorf("ticket ref wrong: %+v", ticket)
	}
	link := e.References[1]
	if link.Kind != RefKindLink || link.Value != "https://docs/x" || link.Label != "design doc" {
		t.Errorf("link ref wrong: %+v", link)
	}
	// The tms link is the join key, never a reference.
	for _, r := range e.References {
		if r.Value == "TMS-9" {
			t.Errorf("tms link must not become a reference: %+v", r)
		}
	}
}

// A test with no issue/link labels yields no references — nothing invented (AC2).
func TestToEntry_NoReferencesWhenNoLinks(t *testing.T) {
	const result = `{
      "name": "bare",
      "fullName": "api.BareTest.bare",
      "status": "passed",
      "labels": [{"name": "testClass", "value": "api.BareTest"}, {"name": "testMethod", "value": "bare"}]
    }`
	e, err := decodeAllure(strings.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	if len(e.References) != 0 {
		t.Errorf("want no references, got %+v", e.References)
	}
}
