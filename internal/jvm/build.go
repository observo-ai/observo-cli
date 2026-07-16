package jvm

import (
	"errors"
	"fmt"
	"os"
)

// ErrNoSource is returned when BuildManifest is called with no report
// source at all.
var ErrNoSource = errors.New("no report source: set --from (allure-results) and/or --testng-results")

// BuildOptions configures BuildManifest.
type BuildOptions struct {
	AllureDir     string // directory of *-result.json (optional)
	TestNGResults string // path to testng-results.xml (optional)
	Framework     string // testng | junit5 | playwright | auto | "" (auto)
}

// BuildManifest constructs a LinkManifest from whatever report sources are
// provided and runs duplicate detection before returning (OB-543 AC2).
// At least one source must be set. When both are given they are merged —
// TestNG join + Allure metadata, the pilot client's shape.
func BuildManifest(opts BuildOptions) (*LinkManifest, error) {
	// Key the "was a source given?" decision on the FLAGS, not on whether a
	// parser produced rows. The two parsers disagree on nil-vs-empty (a
	// valid report with zero real tests is a legitimate empty run, not a
	// missing source), so keying on nil-ness would wrongly report
	// ErrNoSource for `--testng-results <config-only.xml>`.
	hasAllure := opts.AllureDir != ""
	hasTestNG := opts.TestNGResults != ""
	if !hasAllure && !hasTestNG {
		return nil, ErrNoSource
	}

	var allure, testng []LinkEntry
	var err error

	if hasAllure {
		// Fail loud on a mistyped path: Glob silently returns no matches
		// for a non-existent directory, which would otherwise produce a
		// deceptively empty manifest instead of an error.
		if fi, statErr := os.Stat(opts.AllureDir); statErr != nil || !fi.IsDir() {
			return nil, fmt.Errorf("allure results dir %q: not a readable directory", opts.AllureDir)
		}
		if allure, err = ParseAllureResults(opts.AllureDir); err != nil {
			return nil, err
		}
		// Fold parametrized / retried tests that share an fq_name into one
		// logical entry (TestNG entries are already collapsed per class).
		allure = collapseByFQName(allure)
	}
	if hasTestNG {
		if testng, err = ParseTestNGResults(opts.TestNGResults); err != nil {
			return nil, err
		}
	}

	var entries []LinkEntry
	switch {
	case hasTestNG && hasAllure:
		entries = Merge(testng, allure)
	case hasTestNG:
		entries = testng
	default:
		entries = allure
	}
	if entries == nil {
		entries = []LinkEntry{} // emit "entries": [] not null for an empty run
	}

	if err := DetectDuplicates(entries); err != nil {
		return nil, err
	}

	return &LinkManifest{
		Version:   ManifestVersion,
		Framework: detectFramework(opts, len(testng) > 0),
		Entries:   entries,
	}, nil
}

// detectFramework resolves the manifest's framework label. An explicit
// non-auto value wins; otherwise "testng" is inferred only when the TestNG
// report actually contributed entries (not merely because the flag was
// set), and anything else is left blank — Allure alone can't distinguish
// TestNG from JUnit5, and we don't guess.
func detectFramework(opts BuildOptions, testngContributed bool) string {
	switch opts.Framework {
	case "", "auto":
		if testngContributed {
			return "testng"
		}
		return ""
	default:
		return opts.Framework
	}
}
