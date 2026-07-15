package jvm

import (
	"strings"
	"testing"
)

func findByFQ(t *testing.T, entries []LinkEntry, fq string) LinkEntry {
	t.Helper()
	for _, e := range entries {
		if e.FQName == fq {
			return e
		}
	}
	t.Fatalf("no entry with fq_name %q in %d entries", fq, len(entries))
	return LinkEntry{}
}

func TestParseAllureResults(t *testing.T) {
	entries, err := ParseAllureResults("testdata/allure-results")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}

	// JUnit5 @Tag surfaces as a `tag` label → tracked.
	health := findByFQ(t, entries, "api.HealthTest#healthReturns200")
	if !health.Tracked() || *health.Code != "PD-200" {
		t.Errorf("health: want code PD-200, got %v", health.Code)
	}
	if health.Feature != "Platform" {
		t.Errorf("health: want feature Platform, got %q", health.Feature)
	}
	if health.Result == nil || health.Result.Status != StatusPass {
		t.Errorf("health: want PASS result, got %+v", health.Result)
	}
	if health.Result.DurationMs != 30 {
		t.Errorf("health: want duration 30ms, got %d", health.Result.DurationMs)
	}

	// @TmsLink fallback join.
	legacy := findByFQ(t, entries, "api.LegacyTest#legacyCase")
	if !legacy.Tracked() || *legacy.Code != "PD-300" {
		t.Errorf("legacy: want code PD-300 via tms link, got %v", legacy.Code)
	}
	if legacy.Result == nil || legacy.Result.Status != StatusSkip {
		t.Errorf("legacy: want SKIP, got %+v", legacy.Result)
	}

	// broken/failed mapping + description carried.
	deposit := findByFQ(t, entries, "api.pd.PdBackendE2ETest#traderDeposits")
	if deposit.Result == nil || deposit.Result.Status != StatusFail {
		t.Errorf("deposit: want FAIL, got %+v", deposit.Result)
	}
	if !strings.Contains(deposit.Description, "live-verified") {
		t.Errorf("deposit: description not carried: %q", deposit.Description)
	}

	// Untracked test kept with nil code (AC4), not dropped.
	probe := findByFQ(t, entries, "api.pd.PdBackendE2ETest#untaggedProbe")
	if probe.Tracked() {
		t.Errorf("probe: expected untracked, got code %v", probe.Code)
	}
}

func TestMapAllureStatus(t *testing.T) {
	cases := map[string]string{
		"passed": StatusPass, "failed": StatusFail, "broken": StatusFail,
		"skipped": StatusSkip, "unknown": StatusSkip, "": StatusSkip,
	}
	for in, want := range cases {
		if got := mapAllureStatus(in); got != want {
			t.Errorf("mapAllureStatus(%q) = %q, want %q", in, got, want)
		}
	}
}
