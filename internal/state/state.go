// Package state persists per-CI-job pipeline state across CLI invocations.
//
// The CI workflow runs many CLI commands in different jobs (`run create`
// in one job, `run pipeline-layer set` per layer, `run finish` last).
// Each is its own process — they need a shared place to read/write
// run_id + accumulated layer outcomes. We use a local file dropped at
// the workspace root by `run create`, picked up by everyone else.
//
// File path defaults to `.observo-pipeline-state.json` in the current
// working dir. CI templates upload this between jobs via `actions/upload-artifact`
// + `actions/download-artifact` (or a similar GitLab/Jenkins primitive).
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultPath is the conventional filename. Callers may override via flag.
const DefaultPath = ".observo-pipeline-state.json"

// State is the on-disk format. Field shape is stable across CLI versions
// — bumping requires a major release.
type State struct {
	RunID     string `json:"run_id"`
	RunURL    string `json:"run_url,omitempty"`
	ProjectID string `json:"project_id"`

	// Layers records the final pass/fail of each layer as it completes.
	// `run finish --status auto` derives run status from this map.
	// Empty map → status defaults to passed.
	Layers map[string]LayerState `json:"layers,omitempty"`
}

// LayerState is one entry in State.Layers.
type LayerState struct {
	Status string `json:"status"` // passed|failed|skipped|aborted
}

// ErrNotFound signals the state file does not exist yet — typically
// because `run create` hasn't run, or the file wasn't propagated between
// CI jobs. Distinct from a corrupt/unreadable file (returned as raw err).
var ErrNotFound = errors.New("pipeline state file not found")

// Load reads + decodes the file. Returns ErrNotFound when the file is
// absent so callers can decide whether that's fatal or expected.
func Load(path string) (*State, error) {
	if path == "" {
		path = DefaultPath
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return nil, fmt.Errorf("read state file %s: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("decode state file %s: %w", path, err)
	}
	return &s, nil
}

// Save writes s to path as pretty-printed JSON. Creates parent dirs as
// needed (useful when CI mounts the state file under a custom path).
// Atomicity: write to a temp file in the same dir, fsync, rename — so a
// crashed write doesn't leave a half-file that breaks the next Load.
func Save(path string, s *State) error {
	if path == "" {
		path = DefaultPath
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	// Append a newline so files compose cleanly with `cat` / commit hooks.
	b = append(b, '\n')

	tmp, err := os.CreateTemp(dir, ".observo-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once rename succeeds

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename to %s: %w", path, err)
	}
	return nil
}

// RecordLayer is a load-modify-save helper used by `run pipeline-layer set`
// to add/update a layer entry without race-prone manual JSON edits.
// Tolerates a missing file (creates one with just the layer entry).
// Caller is responsible for providing project_id + run_id if the file is
// new (otherwise leaves them empty for `run finish` to error on).
func RecordLayer(path, layerID, status string) error {
	s, err := Load(path)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		s = &State{}
	}
	if s.Layers == nil {
		s.Layers = make(map[string]LayerState, 4)
	}
	s.Layers[layerID] = LayerState{Status: status}
	return Save(path, s)
}

// DeriveStatus reduces accumulated layer outcomes to a single run status:
//   - any layer failed → "failed"
//   - any layer aborted → "aborted"  (precedence: aborted > failed > passed)
//   - all passed / skipped → "passed"
//   - empty / nil → "passed" (no layers recorded — degenerate but valid)
//
// This is the auto-status logic for `run finish --status auto`. Kept
// here (not in cmd/) so it's unit-testable in isolation.
func DeriveStatus(s *State) string {
	if s == nil || len(s.Layers) == 0 {
		return "passed"
	}
	hasAborted := false
	hasFailed := false
	for _, l := range s.Layers {
		switch l.Status {
		case "aborted":
			hasAborted = true
		case "failed":
			hasFailed = true
		}
	}
	switch {
	case hasAborted:
		return "aborted"
	case hasFailed:
		return "failed"
	default:
		return "passed"
	}
}
