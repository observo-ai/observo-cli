package api

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUploadAttachment_PostsMultipartWithFileAndScope(t *testing.T) {
	// Test fixture file.
	dir := t.TempDir()
	file := filepath.Join(dir, "sample.lcov")
	if err := os.WriteFile(file, []byte("TN:\nSF:foo.go\nDA:1,1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotMethod, gotPath, gotContentType string
	var gotRunID string
	var gotFileBody string
	var gotFileName string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")

		// Parse the multipart body to verify shape.
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("multipart: %v", err)
			}
			switch p.FormName() {
			case "run_id":
				b, _ := io.ReadAll(p)
				gotRunID = string(b)
			case "file":
				gotFileName = p.FileName()
				b, _ := io.ReadAll(p)
				gotFileBody = string(b)
			}
		}

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
	if !strings.HasPrefix(gotContentType, "multipart/form-data; boundary=") {
		t.Errorf("content-type: %s", gotContentType)
	}
	if gotRunID != "r1" {
		t.Errorf("run_id field: %q", gotRunID)
	}
	if gotFileName != "sample.lcov" {
		t.Errorf("filename: %q", gotFileName)
	}
	if !strings.Contains(gotFileBody, "SF:foo.go") {
		t.Errorf("file body: %q", gotFileBody)
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
