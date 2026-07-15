package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

// writeWorkflow drops a file under repoRoot/.github/workflows/.
func writeWorkflow(t *testing.T, repoRoot, name, body string) {
	t.Helper()
	dir := filepath.Join(repoRoot, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestScanWorkflows_MissingDirIsZeroNotError(t *testing.T) {
	got := ScanWorkflows(t.TempDir()) // no .github/workflows
	if got.Verdict || got.Pipeline {
		t.Errorf("expected zero scan for missing dir, got %+v", got)
	}
}

func TestScanWorkflows_DetectsVerdictByFilename(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "claude-coverage-verdict.yml", "name: verdict\njobs: {}\n")
	got := ScanWorkflows(root)
	if !got.Verdict {
		t.Errorf("expected Verdict=true from filename signature, got %+v", got)
	}
	if got.Pipeline {
		t.Errorf("expected Pipeline=false, got %+v", got)
	}
}

func TestScanWorkflows_DetectsVerdictAndPipelineByContent(t *testing.T) {
	root := t.TempDir()
	// Verdict detected by an env-var signature; pipeline by the setup action.
	writeWorkflow(t, root, "verify.yml", "env:\n  X: ${{ secrets.OBSERVO_MCP_API_KEY }}\n")
	writeWorkflow(t, root, "e2e.yml", "steps:\n  - uses: observo-ai/setup@v1\n")
	got := ScanWorkflows(root)
	if !got.Verdict || !got.Pipeline {
		t.Errorf("expected both detected, got %+v", got)
	}
}

func TestScanWorkflows_IgnoresNonYAML(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "README.md", "observo-ai/setup and OBSERVO_MCP_API_KEY mentioned in prose")
	got := ScanWorkflows(root)
	if got.Verdict || got.Pipeline {
		t.Errorf("non-YAML file must not match, got %+v", got)
	}
}
