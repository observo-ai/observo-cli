package cmd

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func resetPushFlags() {
	jpManifest, jpFrom, jpTestNG, jpRun, jpPlan, jpProject, jpDryRun = "", "", "", "", "", "", false
	jpStateFile = "/nonexistent-state.json" // avoid picking up a real state file
}

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "observo-link-manifest.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestJVMPush_WritebackStatuses_FailNeverReportedPass(t *testing.T) {
	resetPushFlags()
	var mu sync.Mutex
	patched := map[string]string{} // code → status
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" && strings.Contains(r.URL.Path, "/cases/") {
			var b struct {
				Status string `json:"status"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			code := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			mu.Lock()
			patched[code] = b.Status
			mu.Unlock()
			w.Write([]byte(`{}`))
			return
		}
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 500)
	}))
	defer srv.Close()

	mf := writeManifest(t, `{"version":1,"entries":[
		{"code":"PD-101","fq_name":"a.A#one","result":{"status":"PASS"}},
		{"code":"PD-102","fq_name":"a.A#two","result":{"status":"FAIL"}},
		{"code":"PD-103","fq_name":"a.A#three","result":{"status":"SKIP"}},
		{"code":null,"fq_name":"a.A#untracked","result":{"status":"PASS"}}
	]}`)

	out, err := executeRoot(t, "jvm", "push", "--manifest", mf, "--run", "RUN-1",
		"--project", "PD", "--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if patched["PD-101"] != "passed" || patched["PD-102"] != "failed" || patched["PD-103"] != "skipped" {
		t.Errorf("status mapping wrong: %v", patched)
	}
	// FAIL must never be reported as passed.
	if patched["PD-102"] == "passed" {
		t.Error("FAIL reported as passed")
	}
	// Untracked case must not be patched.
	if _, ok := patched["a.A#untracked"]; ok {
		t.Error("untracked entry was patched")
	}
	if len(patched) != 3 {
		t.Errorf("expected 3 cases patched, got %d: %v", len(patched), patched)
	}
}

func TestJVMPush_HTTPEvidenceUploaded(t *testing.T) {
	resetPushFlags()
	// allure dir: one tagged case with split request/response attachments.
	dir := t.TempDir()
	mustWrite := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("r-attachment.txt", "POST /wallet HTTP/1.1")
	mustWrite("s-attachment.txt", "HTTP/1.1 201 Created")
	mustWrite("a-result.json", `{"name":"one","fullName":"a.A.one","status":"passed","start":0,"stop":10,
		"labels":[{"name":"tag","value":"observo:PD-101"},{"name":"testClass","value":"a.A"},{"name":"testMethod","value":"one"}],
		"attachments":[{"name":"Request","source":"r-attachment.txt"},{"name":"Response","source":"s-attachment.txt"}]}`)

	var uploadedContent string
	var uploadedCase string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "PATCH" && strings.Contains(r.URL.Path, "/cases/"):
			w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/attachments:upload"):
			var b struct {
				RunCaseID string `json:"run_case_id"`
				FileName  string `json:"file_name"`
				Content   string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b.FileName == "http-evidence.json" {
				raw, _ := base64.StdEncoding.DecodeString(b.Content)
				uploadedContent = string(raw)
				uploadedCase = b.RunCaseID
			}
			w.Write([]byte(`{"attachment":{"id":"att-1"}}`))
		default:
			http.Error(w, "unexpected "+r.URL.Path, 500)
		}
	}))
	defer srv.Close()

	out, err := executeRoot(t, "jvm", "push", "--from", dir, "--run", "RUN-1",
		"--project", "PD", "--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if uploadedCase != "PD-101" {
		t.Errorf("evidence attached to wrong case: %q", uploadedCase)
	}
	for _, want := range []string{`"method": "POST"`, `"path": "/wallet"`, `"status": 201`, `"captured_at"`} {
		if !strings.Contains(uploadedContent, want) {
			t.Errorf("http-evidence.json missing %q:\n%s", want, uploadedContent)
		}
	}
}

func TestJVMPush_DryRunCallsNoWriteAPIs(t *testing.T) {
	resetPushFlags()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("dry-run must not call the API: %s %s", r.Method, r.URL.Path)
		http.Error(w, "no", 500)
	}))
	defer srv.Close()
	mf := writeManifest(t, `{"version":1,"entries":[{"code":"PD-1","fq_name":"a#b","result":{"status":"PASS"}}]}`)

	out, err := executeRoot(t, "jvm", "push", "--manifest", mf, "--run", "RUN-1",
		"--project", "PD", "--dry-run", "--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
}

func TestJVMPush_RequiresSourceAndTarget(t *testing.T) {
	resetPushFlags()
	// No source.
	if _, err := executeRoot(t, "jvm", "push", "--run", "RUN-1", "--project", "PD",
		"--api-key", "k", "--dry-run"); err == nil {
		t.Error("expected error with no result source")
	}
	// Source but no target.
	resetPushFlags()
	mf := writeManifest(t, `{"version":1,"entries":[]}`)
	if _, err := executeRoot(t, "jvm", "push", "--manifest", mf, "--project", "PD",
		"--api-key", "k", "--dry-run"); err == nil {
		t.Error("expected error with no run target")
	}
	// --run and --plan mutually exclusive.
	resetPushFlags()
	if _, err := executeRoot(t, "jvm", "push", "--manifest", mf, "--run", "RUN-1",
		"--plan", "REGR", "--project", "PD", "--api-key", "k", "--dry-run"); err == nil {
		t.Error("expected error when both --run and --plan set")
	}
}

func TestJVMPush_EvidenceRequiresProject(t *testing.T) {
	resetPushFlags()
	// The documented headline example: --from + --run, NO --project, no
	// state file. Evidence upload needs a project, so this must fail loud
	// up front rather than silently uploading zero evidence and exiting 0.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a-result.json"),
		[]byte(`{"fullName":"a.A.one","status":"passed","labels":[{"name":"tag","value":"observo:PD-1"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := executeRoot(t, "jvm", "push", "--from", dir, "--run", "RUN-1",
		"--api-key", "k", "--dry-run")
	if err == nil {
		t.Fatal("expected error when evidence upload has no project")
	}
	if !strings.Contains(err.Error(), "requires a project") {
		t.Errorf("unexpected error: %v", err)
	}

	// Status-only push (--manifest, no evidence source) must NOT require a
	// project.
	resetPushFlags()
	mf := writeManifest(t, `{"version":1,"entries":[{"code":"PD-1","fq_name":"a#b","result":{"status":"PASS"}}]}`)
	if _, err := executeRoot(t, "jvm", "push", "--manifest", mf, "--run", "RUN-1",
		"--api-key", "k", "--dry-run"); err != nil {
		t.Errorf("status-only push should not require project: %v", err)
	}
}

func TestJVMPush_RunTargetFromStateFile(t *testing.T) {
	resetPushFlags()
	// State file (written by `run create`) carries run_id + project_id;
	// push with neither --run nor --plan must resolve the run from it.
	sf := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(sf, []byte(`{"run_id":"RUN-77","project_id":"PD"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var patchedRun string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" && strings.Contains(r.URL.Path, "/cases/") {
			patchedRun = strings.Split(r.URL.Path, "/")[3] // /api/runs/{run}/cases/{code}
			w.Write([]byte(`{}`))
			return
		}
		http.Error(w, "unexpected "+r.URL.Path, 500)
	}))
	defer srv.Close()

	mf := writeManifest(t, `{"version":1,"entries":[{"code":"PD-1","fq_name":"a#b","result":{"status":"PASS"}}]}`)
	jpStateFile = sf // executeRoot doesn't reset jvm-push flags
	out, err := executeRoot(t, "jvm", "push", "--manifest", mf, "--project", "PD",
		"--state-file", sf, "--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if patchedRun != "RUN-77" {
		t.Errorf("run should resolve from state file, patched run = %q", patchedRun)
	}
}

func TestRunCaseStatus(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"PASS", "passed", true},
		{"FAIL", "failed", true},
		{"SKIP", "skipped", true},
		{"weird", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := runCaseStatus(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("runCaseStatus(%q) = (%q,%v), want (%q,%v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}
