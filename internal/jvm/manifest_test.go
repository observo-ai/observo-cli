package jvm

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestEmit_UntrackedCodeIsNull is the OB-543 AC4 test: a test without a
// join key appears in the manifest with "code": null, never dropped.
func TestEmit_UntrackedCodeIsNull(t *testing.T) {
	m := LinkManifest{
		Version: ManifestVersion,
		Entries: []LinkEntry{
			{Code: strPtr("PD-1"), FQName: "a.A#one", DisplayName: "one"},
			{Code: nil, FQName: "a.A#two", DisplayName: "two"}, // untracked
		},
	}
	var buf bytes.Buffer
	if err := m.Emit(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `"code": null`) {
		t.Errorf("untracked entry must emit `\"code\": null`, got:\n%s", out)
	}

	// Structurally: both entries survive, and the untracked one's code
	// unmarshals back to a JSON null (nil pointer).
	var got LinkManifest
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got.Entries))
	}
	for _, e := range got.Entries {
		if e.FQName == "a.A#two" && e.Code != nil {
			t.Errorf("untracked entry round-tripped with non-nil code %v", e.Code)
		}
	}
}

func TestEmit_StableOrdering(t *testing.T) {
	// Same entries, different insertion order → identical bytes.
	a := LinkManifest{Version: 1, Entries: []LinkEntry{
		{Code: strPtr("PD-2"), FQName: "z.Z#b"},
		{Code: strPtr("PD-1"), FQName: "a.A#a"},
	}}
	b := LinkManifest{Version: 1, Entries: []LinkEntry{
		{Code: strPtr("PD-1"), FQName: "a.A#a"},
		{Code: strPtr("PD-2"), FQName: "z.Z#b"},
	}}
	var ba, bb bytes.Buffer
	if err := a.Emit(&ba); err != nil {
		t.Fatal(err)
	}
	if err := b.Emit(&bb); err != nil {
		t.Fatal(err)
	}
	if ba.String() != bb.String() {
		t.Errorf("Emit not order-stable:\n--- a ---\n%s\n--- b ---\n%s", ba.String(), bb.String())
	}
}

func TestTracked(t *testing.T) {
	empty := ""
	cases := []struct {
		e    LinkEntry
		want bool
	}{
		{LinkEntry{Code: strPtr("PD-1")}, true},
		{LinkEntry{Code: nil}, false},
		{LinkEntry{Code: &empty}, false}, // pointer to "" is still untracked
	}
	for _, tc := range cases {
		if got := tc.e.Tracked(); got != tc.want {
			t.Errorf("Tracked(%v) = %v, want %v", tc.e.Code, got, tc.want)
		}
	}
}
