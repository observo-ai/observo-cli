// Package junit parses common JUnit XML dialects (Surefire / gotestsum /
// Vitest / Playwright) into a single Aggregates struct.
//
// Why we don't import a third-party junit lib: the universe of dialect
// quirks is large; existing libs hard-code one dialect each. We only need
// the per-file totals — a ~50-line streaming parser handles every dialect
// we ship with (Vitest, gotestsum, Playwright) without external deps.
package junit

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Aggregates is the per-file roll-up used to populate the PipelineLayer
// shape sent to the server.
type Aggregates struct {
	Total      int   `json:"total"`
	Passed     int   `json:"passed"`
	Failed     int   `json:"failed"`
	Skipped    int   `json:"skipped"`
	Flaky      int   `json:"flaky"`       // approximated; many dialects don't track
	DurationMs int64 `json:"duration_ms"` // sum of testcase time attrs in ms
}

// Outcome reduces Aggregates to a single layer status for the state file
// (used by `run finish --status auto`):
//   - failed > 0 → "failed"
//   - else if all skipped → "skipped"
//   - else → "passed"
func (a Aggregates) Outcome() string {
	if a.Failed > 0 {
		return "failed"
	}
	if a.Total > 0 && a.Skipped == a.Total {
		return "skipped"
	}
	return "passed"
}

// Parse reads junit XML from r and returns aggregates across ALL testcases.
// Tolerates either <testsuites> root or a bare <testsuite> root.
//
// Empty <testsuite tests="0"/> (no <testcase> children) is treated as a
// successful zero-test run, NOT an error. Vitest emits this when all
// tests in a file are .skip()'d; gotestsum emits it when a -run regex
// matches nothing. Pre-fix this failed the entire CI step ("no
// <testcase> elements found") on legitimate empty runs. Now returns
// Aggregates{} and lets the caller (state file recorder) treat the
// layer as a no-op.
//
// We still error on structurally invalid XML — that's a real problem
// the operator needs to know about.
func Parse(r io.Reader) (*Aggregates, error) {
	dec := xml.NewDecoder(r)
	agg := &Aggregates{}

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("junit parse: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		switch start.Name.Local {
		case "testcase":
			tc, err := decodeTestcase(dec, start)
			if err != nil {
				return nil, err
			}
			agg.Total++
			switch tc.outcome {
			case "failed":
				agg.Failed++
			case "skipped":
				agg.Skipped++
			default:
				agg.Passed++
			}
			agg.DurationMs += tc.durationMs
		}
	}

	return agg, nil
}

type testcase struct {
	outcome    string // passed|failed|skipped
	durationMs int64
}

// decodeTestcase consumes the children of a <testcase> start element and
// inspects the time attribute + child tags to determine outcome.
// <failure>/<error> children → failed; <skipped> → skipped; none → passed.
func decodeTestcase(dec *xml.Decoder, start xml.StartElement) (*testcase, error) {
	tc := &testcase{outcome: "passed"}

	for _, attr := range start.Attr {
		if attr.Name.Local == "time" {
			tc.durationMs = parseDurationMs(attr.Value)
		}
	}

	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("junit parse: malformed testcase: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "failure", "error":
				tc.outcome = "failed"
			case "skipped":
				if tc.outcome == "passed" { // failure has precedence over skip
					tc.outcome = "skipped"
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	return tc, nil
}

// parseDurationMs converts a JUnit time string (seconds, possibly
// fractional) into milliseconds. Tolerates blank/garbage by returning 0
// rather than erroring — a missing time attr should NOT fail the whole
// parse, since some dialects (Playwright old versions) omit it.
func parseDurationMs(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	secs, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(secs) || math.IsInf(secs, 0) || secs < 0 {
		return 0
	}
	return int64(secs * 1000)
}
