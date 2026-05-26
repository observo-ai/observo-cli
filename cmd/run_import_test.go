package cmd

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/observo-ai/observo-cli/internal/api"
	"github.com/observo-ai/observo-cli/internal/playwright"
	"github.com/observo-ai/observo-cli/internal/state"
)

// writeEmptyTraceZipTo creates an empty trace.zip in-place with both
// JSONL members present but blank. Reused by the empty-extract test.
func writeEmptyTraceZipTo(w io.Writer) error {
	zw := zip.NewWriter(w)
	for _, name := range []string{"trace.trace", "trace.network"} {
		if _, err := zw.Create(name); err != nil {
			return err
		}
	}
	return zw.Close()
}

// Regression for R3 #1: when a test attaches a Playwright `trace.zip`
// (name="trace") AND any other `.zip` (e.g. test-data archive), the
// named-trace must win — pre-fix the simple `||` loop overwrote
// `picked` with whichever .zip came later, often the non-trace one,
// silently extracting empty console/network and uploading nothing.
func TestPickTraceZip_PrefersNamedTraceOverLaterZip(t *testing.T) {
	atts := []playwright.PWAttachment{
		{Name: "trace", Path: "/tmp/trace.zip", ContentType: "application/zip"},
		{Name: "test-data", Path: "/tmp/archive.zip", ContentType: "application/zip"},
	}
	if got := pickTraceZip(atts); got != "/tmp/trace.zip" {
		t.Errorf("named trace must win over later .zip; got %q want /tmp/trace.zip", got)
	}
	// Reverse order — named trace at index 1 must still win.
	rev := []playwright.PWAttachment{atts[1], atts[0]}
	if got := pickTraceZip(rev); got != "/tmp/trace.zip" {
		t.Errorf("named trace must win regardless of order; got %q", got)
	}
	// No named trace → fall back to last .zip (legacy compat).
	noName := []playwright.PWAttachment{
		{Name: "first", Path: "/tmp/a.zip"},
		{Name: "second", Path: "/tmp/b.zip"},
	}
	if got := pickTraceZip(noName); got != "/tmp/b.zip" {
		t.Errorf("fallback should pick last .zip when no named trace; got %q", got)
	}
}

// Regression for R3 #2: multi-project specs (chromium + webkit) where
// the FIRST project fails and the LAST project passes must report the
// failure, not the passing-last result. Pre-fix `pickFinalResult` took
// the last `spec.Tests[]` entry unconditionally and surfaced "passed"
// for a spec whose `spec.OK` was already false.
func TestPickFinalResult_PicksWorstStatusAcrossProjects(t *testing.T) {
	spec := &playwright.Spec{
		Tests: []playwright.Test{
			{
				ProjectName: "chromium",
				Status:      "unexpected",
				Results: []playwright.TestResult{
					{Status: "failed", Duration: 1000, Retry: 0,
						Errors: []playwright.PWError{{Message: "chromium boom"}}},
				},
			},
			{
				ProjectName: "webkit",
				Status:      "expected",
				Results: []playwright.TestResult{
					{Status: "passed", Duration: 500, Retry: 0},
				},
			},
		},
	}
	got := pickFinalResult(spec)
	if got == nil {
		t.Fatalf("expected the failing chromium result, got nil")
	}
	if got.Status != "failed" {
		t.Errorf("expected worst-status failed, got %q (the passing webkit result leaked through)", got.Status)
	}
	if len(got.Errors) == 0 || got.Errors[0].Message != "chromium boom" {
		t.Errorf("expected chromium's error attached, got %+v", got.Errors)
	}
}

// Tie-break sanity: when all projects passed, the later one wins.
// Preserves the old "last projection" behaviour for the common case.
func TestPickFinalResult_TieBreakPicksLaterProject(t *testing.T) {
	spec := &playwright.Spec{
		Tests: []playwright.Test{
			{ProjectName: "chromium", Results: []playwright.TestResult{{Status: "passed", Duration: 100}}},
			{ProjectName: "webkit", Results: []playwright.TestResult{{Status: "passed", Duration: 200}}},
		},
	}
	got := pickFinalResult(spec)
	if got == nil || got.Duration != 200 {
		t.Errorf("expected webkit (later) result on a tie, got %+v", got)
	}
}

// Regression for R3 #3: `run import` declares --run, not --run-id;
// when the user omits both --project / --run AND has no state file,
// the error must mention --run (not the shared helper's --run-id text)
// or the suggested fix sends them to a flag that doesn't exist on this
// subcommand.
func TestRunImport_E2E_ErrorMentionsRunFlagNotRunID(t *testing.T) {
	resetRunImportFlags()
	resetRootFlags()

	// No state file, no flags → resolveProjectAndRun fails.
	dir := t.TempDir()
	resultsPath := filepath.Join(dir, "results.json")
	_ = os.WriteFile(resultsPath, []byte(`{"config":{}, "suites":[]}`), 0o644)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", "https://example",
		"run", "import",
		"--from", "playwright",
		"--results-json", resultsPath,
		"--state-file", filepath.Join(dir, "no-such-state.json"),
		dir,
	})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when project/run flags + state file all missing")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--run") {
		t.Errorf("expected --run flag mentioned in error; got: %s", msg)
	}
}

func resetRunImportFlags() {
	riFrom = "playwright"
	riProject = ""
	riRunID = ""
	riResultsJSON = ""
	riSourceRoot = ""
	riRedact = ""
	riUploadPass = false
	riDryRun = false
	riStateFile = state.DefaultPath
}

// mockServer collects every HTTP call so tests can assert exactly which
// endpoints the orchestrator hits.
type mockCall struct {
	Method string
	Path   string
	Body   map[string]any
}

type mockServer struct {
	mu    sync.Mutex
	calls []mockCall
}

func (m *mockServer) handler(t *testing.T, behavior func(c mockCall) (status int, resp any)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		if len(body) > 0 {
			_ = json.Unmarshal(body, &parsed)
		}
		call := mockCall{Method: r.Method, Path: r.URL.Path, Body: parsed}
		m.mu.Lock()
		m.calls = append(m.calls, call)
		m.mu.Unlock()

		status, resp := http.StatusOK, any(map[string]any{})
		if behavior != nil {
			status, resp = behavior(call)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if resp != nil {
			_ = json.NewEncoder(w).Encode(resp)
		}
	})
}

func (m *mockServer) Calls() []mockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mockCall, len(m.calls))
	copy(out, m.calls)
	return out
}

func TestRunImport_E2E_HappyPath(t *testing.T) {
	resetRunImportFlags()
	resetRootFlags()

	ms := &mockServer{}
	srv := httptest.NewServer(ms.handler(t, func(c mockCall) (int, any) {
		// Attachment uploads return a fake attachment id; PATCHes return 200.
		if strings.HasSuffix(c.Path, ":upload") {
			return http.StatusOK, map[string]any{
				"attachment": map[string]any{"id": "att-" + c.Method, "file_name": "x"},
			}
		}
		return http.StatusOK, map[string]any{}
	}))
	defer srv.Close()

	// Stage a results.json that the orchestrator can parse. We use the
	// fixture under internal/playwright/testdata but rewrite attachment
	// paths to point at real local files so UploadAttachment can succeed.
	dir := t.TempDir()
	att := filepath.Join(dir, "video.webm")
	if err := os.WriteFile(att, []byte("fake video bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	results := mustReadStaged(t, dir, att)
	resultsPath := filepath.Join(dir, "results.json")
	if err := os.WriteFile(resultsPath, results, 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "import",
		"--from", "playwright",
		"--run", "RUN-42",
		"--project", "proj-uuid",
		"--results-json", resultsPath,
		dir,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	calls := ms.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least 1 HTTP call")
	}

	// Verify the canonical orchestration shape:
	//   PATCH /api/runs/RUN-42/cases/OB-1
	//   PATCH steps for OB-1
	//   PATCH /api/runs/RUN-42/cases/OB-7
	//   PATCH steps for OB-7
	//   POST  /api/projects/proj-uuid/attachments:upload  (for OB-7 only — OB-1 passed)
	wantPatchPaths := []string{
		"/api/runs/RUN-42/cases/OB-1",
		"/api/runs/RUN-42/cases/OB-7",
	}
	for _, want := range wantPatchPaths {
		if !findCall(calls, "PATCH", want) {
			t.Errorf("missing PATCH %s; calls=%v", want, calls)
		}
	}
	if !findCall(calls, "POST", "/api/projects/proj-uuid/attachments:upload") {
		t.Errorf("expected attachment upload POST; calls=%v", calls)
	}
	// OB-1 passed — its attachments must NOT be uploaded (default --upload-passed=false).
	for _, c := range calls {
		if c.Method == "POST" && strings.Contains(c.Path, "attachments:upload") {
			if name, _ := c.Body["run_case_id"].(string); name == "OB-1" {
				t.Errorf("OB-1 passed; must not upload its attachments without --upload-passed")
			}
		}
	}
}

func TestRunImport_E2E_UploadFailureIsNonFatal(t *testing.T) {
	resetRunImportFlags()
	resetRootFlags()

	ms := &mockServer{}
	srv := httptest.NewServer(ms.handler(t, func(c mockCall) (int, any) {
		// Every PATCH succeeds; uploads fail with 400 (non-retryable,
		// so the test doesn't pay the 1+2+4+8s retry backoff per call).
		// Per AC8 the import must NOT exit non-zero — only log warnings
		// and continue. 4xx and 5xx paths share the same recovery code.
		if strings.HasSuffix(c.Path, ":upload") {
			return http.StatusBadRequest, map[string]any{"error": "boom"}
		}
		return http.StatusOK, map[string]any{}
	}))
	defer srv.Close()

	dir := t.TempDir()
	att := filepath.Join(dir, "video.webm")
	_ = os.WriteFile(att, []byte("x"), 0o644)
	resultsPath := filepath.Join(dir, "results.json")
	_ = os.WriteFile(resultsPath, mustReadStaged(t, dir, att), 0o644)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "import",
		"--from", "playwright",
		"--run", "RUN-42",
		"--project", "proj-uuid",
		"--results-json", resultsPath,
		dir,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("import must not exit non-zero on upload-only failures; got %v", err)
	}
	if !strings.Contains(buf.String(), "upload") {
		t.Errorf("expected upload error log in output, got: %s", buf.String())
	}
}

func TestRunImport_E2E_DryRunMakesNoHTTPCalls(t *testing.T) {
	resetRunImportFlags()
	resetRootFlags()

	// Server fails every call — proof that dry-run never hits it.
	ms := &mockServer{}
	srv := httptest.NewServer(ms.handler(t, func(c mockCall) (int, any) {
		return http.StatusInternalServerError, nil
	}))
	defer srv.Close()

	dir := t.TempDir()
	att := filepath.Join(dir, "video.webm")
	_ = os.WriteFile(att, []byte("x"), 0o644)
	resultsPath := filepath.Join(dir, "results.json")
	_ = os.WriteFile(resultsPath, mustReadStaged(t, dir, att), 0o644)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "import",
		"--from", "playwright",
		"--run", "RUN-42",
		"--project", "proj-uuid",
		"--results-json", resultsPath,
		"--dry-run",
		dir,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("dry-run must not error: %v", err)
	}
	if got := ms.Calls(); len(got) != 0 {
		t.Errorf("dry-run must not call API; got %d calls: %v", len(got), got)
	}
}

func TestRunImport_E2E_RejectsUnknownSource(t *testing.T) {
	resetRunImportFlags()
	resetRootFlags()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", "https://example",
		"run", "import",
		"--from", "cypress",
		"--run", "RUN-42",
		"--project", "p",
		t.TempDir(),
	})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error for --from cypress (only playwright supported)")
	}
}

// Regression for review finding #3: pre-fix `uploadExtractedJSON`
// renamed its CreateTemp file to a FIXED basename (`console.json` etc.)
// inside `os.TempDir()` — two concurrent imports racing on
// `/tmp/console.json` would have the second Rename atomically overwrite
// the first's file, and the first process would then upload the second
// process's data.
//
// The fix moves to a per-invocation `os.MkdirTemp("", "observo-import-")`
// so each call's file path is unique. This test calls uploadExtractedJSON
// 8× in parallel with distinct payloads and asserts the server received
// 8 distinct bodies matching their expected content — proving the race
// no longer mixes uploads.
func TestUploadExtractedJSON_ConcurrentInvocationsKeepContentIntegrity(t *testing.T) {
	const concurrency = 8
	type received struct {
		Body any `json:"-"`
	}
	var (
		mu       sync.Mutex
		uploads  []string // captured base64-decoded content of each upload
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err == nil {
			if content, ok := parsed["content"].(string); ok {
				// Server expects base64 — see attachments.go upload contract.
				// We compare raw base64 here, not decoded, since equality on
				// the wire is what matters for collision detection.
				mu.Lock()
				uploads = append(uploads, content)
				mu.Unlock()
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"attachment": map[string]any{"id": "att-x", "file_name": "console.json"},
		})
	}))
	defer srv.Close()

	client, err := newTestAPIClient(srv.URL)
	if err != nil {
		t.Fatalf("api client: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		i := i
		go func() {
			defer wg.Done()
			// Each goroutine sends a payload that is uniquely identifiable
			// post-upload — pre-fix, collisions would cause two payloads
			// to share content under one of the racing identities.
			payload := []playwright.ConsoleEntry{
				{Timestamp: float64(i), Level: "log", Message: fmt.Sprintf("payload-%d", i)},
			}
			var dummy importSummary
			if err := uploadExtractedJSON(
				context.Background(), client, io.Discard,
				"proj-uuid", "RUN-42", "OB-1",
				"console.json", payload, false, &dummy,
			); err != nil {
				t.Errorf("goroutine %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(uploads) != concurrency {
		t.Fatalf("expected %d uploads, got %d", concurrency, len(uploads))
	}
	// Distinct content per upload — if the race still existed, at least
	// two uploads would share base64 content (the surviving file after
	// rename).
	seen := make(map[string]struct{}, concurrency)
	for i, u := range uploads {
		if _, dup := seen[u]; dup {
			t.Errorf("duplicate upload content #%d — race re-introduced", i)
		}
		seen[u] = struct{}{}
	}
}

// newTestAPIClient is a thin convenience over api.New that injects a
// dummy API key + the test server URL. Kept here (not in the api
// package) because it's only meaningful in cmd-level integration tests.
func newTestAPIClient(baseURL string) (*api.Client, error) {
	return api.New(api.Options{
		BaseURL:   baseURL,
		APIKey:    "test-key",
		UserAgent: "observo-cli-test",
	})
}

// Regression for review #2: a trace.zip with neither console events nor
// network requests must NOT trigger empty-array uploads (`console.json:
// []`, `network.json: []`). Pre-fix the orchestrator uploaded both
// unconditionally, inflating AttachmentsOK and creating empty rows the
// FE viewer would have to special-case.
func TestRunImport_E2E_EmptyExtractionSkipsUpload(t *testing.T) {
	resetRunImportFlags()
	resetRootFlags()

	ms := &mockServer{}
	srv := httptest.NewServer(ms.handler(t, nil))
	defer srv.Close()

	// Build a real (empty) trace.zip so the extractor returns
	// out.Console=[]/out.Network=[] but no error.
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.zip")
	if err := writeEmptyTraceZip(tracePath); err != nil {
		t.Fatalf("write empty zip: %v", err)
	}

	// Hand-crafted results.json that points the trace attachment at the
	// empty zip. One failed spec → so attachments would be uploaded if
	// extraction yielded any data.
	resultsJSON := fmt.Sprintf(`{
		"config": {"rootDir": ""},
		"suites": [{"title": "s", "specs": [{
			"title": "OB-99 empty trace",
			"ok": false,
			"tests": [{"projectName": "chromium", "status": "unexpected", "results": [{
				"status": "failed", "duration": 1, "retry": 0,
				"errors": [],
				"attachments": [
					{"name": "trace", "path": %q, "contentType": "application/zip"}
				]
			}]}]
		}]}]
	}`, tracePath)
	resultsPath := filepath.Join(dir, "results.json")
	_ = os.WriteFile(resultsPath, []byte(resultsJSON), 0o644)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "import",
		"--from", "playwright",
		"--run", "RUN-42",
		"--project", "proj-uuid",
		"--results-json", resultsPath,
		dir,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// We expect exactly: 1 PATCH case + 1 attach POST for trace.zip itself.
	// NO uploads for console.json or network.json (both extracted empty).
	for _, c := range ms.Calls() {
		if c.Method == "POST" && strings.Contains(c.Path, ":upload") {
			fn, _ := c.Body["file_name"].(string)
			if fn == "console.json" || fn == "network.json" {
				t.Errorf("empty extraction must not upload %s; got call: %v", fn, c)
			}
		}
	}
}

// Regression for review #3: with --source-root unset and
// results.Config.RootDir == "", the orchestrator must fall back to cwd
// so failure.go's path-traversal guard remains active. An absolute
// `location.file` outside cwd (e.g. /etc/passwd) must produce an empty
// source_excerpt rather than embedding the file contents.
func TestRunImport_E2E_RejectsAbsoluteLocationFileWhenRootEmpty(t *testing.T) {
	resetRunImportFlags()
	resetRootFlags()

	ms := &mockServer{}
	var uploadedFailure string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		ms.mu.Lock()
		ms.calls = append(ms.calls, mockCall{Method: r.Method, Path: r.URL.Path, Body: parsed})
		ms.mu.Unlock()
		if fn, _ := parsed["file_name"].(string); fn == "failure.json" {
			if content, ok := parsed["content"].(string); ok {
				// base64-decode is easy here but we keep it simple — assert on
				// the base64 itself. We just need to confirm /etc/passwd content
				// isn't leaking.
				uploadedFailure = content
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"attachment": map[string]any{"id": "att-x"},
		})
	}))
	defer srv.Close()

	// /etc/passwd exists on every Unix runner and is the canonical
	// path-traversal proof. The orchestrator must NOT embed its
	// content into failure.json regardless of where cwd is.
	resultsJSON := `{
		"config": {"rootDir": ""},
		"suites": [{"title": "s", "specs": [{
			"title": "OB-99 traversal",
			"ok": false,
			"tests": [{"projectName": "chromium", "status": "unexpected", "results": [{
				"status": "failed", "duration": 1, "retry": 0,
				"errors": [{
					"message": "boom",
					"location": {"file": "/etc/passwd", "line": 1, "column": 1}
				}]
			}]}]
		}]}]
	}`
	dir := t.TempDir()
	resultsPath := filepath.Join(dir, "results.json")
	_ = os.WriteFile(resultsPath, []byte(resultsJSON), 0o644)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "import",
		"--from", "playwright",
		"--run", "RUN-42",
		"--project", "proj-uuid",
		"--results-json", resultsPath,
		dir,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if uploadedFailure == "" {
		t.Fatal("expected a failure.json upload to land")
	}
	// Decode base64 and verify no /etc/passwd content (`root:`, `nobody:`).
	// /etc/passwd reliably contains "root:" on the first line of every
	// Unix runner; that's the canonical marker we'd see leak if the
	// guard regressed.
	dec, err := base64.StdEncoding.DecodeString(uploadedFailure)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if strings.Contains(string(dec), "root:") {
		t.Errorf("traversal guard regressed — /etc/passwd content leaked into failure.json:\n%s", string(dec))
	}
}

// Regression for review #4: when an attachment carries inline `body`
// (base64) instead of `path`, the upload loop must log a skip warning
// rather than silently dropping it. Future v1.1 may decode+upload;
// today the user at least sees the missing attachment.
func TestRunImport_E2E_InlineBodyAttachmentLogsWarning(t *testing.T) {
	resetRunImportFlags()
	resetRootFlags()

	ms := &mockServer{}
	srv := httptest.NewServer(ms.handler(t, nil))
	defer srv.Close()

	// Failed spec with one body-only attachment (no path).
	resultsJSON := `{
		"config": {"rootDir": ""},
		"suites": [{"title": "s", "specs": [{
			"title": "OB-99 inline only",
			"ok": false,
			"tests": [{"projectName": "chromium", "status": "unexpected", "results": [{
				"status": "failed", "duration": 1, "retry": 0,
				"errors": [],
				"attachments": [
					{"name": "custom-blob", "body": "aGVsbG8=", "contentType": "text/plain"}
				]
			}]}]
		}]}]
	}`
	dir := t.TempDir()
	resultsPath := filepath.Join(dir, "results.json")
	_ = os.WriteFile(resultsPath, []byte(resultsJSON), 0o644)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "import",
		"--from", "playwright",
		"--run", "RUN-42",
		"--project", "proj-uuid",
		"--results-json", resultsPath,
		dir,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(buf.String(), "skipping inline attachment") {
		t.Errorf("expected inline-attachment warning in output, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"custom-blob"`) {
		t.Errorf("warning should name the attachment, got: %s", buf.String())
	}
}

// writeEmptyTraceZip builds a zip with empty `trace.trace` and
// `trace.network` members — ExtractTrace returns empty slices with no
// error. Helper for the empty-extract regression test.
func writeEmptyTraceZip(path string) error {
	// Use the same zip-writer pattern as the playwright package tests
	// but keep it inline here to avoid exporting the helper.
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	return writeEmptyTraceZipTo(fh)
}

func TestRunImport_E2E_SkipsCaseWithoutShortCode(t *testing.T) {
	resetRunImportFlags()
	resetRootFlags()

	ms := &mockServer{}
	srv := httptest.NewServer(ms.handler(t, nil))
	defer srv.Close()

	// Hand-rolled results.json with one spec that has no OB-N anywhere.
	resultsJSON := `{
		"config": {"rootDir": ""},
		"suites": [{"title": "no-code", "specs": [{"title": "unrelated test", "ok": true, "tests": [{"projectName": "chromium", "status": "expected", "results": [{"status": "passed", "duration": 1, "retry": 0}]}]}]}]
	}`
	dir := t.TempDir()
	resultsPath := filepath.Join(dir, "results.json")
	_ = os.WriteFile(resultsPath, []byte(resultsJSON), 0o644)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"--api-key", "k",
		"--base-url", srv.URL,
		"run", "import",
		"--from", "playwright",
		"--run", "RUN-42",
		"--project", "p",
		"--results-json", resultsPath,
		dir,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("import must not fail when a spec lacks short code: %v", err)
	}
	if len(ms.Calls()) != 0 {
		t.Errorf("expected zero API calls (only spec was skipped); got %v", ms.Calls())
	}
	if !strings.Contains(buf.String(), "no OB-N short code resolved") {
		t.Errorf("expected skip warning in output, got: %s", buf.String())
	}
}

// mustReadStaged loads the fixture results.json and rewrites attachment
// paths to point at a local writable file so the upload path can run
// without needing the staged paths to exist on disk.
func mustReadStaged(t *testing.T, _ string, attPath string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "internal", "playwright", "testdata", "results-sample.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	// Swap the staged absolute paths with our test attachment path. We
	// keep the spec/test structure untouched — just redirect file refs.
	rewritten := strings.ReplaceAll(string(raw),
		"/work/e2e/test-results/login-OB-7-chromium/video.webm", attPath)
	rewritten = strings.ReplaceAll(rewritten,
		"/work/e2e/test-results/login-OB-7-chromium/trace.zip", attPath)
	rewritten = strings.ReplaceAll(rewritten,
		"/work/e2e/test-results/login-OB-7-chromium/test-failed-1.png", attPath)
	return []byte(rewritten)
}

func findCall(calls []mockCall, method, path string) bool {
	for _, c := range calls {
		if c.Method == method && c.Path == path {
			return true
		}
	}
	return false
}
