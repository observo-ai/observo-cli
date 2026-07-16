package jvm

import "testing"

func TestMerge_TestNGJoinWithAllureMetadata(t *testing.T) {
	testng := []LinkEntry{
		{Code: strPtr("PD-101"), FQName: "api.pd.PdBackendE2ETest#traderCreatesWallet",
			DisplayName: "traderCreatesWallet", // bare method name (TestNG default)
			ChainID:     "api.pd.PdBackendE2ETest", Order: intPtr(1),
			Result: &Result{Status: StatusPass, DurationMs: 120}},
	}
	allure := []LinkEntry{
		{FQName: "api.pd.PdBackendE2ETest#traderCreatesWallet",
			DisplayName: "trader creates a wallet", // friendlier
			Description: "Wallet is created on first login",
			Feature:     "PD Wallet", Story: "Onboarding",
			Result: &Result{Status: StatusPass}},
	}

	out := Merge(testng, allure)
	if len(out) != 1 {
		t.Fatalf("expected 1 merged entry, got %d", len(out))
	}
	e := out[0]
	// TestNG-owned fields preserved.
	if *e.Code != "PD-101" || e.ChainID != "api.pd.PdBackendE2ETest" || e.Order == nil || *e.Order != 1 {
		t.Errorf("TestNG join/chain not preserved: %+v", e)
	}
	if e.Result.DurationMs != 120 {
		t.Errorf("TestNG duration clobbered: %d", e.Result.DurationMs)
	}
	// Allure metadata filled in.
	if e.DisplayName != "trader creates a wallet" {
		t.Errorf("want Allure friendly name, got %q", e.DisplayName)
	}
	if e.Feature != "PD Wallet" || e.Story != "Onboarding" || e.Description == "" {
		t.Errorf("Allure metadata not merged: %+v", e)
	}
}

func TestMerge_AllureOnlyEntryAppended(t *testing.T) {
	testng := []LinkEntry{
		{Code: strPtr("PD-1"), FQName: "a.A#x"},
	}
	allure := []LinkEntry{
		{Code: strPtr("PD-2"), FQName: "b.B#y", Feature: "F"}, // no TestNG match
	}
	out := Merge(testng, allure)
	if len(out) != 2 {
		t.Fatalf("expected 2 entries (1 base + 1 appended), got %d", len(out))
	}
	findByFQ(t, out, "b.B#y") // must be present
}

func TestMerge_TwoAllureOnlySameFQCoalesce(t *testing.T) {
	// Regression: byFQ must index appended entries, so two Allure-only
	// entries sharing an fq_name coalesce into one instead of producing a
	// divergent duplicate row.
	var testng []LinkEntry
	allure := []LinkEntry{
		{Code: strPtr("PD-9"), FQName: "z.Z#p", Feature: "F"},
		{Code: strPtr("PD-9"), FQName: "z.Z#p", Description: "D"},
	}
	out := Merge(testng, allure)
	if len(out) != 1 {
		t.Fatalf("two same-fq Allure entries must coalesce, got %d", len(out))
	}
	if out[0].Feature != "F" || out[0].Description != "D" {
		t.Errorf("coalesced entry lost fields: %+v", out[0])
	}
}

func TestMerge_DoesNotClobberExistingMetadata(t *testing.T) {
	testng := []LinkEntry{
		{Code: strPtr("PD-1"), FQName: "a.A#x", DisplayName: "Nice Name", Feature: "TestNGFeature"},
	}
	allure := []LinkEntry{
		{FQName: "a.A#x", DisplayName: "other", Feature: "AllureFeature"},
	}
	out := Merge(testng, allure)
	e := out[0]
	if e.DisplayName != "Nice Name" {
		t.Errorf("display name should not be clobbered when non-default: %q", e.DisplayName)
	}
	if e.Feature != "TestNGFeature" {
		t.Errorf("feature should not be clobbered: %q", e.Feature)
	}
}
