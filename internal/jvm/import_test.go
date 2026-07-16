package jvm

import "testing"

// chainManifest builds a manifest with a 3-method chain (2 tracked, 1
// untracked) plus one standalone tracked case.
func chainManifest() *LinkManifest {
	o1, o2, o3 := 1, 2, 3
	return &LinkManifest{
		Version: 1,
		Entries: []LinkEntry{
			{Code: strPtr("PD-101"), FQName: "api.pd.Chain#a", DisplayName: "a", Feature: "PD Wallet", ChainID: "api.pd.Chain", Order: &o1},
			{Code: strPtr("PD-102"), FQName: "api.pd.Chain#b", DisplayName: "b", Feature: "PD Wallet", ChainID: "api.pd.Chain", Order: &o2},
			{Code: nil, FQName: "api.pd.Chain#c", DisplayName: "c", Feature: "PD Wallet", ChainID: "api.pd.Chain", Order: &o3},
			{Code: strPtr("PD-200"), FQName: "api.Health#ok", DisplayName: "ok", Feature: "Platform"},
		},
	}
}

func TestPlanImport_Flat(t *testing.T) {
	plan, err := PlanImport(chainManifest(), ImportOptions{ChainMode: ChainFlat})
	if err != nil {
		t.Fatal(err)
	}
	// Flat: 3 chain cases + 1 standalone = 4 cases.
	if len(plan.Cases) != 4 {
		t.Fatalf("flat: want 4 cases, got %d", len(plan.Cases))
	}
	// One plan preserving chain order.
	if len(plan.Plans) != 1 {
		t.Fatalf("flat: want 1 plan, got %d", len(plan.Plans))
	}
	pp := plan.Plans[0]
	want := []string{"api.pd.Chain#a", "api.pd.Chain#b", "api.pd.Chain#c"}
	if len(pp.CaseFQNames) != 3 || pp.CaseFQNames[0] != want[0] || pp.CaseFQNames[2] != want[2] {
		t.Errorf("plan order wrong: %v", pp.CaseFQNames)
	}
	if pp.Key != "CHAIN-CHAIN" {
		t.Errorf("plan key = %q, want CHAIN-CHAIN", pp.Key)
	}
	// Features deduped (PD Wallet once + Platform).
	if len(plan.Features) != 2 {
		t.Errorf("features = %v, want 2 distinct", plan.Features)
	}
	// Tracked flags: PD-101/102/200 tracked, chain#c untracked.
	var untracked int
	for _, c := range plan.Cases {
		if !c.Tracked {
			untracked++
		}
	}
	if untracked != 1 {
		t.Errorf("want 1 untracked case, got %d", untracked)
	}
}

func TestPlanImport_Steps(t *testing.T) {
	plan, err := PlanImport(chainManifest(), ImportOptions{ChainMode: ChainSteps})
	if err != nil {
		t.Fatal(err)
	}
	// Steps: chain → 1 case with 3 steps; standalone → 1 case = 2 cases total.
	if len(plan.Cases) != 2 {
		t.Fatalf("steps: want 2 cases, got %d: %+v", len(plan.Cases), plan.Cases)
	}
	// No plan in steps mode.
	if len(plan.Plans) != 0 {
		t.Errorf("steps: want 0 plans, got %d", len(plan.Plans))
	}
	// Find the chain case (has steps).
	var chain *PlannedCase
	for i := range plan.Cases {
		if len(plan.Cases[i].Steps) > 0 {
			chain = &plan.Cases[i]
		}
	}
	if chain == nil {
		t.Fatal("no steps-mode chain case found")
	}
	if len(chain.Steps) != 3 {
		t.Fatalf("chain case should have 3 steps, got %d", len(chain.Steps))
	}
	// Steps ordered a,b,c.
	if chain.Steps[0].Title != "a" || chain.Steps[2].Title != "c" {
		t.Errorf("steps not ordered: %+v", chain.Steps)
	}
	if chain.Name != "PD Wallet" { // feature wins over class name
		t.Errorf("chain case name = %q, want PD Wallet", chain.Name)
	}
	// Chain case is created new (not tracked) in steps mode.
	if chain.Tracked {
		t.Error("steps-mode chain case must not be marked tracked")
	}
}

func TestPlanImport_ChainOfOneIsJustACase(t *testing.T) {
	o1 := 1
	m := &LinkManifest{Entries: []LinkEntry{
		{Code: strPtr("PD-1"), FQName: "a.A#only", DisplayName: "only", ChainID: "a.A", Order: &o1},
	}}
	// Even in steps mode, a single-method "chain" is a plain case (no steps).
	plan, err := PlanImport(m, ImportOptions{ChainMode: ChainSteps})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Cases) != 1 || len(plan.Cases[0].Steps) != 0 {
		t.Errorf("chain-of-one should be a plain case: %+v", plan.Cases)
	}
	if !plan.Cases[0].Tracked {
		t.Error("chain-of-one tracked flag lost")
	}
}

func TestPlanImport_DedupsFQName(t *testing.T) {
	m := &LinkManifest{Entries: []LinkEntry{
		{Code: strPtr("PD-1"), FQName: "a#b", DisplayName: "x"},
		{Code: strPtr("PD-1"), FQName: "a#b", DisplayName: "x-dup"}, // same fqName
	}}
	plan, err := PlanImport(m, ImportOptions{ChainMode: ChainFlat})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Cases) != 1 {
		t.Errorf("duplicate fqName should be guarded to 1 case, got %d", len(plan.Cases))
	}
}

func TestPlanImport_InvalidMode(t *testing.T) {
	if _, err := PlanImport(&LinkManifest{}, ImportOptions{ChainMode: "weird"}); err == nil {
		t.Error("expected error for invalid chain mode")
	}
}
