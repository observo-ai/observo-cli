package jvm

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// allureResult is the subset of an Allure 2.x `*-result.json` file we
// consume (io.qameta.allure writes one per test method). Unknown keys are
// ignored by encoding/json, so we model only what feeds a LinkEntry.
type allureResult struct {
	Name        string        `json:"name"`
	FullName    string        `json:"fullName"`
	Status      string        `json:"status"` // passed|failed|broken|skipped|unknown
	Description string        `json:"description"`
	Start       int64         `json:"start"` // epoch ms
	Stop        int64         `json:"stop"`  // epoch ms
	Labels      []allureLabel `json:"labels"`
	Links       []allureLink  `json:"links"`
}

type allureLabel struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type allureLink struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ParseAllureResults reads every `*-result.json` in an Allure results
// directory and returns one LinkEntry per test. The join code comes from a
// `tag` label of the form observo:<code> (JUnit5 @Tag surfaces here) or,
// failing that, a `tms`-typed link (Allure @TmsLink fallback). Tests with
// neither yield an untracked entry (code=null) — never dropped (AC4).
//
// IMPORTANT (client fit): allure-testng does NOT surface @Test(groups=…)
// as tag labels, so a TestNG-groups suite links through testng-results.xml
// instead (see ParseTestNGResults); this reader still runs to enrich those
// entries with feature/story/description/display_name via Merge.
func ParseAllureResults(dir string) ([]LinkEntry, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*-result.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches) // deterministic order (Emit re-sorts, but keep parse stable)
	entries := make([]LinkEntry, 0, len(matches))
	for _, path := range matches {
		e, err := parseAllureFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func parseAllureFile(path string) (LinkEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return LinkEntry{}, err
	}
	defer f.Close()
	return decodeAllure(f)
}

func decodeAllure(r io.Reader) (LinkEntry, error) {
	var ar allureResult
	if err := json.NewDecoder(r).Decode(&ar); err != nil {
		return LinkEntry{}, fmt.Errorf("decode allure result: %w", err)
	}
	return ar.toEntry(), nil
}

func (ar allureResult) toEntry() LinkEntry {
	var tags, tms []string
	var feature, story, testClass, testMethod string
	for _, l := range ar.Labels {
		switch l.Name {
		case "tag":
			tags = append(tags, l.Value)
		case "feature":
			if feature == "" {
				feature = l.Value
			}
		case "story":
			if story == "" {
				story = l.Value
			}
		case "testClass":
			testClass = l.Value
		case "testMethod":
			testMethod = l.Value
		}
	}
	for _, l := range ar.Links {
		if strings.EqualFold(l.Type, "tms") {
			tms = append(tms, l.Name)
		}
	}

	fq := fqName(testClass, testMethod)
	if fq == "" {
		fq = fqNameFromFullName(ar.FullName)
	}

	e := LinkEntry{
		FQName:      fq,
		DisplayName: ar.Name,
		Description: ar.Description,
		Feature:     feature,
		Story:       story,
	}
	if code := Resolve(tags, tms); code != "" {
		e.Code = &code
	}
	if ar.Status != "" {
		e.Result = &Result{
			Status:     mapAllureStatus(ar.Status),
			DurationMs: durationMs(ar.Start, ar.Stop),
		}
	}
	return e
}

// mapAllureStatus collapses Allure's five statuses onto the manifest's
// three. "broken" (test threw before an assertion) is a failure; the rare
// "unknown"/"" is treated as SKIP rather than fabricating a pass or fail.
func mapAllureStatus(s string) string {
	switch strings.ToLower(s) {
	case "passed":
		return StatusPass
	case "failed", "broken":
		return StatusFail
	case "skipped":
		return StatusSkip
	default:
		return StatusSkip
	}
}

// durationMs returns stop-start in ms, clamped at 0 when the source omits
// or inverts the timestamps.
func durationMs(start, stop int64) int64 {
	if stop > start {
		return stop - start
	}
	return 0
}
