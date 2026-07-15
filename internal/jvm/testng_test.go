package jvm

import "testing"

func TestParseTestNGResults(t *testing.T) {
	entries, err := ParseTestNGResults("testdata/testng-results.xml")
	if err != nil {
		t.Fatal(err)
	}

	// Config methods (setUp/tearDown, is-config=true) are excluded; 3 real
	// test methods remain.
	if len(entries) != 3 {
		t.Fatalf("expected 3 test methods, got %d: %+v", len(entries), entries)
	}

	// Groups join: observo:PD-101 → traderCreatesWallet; the plain "smoke"
	// group is ignored.
	wallet := findByFQ(t, entries, "api.pd.PdBackendE2ETest#traderCreatesWallet")
	if !wallet.Tracked() || *wallet.Code != "PD-101" {
		t.Errorf("wallet: want PD-101, got %v", wallet.Code)
	}
	if wallet.Result == nil || wallet.Result.Status != StatusPass || wallet.Result.DurationMs != 120 {
		t.Errorf("wallet: want PASS/120ms, got %+v", wallet.Result)
	}

	deposit := findByFQ(t, entries, "api.pd.PdBackendE2ETest#traderDeposits")
	if !deposit.Tracked() || *deposit.Code != "PD-102" {
		t.Errorf("deposit: want PD-102, got %v", deposit.Code)
	}
	if deposit.Result == nil || deposit.Result.Status != StatusFail {
		t.Errorf("deposit: want FAIL, got %+v", deposit.Result)
	}

	// Untracked method kept with nil code (AC4).
	probe := findByFQ(t, entries, "api.pd.PdBackendE2ETest#untaggedProbe")
	if probe.Tracked() {
		t.Errorf("probe: expected untracked, got %v", probe.Code)
	}

	// Chain modeling: class has ≥2 methods → chain_id set, order by
	// started-at (wallet=1, deposit=2, probe=3).
	if wallet.ChainID != "api.pd.PdBackendE2ETest" || wallet.Order == nil || *wallet.Order != 1 {
		t.Errorf("wallet: want chain order 1, got chain=%q order=%v", wallet.ChainID, wallet.Order)
	}
	if deposit.Order == nil || *deposit.Order != 2 {
		t.Errorf("deposit: want order 2, got %v", deposit.Order)
	}
	if probe.Order == nil || *probe.Order != 3 {
		t.Errorf("probe: want order 3, got %v", probe.Order)
	}
}

func TestMapTestNGStatus(t *testing.T) {
	cases := map[string]string{
		"PASS": StatusPass, "FAIL": StatusFail, "SKIP": StatusSkip,
		"pass": StatusPass, "weird": StatusSkip, "": StatusSkip,
	}
	for in, want := range cases {
		if got := mapTestNGStatus(in); got != want {
			t.Errorf("mapTestNGStatus(%q) = %q, want %q", in, got, want)
		}
	}
}
