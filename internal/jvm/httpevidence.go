package jvm

import (
	"regexp"
	"strconv"
	"strings"
)

// HTTPExchange is one request/response pair extracted from a captured HTTP
// log (e.g. an allure-okhttp3 attachment), reduced to the fields the
// coverage verdict consumes as execution-evidence: "this test actually hit
// METHOD path and got status". Status is 0 when the log carried no response.
type HTTPExchange struct {
	Method string `json:"method"`           // GET, POST, PUT, PATCH, DELETE, ...
	Path   string `json:"path"`             // request path, query stripped
	Status int    `json:"status,omitempty"` // response status code; 0 if absent
}

// requestLineRE matches an HTTP request line in the common textual forms an
// OkHttp / allure-okhttp3 log emits:
//
//	POST /wallet HTTP/1.1
//	GET https://api.pd/wallet?x=1
//	--> POST /wallet            (OkHttp logging-interceptor arrow prefix)
//
// Group 1 = method, group 2 = target (path or absolute URL).
var requestLineRE = regexp.MustCompile(`(?i)(?:-->\s*)?\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+(\S+)`)

// statusLineRE matches an HTTP response status line:
//
//	HTTP/1.1 201 Created
//	<-- 200 OK (https://…)      (OkHttp logging-interceptor arrow prefix)
//
// Group 1 (when the HTTP/x form) or group 2 (arrow form) holds the code.
var statusLineRE = regexp.MustCompile(`(?i)(?:HTTP/\d(?:\.\d)?\s+(\d{3})|<--\s+(\d{3}))\b`)

// ParseHTTPExchanges extracts HTTP exchanges from a single captured-log
// blob. It handles the combined case (request line followed later by a
// status line in the same content) and the request-only case. A new
// exchange starts at each request line; a status line fills the most recent
// exchange that still lacks one. Content with no request line yields no
// exchanges (see ParseStatusOnly for the response-only attachment case).
func ParseHTTPExchanges(content string) []HTTPExchange {
	var out []HTTPExchange
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if m := requestLineRE.FindStringSubmatch(line); m != nil {
			// Guard: an OkHttp response line like "<-- END HTTP" won't match
			// requestLineRE, but a stray word "GET involved" could — require
			// the target to look like a path or URL.
			target := m[2]
			if !looksLikeTarget(target) {
				continue
			}
			out = append(out, HTTPExchange{
				Method: strings.ToUpper(m[1]),
				Path:   normalizePath(target),
			})
			continue
		}
		if code, ok := parseStatus(line); ok {
			// Attach to the most recent exchange lacking a status.
			for i := len(out) - 1; i >= 0; i-- {
				if out[i].Status == 0 {
					out[i].Status = code
					break
				}
			}
		}
	}
	return out
}

// ParseStatusOnly returns the first response status found in a blob that
// carries no request line — the "Response" half when allure-okhttp3 splits
// request and response into separate attachments. Returns 0 when none.
func ParseStatusOnly(content string) int {
	for _, line := range strings.Split(content, "\n") {
		if code, ok := parseStatus(strings.TrimSpace(line)); ok {
			return code
		}
	}
	return 0
}

// FirstRequest returns the first (method, path) in a blob, or ("","",false).
// Used to pair a "Request" attachment with its sibling "Response".
func FirstRequest(content string) (method, path string, ok bool) {
	for _, line := range strings.Split(content, "\n") {
		if m := requestLineRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil && looksLikeTarget(m[2]) {
			return strings.ToUpper(m[1]), normalizePath(m[2]), true
		}
	}
	return "", "", false
}

func parseStatus(line string) (int, bool) {
	m := statusLineRE.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	raw := m[1]
	if raw == "" {
		raw = m[2]
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 100 || n > 599 {
		return 0, false
	}
	return n, true
}

// looksLikeTarget rejects false-positive "method words" — the target must
// be an absolute URL or begin with '/'.
func looksLikeTarget(t string) bool {
	return strings.HasPrefix(t, "/") ||
		strings.HasPrefix(t, "http://") ||
		strings.HasPrefix(t, "https://")
}

// normalizePath reduces a request target to a bare path: strips the
// scheme+host from an absolute URL and drops the query string, so evidence
// keys on the endpoint, not per-call query values.
func normalizePath(target string) string {
	// Strip scheme://host.
	if i := strings.Index(target, "://"); i >= 0 {
		rest := target[i+3:]
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			target = rest[slash:]
		} else {
			target = "/"
		}
	}
	// Drop query + fragment.
	if q := strings.IndexAny(target, "?#"); q >= 0 {
		target = target[:q]
	}
	if target == "" {
		return "/"
	}
	return target
}
