package junit

import (
	"strings"
	"testing"
)

const gotestsumSample = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites tests="3" failures="1" errors="0" skipped="0">
  <testsuite name="pkg/foo" tests="3" failures="1" errors="0" skipped="0" time="0.4">
    <testcase classname="pkg/foo" name="TestA" time="0.100"/>
    <testcase classname="pkg/foo" name="TestB" time="0.200">
      <failure type="AssertionError" message="expected 1 got 2">stack trace here</failure>
    </testcase>
    <testcase classname="pkg/foo" name="TestC" time="0.1">
      <skipped/>
    </testcase>
  </testsuite>
</testsuites>`

func TestParse_GotestsumDialect(t *testing.T) {
	agg, err := Parse(strings.NewReader(gotestsumSample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if agg.Total != 3 || agg.Passed != 1 || agg.Failed != 1 || agg.Skipped != 1 {
		t.Errorf("counts: %+v", agg)
	}
	// 100 + 200 + 100 ≈ 400ms (float rounding may give 399 in some Go versions)
	if agg.DurationMs < 395 || agg.DurationMs > 405 {
		t.Errorf("duration: got %d ms", agg.DurationMs)
	}
}

const flatTestsuiteSample = `<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="Vitest" tests="2" time="1.5">
  <testcase name="passes" time="0.5"/>
  <testcase name="errors out" time="1.0">
    <error message="boom"/>
  </testcase>
</testsuite>`

func TestParse_FlatTestsuiteDialect_ErrorCountsAsFailed(t *testing.T) {
	agg, err := Parse(strings.NewReader(flatTestsuiteSample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if agg.Total != 2 || agg.Passed != 1 || agg.Failed != 1 {
		t.Errorf("counts: %+v", agg)
	}
	if agg.DurationMs != 1500 {
		t.Errorf("duration: %d ms", agg.DurationMs)
	}
}

const noTimeAttrSample = `<?xml version="1.0"?>
<testsuite>
  <testcase name="no time attr"/>
  <testcase name="garbage time" time="not-a-number"/>
</testsuite>`

func TestParse_MissingOrGarbageTimeAttr_DoesNotErrorOrCount(t *testing.T) {
	agg, err := Parse(strings.NewReader(noTimeAttrSample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if agg.Total != 2 || agg.DurationMs != 0 {
		t.Errorf("counts/duration: %+v", agg)
	}
}

func TestParse_FailureTakesPrecedenceOverSkipped(t *testing.T) {
	x := `<testsuite><testcase name="weird"><failure/><skipped/></testcase></testsuite>`
	agg, err := Parse(strings.NewReader(x))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if agg.Failed != 1 || agg.Skipped != 0 {
		t.Errorf("expected failed=1 skipped=0; got %+v", agg)
	}
}

func TestParse_NoTestcasesIsZeroAggregate(t *testing.T) {
	// Vitest with all-skipped files, gotestsum with non-matching -run
	// regex, Playwright with empty grep, etc. all emit <testsuite/>
	// without children. Pre-fix this errored, failing CI for a
	// legitimately empty run.
	x := `<testsuite tests="0"></testsuite>`
	agg, err := Parse(strings.NewReader(x))
	if err != nil {
		t.Fatalf("empty testsuite should NOT error; got: %v", err)
	}
	if agg.Total != 0 || agg.Passed != 0 || agg.Failed != 0 {
		t.Errorf("zero-test run aggregate: %+v", agg)
	}
	if agg.Outcome() != "passed" {
		t.Errorf("zero-test Outcome should be 'passed'; got %q", agg.Outcome())
	}
}

func TestParse_MalformedXMLErrors(t *testing.T) {
	x := `<testsuite><testcase`
	_, err := Parse(strings.NewReader(x))
	if err == nil {
		t.Fatal("expected error on malformed XML")
	}
}

func TestOutcome(t *testing.T) {
	tests := []struct {
		name string
		a    Aggregates
		want string
	}{
		{"passed", Aggregates{Total: 3, Passed: 3}, "passed"},
		{"failed dominates", Aggregates{Total: 3, Passed: 2, Failed: 1}, "failed"},
		{"all skipped", Aggregates{Total: 2, Skipped: 2}, "skipped"},
		{"mixed pass+skip = passed", Aggregates{Total: 3, Passed: 2, Skipped: 1}, "passed"},
		{"empty = passed", Aggregates{}, "passed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Outcome(); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}
