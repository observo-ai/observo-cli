package api

import (
	"errors"
	"fmt"
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

// IsRetryable returns true for any error that can be retried — wraps the
// HTTPError-aware variant for use with non-HTTP errors (network blips,
// context.DeadlineExceeded, etc.).
func IsRetryable(err error) bool {
	var herr *HTTPError
	if errors.As(err, &herr) {
		return herr.IsRetryable()
	}
	// Network errors (DNS, dial timeout, TLS handshake) — retry.
	// Skip context cancellation: the caller asked to stop.
	if errors.Is(err, errCtxCancelled) {
		return false
	}
	return err != nil
}

// errCtxCancelled is a sentinel for context.Canceled mapped through Do —
// kept separate so IsRetryable can short-circuit without retrying a user-
// initiated cancel.
var errCtxCancelled = errors.New("context cancelled")
