package jvm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildManifest_MergedClientShape(t *testing.T) {
	// The pilot client's shape: TestNG groups join + Allure metadata.
	m, err := BuildManifest(BuildOptions{
		AllureDir:     "testdata/allure-results",
		TestNGResults: "testdata/testng-results.xml",
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != ManifestVersion {
		t.Errorf("version = %d, want %d", m.Version, ManifestVersion)
	}
	if m.Framework != "testng" {
		t.Errorf("framework = %q, want testng (auto from testng-results)", m.Framework)
	}

	// traderCreatesWallet: TestNG join (PD-101) + Allure metadata merged.
	wallet := findByFQ(t, m.Entries, "api.pd.PdBackendE2ETest#traderCreatesWallet")
	if !wallet.Tracked() || *wallet.Code != "PD-101" {
		t.Errorf("wallet code = %v, want PD-101", wallet.Code)
	}
	if wallet.DisplayName != "trader creates a wallet" || wallet.Feature != "PD Wallet" {
		t.Errorf("wallet metadata not merged from Allure: %+v", wallet)
	}
	if wallet.ChainID == "" || wallet.Order == nil {
		t.Errorf("wallet chain modeling lost: chain=%q order=%v", wallet.ChainID, wallet.Order)
	}
}

func TestBuildManifest_AllureOnly(t *testing.T) {
	m, err := BuildManifest(BuildOptions{AllureDir: "testdata/allure-results"})
	if err != nil {
		t.Fatal(err)
	}
	// 5 allure files → 5 entries; PD-200 (tag) and PD-300 (tms) tracked.
	if len(m.Entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(m.Entries))
	}
	health := findByFQ(t, m.Entries, "api.HealthTest#healthReturns200")
	if !health.Tracked() || *health.Code != "PD-200" {
		t.Errorf("health code = %v, want PD-200", health.Code)
	}
	// Allure alone can't tell testng from junit5 → framework blank.
	if m.Framework != "" {
		t.Errorf("framework = %q, want empty for allure-only", m.Framework)
	}
}

func TestBuildManifest_NoSource(t *testing.T) {
	_, err := BuildManifest(BuildOptions{})
	if !errors.Is(err, ErrNoSource) {
		t.Errorf("expected ErrNoSource, got %v", err)
	}
}

func TestBuildManifest_TestNGOnlyZeroMethodsIsEmptyRunNotNoSource(t *testing.T) {
	// A valid testng-results.xml with only config methods yields zero
	// entries. Because the user DID pass --testng-results, this is an empty
	// run (valid empty manifest), NOT a "no report source" error.
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "testng-results.xml")
	const xml = `<?xml version="1.0"?>
<testng-results><suite name="s"><test name="t"><class name="a.A">
  <test-method status="PASS" name="setUp" is-config="true" started-at="x" duration-ms="1"/>
</class></test></suite></testng-results>`
	if err := os.WriteFile(xmlPath, []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := BuildManifest(BuildOptions{TestNGResults: xmlPath})
	if err != nil {
		t.Fatalf("empty testng run must not error, got: %v", err)
	}
	if len(m.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(m.Entries))
	}
	// framework must NOT be stamped "testng" when TestNG contributed nothing.
	if m.Framework != "" {
		t.Errorf("framework = %q, want empty when TestNG contributed no entries", m.Framework)
	}
}

func TestBuildManifest_EmptyRunEmitsArrayNotNull(t *testing.T) {
	// Entries must serialize as [] not null for an empty run.
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "t.xml")
	if err := os.WriteFile(xmlPath, []byte(`<testng-results/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := BuildManifest(BuildOptions{TestNGResults: xmlPath})
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := m.Emit(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"entries": []`) {
		t.Errorf("empty run must emit entries: [], got:\n%s", buf.String())
	}
}

func TestBuildManifest_MissingAllureDirErrors(t *testing.T) {
	// A mistyped --from path must fail loud, not yield an empty manifest.
	_, err := BuildManifest(BuildOptions{AllureDir: "testdata/does-not-exist"})
	if err == nil {
		t.Fatal("expected error for non-existent allure dir, got nil")
	}
	if !strings.Contains(err.Error(), "not a readable directory") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildManifest_FrameworkOverride(t *testing.T) {
	m, err := BuildManifest(BuildOptions{AllureDir: "testdata/allure-results", Framework: "junit5"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Framework != "junit5" {
		t.Errorf("framework override = %q, want junit5", m.Framework)
	}
}
