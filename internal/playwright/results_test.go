package playwright

import (
	"os"
	"strings"
	"testing"
)

func TestParseResults_Golden(t *testing.T) {
	fh, err := os.Open("testdata/results-sample.json")
	if err != nil {
		t.Fatalf("open testdata: %v", err)
	}
	defer fh.Close()

	r, err := ParseResults(fh)
	if err != nil {
		t.Fatalf("ParseResults: %v", err)
	}
	if r.Config.RootDir != "/work/e2e" {
		t.Errorf("RootDir mismatch: got %q", r.Config.RootDir)
	}
	if len(r.Suites) != 2 {
		t.Fatalf("expected 2 top-level suites, got %d", len(r.Suites))
	}

	// IterTests should visit every leaf spec exactly once.
	var visited []string
	r.IterTests(func(parents []string, spec *Spec) {
		visited = append(visited, strings.Join(parents, " » ")+" » "+spec.Title)
	})
	if len(visited) != 2 {
		t.Fatalf("expected 2 specs visited, got %d: %v", len(visited), visited)
	}
}

func TestParseResults_MissingSuitesRoot(t *testing.T) {
	if _, err := ParseResults(strings.NewReader(`{"config":{}}`)); err == nil {
		t.Fatal("expected error for results.json without suites[]")
	}
}

func TestParseResults_MalformedJSON(t *testing.T) {
	if _, err := ParseResults(strings.NewReader(`{ not json`)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestFinalResult(t *testing.T) {
	tt := Test{Results: []TestResult{{Status: "failed", Retry: 0}, {Status: "passed", Retry: 1}}}
	got := tt.FinalResult()
	if got == nil || got.Status != "passed" || got.Retry != 1 {
		t.Errorf("FinalResult: got %+v, want last retry=1 passed", got)
	}

	empty := Test{}
	if empty.FinalResult() != nil {
		t.Error("FinalResult on empty Results must return nil")
	}
}

func TestMapStatus(t *testing.T) {
	cases := map[string]string{
		"passed":      "passed",
		"failed":      "failed",
		"skipped":     "skipped",
		"timedOut":    "blocked",
		"interrupted": "blocked",
		"":            "blocked", // unknown → defensive blocked
		"weird":       "blocked",
	}
	for in, want := range cases {
		if got := MapStatus(in); got != want {
			t.Errorf("MapStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIterTests_RetainsParentChain(t *testing.T) {
	fh, err := os.Open("testdata/results-sample.json")
	if err != nil {
		t.Fatalf("open testdata: %v", err)
	}
	defer fh.Close()

	r, err := ParseResults(fh)
	if err != nil {
		t.Fatalf("ParseResults: %v", err)
	}

	var foundOB1, foundOB7 bool
	r.IterTests(func(parents []string, spec *Spec) {
		// short-code resolver expects parents[] + spec.Title for the
		// title-fallback branch — assert that combo finds the codes.
		// Tags first, then titles. The OB-1 spec has tag; the OB-7 spec
		// has the code in the title only.
		titles := append([]string(nil), parents...)
		titles = append(titles, spec.Title)
		code := ResolveShortCode(spec.Tags, titles, nil)
		switch code {
		case "OB-1":
			foundOB1 = true
		case "OB-7":
			foundOB7 = true
		}
	})
	if !foundOB1 || !foundOB7 {
		t.Errorf("expected to resolve both OB-1 (tag) and OB-7 (title), got foundOB1=%v foundOB7=%v",
			foundOB1, foundOB7)
	}
}
