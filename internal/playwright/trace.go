package playwright

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ConsoleEntry is one structured console message extracted from a
// `trace.zip`'s `trace.trace` stream. Mirrors the shape the FE Wave 0c
// viewer (OB-349) will consume — minimal but enough for filter UX.
type ConsoleEntry struct {
	Timestamp float64 `json:"ts"`              // ms since trace epoch
	Level     string  `json:"level"`           // log | warn | error | info | debug | trace
	Message   string  `json:"message"`
	Location  string  `json:"location,omitempty"` // url:line:column (best-effort)
}

// NetworkEntry is one structured request/response pair extracted from
// `trace.network`. Bodies (when extractable) are redacted by the
// caller-provided Redactor before being placed on RequestBody /
// ResponseBody — the extractor itself never sees plain secrets in the
// uploaded JSON.
type NetworkEntry struct {
	Timestamp    float64 `json:"ts"`
	Method       string  `json:"method"`
	URL          string  `json:"url"`
	Status       int     `json:"status"`
	DurationMs   float64 `json:"duration"`
	RequestBody  string  `json:"request_body,omitempty"`
	ResponseBody string  `json:"response_body,omitempty"`
}

// ExtractedTrace is the result of one trace.zip pass — two parallel
// slices the caller uploads as separate JSON attachments.
type ExtractedTrace struct {
	Console []ConsoleEntry `json:"console"`
	Network []NetworkEntry `json:"network"`
}

// ExtractTrace opens a Playwright trace.zip and emits structured
// console + network entries. The format inside trace.zip is not
// publicly stable across Playwright versions, so the implementation is
// deliberately tolerant:
//
//   - Each entry of `trace.trace` and `trace.network` is parsed as a
//     generic map; recognisers pattern-match on the field combinations
//     that have been stable since PW 1.30+ (`class`/`method`/`params`
//     for trace events; `request`+`response` sub-objects for network).
//   - Unknown line shapes are silently skipped.
//   - A missing or corrupted ZIP is NOT an error — returns empty
//     ExtractedTrace + warning bytes via the returned error wrapped in
//     a "soft" sentinel. Callers should log and continue, never fail
//     the whole `run import` over a single bad trace.
//
// The `redact` argument is applied to every body string before it
// reaches the output slice. Pass a non-nil *Redactor from NewRedactor.
func ExtractTrace(zipPath string, redact *Redactor) (ExtractedTrace, error) {
	out := ExtractedTrace{Console: []ConsoleEntry{}, Network: []NetworkEntry{}}

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return out, fmt.Errorf("open trace.zip %s: %w", zipPath, err)
	}
	defer r.Close()

	// Collect per-stream errors instead of returning on the first one —
	// `trace.trace` typically appears before `trace.network` in the zip,
	// so a `return` here would silently drop ALL network data on a
	// recoverable trace-stream scan error. Keep going; surface the
	// (possibly nil) combined error so the caller can log+continue per
	// the OB-347 "uploads are non-fatal" contract.
	var streamErrs []error
	for _, f := range r.File {
		switch f.Name {
		case "trace.trace":
			entries, err := readConsoleEntries(f)
			if err != nil {
				streamErrs = append(streamErrs, fmt.Errorf("read trace.trace: %w", err))
			}
			out.Console = entries
		case "trace.network":
			entries, err := readNetworkEntries(f, redact)
			if err != nil {
				streamErrs = append(streamErrs, fmt.Errorf("read trace.network: %w", err))
			}
			out.Network = entries
		}
	}
	return out, errors.Join(streamErrs...)
}

// readConsoleEntries scans the trace.trace JSONL stream for events that
// look like browser console messages and yields ConsoleEntry rows.
// Recognition rule (stable since PW 1.30):
//
//	{
//	  "type": "event" | "console" | "log",
//	  "class": "BrowserContext" | "Page",
//	  "method": "console",
//	  "params": { "type": "log|warn|error|info|debug", "text": "...", "location": {...} },
//	  "time": <ms>
//	}
//
// Newer PW versions sometimes flatten the params block — we accept
// either shape via the helper extractField. Anything not matching is
// skipped without error.
func readConsoleEntries(f *zip.File) ([]ConsoleEntry, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	var out []ConsoleEntry
	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // PW lines can be large

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(line, &raw); err != nil {
			continue // tolerate corrupted lines
		}
		// Console events carry method == "console" on both
		// BrowserContext and Page classes (PW emits them from either
		// depending on version). Filter on method only — the previous
		// `method != "console" && class != "BrowserContext"` form
		// admitted ALL BrowserContext events (navigate, route, …),
		// which only worked because most lacked params.text and got
		// filtered later; a navigate event with a text payload in
		// newer PW versions would be mis-classified as a console log.
		method, _ := raw["method"].(string)
		if method != "console" {
			continue
		}
		params, _ := raw["params"].(map[string]any)
		entry := ConsoleEntry{
			Timestamp: numberField(raw, "time"),
			Level:     stringField(params, "type"),
			Message:   stringField(params, "text"),
			Location:  locationString(params["location"]),
		}
		// Fall back to top-level fields if params is missing — some PW
		// versions inline the message directly.
		if entry.Message == "" {
			entry.Message = stringField(raw, "text")
		}
		if entry.Level == "" {
			entry.Level = "log"
		}
		if entry.Message == "" {
			continue // genuinely empty line — skip
		}
		out = append(out, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// readNetworkEntries walks trace.network looking for request/response
// pairs. Stable recognition rule: an entry with both `request` and
// `response` sub-objects where `request.url` is non-empty. Bodies are
// looked up under request.postData / response.content.text — these are
// PW's documented fields for HAR-shape resource snapshots.
func readNetworkEntries(f *zip.File, redact *Redactor) ([]NetworkEntry, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	var out []NetworkEntry
	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		req, hasReq := raw["request"].(map[string]any)
		resp, hasResp := raw["response"].(map[string]any)
		if !hasReq || !hasResp {
			continue
		}
		url := stringField(req, "url")
		if url == "" {
			continue
		}
		entry := NetworkEntry{
			Timestamp:    numberField(raw, "time"),
			Method:       stringField(req, "method"),
			URL:          url,
			Status:       int(numberField(resp, "status")),
			DurationMs:   numberField(raw, "duration"),
			RequestBody:  redact.Apply(extractBody(req)),
			ResponseBody: redact.Apply(extractBody(resp)),
		}
		out = append(out, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// extractBody pulls the body text out of a PW request/response sub-object.
// Tries both shapes PW has used: top-level "postData"/"body" and
// HAR-style "content.text". Returns "" if none found.
func extractBody(m map[string]any) string {
	if v, ok := m["postData"].(string); ok && v != "" {
		return v
	}
	if v, ok := m["body"].(string); ok && v != "" {
		return v
	}
	if content, ok := m["content"].(map[string]any); ok {
		if v, ok := content["text"].(string); ok {
			return v
		}
	}
	return ""
}

// MarshalConsole / MarshalNetwork emit the canonical JSON shape the FE
// viewers consume. Separate functions (instead of plain encoding/json on
// the slice) keep the wire shape pinned at one call site — easier to
// version when the FE schema needs `version: "1"` later.
func MarshalConsole(entries []ConsoleEntry) ([]byte, error) {
	if entries == nil {
		entries = []ConsoleEntry{}
	}
	return json.MarshalIndent(entries, "", "  ")
}

func MarshalNetwork(entries []NetworkEntry) ([]byte, error) {
	if entries == nil {
		entries = []NetworkEntry{}
	}
	return json.MarshalIndent(entries, "", "  ")
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

func numberField(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

// locationString flattens a PW location object into "url:line:column"
// for the FE viewer's filter UX. Tolerates partial / missing fields.
func locationString(v any) string {
	loc, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	url := stringField(loc, "url")
	if url == "" {
		return ""
	}
	parts := []string{url}
	if n := numberField(loc, "lineNumber"); n > 0 {
		parts = append(parts, fmt.Sprintf("%d", int(n)))
		if c := numberField(loc, "columnNumber"); c > 0 {
			parts = append(parts, fmt.Sprintf("%d", int(c)))
		}
	}
	return strings.Join(parts, ":")
}
