package playwright

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractFailure_MessageOnly(t *testing.T) {
	in := PWError{Message: "expect(received).toBe(expected)"}
	f := ExtractFailure(in, "")
	if f.Message != in.Message {
		t.Errorf("message mismatch: got %q want %q", f.Message, in.Message)
	}
	if f.Stack != "" || f.Location != nil || f.SourceExcerpt != "" {
		t.Errorf("expected only Message populated, got %+v", f)
	}
}

func TestExtractFailure_ValueFallback(t *testing.T) {
	// Some matcher libs fill `value` (structured matcher output) without
	// `message`. Ensure that's the fallback.
	in := PWError{Value: "Expected: 200\nReceived: 401"}
	f := ExtractFailure(in, "")
	if f.Message != in.Value {
		t.Errorf("value-fallback message mismatch: got %q want %q", f.Message, in.Value)
	}
}

func TestExtractFailure_SourceExcerpt(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "test_file.ts")
	body := strings.Join([]string{
		"import { test, expect } from '@playwright/test';",
		"",
		"test('login redirect', async ({ page }) => {",
		"  await page.goto('/login');",
		"  await expect(page).toHaveURL(/login/);",
		"  // failure happens on the next line",
		"  await expect(page.locator('#email')).toBeVisible();",
		"  await page.locator('#email').fill('x@y.z');",
		"});",
	}, "\n")
	if err := os.WriteFile(src, []byte(body), 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	in := PWError{
		Message: "Element not visible",
		Location: &struct {
			File   string `json:"file"`
			Line   int    `json:"line"`
			Column int    `json:"column"`
		}{File: "test_file.ts", Line: 7, Column: 3},
	}
	f := ExtractFailure(in, root)
	if f.Location == nil {
		t.Fatalf("expected Location populated")
	}
	if f.Location.Line != 7 {
		t.Errorf("Location.Line: got %d want 7", f.Location.Line)
	}
	if !strings.Contains(f.SourceExcerpt, "→    7") {
		t.Errorf("expected → marker on line 7, got:\n%s", f.SourceExcerpt)
	}
	// Should include 3 lines either side (radius=3): lines 4..10.
	// Line 4 = await page.goto('/login');
	if !strings.Contains(f.SourceExcerpt, "page.goto('/login')") {
		t.Errorf("expected line 4 in excerpt, got:\n%s", f.SourceExcerpt)
	}
}

func TestExtractFailure_MissingSourceFile(t *testing.T) {
	// Source file doesn't exist — must NOT error, just leaves excerpt empty.
	in := PWError{
		Message: "boom",
		Location: &struct {
			File   string `json:"file"`
			Line   int    `json:"line"`
			Column int    `json:"column"`
		}{File: "no-such-file.ts", Line: 1},
	}
	f := ExtractFailure(in, t.TempDir())
	if f.Location == nil {
		t.Fatalf("expected Location to still be populated")
	}
	if f.SourceExcerpt != "" {
		t.Errorf("expected empty SourceExcerpt, got %q", f.SourceExcerpt)
	}
}

func TestExtractFailure_PathTraversalRejected(t *testing.T) {
	// A malicious results.json pointing outside the source root must
	// not be allowed to read /etc/* — defence in depth.
	root := t.TempDir()
	in := PWError{
		Message: "boom",
		Location: &struct {
			File   string `json:"file"`
			Line   int    `json:"line"`
			Column int    `json:"column"`
		}{File: "../../../etc/passwd", Line: 1},
	}
	f := ExtractFailure(in, root)
	if f.SourceExcerpt != "" {
		t.Errorf("traversal path must not leak excerpt; got %q", f.SourceExcerpt)
	}
}

func TestExtractFailure_NoLocationNoExcerpt(t *testing.T) {
	f := ExtractFailure(PWError{Message: "boom"}, t.TempDir())
	if f.Location != nil || f.SourceExcerpt != "" {
		t.Errorf("expected no Location/excerpt, got %+v", f)
	}
}
