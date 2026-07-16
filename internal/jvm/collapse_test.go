package jvm

import (
	"strings"
	"testing"
)

func TestCollapseByFQName_ParametrizedIntoOne(t *testing.T) {
	// Three invocations of one JUnit5 @ParameterizedTest → one entry.
	entries := []LinkEntry{
		{Code: strPtr("PD-500"), FQName: "a.P#run", DisplayName: "run", Result: &Result{Status: StatusPass, DurationMs: 10}},
		{Code: strPtr("PD-500"), FQName: "a.P#run", Result: &Result{Status: StatusFail, DurationMs: 20}},
		{Code: strPtr("PD-500"), FQName: "a.P#run", Result: &Result{Status: StatusPass, DurationMs: 5}},
	}
	out := collapseByFQName(entries)
	if len(out) != 1 {
		t.Fatalf("expected 1 collapsed entry, got %d", len(out))
	}
	e := out[0]
	if e.Result.Status != StatusFail {
		t.Errorf("aggregate status: any FAIL wins, got %s", e.Result.Status)
	}
	if e.Result.DurationMs != 35 {
		t.Errorf("durations should sum to 35, got %d", e.Result.DurationMs)
	}
	// And it no longer trips duplicate detection (was the HIGH finding).
	if err := DetectDuplicates(out); err != nil {
		t.Errorf("collapsed data-driven test must not be a duplicate: %v", err)
	}
}

func TestCollapseByFQName_EmptyFQNameStaysDistinct(t *testing.T) {
	// Two distinct tests that both fail to yield an fq_name must NOT merge
	// (AC4: never silently drop a test).
	entries := []LinkEntry{
		{Code: strPtr("PD-1"), FQName: ""},
		{Code: strPtr("PD-2"), FQName: ""},
	}
	out := collapseByFQName(entries)
	if len(out) != 2 {
		t.Fatalf("empty-fq entries must stay distinct, got %d", len(out))
	}
}

func TestAggregateResult(t *testing.T) {
	cases := []struct {
		a, b *Result
		want string
	}{
		{nil, &Result{Status: StatusPass}, StatusPass},
		{&Result{Status: StatusPass}, nil, StatusPass},
		{&Result{Status: StatusPass}, &Result{Status: StatusFail}, StatusFail},
		{&Result{Status: StatusFail}, &Result{Status: StatusPass}, StatusFail},
		{&Result{Status: StatusSkip}, &Result{Status: StatusPass}, StatusPass},
		{&Result{Status: StatusSkip}, &Result{Status: StatusSkip}, StatusSkip},
	}
	for _, tc := range cases {
		got := aggregateResult(tc.a, tc.b)
		if got.Status != tc.want {
			t.Errorf("aggregateResult(%v,%v).Status = %s, want %s", tc.a, tc.b, got.Status, tc.want)
		}
	}
}

// TestParseTestNGResults_DataDrivenCollapsed is the HIGH-finding regression:
// a data-driven method (repeated <test-method name=…>) collapses to one
// logical test, is NOT flagged as an order-dependent chain, and does not
// trip duplicate detection.
func TestParseTestNGResults_DataDrivenCollapsed(t *testing.T) {
	const xml = `<?xml version="1.0"?>
<testng-results>
  <suite name="s">
    <groups>
      <group name="observo:PD-500">
        <method name="depositAmounts" class="api.pd.PdDataDriven"/>
      </group>
    </groups>
    <test name="t">
      <class name="api.pd.PdDataDriven">
        <test-method status="PASS" name="depositAmounts" started-at="2026-07-15T10:00:01Z" duration-ms="10"/>
        <test-method status="FAIL" name="depositAmounts" started-at="2026-07-15T10:00:02Z" duration-ms="20"/>
        <test-method status="PASS" name="depositAmounts" started-at="2026-07-15T10:00:03Z" duration-ms="5"/>
      </class>
    </test>
  </suite>
</testng-results>`
	entries, err := decodeTestNG(strings.NewReader(xml))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("data-driven method must collapse to 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if !e.Tracked() || *e.Code != "PD-500" {
		t.Errorf("code = %v, want PD-500", e.Code)
	}
	if e.Result.Status != StatusFail || e.Result.DurationMs != 35 {
		t.Errorf("aggregate result = %+v, want FAIL/35ms", e.Result)
	}
	// A single (collapsed) method is NOT a chain.
	if e.ChainID != "" || e.Order != nil {
		t.Errorf("single data-driven method must not be a chain: chain=%q order=%v", e.ChainID, e.Order)
	}
	if err := DetectDuplicates(entries); err != nil {
		t.Errorf("collapsed data-driven test wrongly flagged duplicate: %v", err)
	}
}
