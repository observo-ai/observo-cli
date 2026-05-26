package playwright

import (
	"encoding/json"
	"fmt"
	"io"
)

// Results is the parsed shape of Playwright's JSON reporter output
// (`reporter: [['json', { outputFile: 'results.json' }]]`). The schema
// is loosely documented at https://playwright.dev/docs/test-reporters
// and stable enough that we model the fields we need explicitly rather
// than carrying a generic map[string]any.
//
// Fields not consumed by `observo run import` are omitted on purpose —
// adding them later does not break compatibility (encoding/json
// silently ignores unknown keys).
type Results struct {
	Config struct {
		// RootDir is Playwright's notion of the test project root. We
		// use it as the default for source_excerpt path resolution if
		// the caller doesn't pass --source-root. May be absolute on CI.
		RootDir string `json:"rootDir,omitempty"`
	} `json:"config"`
	Suites []Suite `json:"suites"`
}

// Suite mirrors a Playwright suite node. Playwright recursively nests
// suites under suites (a top-level suite per file, then describe()
// blocks, then specs). We carry both Suites and Specs because the same
// suite node can have either or both.
type Suite struct {
	Title  string  `json:"title"`
	File   string  `json:"file,omitempty"`
	Suites []Suite `json:"suites,omitempty"`
	Specs  []Spec  `json:"specs,omitempty"`
}

// Spec is one `test('…', …)` declaration. Playwright projects the spec
// across each configured `projects[]` entry → that's the Tests slice.
// `ok` is the final-status rollup across all projects/retries.
type Spec struct {
	Title string   `json:"title"`
	OK    bool     `json:"ok"`
	Tags  []string `json:"tags,omitempty"`
	Tests []Test   `json:"tests"`
	File  string   `json:"file,omitempty"`
	Line  int      `json:"line,omitempty"`
}

// Test is the per-project projection of a spec. `results[]` is one
// entry per attempt (retry 0, retry 1, …) — Playwright re-runs failing
// tests N times where N = retries config.
type Test struct {
	ProjectName string       `json:"projectName,omitempty"`
	Results     []TestResult `json:"results"`
	// Status is the rolled-up final status:
	//   expected   = all attempts in line with `test.fail()` etc — usually all-pass
	//   unexpected = final attempt failed; debugger needs to look at it
	//   flaky      = passed on a retry after at least one fail
	//   skipped    = test.skip() / fixture skip
	Status string `json:"status,omitempty"`
}

// TestResult is one attempt of a test on one project. `results[len-1]`
// is the definitive outcome; earlier entries are retries we surface
// only to attribute trace.zip / video.webm to the right attempt.
type TestResult struct {
	WorkerIndex int             `json:"workerIndex,omitempty"`
	Status      string          `json:"status"` // passed | failed | timedOut | skipped | interrupted
	Duration    int64           `json:"duration,omitempty"` // ms
	Retry       int             `json:"retry"`
	StartTime   string          `json:"startTime,omitempty"`
	Errors      []PWError       `json:"errors,omitempty"`
	Attachments []PWAttachment  `json:"attachments,omitempty"`
	Steps       []TestStep      `json:"steps,omitempty"`
	Stdout      []OutputChunk   `json:"stdout,omitempty"`
	Stderr      []OutputChunk   `json:"stderr,omitempty"`
}

// PWAttachment is the JSON reporter's view of an attachment the test
// produced (video / trace / screenshot / custom). Either `Path` is set
// (local file the runner wrote) or `Body` is set (base64-encoded inline
// blob — Playwright emits this for small attachments).
type PWAttachment struct {
	Name        string `json:"name"`
	Path        string `json:"path,omitempty"`
	Body        string `json:"body,omitempty"` // base64
	ContentType string `json:"contentType,omitempty"`
}

// TestStep is a Playwright execution step. Category "test.step" is the
// user's own `await test.step('login', …)` call; other categories
// (`hook`, `fixture`, `expect`, `pw:api`) are framework noise.
//
// We mirror the live observo-reporter's rule: only top-level user steps
// (category == "test.step") map to Observo case.steps[]; nested ones
// are surfaced as "flatten in test code" warnings, not mid-tree
// flattened by the parser.
type TestStep struct {
	Title    string     `json:"title"`
	Category string     `json:"category"`
	Duration int64      `json:"duration,omitempty"`
	Error    *PWError   `json:"error,omitempty"`
	Steps    []TestStep `json:"steps,omitempty"`
}

// OutputChunk is one stdout/stderr write the test runner captured.
// Playwright emits one chunk per write(), so a long log appears as N
// chunks; we glue them together when summarising.
type OutputChunk struct {
	Text string `json:"text,omitempty"`
}

// ParseResults reads a Playwright JSON reporter file. We reject payloads
// missing the suites[] key entirely (likely the wrong file — JUnit XML
// renamed to .json, or a partial run that never wrote the array), but
// EMPTY `"suites": []` is accepted: that's the legitimate shape when
// `playwright test --grep <no-matches>` (or any pre-filter) selects
// zero tests. The orchestrator surfaces total_specs=0 in its summary
// so CI can gate on it explicitly.
func ParseResults(r io.Reader) (*Results, error) {
	var out Results
	dec := json.NewDecoder(r)
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("decode playwright results: %w", err)
	}
	if out.Suites == nil {
		return nil, fmt.Errorf("results.json: missing suites[] — is this a Playwright JSON reporter file?")
	}
	return &out, nil
}

// IterTests walks the suite tree and invokes fn for every leaf Spec
// found, passing the chain of parent suite titles down so callers can
// build "describe » describe » test" titles for short-code resolution.
//
// Walking depth-first keeps spec order matching the source file order
// Playwright reported, which is the order CI logs will show — easier
// to correlate dashboard line-by-line with the local terminal.
func (r *Results) IterTests(fn func(parentTitles []string, spec *Spec)) {
	for i := range r.Suites {
		walkSuite(&r.Suites[i], nil, fn)
	}
}

func walkSuite(s *Suite, parents []string, fn func([]string, *Spec)) {
	// Append the current suite title to the parent chain only when it's
	// non-empty; root suites in Playwright sometimes have "" title.
	chain := parents
	if s.Title != "" {
		chain = append(append([]string(nil), parents...), s.Title)
	}
	for i := range s.Specs {
		fn(chain, &s.Specs[i])
	}
	for i := range s.Suites {
		walkSuite(&s.Suites[i], chain, fn)
	}
}

// FinalResult returns the last TestResult in t.Results (the definitive
// attempt) or nil if results[] is empty (skipped tests). Callers should
// nil-guard before reading status / errors / attachments.
func (t *Test) FinalResult() *TestResult {
	if len(t.Results) == 0 {
		return nil
	}
	return &t.Results[len(t.Results)-1]
}

// MapStatus converts a Playwright `TestResult.status` to the Observo
// `test_case_result_status_enum`. Mirrors the live reporter's mapping
// (e2e/reporters/observo-reporter.ts:82): timedOut + interrupted are
// "blocked" so the dashboard can distinguish infra-level failures from
// real assertion failures.
func MapStatus(pw string) string {
	switch pw {
	case "passed":
		return "passed"
	case "failed":
		return "failed"
	case "skipped":
		return "skipped"
	case "timedOut", "interrupted":
		return "blocked"
	default:
		return "blocked"
	}
}
