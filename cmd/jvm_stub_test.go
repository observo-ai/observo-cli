package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resetStubFlags() {
	jsCases, jsOut, jsFramework, jsPackage, jsForce, jsDryRun = "", "", "testng", "", false, false
}

// stubServer mocks the two endpoints `jvm stub` calls: fetch case by code
// and fetch suite by id. PD-101 and PD-102 share suite s1 ("PD Wallet").
func stubServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/case/PD-101":
			_, _ = w.Write([]byte(`{"test_case":{"short_code":"PD-101","name":"trader creates a wallet",
				"description":"Wallet is created on first login","layer":"LAYER_API",
				"suite_id":"s1","project_id":"p1",
				"steps":[{"action":"POST /wallet","expected":"201","step_number":1}]}}`))
		case "/api/case/PD-102":
			_, _ = w.Write([]byte(`{"test_case":{"short_code":"PD-102","name":"trader deposits funds",
				"layer":"LAYER_API","suite_id":"s1","project_id":"p1"}}`))
		case "/api/projects/p1/suites/s1":
			_, _ = w.Write([]byte(`{"suite":{"id":"s1","name":"PD Wallet"}}`))
		default:
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
		}
	}))
}

func TestJVMStub_GeneratesGroupedClass(t *testing.T) {
	resetStubFlags()
	srv := stubServer(t)
	defer srv.Close()
	outDir := t.TempDir()

	out, err := executeRoot(t, "jvm", "stub", "--cases", "PD-101,PD-102",
		"--out", outDir, "--framework", "testng", "--package", "api.pd",
		"--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	// Both cases share suite s1 → one class file PDWalletTest.kt.
	path := filepath.Join(outDir, "PDWalletTest.kt")
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("expected %s written: %v", path, readErr)
	}
	body := string(data)
	for _, want := range []string{
		"package api.pd",
		`@Feature("PD Wallet")`,
		"class PDWalletTest {",
		`@Test(groups = ["observo:PD-101"])`,
		`@Test(groups = ["observo:PD-102"])`,
		"fun `trader creates a wallet`()",
		"fun `trader deposits funds`()",
		`TODO("implement PD-101")`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("generated file missing %q\n---\n%s", want, body)
		}
	}
}

func TestJVMStub_DoesNotOverwriteWithoutForce(t *testing.T) {
	resetStubFlags()
	srv := stubServer(t)
	defer srv.Close()
	outDir := t.TempDir()
	path := filepath.Join(outDir, "PDWalletTest.kt")
	if err := os.WriteFile(path, []byte("PRE-EXISTING"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without --force: file is preserved, skip reported.
	out, err := executeRoot(t, "jvm", "stub", "--cases", "PD-101",
		"--out", outDir, "--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "PRE-EXISTING" {
		t.Errorf("file was overwritten without --force:\n%s", data)
	}

	// With --force: file is regenerated.
	resetStubFlags()
	out, err = executeRoot(t, "jvm", "stub", "--cases", "PD-101",
		"--out", outDir, "--force", "--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute --force: %v\n%s", err, out)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "PRE-EXISTING") {
		t.Errorf("--force did not overwrite:\n%s", data)
	}
	if !strings.Contains(string(data), "observo:PD-101") {
		t.Errorf("--force wrote unexpected content:\n%s", data)
	}
}

func TestJVMStub_CollidingClassNamesStayValidIdentifiers(t *testing.T) {
	resetStubFlags()
	// Two cases in two distinct suites both literally named "Test" →
	// class names collide on "Test"; the disambiguated one must not be an
	// invalid leading-digit identifier like "2Test".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/case/PD-201":
			_, _ = w.Write([]byte(`{"test_case":{"short_code":"PD-201","name":"a","layer":"LAYER_API","suite_id":"s2","project_id":"p1"}}`))
		case "/api/case/PD-202":
			_, _ = w.Write([]byte(`{"test_case":{"short_code":"PD-202","name":"b","layer":"LAYER_API","suite_id":"s3","project_id":"p1"}}`))
		case "/api/projects/p1/suites/s2", "/api/projects/p1/suites/s3":
			id := "s2"
			if strings.HasSuffix(r.URL.Path, "s3") {
				id = "s3"
			}
			_, _ = w.Write([]byte(`{"suite":{"id":"` + id + `","name":"Test"}}`))
		default:
			http.Error(w, "nf", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	outDir := t.TempDir()

	out, err := executeRoot(t, "jvm", "stub", "--cases", "PD-201,PD-202",
		"--out", outDir, "--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	entries, _ := os.ReadDir(outDir)
	if len(entries) != 2 {
		t.Fatalf("expected 2 files, got %d", len(entries))
	}
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(outDir, e.Name()))
		body := string(data)
		// Extract the class name and assert it's a valid Kotlin identifier
		// start (letter or underscore, never a digit).
		idx := strings.Index(body, "class ")
		if idx < 0 {
			t.Fatalf("no class in %s:\n%s", e.Name(), body)
		}
		first := body[idx+len("class ")]
		if first >= '0' && first <= '9' {
			t.Errorf("invalid class name starts with digit in %s:\n%s", e.Name(), body)
		}
	}
}

func TestJVMStub_SuiteFetchFailureWarnsNotSilent(t *testing.T) {
	resetStubFlags()
	// GetSuite returns 500 (not just not-found): the case must still stub
	// (without @Feature) AND a warning must be surfaced, not swallowed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/case/PD-101":
			_, _ = w.Write([]byte(`{"test_case":{"short_code":"PD-101","name":"one","layer":"LAYER_API","suite_id":"s1","project_id":"p1"}}`))
		case "/api/projects/p1/suites/s1":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.Error(w, "nf", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	outDir := t.TempDir()

	out, err := executeRoot(t, "jvm", "stub", "--cases", "PD-101",
		"--out", outDir, "--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "warning: suite s1") {
		t.Errorf("suite-fetch failure must warn, got:\n%s", out)
	}
	// The stub is still generated, just without @Feature.
	entries, _ := os.ReadDir(outDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 stub file despite suite failure, got %d", len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(outDir, entries[0].Name()))
	if strings.Contains(string(data), "@Feature") {
		t.Errorf("expected no @Feature when suite unreadable:\n%s", data)
	}
}

func TestJVMStub_RequiresCasesAndOut(t *testing.T) {
	resetStubFlags()
	if _, err := executeRoot(t, "jvm", "stub", "--out", "x", "--api-key", "k"); err == nil {
		t.Error("expected error when --cases missing")
	}
	resetStubFlags()
	if _, err := executeRoot(t, "jvm", "stub", "--cases", "PD-1", "--api-key", "k"); err == nil {
		t.Error("expected error when --out missing")
	}
}

func TestInferPackage(t *testing.T) {
	cases := map[string]string{
		"src/test/kotlin/api/pd": "api.pd",
		"src/main/java/com/x":    "com.x",
		"gen":                    "",
		"src/test/kotlin":        "",
		// A "java"/"kotlin" segment ABOVE the real source root must not
		// win — match the last one.
		"/opt/java/proj/src/main/kotlin/api/pd": "api.pd",
	}
	for in, want := range cases {
		if got := inferPackage(in); got != want {
			t.Errorf("inferPackage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitCodes_Dedupes(t *testing.T) {
	got := splitCodes("PD-1, PD-2 ,PD-1,, PD-2")
	if len(got) != 2 || got[0] != "PD-1" || got[1] != "PD-2" {
		t.Errorf("splitCodes should dedupe preserving order, got %v", got)
	}
}

func TestJVMStub_DryRunWritesNothing(t *testing.T) {
	resetStubFlags()
	srv := stubServer(t)
	defer srv.Close()
	base := t.TempDir()
	outDir := filepath.Join(base, "gen", "new", "dir") // does not exist yet

	out, err := executeRoot(t, "jvm", "stub", "--cases", "PD-101",
		"--out", outDir, "--dry-run", "--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	// --dry-run must not create the directory tree or any file.
	if _, statErr := os.Stat(outDir); statErr == nil {
		t.Errorf("--dry-run created directory %s", outDir)
	}
}
