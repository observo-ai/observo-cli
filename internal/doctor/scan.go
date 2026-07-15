package doctor

import (
	"os"
	"path/filepath"
	"strings"
)

// WorkflowScan is the result of inspecting a repo's .github/workflows/ dir.
type WorkflowScan struct {
	Verdict  bool // a Coverage-Truth Verdict workflow is present
	Pipeline bool // an Observo CI pipeline workflow (run create + artifact push) is present
}

// verdictSignatures are content substrings that identify the Coverage-Truth
// Verdict workflow. Matched case-insensitively against each workflow file so
// the check is resilient to client repos renaming the file (the canonical name
// is claude-coverage-verdict.yml, but customers may name it anything).
var verdictSignatures = []string{
	"coverage-verdict",
	"coverage_verdict",
	"observo_mcp_api_key",
	"mcp.observoai.co",
	"coverage-truth verdict",
}

// pipelineSignatures identify the Observo CI pipeline workflow — the one that
// drives the `observo` CLI run lifecycle (create → publish layers → attach →
// finish) or wires the Playwright reporter. Any one match is enough.
var pipelineSignatures = []string{
	"observo-ai/setup",
	"observo run ",
	"observo run\n",
	"@observo/playwright-reporter",
	"observo-pipeline",
}

// ScanWorkflows reads repoRoot/.github/workflows/*.{yml,yaml} and classifies
// which Observo workflows are present. A missing or unreadable directory
// yields a zero WorkflowScan (both false) — that is a legitimate "not set up"
// state, not an error, so doctor can still render the ladder. Read-only.
func ScanWorkflows(repoRoot string) WorkflowScan {
	dir := filepath.Join(repoRoot, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return WorkflowScan{}
	}

	var scan WorkflowScan
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := strings.ToLower(filepath.Ext(e.Name())); ext != ".yml" && ext != ".yaml" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		hay := strings.ToLower(e.Name() + "\n" + string(b))
		if !scan.Verdict && containsAny(hay, verdictSignatures) {
			scan.Verdict = true
		}
		if !scan.Pipeline && containsAny(hay, pipelineSignatures) {
			scan.Pipeline = true
		}
		if scan.Verdict && scan.Pipeline {
			break // both found — no need to read the rest
		}
	}
	return scan
}

// containsAny reports whether hay contains any of the needles. hay is expected
// to already be lower-cased; needles are lower-case literals.
func containsAny(hay string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(hay, n) {
			return true
		}
	}
	return false
}
