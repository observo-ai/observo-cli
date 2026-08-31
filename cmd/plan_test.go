package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resetPlanResolveFlags() {
	prProject = ""
	prPlan = ""
	prFormat = "codes"
}

// fakePlanServer answers BOTH list (/api/projects/{p}/plans) and get
// (/api/projects/{p}/plans/{uuid}) — the CLI's `plan resolve` now does
// list-then-get when the input is a plan_key (server's GET-by-key
// returns 400 per the OB-257 UUID-only limitation).
func fakePlanServer(t *testing.T, cases []map[string]string) *httptest.Server {
	t.Helper()
	const planUUID = "11111111-2222-3333-4444-555555555555"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected method: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/plans"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"plans": []map[string]any{
					{"id": planUUID, "plan_key": "REGR-MAIN-CI"},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/plans/"+planUUID):
			// `cases` is a SIBLING of `plan`, which is what GetTestPlan
			// actually sends. This fake used to nest it inside `plan`, so
			// every test below passed against a response no server produces
			// while the real one resolved to an empty plan (OB-852).
			_ = json.NewEncoder(w).Encode(map[string]any{
				"plan": map[string]any{
					"id":       planUUID,
					"plan_key": "REGR-MAIN-CI",
				},
				"cases": cases,
			})
		default:
			t.Errorf("unexpected req: %s %s", r.Method, r.URL.Path)
		}
	}))
}

func TestPlanResolve_CodesFormat_NewlineDelimited(t *testing.T) {
	resetRootFlags()
	resetPlanResolveFlags()

	srv := fakePlanServer(t, []map[string]string{
		{"short_code": "OB-50", "title": "Login"},
		{"short_code": "OB-51", "title": "Reset password"},
	})
	defer srv.Close()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"plan", "resolve",
		"--project", "OB",
		"--plan", "REGR-MAIN-CI",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "OB-50\nOB-51\n"
	if buf.String() != want {
		t.Errorf("output:\n got: %q\nwant: %q", buf.String(), want)
	}
}

func TestPlanResolve_GrepFormat_PlaywrightRegex(t *testing.T) {
	resetRootFlags()
	resetPlanResolveFlags()

	srv := fakePlanServer(t, []map[string]string{
		{"short_code": "OB-1"}, {"short_code": "OB-2"}, {"short_code": "OB-3"},
	})
	defer srv.Close()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"plan", "resolve",
		"--project", "OB",
		"--plan", "REGR-MAIN-CI",
		"--format", "grep",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := `@observo:(OB\-1|OB\-2|OB\-3)(?![0-9])` + "\n"
	if buf.String() != want {
		t.Errorf("output:\n got: %q\nwant: %q", buf.String(), want)
	}
}

func TestPlanResolve_EmptyPlan_GrepEmitsNeverMatchSentinel(t *testing.T) {
	resetRootFlags()
	resetPlanResolveFlags()

	srv := fakePlanServer(t, nil)
	defer srv.Close()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"plan", "resolve",
		"--project", "OB",
		"--plan", "REGR-MAIN-CI",
		"--format", "grep",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Empty grep would match everything in Playwright; sentinel matches nothing.
	if !strings.Contains(buf.String(), "NEVER_MATCH") {
		t.Errorf("empty plan in grep mode should emit sentinel; got %q", buf.String())
	}
}

func TestPlanResolve_EmptyPlan_CodesEmitsNothing(t *testing.T) {
	resetRootFlags()
	resetPlanResolveFlags()

	srv := fakePlanServer(t, nil)
	defer srv.Close()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"plan", "resolve",
		"--project", "OB",
		"--plan", "REGR-MAIN-CI",
		// format defaults to codes
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if buf.String() != "" {
		t.Errorf("empty plan in codes mode should emit nothing; got %q", buf.String())
	}
}

func TestPlanResolve_JSONFormat_EmitsFullPlan(t *testing.T) {
	resetRootFlags()
	resetPlanResolveFlags()

	srv := fakePlanServer(t, []map[string]string{
		{"short_code": "OB-1", "title": "Login"},
	})
	defer srv.Close()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"plan", "resolve",
		"--project", "OB",
		"--plan", "REGR-MAIN-CI",
		"--format", "json",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Parseable JSON with expected fields
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, buf.String())
	}
	if got["plan_key"] != "REGR-MAIN-CI" {
		t.Errorf("plan_key missing/wrong: %+v", got)
	}
}

func TestPlanResolve_InvalidFormatRejected(t *testing.T) {
	resetRootFlags()
	resetPlanResolveFlags()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", "https://example",
		"plan", "resolve",
		"--project", "OB",
		"--plan", "REGR-MAIN-CI",
		"--format", "xml",
	})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error for --format=xml")
	}
}

func TestBuildGrepRegex_EscapesDashes(t *testing.T) {
	r := buildGrepRegex([]string{"OB-1"})
	if r != `@observo:(OB\-1)(?![0-9])` {
		t.Errorf("got %q", r)
	}
}

// A short code must not select a longer code that starts with it. REGR-MAIN-CI
// holds OB-1 and OB-5 while the suite carries OB-171/172/173 and OB-50..OB-59,
// so an unbounded alternation runs specs the plan never attached — and the run
// created from that plan has no case for their results to land on.
//
// Asserted as a string, not by compiling it: Go's regexp is RE2 and has no
// lookahead, while Playwright builds the filter with JS `new RegExp`, which
// does. The expectations below were checked against the JS engine.
func TestBuildGrepRegex_CodeDoesNotMatchALongerCode(t *testing.T) {
	got := buildGrepRegex([]string{"OB-1", "OB-5"})
	want := `@observo:(OB\-1|OB\-5)(?![0-9])`
	if got != want {
		t.Fatalf("regex:\n got: %q\nwant: %q", got, want)
	}
	// What that boundary buys, spelled out so the intent survives a rewrite:
	// under JS RegExp.test, "@observo:OB-171" and "@observo:OB-53" do NOT match
	// this pattern, while "@observo:OB-1" and "@observo:OB-5" do.
	if !strings.HasSuffix(got, "(?![0-9])") {
		t.Error("the alternation must be followed by a non-digit boundary")
	}
}

// The whole chain the CI step depends on, driven by a verbatim capture of a
// real GetTestPlan response body: bytes off the wire → short codes → the
// Playwright --grep the workflow passes on.
//
// Shares the fixture with internal/api rather than copying it, so the two
// layers can never drift onto different ideas of what the server sends.
func TestPlanResolve_GrepFromCapturedServerResponse(t *testing.T) {
	resetRootFlags()
	resetPlanResolveFlags()

	body, err := os.ReadFile(filepath.Join("..", "internal", "api", "testdata", "plan_get_response.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	const planUUID = "11111111-2222-3333-4444-555555555555"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/plans") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"plans": []map[string]any{{"id": planUUID, "plan_key": "REGR-MAIN-CI"}},
			})
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"plan", "resolve",
		"--project", "OB",
		"--plan", "REGR-MAIN-CI",
		"--format", "grep",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := `@observo:(JFHYPGZZ\-1|JFHYPGZZ\-2)(?![0-9])` + "\n"
	if buf.String() != want {
		t.Errorf("grep:\n got: %q\nwant: %q", buf.String(), want)
	}
	if strings.Contains(buf.String(), "NEVER_MATCH") {
		t.Error("a plan with cases must not resolve to the empty-plan sentinel")
	}
}

// The headline contract of OB-852, asserted where a user meets it: cases
// without short codes make `plan resolve` FAIL, and print no filter at all.
//
// Pinned here and not only at the api layer because the tempting "friendlier"
// change lives here — catching the error and printing the sentinel, or
// skipping blank codes — and either one silently restores the behaviour this
// ticket exists to delete, with every api-layer test still green.
func TestPlanResolve_CasesWithoutShortCodes_FailsInsteadOfEmittingASentinel(t *testing.T) {
	resetRootFlags()
	resetPlanResolveFlags()

	body, err := os.ReadFile(filepath.Join("..", "internal", "api", "testdata", "plan_get_response_pre_ob852.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	const planUUID = "11111111-2222-3333-4444-555555555555"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/plans") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"plans": []map[string]any{{"id": planUUID, "plan_key": "REGR-MAIN-CI"}},
			})
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"plan", "resolve",
		"--project", "OB",
		"--plan", "REGR-MAIN-CI",
		"--format", "grep",
	})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected a non-zero exit; a plan whose cases have no code is not an empty plan")
	}
	// The sentinel would be read by CI as "nobody attached a case", sending the
	// reader to the dashboard instead of to the server that broke.
	if strings.Contains(buf.String(), "NEVER_MATCH") {
		t.Errorf("must not emit the empty-plan sentinel on a contract failure; got %q", buf.String())
	}
	if strings.Contains(buf.String(), "@observo:") {
		t.Errorf("must not emit a filter it could not build; got %q", buf.String())
	}
}
