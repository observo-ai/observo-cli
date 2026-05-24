// Package api is a minimal HTTP client for the Observo REST API.
//
// Scope (OB-B): authenticated request/response with retry on 5xx + network
// blips, JSON encoding/decoding helpers, optional verbose request logging.
// No endpoint wrappers — those land per-subcommand in OB-C..F.
//
// Why not pull in a third-party retry lib: the matrix is small (3 retries
// with capped exponential backoff) and stdlib net/http + a for-loop is
// transparent. Avoids a dep that future versions might silently rewrite.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client wraps an *http.Client with auth + retry. Safe for concurrent use
// (matches stdlib *http.Client semantics).
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	userAgent  string
	verbose    bool

	// Retry policy. Fields exposed so tests can shorten waits.
	MaxRetries  int
	InitialWait time.Duration
	MaxWait     time.Duration
}

// Options is the constructor input. APIKey + BaseURL are required;
// callers should call config.Resolve first and pass the resolved values.
type Options struct {
	BaseURL   string
	APIKey    string
	UserAgent string // e.g. "observo-cli/0.2.0 (go1.24; darwin/arm64)"
	Verbose   bool
	Timeout   time.Duration // per-request; 0 → 30s
}

// New returns a Client ready to issue requests. Validates that BaseURL +
// APIKey are non-empty so misuse fails at construction, not first call.
func New(opts Options) (*Client, error) {
	if strings.TrimSpace(opts.BaseURL) == "" {
		return nil, errors.New("api.New: BaseURL is required")
	}
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, errors.New("api.New: APIKey is required")
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		httpClient:  &http.Client{Timeout: timeout},
		baseURL:     strings.TrimRight(opts.BaseURL, "/"),
		apiKey:      opts.APIKey,
		userAgent:   opts.UserAgent,
		verbose:     opts.Verbose,
		MaxRetries:  3,
		InitialWait: 1 * time.Second,
		MaxWait:     8 * time.Second,
	}, nil
}

// DoJSON encodes `body` as JSON, sends `method path` to baseURL+path with
// Bearer auth, and decodes the 2xx response into `out` (nil → discard).
// Retries 5xx / 429 / 408 / network errors up to MaxRetries with
// exponential backoff capped at MaxWait. 4xx (non-retry) returns an
// *HTTPError immediately.
//
// `path` must begin with "/". `body == nil` sends no request body.
// `out == nil` discards the response body.
func (c *Client) DoJSON(ctx context.Context, method, path string, body any, out any) error {
	url := c.baseURL + path

	var encoded []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		encoded = b
	}

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

		err := c.doOnce(ctx, method, url, encoded, out)
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

func (c *Client) doOnce(ctx context.Context, method, url string, body []byte, out any) error {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	c.logf("→ %s %s (body=%d bytes)", method, url, len(body))
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// net/http wraps context cancel as url.Error → unwrap and map.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
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

func (c *Client) logf(format string, args ...any) {
	if !c.verbose {
		return
	}
	fmt.Fprintf(stderr(), "[observo-cli] "+format+"\n", args...)
}

// stderr indirection so tests can capture; defaults to os.Stderr.
var stderr = func() io.Writer { return defaultStderr }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func mapCtxErr(ctx context.Context) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %s", errCtxCancelled, ctx.Err())
	}
	return errCtxCancelled
}
