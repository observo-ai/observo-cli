package playwright

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTraceZip writes a synthetic trace.zip with hand-crafted
// trace.trace + trace.network JSONL streams matching the recogniser
// rules in trace.go. Keeping the fixture builder in code (not a binary
// asset under testdata) makes it easy to evolve when Playwright's
// internal schema shifts — flip a few field names, re-run.
func buildTraceZip(t *testing.T, path string, traceLines, networkLines []string) {
	t.Helper()
	fh, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer fh.Close()

	w := zip.NewWriter(fh)
	defer func() {
		if err := w.Close(); err != nil {
			t.Fatalf("close zip: %v", err)
		}
	}()

	for name, lines := range map[string][]string{
		"trace.trace":   traceLines,
		"trace.network": networkLines,
	} {
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("create %s entry: %v", name, err)
		}
		if _, err := f.Write([]byte(strings.Join(lines, "\n") + "\n")); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestExtractTrace_ConsoleAndNetwork(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.zip")
	buildTraceZip(t, path,
		[]string{
			// One log-level console event with location.
			`{"type":"event","class":"BrowserContext","method":"console","time":1234.5,"params":{"type":"warn","text":"Slow request","location":{"url":"https://example.com/app.js","lineNumber":42,"columnNumber":7}}}`,
			// One error-level console event without location.
			`{"class":"BrowserContext","method":"console","time":1300,"params":{"type":"error","text":"Uncaught TypeError"}}`,
			// A random non-console event the recogniser must skip.
			`{"type":"event","class":"Page","method":"click","time":1400}`,
			// A corrupted line — must not abort the scan.
			`{ this is not valid json`,
		},
		[]string{
			// One canonical request/response pair.
			`{"time":1500,"duration":120,"request":{"method":"POST","url":"https://api.example.com/login","postData":"{\"email\":\"a@b.com\"}"},"response":{"status":200,"content":{"text":"{\"ok\":true}"}}}`,
			// One pair with a sensitive Authorization header in postData — must be redacted.
			`{"time":1600,"duration":50,"request":{"method":"GET","url":"https://api.example.com/me","postData":"Authorization: Bearer abc123"},"response":{"status":401,"body":"{}"}}`,
			// A request-only line (no response) — recogniser must skip.
			`{"time":1700,"request":{"url":"https://example.com/no-resp","method":"GET"}}`,
		},
	)

	redact, err := NewRedactor("")
	if err != nil {
		t.Fatalf("NewRedactor: %v", err)
	}
	out, err := ExtractTrace(path, redact)
	if err != nil {
		t.Fatalf("ExtractTrace: %v", err)
	}

	if len(out.Console) != 2 {
		t.Errorf("expected 2 console entries, got %d: %+v", len(out.Console), out.Console)
	}
	if len(out.Console) >= 1 && out.Console[0].Level != "warn" {
		t.Errorf("entry[0].Level: got %q want warn", out.Console[0].Level)
	}
	if len(out.Console) >= 1 && out.Console[0].Location != "https://example.com/app.js:42:7" {
		t.Errorf("entry[0].Location: got %q", out.Console[0].Location)
	}
	if len(out.Network) != 2 {
		t.Errorf("expected 2 network entries, got %d", len(out.Network))
	}
	if len(out.Network) >= 1 && out.Network[0].Method != "POST" {
		t.Errorf("network[0].Method: got %q", out.Network[0].Method)
	}
	if len(out.Network) >= 2 && !strings.Contains(out.Network[1].RequestBody, "redacted by observo") {
		t.Errorf("network[1].RequestBody must be redacted, got %q", out.Network[1].RequestBody)
	}
}

func TestExtractTrace_MissingFile(t *testing.T) {
	redact, _ := NewRedactor("")
	_, err := ExtractTrace("/no/such/path.zip", redact)
	if err == nil {
		t.Fatal("expected error for missing zip")
	}
}

func TestExtractTrace_EmptyZipReturnsEmptyResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.zip")
	fh, _ := os.Create(path)
	w := zip.NewWriter(fh)
	w.Close()
	fh.Close()

	redact, _ := NewRedactor("")
	out, err := ExtractTrace(path, redact)
	if err != nil {
		t.Fatalf("empty zip must not error, got %v", err)
	}
	if len(out.Console) != 0 || len(out.Network) != 0 {
		t.Errorf("expected empty extraction, got %+v", out)
	}
}

func TestMarshalConsoleNetwork_NilSafe(t *testing.T) {
	// Nil slices must marshal as `[]` not `null` — the FE viewer expects
	// arrays. Encoding empty arrays explicitly guards against that.
	b, err := MarshalConsole(nil)
	if err != nil {
		t.Fatalf("MarshalConsole: %v", err)
	}
	var anyVal any
	if err := json.Unmarshal(b, &anyVal); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if _, ok := anyVal.([]any); !ok {
		t.Errorf("expected JSON array, got %T", anyVal)
	}

	b, err = MarshalNetwork(nil)
	if err != nil {
		t.Fatalf("MarshalNetwork: %v", err)
	}
	if err := json.Unmarshal(b, &anyVal); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if _, ok := anyVal.([]any); !ok {
		t.Errorf("expected JSON array, got %T", anyVal)
	}
}
