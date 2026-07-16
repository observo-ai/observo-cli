package jvm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseHTTPExchanges_Combined(t *testing.T) {
	content := `POST /wallet HTTP/1.1
Content-Type: application/json

HTTP/1.1 201 Created
Location: /wallet/1`
	ex := ParseHTTPExchanges(content)
	if len(ex) != 1 {
		t.Fatalf("want 1 exchange, got %d: %+v", len(ex), ex)
	}
	if ex[0].Method != "POST" || ex[0].Path != "/wallet" || ex[0].Status != 201 {
		t.Errorf("got %+v", ex[0])
	}
}

func TestParseHTTPExchanges_AbsoluteURLAndQueryStripped(t *testing.T) {
	ex := ParseHTTPExchanges("GET https://api.pd/wallet?trader=7&x=1 HTTP/1.1\nHTTP/1.1 200 OK")
	if len(ex) != 1 {
		t.Fatalf("want 1, got %d", len(ex))
	}
	if ex[0].Path != "/wallet" || ex[0].Method != "GET" || ex[0].Status != 200 {
		t.Errorf("url/query not normalized: %+v", ex[0])
	}
}

func TestParseHTTPExchanges_OkHttpArrowForm(t *testing.T) {
	// OkHttp logging-interceptor style.
	content := "--> POST /deposit\n<-- 500 Server Error (https://api.pd/deposit)"
	ex := ParseHTTPExchanges(content)
	if len(ex) != 1 || ex[0].Method != "POST" || ex[0].Path != "/deposit" || ex[0].Status != 500 {
		t.Fatalf("arrow form: %+v", ex)
	}
}

func TestParseHTTPExchanges_RequestOnlyStatusZero(t *testing.T) {
	ex := ParseHTTPExchanges("DELETE /wallet/1 HTTP/1.1")
	if len(ex) != 1 || ex[0].Status != 0 || ex[0].Method != "DELETE" {
		t.Fatalf("request-only: %+v", ex)
	}
}

func TestParseHTTPExchanges_IgnoresNonTargetWords(t *testing.T) {
	// A prose line containing a method word but no path/URL must not match.
	if ex := ParseHTTPExchanges("the GET request was fine"); len(ex) != 0 {
		t.Errorf("false positive on prose: %+v", ex)
	}
}

func TestParseStatusOnly(t *testing.T) {
	if got := ParseStatusOnly("HTTP/1.1 404 Not Found"); got != 404 {
		t.Errorf("want 404, got %d", got)
	}
	if got := ParseStatusOnly("no status here"); got != 0 {
		t.Errorf("want 0, got %d", got)
	}
	// Out-of-range codes are rejected.
	if got := ParseStatusOnly("HTTP/1.1 999 weird"); got != 0 {
		t.Errorf("999 should be rejected (>599 guard is on code range), got %d", got)
	}
}

func TestExtractHTTPEvidence_SplitRequestResponse(t *testing.T) {
	dir := t.TempDir()
	// allure-okhttp3 default: Request and Response are separate attachments,
	// adjacent in the result's attachments[] list.
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("req-attachment.txt", "POST /wallet HTTP/1.1")
	write("resp-attachment.txt", "HTTP/1.1 201 Created")
	write("a-result.json", `{
		"fullName":"api.pd.PdBackendE2ETest.traderCreatesWallet",
		"labels":[{"name":"testClass","value":"api.pd.PdBackendE2ETest"},
		          {"name":"testMethod","value":"traderCreatesWallet"}],
		"attachments":[
			{"name":"Request","source":"req-attachment.txt","type":"text/plain"},
			{"name":"Response","source":"resp-attachment.txt","type":"text/plain"}
		]}`)

	ev, err := ExtractHTTPEvidence(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := ev["api.pd.PdBackendE2ETest#traderCreatesWallet"]
	if len(got) != 1 {
		t.Fatalf("want 1 exchange, got %d: %+v", len(got), got)
	}
	if got[0].Method != "POST" || got[0].Path != "/wallet" || got[0].Status != 201 {
		t.Errorf("split req/resp not paired: %+v", got[0])
	}
}

func TestExtractHTTPEvidence_NestedStepAttachment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "c-attachment.txt"),
		[]byte("GET /health HTTP/1.1\nHTTP/1.1 200 OK"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b-result.json"), []byte(`{
		"fullName":"api.HealthTest.health",
		"labels":[{"name":"testClass","value":"api.HealthTest"},{"name":"testMethod","value":"health"}],
		"steps":[{"attachments":[{"name":"http","source":"c-attachment.txt","type":"text/plain"}]}]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ev, err := ExtractHTTPEvidence(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := ev["api.HealthTest#health"]
	if len(got) != 1 || got[0].Status != 200 || got[0].Path != "/health" {
		t.Fatalf("nested step attachment not extracted: %+v", got)
	}
}

func TestReadAttachment_RejectsPathTraversal(t *testing.T) {
	if got := readAttachment(t.TempDir(), "../../etc/passwd"); got != "" {
		t.Errorf("path traversal not rejected: %q", got)
	}
}
