package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func resetPlanResolveFlags() {
	prProject = ""
	prPlan = ""
	prFormat = "codes"
}

// fakePlanServer answers GET /api/projects/{p}/plans/{k} with the given cases.
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
			_ = json.NewEncoder(w).Encode(map[string]any{
				"plan": map[string]any{
					"id":       planUUID,
					"plan_key": "REGR-MAIN-CI",
					"cases":    cases,
				},
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
	want := `@observo:(OB\-1|OB\-2|OB\-3)` + "\n"
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
	if r != `@observo:(OB\-1)` {
		t.Errorf("got %q", r)
	}
}
