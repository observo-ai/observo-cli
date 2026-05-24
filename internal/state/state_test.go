package state

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".observo-pipeline-state.json")

	s := &State{
		RunID:     "abc-123",
		RunURL:    "https://app.observoai.co/run/abc-123",
		ProjectID: "OB",
		Layers: map[string]LayerState{
			"server-unit":   {Status: "passed"},
			"frontend-unit": {Status: "failed"},
		},
	}
	if err := Save(path, s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.RunID != s.RunID || loaded.ProjectID != s.ProjectID {
		t.Errorf("metadata round-trip: %+v", loaded)
	}
	if loaded.Layers["frontend-unit"].Status != "failed" {
		t.Errorf("layers round-trip: %+v", loaded.Layers)
	}
}

func TestLoad_NotFoundReturnsErrNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSave_AtomicityViaRename(t *testing.T) {
	// Sanity: Save should not leave temp files around if rename succeeds.
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := Save(path, &State{RunID: "x"}); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".observo-state-*.tmp"))
	if len(matches) != 0 {
		t.Errorf("temp files leaked: %v", matches)
	}
}

func TestSave_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c", "state.json")
	if err := Save(deep, &State{RunID: "x"}); err != nil {
		t.Fatalf("Save in nested dir: %v", err)
	}
	if _, err := Load(deep); err != nil {
		t.Fatalf("Load after Save in nested dir: %v", err)
	}
}

func TestDeriveStatus(t *testing.T) {
	tests := []struct {
		name string
		s    *State
		want string
	}{
		{"nil", nil, "passed"},
		{"empty layers", &State{}, "passed"},
		{"all passed", &State{Layers: map[string]LayerState{"a": {Status: "passed"}, "b": {Status: "passed"}}}, "passed"},
		{"one failed", &State{Layers: map[string]LayerState{"a": {Status: "passed"}, "b": {Status: "failed"}}}, "failed"},
		{"aborted dominates failed", &State{Layers: map[string]LayerState{"a": {Status: "failed"}, "b": {Status: "aborted"}}}, "aborted"},
		{"skipped counts as passed", &State{Layers: map[string]LayerState{"a": {Status: "skipped"}, "b": {Status: "passed"}}}, "passed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveStatus(tc.s); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}
