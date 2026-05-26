package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// Attachment is the subset of the server's Attachment shape the CLI needs.
type Attachment struct {
	ID       string `json:"id"`
	FileName string `json:"file_name,omitempty"`
}

// UploadAttachmentRequest carries the source file + the scope it
// attaches to. At least one of RunID / RunCaseID / RunCaseStepID must
// be set (server: rpc_upload_attachment.go:120-122). They are NOT
// mutually exclusive: when RunCaseID is a short code like "OB-7", the
// server requires RunID to be set too so it can resolve the per-case
// row (rpc_upload_attachment.go:78-79). Set both for per-case uploads;
// set only RunID for run-level attachments (e.g. results.json).
type UploadAttachmentRequest struct {
	ProjectID     string // path scope
	FilePath      string // local file path (read at upload time)
	RunID         string
	RunCaseID     string
	RunCaseStepID string
}

// uploadBody mirrors the server's UploadAttachmentRequest proto. The
// server's grpc-gateway pipeline accepts ONLY JSON (no multipart
// marshaler registered in main.go's runGatewayServer), so the file is
// base64-encoded into `content`. proto `bytes content` rule:
// protojson encodes bytes as standard base64. Field names use
// snake_case (server uses UseProtoNames=true on the marshaler).
//
// project_id is NOT a field here on purpose. The proto's HTTP binding
// is `post: "/api/projects/{project_id}/attachments:upload"` with
// `body: "*"`. grpc-gateway v2 rejects requests where a field bound
// to a URL path segment also appears in the JSON body ("field already
// bound to URL path parameter"). The URL path is the source of truth
// for project_id.
type uploadBody struct {
	RunID         string `json:"run_id,omitempty"`
	RunCaseID     string `json:"run_case_id,omitempty"`
	RunCaseStepID string `json:"run_case_step_id,omitempty"`
	FileName      string `json:"file_name"`
	ContentType   string `json:"content_type,omitempty"`
	Content       string `json:"content"` // base64-std-encoded file bytes
}

type uploadResponse struct {
	Attachment Attachment `json:"attachment"`
}

// UploadAttachment posts the file as JSON+base64 to
// POST /api/projects/{project_id}/attachments:upload.
//
// Earlier the CLI used multipart/form-data — that matched the upstream
// upload-pipeline-action's shape, but the server's gRPC-gateway uses
// the default JSONPb marshaler (server/main.go:393) with no multipart
// registration, so multipart requests fail with 415 Unsupported Media
// Type. The upstream action's uploads were silently swallowed by
// `curl -sS ... || warning` — verified by inspecting production runs
// with zero attachment IDs spliced into pipeline.layers.
//
// JSON+base64 ~ 4/3× the file's raw byte size; for our junit/lcov
// (sub-MB) the cost is negligible. If a future use case ships >50MB
// attachments, switch to a dedicated multipart server endpoint instead
// (requires a server-side handler registered outside the gateway).
func (c *Client) UploadAttachment(ctx context.Context, req UploadAttachmentRequest) (*Attachment, error) {
	if req.ProjectID == "" {
		return nil, errors.New("UploadAttachment: project_id required")
	}
	if req.FilePath == "" {
		return nil, errors.New("UploadAttachment: file_path required")
	}

	f, err := os.Open(req.FilePath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", req.FilePath, err)
	}
	defer f.Close()

	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", req.FilePath, err)
	}

	body := uploadBody{
		RunID:         req.RunID,
		RunCaseID:     req.RunCaseID,
		RunCaseStepID: req.RunCaseStepID,
		FileName:      filepath.Base(req.FilePath),
		ContentType:   detectContentType(req.FilePath, raw),
		Content:       base64.StdEncoding.EncodeToString(raw),
	}

	path := "/api/projects/" + url.PathEscape(req.ProjectID) + "/attachments:upload"

	var resp uploadResponse
	if err := c.DoJSON(ctx, "POST", path, body, &resp); err != nil {
		return nil, err
	}
	if resp.Attachment.ID == "" {
		return nil, errors.New("UploadAttachment: response missing attachment.id")
	}
	return &resp.Attachment, nil
}

// detectContentType infers a content_type for the upload body. Uses the
// stdlib http.DetectContentType (sniffs first 512 bytes) and falls back
// to filename extension for text formats it can't tell apart (junit
// XML, lcov text, etc.). Server doesn't strictly require this — it's a
// hint for the dashboard's download UX.
func detectContentType(name string, body []byte) string {
	switch ext := filepath.Ext(name); ext {
	case ".lcov", ".info":
		return "text/plain; charset=utf-8"
	case ".xml":
		return "application/xml"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".json":
		return "application/json"
	case ".md", ".txt":
		return "text/plain; charset=utf-8"
	default:
		if len(body) == 0 {
			return "application/octet-stream"
		}
		return http.DetectContentType(body)
	}
}
