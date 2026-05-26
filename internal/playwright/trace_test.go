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

// Regression for review finding #1: a trace.trace scan error must NOT
// abort ExtractTrace before trace.network is read. Pre-fix the function
// did `return out, err` on the first stream's error and silently dropped
// the second stream's data on the floor — even though the comment said
// "keep going on the other stream".
//
// We trigger a scan error by writing a single trace.trace line longer
// than the scanner's max buffer (4MB in trace.go). Network stream stays
// valid; the extracted network entries must come through.
func TestExtractTrace_TraceErrorDoesNotDropNetwork(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.zip")

	// Build a zip manually: one oversized trace.trace line + one
	// valid trace.network entry.
	fh, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	w := zip.NewWriter(fh)

	traceFile, err := w.Create("trace.trace")
	if err != nil {
		t.Fatal(err)
	}
	// 5MB single line — scanner buffer cap is 4MB.
	huge := strings.Repeat("a", 5*1024*1024)
	if _, err := traceFile.Write([]byte(huge + "\n")); err != nil {
		t.Fatal(err)
	}

	netFile, err := w.Create("trace.network")
	if err != nil {
		t.Fatal(err)
	}
	netLine := `{"time":1500,"duration":120,"request":{"method":"POST","url":"https://api.example.com/login"},"response":{"status":200,"content":{"text":"{\"ok\":true}"}}}`
	if _, err := netFile.Write([]byte(netLine + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fh.Close(); err != nil {
		t.Fatal(err)
	}

	redact, _ := NewRedactor("")
	out, err := ExtractTrace(path, redact)
	// Error is expected (the trace.trace scan failed) but it must be
	// non-nil joined error, not a hard return — and out.Network must
	// reflect the successfully-read network stream.
	if err == nil {
		t.Errorf("expected an error from oversized trace.trace line")
	}
	if len(out.Network) != 1 {
		t.Errorf("network data dropped despite trace.trace error: got %d entries (want 1)", len(out.Network))
	}
	if len(out.Network) >= 1 && out.Network[0].URL != "https://api.example.com/login" {
		t.Errorf("network entry shape lost: got %+v", out.Network[0])
	}
}

// Regression for R5 #5 (LOW): when the scanner errors midway through
// trace.trace (oversized line), entries collected BEFORE the bad line
// must be preserved — not discarded along with the error. Pre-fix
// `return nil, err` lost everything, and the orchestrator's
// `len(tr.Console) > 0` guard then suppressed an upload despite N
// valid console events being parseable.
func TestExtractTrace_ScannerErrorPreservesPriorEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.zip")

	fh, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	w := zip.NewWriter(fh)

	// Two valid console events, then an oversized line (>4MB buffer
	// cap in trace.go) that will trip the scanner.
	traceFile, err := w.Create("trace.trace")
	if err != nil {
		t.Fatal(err)
	}
	good := []string{
		`{"class":"BrowserContext","method":"console","time":1,"params":{"type":"log","text":"hello"}}`,
		`{"class":"BrowserContext","method":"console","time":2,"params":{"type":"warn","text":"world"}}`,
	}
	if _, err := traceFile.Write([]byte(strings.Join(good, "\n") + "\n")); err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("a", 5*1024*1024)
	if _, err := traceFile.Write([]byte(huge + "\n")); err != nil {
		t.Fatal(err)
	}

	// Empty network stream — keeps the test focused on the trace path.
	if _, err := w.Create("trace.network"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fh.Close(); err != nil {
		t.Fatal(err)
	}

	redact, _ := NewRedactor("")
	out, err := ExtractTrace(path, redact)
	if err == nil {
		t.Errorf("expected joined error from oversized line")
	}
	if len(out.Console) != 2 {
		t.Errorf("expected 2 preserved console entries, got %d: %+v", len(out.Console), out.Console)
	}
	if len(out.Console) >= 2 && out.Console[1].Message != "world" {
		t.Errorf("preserved entries lost shape: got %+v", out.Console[1])
	}
}

// Regression for review finding #2: previously the recogniser said
// `method != "console" && class != "BrowserContext"` — `&&` not `||` —
// which admitted ANY BrowserContext event (navigate, route, …) into
// the console-message extraction loop. Most of those got filtered later
// by the empty-message check, but a non-console BrowserContext event
// that carried a `params.text` field (which newer PW versions do for
// some lifecycle events) would be mis-classified as a console log.
func TestExtractTrace_NonConsoleBrowserContextEventFiltered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.zip")
	buildTraceZip(t, path,
		[]string{
			// Legit console event — must show up.
			`{"class":"BrowserContext","method":"console","time":1,"params":{"type":"log","text":"hello"}}`,
			// Non-console BrowserContext event WITH a text payload —
			// pre-fix this leaked into Console because the old guard
			// admitted any BrowserContext event regardless of method.
			`{"class":"BrowserContext","method":"navigate","time":2,"params":{"text":"navigated to /foo"}}`,
		},
		nil,
	)

	redact, _ := NewRedactor("")
	out, err := ExtractTrace(path, redact)
	if err != nil {
		t.Fatalf("ExtractTrace: %v", err)
	}
	if len(out.Console) != 1 {
		t.Errorf("expected only the console event; got %d entries: %+v", len(out.Console), out.Console)
	}
	if len(out.Console) >= 1 && out.Console[0].Message != "hello" {
		t.Errorf("wrong message admitted: %+v", out.Console[0])
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
