package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUploadAttachment_PostsJSONWithBase64Content(t *testing.T) {
	// Test fixture file.
	dir := t.TempDir()
	file := filepath.Join(dir, "sample.lcov")
	if err := os.WriteFile(file, []byte("TN:\nSF:foo.go\nDA:1,1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotMethod, gotPath, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"attachment": Attachment{ID: "att-uuid", FileName: "sample.lcov"},
		})
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	att, err := c.UploadAttachment(context.Background(), UploadAttachmentRequest{
		ProjectID: "OB", FilePath: file, RunID: "r1",
	})
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}

	if att.ID != "att-uuid" {
		t.Errorf("att.ID: %q", att.ID)
	}
	if gotMethod != "POST" {
		t.Errorf("method: %s", gotMethod)
	}
	if gotPath != "/api/projects/OB/attachments:upload" {
		t.Errorf("path: %s", gotPath)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type: got %q, want application/json (server gateway has no multipart marshaler)", gotCT)
	}

	// Decode body and verify shape + base64 round-trips.
	var body uploadBody
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, gotBody)
	}
	if body.ProjectID != "OB" || body.RunID != "r1" || body.FileName != "sample.lcov" {
		t.Errorf("body metadata: %+v", body)
	}
	decoded, err := base64.StdEncoding.DecodeString(body.Content)
	if err != nil {
		t.Fatalf("content must decode as base64: %v", err)
	}
	if string(decoded) != "TN:\nSF:foo.go\nDA:1,1\n" {
		t.Errorf("decoded content mismatch: %q", decoded)
	}
}

func TestUploadAttachment_RequiresProjectAndFile(t *testing.T) {
	c, _ := New(Options{BaseURL: "https://x", APIKey: "k"})
	if _, err := c.UploadAttachment(context.Background(), UploadAttachmentRequest{FilePath: "/tmp/x"}); err == nil {
		t.Error("expected error for missing project_id")
	}
	if _, err := c.UploadAttachment(context.Background(), UploadAttachmentRequest{ProjectID: "p"}); err == nil {
		t.Error("expected error for missing file_path")
	}
}

func TestUploadAttachment_PropagatesHTTPError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "x")
	_ = os.WriteFile(file, []byte("x"), 0o644)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"file too big"}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	c.InitialWait = 1 * time.Millisecond
	c.MaxWait = 2 * time.Millisecond

	_, err := c.UploadAttachment(context.Background(), UploadAttachmentRequest{
		ProjectID: "OB", FilePath: file, RunID: "r1",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "file too big") {
		t.Errorf("error should propagate body: %v", err)
	}
}

func TestDetectContentType(t *testing.T) {
	for _, tc := range []struct {
		name, want string
	}{
		{"x.lcov", "text/plain; charset=utf-8"},
		{"junit.xml", "application/xml"},
		{"report.html", "text/html; charset=utf-8"},
		{"x.json", "application/json"},
		{"summary.md", "text/plain; charset=utf-8"},
	} {
		if got := detectContentType(tc.name, []byte("...")); got != tc.want {
			t.Errorf("detectContentType(%s): got %q want %q", tc.name, got, tc.want)
		}
	}
}
