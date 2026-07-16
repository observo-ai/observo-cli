package jvm

import (
	"errors"
	"strings"
	"testing"
)

// TestDetectDuplicates_FailLoudWithBothLocations is the OB-543 AC2 test:
// two tests sharing a code fail with BOTH fq_names named, not "last wins".
func TestDetectDuplicates_FailLoudWithBothLocations(t *testing.T) {
	entries := []LinkEntry{
		{Code: strPtr("PD-1"), FQName: "a.A#one"},
		{Code: strPtr("PD-1"), FQName: "b.B#copy"},
		{Code: strPtr("PD-2"), FQName: "c.C#unique"},
	}
	err := DetectDuplicates(entries)
	if err == nil {
		t.Fatal("expected a duplicate error, got nil")
	}
	var de *DuplicateError
	if !errors.As(err, &de) {
		t.Fatalf("expected *DuplicateError, got %T", err)
	}
	fqs := de.Collisions["PD-1"]
	if len(fqs) != 2 {
		t.Fatalf("PD-1 should list 2 locations, got %v", fqs)
	}
	msg := err.Error()
	for _, want := range []string{"PD-1", "a.A#one", "b.B#copy"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q:\n%s", want, msg)
		}
	}
	// PD-2 is unique — must not be reported.
	if _, bad := de.Collisions["PD-2"]; bad {
		t.Errorf("unique code PD-2 wrongly flagged as duplicate")
	}
}

func TestDetectDuplicates_UntrackedShareNoCode(t *testing.T) {
	// Multiple untracked entries (code=null) are not a collision.
	entries := []LinkEntry{
		{Code: nil, FQName: "a.A#one"},
		{Code: nil, FQName: "b.B#two"},
		{Code: strPtr("PD-1"), FQName: "c.C#tagged"},
	}
	if err := DetectDuplicates(entries); err != nil {
		t.Errorf("untracked entries must not collide, got: %v", err)
	}
}

func TestDetectDuplicates_AllUnique(t *testing.T) {
	entries := []LinkEntry{
		{Code: strPtr("PD-1"), FQName: "a.A#one"},
		{Code: strPtr("PD-2"), FQName: "b.B#two"},
	}
	if err := DetectDuplicates(entries); err != nil {
		t.Errorf("expected nil for unique codes, got: %v", err)
	}
}
