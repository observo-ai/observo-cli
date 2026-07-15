package doctor

import (
	"context"
	"strings"
	"testing"

	"github.com/observo-ai/observo-cli/internal/api"
)

// fakeProber is an in-memory Prober for table tests — no network.
type fakeProber struct {
	projects []api.Project
	projErr  error
	runs     []api.Run
	runsErr  error
}

func (f fakeProber) ListProjects(context.Context) ([]api.Project, error) {
	return f.projects, f.projErr
}
func (f fakeProber) ListRuns(context.Context, string) ([]api.Run, error) {
	return f.runs, f.runsErr
}

// runWith builds a single-CI-run slice with one layer carrying the given
// coverage id + framework. Empty strings omit that signal.
func runWith(lcovID, framework string) []api.Run {
	layer := map[string]any{}
	if lcovID != "" {
		layer["coverage"] = map[string]any{"lcov_attachment_id": lcovID}
	}
	if framework != "" {
		layer["framework"] = framework
	}
	return []api.Run{{
		ID:       "r1",
		Pipeline: &api.Pipeline{Layers: []map[string]any{layer}},
	}}
}

var oneProject = []api.Project{{ID: "p1", Name: "Observo", Code: "OB"}}

func TestDiagnose_LadderPerLevel(t *testing.T) {
	tests := []struct {
		name        string
		opts        Options
		prober      fakeProber
		wantReached int
		wantFirst   int // -1 => fully grounded
	}{
		{
			name:        "below L0 — no verdict workflow",
			opts:        Options{VerdictWorkflow: false},
			prober:      fakeProber{projects: oneProject, runs: runWith("att", "playwright")},
			wantReached: 0,
			wantFirst:   0,
		},
		{
			name:        "L0 only — project unresolved (2 projects, no var)",
			opts:        Options{VerdictWorkflow: true},
			prober:      fakeProber{projects: []api.Project{{Code: "OB"}, {Code: "WEB"}}},
			wantReached: 0,
			wantFirst:   1,
		},
		{
			name:        "L1 — sole-project fallback, no coverage run",
			opts:        Options{VerdictWorkflow: true, PipelineWorkflow: true},
			prober:      fakeProber{projects: oneProject, runs: runWith("", "")},
			wantReached: 1,
			wantFirst:   2,
		},
		{
			name:        "L2 — coverage present, no playwright layer",
			opts:        Options{VerdictWorkflow: true, PipelineWorkflow: true, EnvProject: "OB"},
			prober:      fakeProber{projects: oneProject, runs: runWith("att-1", "go")},
			wantReached: 2,
			wantFirst:   3,
		},
		{
			name:        "L3 — fully grounded",
			opts:        Options{VerdictWorkflow: true, PipelineWorkflow: true, EnvProject: "OB"},
			prober:      fakeProber{projects: oneProject, runs: runWith("att-1", "playwright")},
			wantReached: 3,
			wantFirst:   -1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := Diagnose(context.Background(), tc.prober, tc.opts)
			if r.Reached != tc.wantReached {
				t.Errorf("Reached = %d, want %d", r.Reached, tc.wantReached)
			}
			if r.FirstFail != tc.wantFirst {
				t.Errorf("FirstFail = %d, want %d", r.FirstFail, tc.wantFirst)
			}
			// Fully-grounded must carry no fix; every other case must carry one.
			if tc.wantFirst == -1 {
				if !r.Fully() || r.Fix != "" {
					t.Errorf("expected fully grounded with no fix, got Fully=%v fix=%q", r.Fully(), r.Fix)
				}
			} else if r.Fix == "" {
				t.Errorf("expected a fix for first failing level %d, got empty", tc.wantFirst)
			}
		})
	}
}

func TestDiagnose_InvalidAPIKey_FailsL1WithKeyFix(t *testing.T) {
	prober := fakeProber{projErr: &api.HTTPError{StatusCode: 401, Body: "unauthorized"}}
	r := Diagnose(context.Background(), prober, Options{VerdictWorkflow: true})
	if r.Reached != 0 || r.FirstFail != LevelProject {
		t.Fatalf("Reached=%d FirstFail=%d, want 0 / %d", r.Reached, r.FirstFail, LevelProject)
	}
	if !strings.Contains(r.Fix, "OBSERVO_API_KEY") {
		t.Errorf("L1 fix should mention OBSERVO_API_KEY, got %q", r.Fix)
	}
}

func TestDiagnose_DeprecatedProjectCodeWarns(t *testing.T) {
	prober := fakeProber{projects: oneProject, runs: runWith("att", "playwright")}
	r := Diagnose(context.Background(), prober, Options{
		VerdictWorkflow:  true,
		PipelineWorkflow: true,
		EnvProjectCode:   "OB", // deprecated var used → warning, still resolves
	})
	if r.ProjectCode != "OB" {
		t.Errorf("ProjectCode = %q, want OB", r.ProjectCode)
	}
	if len(r.Warnings) == 0 || !strings.Contains(r.Warnings[0], "OBSERVO_PROJECT_CODE") {
		t.Errorf("expected a deprecation warning for OBSERVO_PROJECT_CODE, got %v", r.Warnings)
	}
}

func TestDiagnose_ProjectFlagWinsOverEnv(t *testing.T) {
	prober := fakeProber{
		projects: []api.Project{{Code: "OB", Name: "Observo"}, {Code: "WEB", Name: "Web"}},
		runs:     runWith("att", "playwright"),
	}
	r := Diagnose(context.Background(), prober, Options{
		VerdictWorkflow:  true,
		PipelineWorkflow: true,
		ProjectFlag:      "WEB",
		EnvProject:       "OB",
	})
	if r.ProjectCode != "WEB" {
		t.Errorf("ProjectCode = %q, want WEB (flag wins over env)", r.ProjectCode)
	}
}

func TestDiagnose_UnknownProjectCodeFailsL1(t *testing.T) {
	prober := fakeProber{projects: oneProject}
	r := Diagnose(context.Background(), prober, Options{VerdictWorkflow: true, ProjectFlag: "NOPE"})
	if r.FirstFail != LevelProject {
		t.Fatalf("FirstFail = %d, want %d", r.FirstFail, LevelProject)
	}
	if !strings.Contains(r.Levels[LevelProject].Detail, "not found") {
		t.Errorf("expected 'not found' detail, got %q", r.Levels[LevelProject].Detail)
	}
}

func TestLatestCIRun_SkipsManualRunsPicksMostRecentPipeline(t *testing.T) {
	runs := []api.Run{
		{ID: "manual", Pipeline: nil},                                         // manual run, skipped
		{ID: "ci-new", Pipeline: &api.Pipeline{Layers: []map[string]any{{}}}}, // most recent CI run
		{ID: "ci-old", Pipeline: &api.Pipeline{Layers: []map[string]any{{}}}},
	}
	got := latestCIRun(runs)
	if got == nil || got.ID != "ci-new" {
		t.Fatalf("latestCIRun = %+v, want ci-new", got)
	}
}
