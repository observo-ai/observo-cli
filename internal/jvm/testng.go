package jvm

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// testng-results.xml is TestNG's native report. Unlike Allure it records
// @Test(groups=…) — the join source for a TestNG-groups client — in a
// per-suite <groups> section mapping each group to its (class, method).
// The per-<class><test-method> section carries status + timing + order.
//
// We model only the elements we need; encoding/xml ignores the rest.
type testngResults struct {
	Suites []testngSuite `xml:"suite"`
}

type testngSuite struct {
	Groups []testngGroup `xml:"groups>group"`
	Tests  []testngTest  `xml:"test"`
}

type testngGroup struct {
	Name    string              `xml:"name,attr"`
	Methods []testngGroupMethod `xml:"method"`
}

type testngGroupMethod struct {
	Name  string `xml:"name,attr"`
	Class string `xml:"class,attr"`
}

type testngTest struct {
	Classes []testngClass `xml:"class"`
}

type testngClass struct {
	Name    string             `xml:"name,attr"`
	Methods []testngTestMethod `xml:"test-method"`
}

type testngTestMethod struct {
	Name       string `xml:"name,attr"`
	Status     string `xml:"status,attr"`      // PASS | FAIL | SKIP
	DurationMs int64  `xml:"duration-ms,attr"` // TestNG writes this directly in ms
	StartedAt  string `xml:"started-at,attr"`  // ISO-8601 → orders a chain
	IsConfig   string `xml:"is-config,attr"`   // "true" for @BeforeX/@AfterX
}

// ParseTestNGResults reads a testng-results.xml file and returns one
// LinkEntry per real test method (config methods are skipped). The join
// code comes from the method's observo:<code> group membership; result and
// duration come from <test-method>. Methods of a class with ≥2 tests get a
// chain_id (the class fq-name) and a 1-based order derived from execution
// start time — a best-effort model of an order-dependent chain that import
// (OB-544) later resolves into steps-vs-flat.
func ParseTestNGResults(path string) ([]LinkEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return decodeTestNG(f)
}

func decodeTestNG(r io.Reader) ([]LinkEntry, error) {
	var res testngResults
	if err := xml.NewDecoder(r).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode testng-results: %w", err)
	}

	// Invert the <groups> section: (class#method) → observo codes. Only
	// observo:<code> groups are indexed; ordinary TestNG groups (smoke,
	// regression, …) are ignored.
	codesByMethod := map[string][]string{}
	for _, s := range res.Suites {
		for _, g := range s.Groups {
			if _, ok := FromTag(g.Name); !ok {
				continue
			}
			for _, m := range g.Methods {
				key := fqName(m.Class, m.Name)
				if key == "" {
					continue
				}
				codesByMethod[key] = append(codesByMethod[key], g.Name)
			}
		}
	}

	var entries []LinkEntry
	for _, s := range res.Suites {
		for _, t := range s.Tests {
			for _, c := range t.Classes {
				entries = append(entries, entriesForClass(c, codesByMethod)...)
			}
		}
	}
	return entries, nil
}

// collapsedMethod is one logical test method after repeated invocations
// (data-driven / @Factory) with the same name are folded together.
type collapsedMethod struct {
	name      string
	startedAt string // earliest invocation start (orders the chain)
	result    *Result
}

func entriesForClass(c testngClass, codesByMethod map[string][]string) []LinkEntry {
	// Fold repeated invocations of one method — a data-driven @Test writes
	// several <test-method name="x"> entries, but they are ONE logical
	// test. Collapse them BEFORE the chain decision so a single
	// data-driven method isn't mistaken for an order-dependent chain.
	byName := map[string]*collapsedMethod{}
	seen := []string{} // first-seen order, to keep output deterministic
	for _, m := range c.Methods {
		if strings.EqualFold(m.IsConfig, "true") { // skip @BeforeX/@AfterX
			continue
		}
		cm, ok := byName[m.Name]
		if !ok {
			cm = &collapsedMethod{name: m.Name, startedAt: m.StartedAt}
			byName[m.Name] = cm
			seen = append(seen, m.Name)
		}
		if m.StartedAt != "" && (cm.startedAt == "" || m.StartedAt < cm.startedAt) {
			cm.startedAt = m.StartedAt
		}
		cm.result = aggregateResult(cm.result,
			&Result{Status: mapTestNGStatus(m.Status), DurationMs: m.DurationMs})
	}

	// Distinct methods, ordered by earliest execution start (proxy for the
	// TestNG priority sequence on a single-threaded chain).
	methods := make([]*collapsedMethod, 0, len(seen))
	for _, n := range seen {
		methods = append(methods, byName[n])
	}
	sort.SliceStable(methods, func(i, j int) bool {
		return methods[i].startedAt < methods[j].startedAt
	})

	chain := len(methods) > 1 // ≥2 distinct methods → treat the class as a chain
	entries := make([]LinkEntry, 0, len(methods))
	for i, m := range methods {
		fq := fqName(c.Name, m.name)
		e := LinkEntry{
			FQName:      fq,
			DisplayName: m.name,
			Result:      m.result,
		}
		if code := Resolve(codesByMethod[fq], nil); code != "" {
			e.Code = strPtr(code)
		}
		if chain {
			e.ChainID = c.Name
			e.Order = intPtr(i + 1)
		}
		entries = append(entries, e)
	}
	return entries
}

// mapTestNGStatus normalises TestNG's status attribute. Anything other
// than the three known values is treated as SKIP rather than inventing a
// pass/fail.
func mapTestNGStatus(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PASS":
		return StatusPass
	case "FAIL":
		return StatusFail
	case "SKIP":
		return StatusSkip
	default:
		return StatusSkip
	}
}
