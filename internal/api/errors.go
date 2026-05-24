package api

import (
	"errors"
	"fmt"
	"net"
	"syscall"
)

// HTTPError is returned for non-2xx responses from the Observo API.
// Carries the status code + raw body so callers can show actionable
// hints (e.g. "401 → check your API key") without re-parsing.
type HTTPError struct {
	StatusCode int
	URL        string // request URL (for log/error message; does NOT include API key)
	Body       string // response body, truncated to BodyMaxLen
}

const BodyMaxLen = 2048

func (e *HTTPError) Error() string {
	return fmt.Sprintf("observo API: HTTP %d on %s: %s", e.StatusCode, e.URL, e.Body)
}

// IsRetryable returns true when the API failure is one the caller may
// retry without changing inputs. 5xx + 429 + 408 are retryable; 4xx
// otherwise is a permanent client error and should NOT trigger backoff.
func (e *HTTPError) IsRetryable() bool {
	switch {
	case e.StatusCode == 429, e.StatusCode == 408:
		return true
	case e.StatusCode >= 500 && e.StatusCode <= 599:
		return true
	default:
		return false
	}
}

// IsRetryable returns true ONLY for errors that are known to be transient.
// Previously this whitelisted HTTPErrors via the IsRetryable method but
// then default-true'd everything else — meaning malformed BaseURLs,
// JSON decode failures, and other permanent errors got 3× backoff
// retries (~14s of dead time before surfacing to the user). The new
// shape is whitelist-only: HTTPError per its own predicate, classic
// network errors (timeout / temporary), and nothing else.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errCtxCancelled) {
		return false
	}
	var herr *HTTPError
	if errors.As(err, &herr) {
		return herr.IsRetryable()
	}
	// net.Error covers DNS failures, connection refused, TLS handshake
	// resets, deadline exceeded — all transient. Other error types
	// (json.SyntaxError, url.Error wrapping a non-net cause, etc.) are
	// permanent and should not retry.
	var nerr net.Error
	if errors.As(err, &nerr) {
		if nerr.Timeout() {
			return true
		}
	}
	// Connection-level errors that the stdlib doesn't surface as
	// net.Error but are transient: ECONNREFUSED, ECONNRESET.
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	return false
}

// errCtxCancelled is a sentinel for context.Canceled mapped through Do —
// kept separate so IsRetryable can short-circuit without retrying a user-
// initiated cancel.
var errCtxCancelled = errors.New("context cancelled")
