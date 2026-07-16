package jvm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// allureAttachmentResult is the subset of an Allure *-result.json we read
// for HTTP evidence: the test identity plus its attachments (top-level and
// nested in steps). Kept separate from allureResult (allure.go) so the
// evidence path doesn't perturb the manifest-building parser.
type allureAttachmentResult struct {
	FullName string             `json:"fullName"`
	Labels   []allureLabel      `json:"labels"`
	Attach   []allureAttachment `json:"attachments"`
	Steps    []allureStep       `json:"steps"`
}

type allureAttachment struct {
	Name   string `json:"name"`
	Source string `json:"source"` // file name within the allure-results dir
	Type   string `json:"type"`
}

type allureStep struct {
	Attach []allureAttachment `json:"attachments"`
	Steps  []allureStep       `json:"steps"`
}

// ExtractHTTPEvidence reads an Allure results directory and returns, per
// test fq_name, the HTTP exchanges captured for that test (from
// allure-okhttp3 / OkHttp-logging attachments). It is tolerant: tests with
// no HTTP attachments simply map to nothing, and unreadable attachment
// files are skipped rather than failing the run.
func ExtractHTTPEvidence(allureDir string) (map[string][]HTTPExchange, error) {
	matches, err := filepath.Glob(filepath.Join(allureDir, "*-result.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)

	out := map[string][]HTTPExchange{}
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var ar allureAttachmentResult
		if err := json.Unmarshal(raw, &ar); err != nil {
			return nil, err
		}
		fq := fqNameFromLabels(ar.Labels)
		if fq == "" {
			fq = fqNameFromFullName(ar.FullName)
		}
		if fq == "" {
			continue
		}
		ex := exchangesForAttachments(allureDir, collectAttachments(ar))
		if len(ex) > 0 {
			out[fq] = append(out[fq], ex...)
		}
	}
	return out, nil
}

// collectAttachments flattens a result's top-level + recursively-nested
// step attachments into a single ordered slice (request/response pairs stay
// in emission order so they can be paired).
func collectAttachments(ar allureAttachmentResult) []allureAttachment {
	var out []allureAttachment
	out = append(out, ar.Attach...)
	var walk func(steps []allureStep)
	walk = func(steps []allureStep) {
		for _, s := range steps {
			out = append(out, s.Attach...)
			walk(s.Steps)
		}
	}
	walk(ar.Steps)
	return out
}

// exchangesForAttachments reads each attachment file in order and extracts
// HTTP exchanges, pairing a request-only attachment with the following
// response-only attachment (the allure-okhttp3 default split), while also
// handling combined request+response blobs.
func exchangesForAttachments(dir string, atts []allureAttachment) []HTTPExchange {
	var result []HTTPExchange
	var pending *HTTPExchange

	flush := func() {
		if pending != nil {
			result = append(result, *pending)
			pending = nil
		}
	}

	for _, a := range atts {
		if a.Source == "" {
			continue
		}
		content := readAttachment(dir, a.Source)
		if content == "" {
			continue
		}

		if ex := ParseHTTPExchanges(content); len(ex) > 0 {
			for i := range ex {
				e := ex[i]
				if e.Status != 0 {
					flush() // a prior request with no response ends here
					result = append(result, e)
					continue
				}
				flush()
				pending = &e
			}
			continue
		}
		// No request line: a response-only attachment completes a pending
		// request (allure-okhttp3 splits Request / Response).
		if code := ParseStatusOnly(content); code != 0 && pending != nil {
			pending.Status = code
			result = append(result, *pending)
			pending = nil
		}
	}
	flush()
	return result
}

// readAttachment reads an attachment source file, guarding against a
// source that tries to escape the results dir. Returns "" on any problem
// (tolerant — evidence is best-effort).
func readAttachment(dir, source string) string {
	// Only accept a plain base name; allure writes attachments flat in the
	// results dir, so a source containing a path separator is suspect.
	if source != filepath.Base(source) {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, source))
	if err != nil {
		return ""
	}
	return string(b)
}

// fqNameFromLabels builds class#method from the testClass/testMethod
// Allure labels, or "" when either is absent.
func fqNameFromLabels(labels []allureLabel) string {
	var class, method string
	for _, l := range labels {
		switch l.Name {
		case "testClass":
			class = l.Value
		case "testMethod":
			method = l.Value
		}
	}
	return fqName(class, method)
}
