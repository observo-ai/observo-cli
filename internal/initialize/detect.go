// Package initialize implements OB-356 — the `observo init` subcommand.
//
// It is intentionally split into small testable units (detect / patch /
// workflow / prompt) so each one is unit-testable without spinning up an
// interactive shell or hitting the Observo backend. The cmd/init.go thin
// layer just wires user-input through these functions.
//
// Naming: the package is `initialize` (not `init`) because Go reserves
// the identifier `init` for special functions; importing a package named
// `init` is awkward.
package initialize

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// RepoInfo carries everything we can discover about the current repo
// without contacting any remote service. Populated by DetectRepo.
type RepoInfo struct {
	Host        string // "github.com" | "gitlab.com" | empty if non-supported
	Owner       string // e.g. "observo-ai"
	Name        string // e.g. "example-app"
	DefaultPlan string // suggested plan name, derived from Name
}

// ErrRepoNotDetected signals we couldn't infer the repo. Callers fall back
// to prompting the user manually.
var ErrRepoNotDetected = errors.New("observo init: could not detect git remote")

// DetectRepo runs `git remote get-url origin` and parses the result. It
// accepts both HTTPS and SSH forms — github:
//
//	https://github.com/observo-ai/example-app.git
//	git@github.com:observo-ai/example-app.git
//
// gitlab.com works identically. Self-hosted GitLab is detected by host
// presence but RepoInfo.Host returns the raw hostname; the caller decides
// whether to treat unknown hosts as supported.
func DetectRepo(cwd string) (*RepoInfo, error) {
	cmd := exec.Command("git", "-C", cwd, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRepoNotDetected, err)
	}
	raw := strings.TrimSpace(string(out))
	return parseRemoteURL(raw)
}

// parseRemoteURL exposes the parsing logic for tests — no exec.
func parseRemoteURL(raw string) (*RepoInfo, error) {
	if raw == "" {
		return nil, ErrRepoNotDetected
	}
	// SSH form: git@host:owner/repo(.git)?
	if strings.HasPrefix(raw, "git@") {
		// strip "git@", split on first ":".
		rest := strings.TrimPrefix(raw, "git@")
		i := strings.Index(rest, ":")
		if i < 0 {
			return nil, ErrRepoNotDetected
		}
		host := rest[:i]
		path := strings.TrimSuffix(rest[i+1:], ".git")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, ErrRepoNotDetected
		}
		return &RepoInfo{
			Host:        host,
			Owner:       parts[0],
			Name:        parts[1],
			DefaultPlan: derivePlanName(parts[1]),
		}, nil
	}
	// HTTPS form: https://host/owner/repo(.git)?
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, ErrRepoNotDetected
	}
	path := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return nil, ErrRepoNotDetected
	}
	return &RepoInfo{
		Host:        u.Host,
		Owner:       parts[0],
		Name:        parts[1],
		DefaultPlan: derivePlanName(parts[1]),
	}, nil
}

// derivePlanName turns a repo name into an uppercase plan key. "example-app"
// becomes "EXAMPLE-APP-E2E". Customers can override at the prompt; this is
// just a sensible default that surfaces the repo and the intent.
func derivePlanName(repoName string) string {
	upper := strings.ToUpper(repoName)
	// Strip anything that's not [A-Z0-9-_]; replace with dash.
	cleaned := regexp.MustCompile(`[^A-Z0-9_-]`).ReplaceAllString(upper, "-")
	cleaned = strings.Trim(cleaned, "-")
	if cleaned == "" {
		return "E2E"
	}
	if strings.HasSuffix(cleaned, "-E2E") || cleaned == "E2E" {
		return cleaned
	}
	return cleaned + "-E2E"
}

// PlaywrightConfig is the location of a detected Playwright config file plus
// the spec-file count for that config. cwd-relative paths.
type PlaywrightConfig struct {
	ConfigPath string   // e.g. "playwright.config.ts" or "web-portal/playwright.config.ts"
	SpecFiles  []string // discovered *.spec.ts under the config's project dir
}

// skipDirsForWalk is the explicit set of directories the FindPlaywrightConfig
// walk descends INTO false. Listed by name rather than a blanket "anything
// starting with `.`" because some monorepos use dot-prefixed convention dirs
// (e.g. `.apps/web/`) that may legitimately host a playwright.config.ts. The
// earlier blanket-exclude version of this skip would produce a misleading
// "playwright not detected" error for those layouts.
//
// Add new entries here when they're known noise / cause-of-slow-scans, not
// preemptively for the dot-prefix pattern.
var skipDirsForWalk = map[string]bool{
	"node_modules": true,
	".git":         true,
	".cache":       true,
	".next":        true,
	".turbo":       true,
	".nuxt":        true,
	".svelte-kit":  true,
	"dist":         true,
	"build":        true,
}

// FindPlaywrightConfig walks the repo from cwd looking for playwright.config.ts
// or .js. Skips known noise dirs (see skipDirsForWalk) for speed. Returns the
// first match — for monorepos with multiple Playwright projects, the user must
// specify path explicitly via --config (a follow-up flag).
func FindPlaywrightConfig(cwd string) (*PlaywrightConfig, error) {
	var found string
	err := filepath.WalkDir(cwd, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable dirs
		}
		if d.IsDir() {
			if skipDirsForWalk[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if name == "playwright.config.ts" || name == "playwright.config.js" || name == "playwright.config.mjs" {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if found == "" {
		return nil, fmt.Errorf("no playwright.config.{ts,js,mjs} found under %s", cwd)
	}
	rel, _ := filepath.Rel(cwd, found)
	specs := findSpecFiles(filepath.Dir(found))
	return &PlaywrightConfig{ConfigPath: rel, SpecFiles: specs}, nil
}

// findSpecFiles walks `dir` looking for *.spec.{ts,js,tsx,jsx}. Used purely
// for the count we display at the prompt — heuristic, not exhaustive (custom
// testMatch patterns aren't honored). Sorted for stable test fixtures.
func findSpecFiles(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if base == "node_modules" || base == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.Contains(name, ".spec.") {
			return nil
		}
		ext := filepath.Ext(name)
		switch ext {
		case ".ts", ".js", ".tsx", ".jsx", ".mts", ".mjs":
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// FileExists is a tiny helper that's awkward to inline at call sites.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
