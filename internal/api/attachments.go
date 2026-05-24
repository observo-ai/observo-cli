package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Attachment is the subset of the server's Attachment shape the CLI needs.
type Attachment struct {
	ID       string `json:"id"`
	FileName string `json:"file_name,omitempty"`
}

// UploadAttachmentRequest carries the source file + the scope it attaches to.
// Exactly one of RunID / RunCaseID / RunCaseStepID may be set (server validates).
type UploadAttachmentRequest struct {
	ProjectID     string // path scope
	FilePath      string // local file path (read at upload time)
	RunID         string
	RunCaseID     string
	RunCaseStepID string
}

// UploadResponse mirrors the server envelope.
type uploadResponse struct {
	Attachment Attachment `json:"attachment"`
}

// UploadAttachment posts the file via multipart/form-data to
// POST /api/projects/{project_id}/attachments:upload.
// Returns the created attachment (ID is what callers actually need to
// splice into pipeline_layer.{junit,coverage}_attachment_id).
//
// The retry path goes through DoMultipart, NOT DoJSON — multipart bodies
// can't replay through the same retry hook (Body is a one-shot reader).
// We pre-buffer into memory; with our sub-MB lcov/junit files that's
// fine. If we ever ship attachments > 100MB, switch to streaming + retry
// by re-opening the file.
func (c *Client) UploadAttachment(ctx context.Context, req UploadAttachmentRequest) (*Attachment, error) {
	if req.ProjectID == "" {
		return nil, errors.New("UploadAttachment: project_id required")
	}
	if req.FilePath == "" {
		return nil, errors.New("UploadAttachment: file_path required")
	}

	body, contentType, err := buildMultipart(req)
	if err != nil {
		return nil, err
	}

	path := "/api/projects/" + url.PathEscape(req.ProjectID) + "/attachments:upload"

	var resp uploadResponse
	if err := c.doMultipart(ctx, "POST", path, body, contentType, &resp); err != nil {
		return nil, err
	}
	if resp.Attachment.ID == "" {
		return nil, errors.New("UploadAttachment: response missing attachment.id")
	}
	return &resp.Attachment, nil
}

// buildMultipart streams the file + form fields into an in-memory buffer.
// Returns the buffer + the Content-Type header value (which carries the
// random boundary token mime/multipart generates).
func buildMultipart(req UploadAttachmentRequest) (*bytes.Buffer, string, error) {
	f, err := os.Open(req.FilePath)
	if err != nil {
		return nil, "", fmt.Errorf("open %s: %w", req.FilePath, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	if req.RunID != "" {
		if err := mw.WriteField("run_id", req.RunID); err != nil {
			return nil, "", err
		}
	}
	if req.RunCaseID != "" {
		if err := mw.WriteField("run_case_id", req.RunCaseID); err != nil {
			return nil, "", err
		}
	}
	if req.RunCaseStepID != "" {
		if err := mw.WriteField("run_case_step_id", req.RunCaseStepID); err != nil {
			return nil, "", err
		}
	}

	fw, err := mw.CreateFormFile("file", filepath.Base(req.FilePath))
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, "", fmt.Errorf("copy file body: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return &buf, mw.FormDataContentType(), nil
}

// doMultipart is the multipart sibling of DoJSON. Same retry logic;
// different content-type header; pre-buffered body (no streaming retry).
func (c *Client) doMultipart(ctx context.Context, method, path string, body *bytes.Buffer, contentType string, out any) error {
	url := c.baseURL + path

	// Snapshot the bytes so each retry attempt gets a fresh reader.
	raw := body.Bytes()

	var lastErr error
	wait := c.InitialWait
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			c.logf("retry %d/%d after %s (prev err: %v)", attempt, c.MaxRetries, wait, lastErr)
			select {
			case <-ctx.Done():
				return mapCtxErr(ctx)
			case <-time.After(wait):
			}
			wait *= 2
			if wait > c.MaxWait {
				wait = c.MaxWait
			}
		}
		err := c.doMultipartOnce(ctx, method, url, raw, contentType, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !IsRetryable(err) {
			return err
		}
	}
	return fmt.Errorf("after %d retries: %w", c.MaxRetries, lastErr)
}

func (c *Client) doMultipartOnce(ctx context.Context, method, url string, body []byte, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	c.logf("→ %s %s (multipart, body=%d bytes)", method, url, len(body))
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return mapCtxErr(ctx)
		}
		return err
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, BodyMaxLen+1))
	if readErr != nil {
		return fmt.Errorf("read response: %w", readErr)
	}
	c.logf("← %d %s (body=%d bytes)", resp.StatusCode, url, len(respBody))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{
			StatusCode: resp.StatusCode,
			URL:        url,
			Body:       truncate(string(respBody), BodyMaxLen),
		}
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

