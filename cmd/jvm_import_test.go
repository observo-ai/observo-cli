package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func resetImportFlags() {
	jiManifest, jiFrom, jiTestNG = "", "", ""
	jiChain, jiProject, jiLayer, jiPriority = "steps", "", "API", "MEDIUM"
	jiApply, jiAllowCI = false, false
	jiStateFile = "/nonexistent-import-state.json"
}

func writeImportManifest(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "observo-link-manifest.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const chainManifestJSON = `{"version":1,"entries":[
	{"code":"PD-101","fq_name":"a.Chain#x","display_name":"x","feature":"Wallet","chain_id":"a.Chain","order":1},
	{"code":null,"fq_name":"a.Chain#y","display_name":"y","feature":"Wallet","chain_id":"a.Chain","order":2}
]}`

func TestJVMImport_DryRunByDefaultNoWrites(t *testing.T) {
	resetImportFlags()
	t.Setenv("CI", "") // dry-run ignores CI, but keep deterministic
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("dry-run must not call the API: %s %s", r.Method, r.URL.Path)
		http.Error(w, "no", 500)
	}))
	defer srv.Close()
	mf := writeImportManifest(t, chainManifestJSON)

	out, err := executeRoot(t, "jvm", "import", "--manifest", mf, "--project", "PD",
		"--chain", "flat", "--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "dry-run") {
		t.Errorf("expected dry-run output, got:\n%s", out)
	}
}

func TestJVMImport_ApplyFlatCreatesSkipsAndPlans(t *testing.T) {
	resetImportFlags()
	t.Setenv("CI", "")
	var mu sync.Mutex
	counts := map[string]int{}
	var planIDs []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/projects/PD/suites":
			counts["list_suites"]++
			w.Write([]byte(`{"suites":[]}`)) // no existing suites
		case r.Method == "POST" && r.URL.Path == "/api/projects/PD/suites":
			counts["create_suite"]++
			w.Write([]byte(`{"suite":{"id":"s1","name":"Wallet"}}`))
		case r.Method == "GET" && r.URL.Path == "/api/case/PD-101":
			counts["get_case"]++
			w.Write([]byte(`{"test_case":{"id":"c1","short_code":"PD-101","name":"x"}}`))
		case r.Method == "POST" && r.URL.Path == "/api/suites/s1/cases":
			counts["create_case"]++
			w.Write([]byte(`{"test_case":{"id":"c2","short_code":"PD-300","name":"y"}}`))
		case r.Method == "GET" && r.URL.Path == "/api/projects/PD/plans":
			counts["list_plans"]++
			w.Write([]byte(`{"plans":[]}`)) // no existing plan → proceed to create
		case r.Method == "POST" && r.URL.Path == "/api/projects/PD/plans":
			counts["create_plan"]++
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			planIDs, _ = b["test_case_ids"].([]any)
			w.Write([]byte(`{"plan":{"id":"p1","plan_key":"CHAIN-D791DF-CHAIN"}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "nf", 404)
		}
	}))
	defer srv.Close()
	mf := writeImportManifest(t, chainManifestJSON)

	out, err := executeRoot(t, "jvm", "import", "--manifest", mf, "--project", "PD",
		"--chain", "flat", "--apply", "--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	// Suite created once; PD-101 linked (GET, no create); y created; plan created.
	if counts["create_suite"] != 1 || counts["get_case"] != 1 || counts["create_case"] != 1 || counts["create_plan"] != 1 {
		t.Errorf("call counts wrong: %v", counts)
	}
	// Plan carries both case UUIDs in chain order (linked c1, created c2).
	if len(planIDs) != 2 || planIDs[0] != "c1" || planIDs[1] != "c2" {
		t.Errorf("plan case order wrong: %v", planIDs)
	}
	// The untracked test is reported as needing a tag.
	if !strings.Contains(out, "a.Chain#y=PD-300") {
		t.Errorf("untagged report missing:\n%s", out)
	}
}

func TestJVMImport_TrackedProbeTransientErrorDoesNotDuplicate(t *testing.T) {
	resetImportFlags()
	t.Setenv("CI", "")
	var mu sync.Mutex
	counts := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/projects/PD/suites" && r.Method == "GET":
			w.Write([]byte(`{"suites":[]}`))
		case r.URL.Path == "/api/projects/PD/suites" && r.Method == "POST":
			w.Write([]byte(`{"suite":{"id":"s1","name":"Wallet"}}`))
		case r.URL.Path == "/api/case/PD-101":
			counts["get_case"]++
			http.Error(w, "boom", http.StatusInternalServerError) // transient!
		case strings.HasSuffix(r.URL.Path, "/cases") && r.Method == "POST":
			counts["create_case"]++ // MUST NOT happen for the tracked PD-101
			w.Write([]byte(`{"test_case":{"id":"c","short_code":"PD-999","name":"x"}}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	// Single tracked entry — no chain, no plan.
	mf := writeImportManifest(t, `{"version":1,"entries":[{"code":"PD-101","fq_name":"a#x","display_name":"x","feature":"Wallet"}]}`)

	out, err := executeRoot(t, "jvm", "import", "--manifest", mf, "--project", "PD",
		"--apply", "--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	// A transient 500 on the idempotency probe must NOT create a duplicate.
	if counts["create_case"] != 0 {
		t.Errorf("transient probe error created a duplicate (create_case=%d)", counts["create_case"])
	}
	if !strings.Contains(out, "to avoid a duplicate") {
		t.Errorf("expected skip-to-avoid-duplicate diagnostic, got:\n%s", out)
	}
}

func TestJVMImport_DryRunSummaryReflectsPlan(t *testing.T) {
	resetImportFlags()
	t.Setenv("CI", "")
	mf := writeImportManifest(t, chainManifestJSON)
	// --json dry-run must carry real counts, not zeros.
	out, err := executeRoot(t, "jvm", "import", "--manifest", mf, "--project", "PD",
		"--chain", "flat", "--json", "--api-key", "k")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	var m map[string]any
	// The JSON summary is on stdout (captured in out); find the JSON object.
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("no JSON in output:\n%s", out)
	}
	if err := json.Unmarshal([]byte(out[start:]), &m); err != nil {
		t.Fatalf("summary not JSON: %v\n%s", err, out)
	}
	// chainManifestJSON: 1 tracked + 1 untracked in a flat chain → 1 created, 1 linked, 1 plan.
	if m["cases_created"].(float64) != 1 || m["cases_linked"].(float64) != 1 || m["plans_created"].(float64) != 1 {
		t.Errorf("dry-run summary not populated from plan: %v", m)
	}
	if m["dry_run"] != true {
		t.Errorf("dry_run flag missing: %v", m)
	}
}

func TestJVMImport_PlanIdempotentWhenKeyExists(t *testing.T) {
	resetImportFlags()
	t.Setenv("CI", "")
	var mu sync.Mutex
	counts := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/projects/PD/suites" && r.Method == "GET":
			w.Write([]byte(`{"suites":[{"suite":{"id":"s1","name":"Wallet"},"children":[]}]}`))
		case r.URL.Path == "/api/case/PD-101":
			w.Write([]byte(`{"test_case":{"id":"c1","short_code":"PD-101","name":"x"}}`))
		case strings.HasSuffix(r.URL.Path, "/cases") && r.Method == "POST":
			w.Write([]byte(`{"test_case":{"id":"c2","short_code":"PD-300","name":"y"}}`))
		case r.URL.Path == "/api/projects/PD/plans" && r.Method == "GET":
			// Plan already exists with the derived key.
			w.Write([]byte(`{"plans":[{"id":"p1","plan_key":"CHAIN-D791DF-CHAIN"}]}`))
		case r.URL.Path == "/api/projects/PD/plans/p1" && r.Method == "GET":
			w.Write([]byte(`{"plan":{"id":"p1","plan_key":"CHAIN-D791DF-CHAIN"}}`))
		case r.URL.Path == "/api/projects/PD/plans" && r.Method == "POST":
			counts["create_plan"]++ // MUST NOT happen — plan exists
			w.Write([]byte(`{"plan":{"id":"p2"}}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	mf := writeImportManifest(t, chainManifestJSON)

	out, err := executeRoot(t, "jvm", "import", "--manifest", mf, "--project", "PD",
		"--chain", "flat", "--apply", "--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if counts["create_plan"] != 0 {
		t.Errorf("existing plan must not be re-created (create_plan=%d)", counts["create_plan"])
	}
	if !strings.Contains(out, "already exists — skipped") {
		t.Errorf("expected plan-skip diagnostic, got:\n%s", out)
	}
}

func TestJVMImport_PlanProbeTransientErrorDoesNotDuplicate(t *testing.T) {
	resetImportFlags()
	t.Setenv("CI", "")
	var mu sync.Mutex
	counts := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/projects/PD/suites" && r.Method == "GET":
			w.Write([]byte(`{"suites":[{"suite":{"id":"s1","name":"Wallet"},"children":[]}]}`))
		case r.URL.Path == "/api/case/PD-101":
			w.Write([]byte(`{"test_case":{"id":"c1","short_code":"PD-101","name":"x"}}`))
		case strings.HasSuffix(r.URL.Path, "/cases") && r.Method == "POST":
			w.Write([]byte(`{"test_case":{"id":"c2","short_code":"PD-300","name":"y"}}`))
		case r.URL.Path == "/api/projects/PD/plans" && r.Method == "GET":
			http.Error(w, "boom", http.StatusInternalServerError) // transient!
		case r.URL.Path == "/api/projects/PD/plans" && r.Method == "POST":
			counts["create_plan"]++ // MUST NOT happen — existence is unknown
			w.Write([]byte(`{"plan":{"id":"p2"}}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	mf := writeImportManifest(t, chainManifestJSON)

	out, err := executeRoot(t, "jvm", "import", "--manifest", mf, "--project", "PD",
		"--chain", "flat", "--apply", "--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	// The key resolves by listing plans, so a 5xx there means "unknown", not
	// "absent". Creating on unknown duplicates the plan on every retried import
	// — the same trap the tracked-case probe above already guards.
	if counts["create_plan"] != 0 {
		t.Errorf("transient plan-probe error created a plan (create_plan=%d)", counts["create_plan"])
	}
	if !strings.Contains(out, "to avoid a duplicate") {
		t.Errorf("expected skip-to-avoid-duplicate diagnostic, got:\n%s", out)
	}
}

func TestJVMImport_PlanCreatedWhenGenuinelyAbsent(t *testing.T) {
	// The other side of the guard above: a real "no such plan" (an empty list,
	// answered successfully) must still create. Without this, tightening the
	// probe could quietly stop creating plans altogether and every flat-chain
	// import would lose its ordering.
	resetImportFlags()
	t.Setenv("CI", "")
	var mu sync.Mutex
	counts := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/projects/PD/suites" && r.Method == "GET":
			w.Write([]byte(`{"suites":[{"suite":{"id":"s1","name":"Wallet"},"children":[]}]}`))
		case r.URL.Path == "/api/case/PD-101":
			w.Write([]byte(`{"test_case":{"id":"c1","short_code":"PD-101","name":"x"}}`))
		case strings.HasSuffix(r.URL.Path, "/cases") && r.Method == "POST":
			w.Write([]byte(`{"test_case":{"id":"c2","short_code":"PD-300","name":"y"}}`))
		case r.URL.Path == "/api/projects/PD/plans" && r.Method == "GET":
			w.Write([]byte(`{"plans":[]}`)) // genuinely absent
		case r.URL.Path == "/api/projects/PD/plans" && r.Method == "POST":
			counts["create_plan"]++
			w.Write([]byte(`{"plan":{"id":"p2","plan_key":"CHAIN-D791DF-CHAIN"}}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	mf := writeImportManifest(t, chainManifestJSON)

	out, err := executeRoot(t, "jvm", "import", "--manifest", mf, "--project", "PD",
		"--chain", "flat", "--apply", "--api-key", "k", "--base-url", srv.URL)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if counts["create_plan"] != 1 {
		t.Errorf("absent plan must be created exactly once (create_plan=%d)\n%s", counts["create_plan"], out)
	}
}

func TestJVMImport_CIGuardBlocksApply(t *testing.T) {
	resetImportFlags()
	t.Setenv("CI", "true")
	mf := writeImportManifest(t, chainManifestJSON)

	// --apply under CI without --allow-ci → refuse.
	_, err := executeRoot(t, "jvm", "import", "--manifest", mf, "--project", "PD",
		"--apply", "--api-key", "k")
	if err == nil {
		t.Fatal("expected CI-guard to block --apply")
	}
	if !strings.Contains(err.Error(), "allow-ci") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestJVMImport_RequiresProjectAndSource(t *testing.T) {
	resetImportFlags()
	t.Setenv("CI", "")
	// No source.
	if _, err := executeRoot(t, "jvm", "import", "--project", "PD", "--api-key", "k"); err == nil {
		t.Error("expected error with no source")
	}
	// No project.
	resetImportFlags()
	mf := writeImportManifest(t, `{"version":1,"entries":[]}`)
	if _, err := executeRoot(t, "jvm", "import", "--manifest", mf, "--api-key", "k"); err == nil {
		t.Error("expected error with no project")
	}
}

func TestLayerAndPriorityEnum(t *testing.T) {
	if got, _ := layerEnum("api"); got != "LAYER_API" {
		t.Errorf("layerEnum(api) = %q", got)
	}
	if _, err := layerEnum("bogus"); err == nil {
		t.Error("expected error for bad layer")
	}
	if got, _ := priorityEnum("high"); got != "PRIORITY_HIGH" {
		t.Errorf("priorityEnum(high) = %q", got)
	}
	if _, err := priorityEnum("bogus"); err == nil {
		t.Error("expected error for bad priority")
	}
}
