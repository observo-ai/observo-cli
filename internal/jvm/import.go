package jvm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Chain-modeling modes for import.
const (
	ChainSteps = "steps" // an order-dependent class → 1 case with N ordered steps
	ChainFlat  = "flat"  // → N cases + a plan preserving the order
)

// plan_key limits mirror the server's val.ValidateRunKey: 1-64 chars from
// [A-Za-z0-9_-]. A generated key that breaks either is rejected on create.
const (
	planKeyMaxLen    = 64
	planKeySuffix    = "-CHAIN"
	planKeyDigestLen = 6
)

// ImportOptions configures PlanImport.
type ImportOptions struct {
	ChainMode string // "steps" (default) | "flat"
}

// PlannedStep is one step of a steps-mode chain case (one per chain method).
type PlannedStep struct {
	Title  string // step action text — the method's display name
	FQName string // source test (for the fqName→code report)
	Code   string // the method's own observo code if it carried one (report only)
}

// PlannedCase is one case the import will create or reconcile.
type PlannedCase struct {
	// Tracked marks an entry whose test already carries an observo:<code>
	// tag → an existing case. The executor verifies it exists and skips
	// (idempotent, no duplicate); a stale code falls through to create.
	Tracked bool
	Code    string // existing short code when Tracked

	Name        string
	Description string
	Feature     string // suite name ("" → project root)
	ChainID     string // set when this case belongs to / represents a chain
	Order       *int   // position within its chain (nil = standalone)

	// Steps is non-nil only for a steps-mode chain case (a whole class
	// collapsed into one case). Such a case is always created new — see the
	// package doc on why steps-mode idempotency ships with AST write-back.
	Steps []PlannedStep

	FQName string // primary source test (standalone case); "" for a chain case
}

// PlannedPlan preserves an order-dependent chain's sequence as a plan
// (flat mode only). CaseFQNames are in chain order; the executor resolves
// each to the created/linked case UUID.
type PlannedPlan struct {
	Key         string
	Name        string
	CaseFQNames []string
}

// ImportPlan is the dry-runnable, executable result of PlanImport.
type ImportPlan struct {
	Features []string // distinct suite names to ensure (deduped)
	Cases    []PlannedCase
	Plans    []PlannedPlan
}

// PlanImport turns a link manifest into an import plan. It groups
// order-dependent chains (entries sharing chain_id) and models them per the
// chosen mode, dedups suite names, and guards against duplicate fqNames
// within one run. It performs NO I/O — the executor applies it.
func PlanImport(m *LinkManifest, opts ImportOptions) (*ImportPlan, error) {
	mode := opts.ChainMode
	if mode == "" {
		mode = ChainSteps
	}
	if mode != ChainSteps && mode != ChainFlat {
		return nil, fmt.Errorf("invalid chain mode %q (want %s|%s)", mode, ChainSteps, ChainFlat)
	}

	plan := &ImportPlan{}
	featureSet := map[string]bool{}
	addFeature := func(f string) {
		if f != "" && !featureSet[f] {
			featureSet[f] = true
			plan.Features = append(plan.Features, f)
		}
	}

	// Partition entries into chains vs standalone, with an in-run guard
	// against duplicate fqNames (a manifest shouldn't carry them post-
	// collapse, but never create the same test twice in one run).
	chains := map[string][]LinkEntry{}
	var chainOrder []string
	var singles []LinkEntry
	seenFQ := map[string]bool{}
	for _, e := range m.Entries {
		if e.FQName != "" {
			if seenFQ[e.FQName] {
				continue
			}
			seenFQ[e.FQName] = true
		}
		if e.ChainID != "" {
			if _, ok := chains[e.ChainID]; !ok {
				chainOrder = append(chainOrder, e.ChainID)
			}
			chains[e.ChainID] = append(chains[e.ChainID], e)
		} else {
			singles = append(singles, e)
		}
	}

	emitStandalone := func(e LinkEntry) {
		addFeature(e.Feature)
		pc := PlannedCase{
			Name:        e.DisplayName,
			Description: e.Description,
			Feature:     e.Feature,
			ChainID:     e.ChainID,
			Order:       e.Order,
			FQName:      e.FQName,
		}
		if e.Tracked() {
			pc.Tracked = true
			pc.Code = *e.Code
		}
		plan.Cases = append(plan.Cases, pc)
	}

	for _, cid := range chainOrder {
		es := chains[cid]
		sort.SliceStable(es, func(i, j int) bool { return orderVal(es[i]) < orderVal(es[j]) })

		if len(es) == 1 {
			emitStandalone(es[0]) // a "chain" of one is just a case
			continue
		}

		switch mode {
		case ChainFlat:
			for _, e := range es {
				emitStandalone(e)
			}
			pp := PlannedPlan{Key: chainPlanKey(cid), Name: chainDisplayName(cid, es)}
			for _, e := range es {
				pp.CaseFQNames = append(pp.CaseFQNames, e.FQName)
			}
			plan.Plans = append(plan.Plans, pp)

		case ChainSteps:
			feature := firstFeature(es)
			addFeature(feature)
			pc := PlannedCase{
				Name:    chainDisplayName(cid, es),
				Feature: feature,
				ChainID: cid,
			}
			for _, e := range es {
				pc.Steps = append(pc.Steps, PlannedStep{
					Title:  e.DisplayName,
					FQName: e.FQName,
					Code:   codeOf(e),
				})
			}
			plan.Cases = append(plan.Cases, pc)
		}
	}

	for _, e := range singles {
		emitStandalone(e)
	}
	return plan, nil
}

func orderVal(e LinkEntry) int {
	if e.Order != nil {
		return *e.Order
	}
	return 0
}

func codeOf(e LinkEntry) string {
	if e.Tracked() {
		return *e.Code
	}
	return ""
}

func firstFeature(es []LinkEntry) string {
	for _, e := range es {
		if e.Feature != "" {
			return e.Feature
		}
	}
	return ""
}

// simpleClassName returns the last dot-separated segment of a chain_id
// (a class fq-name), e.g. "api.pd.PdBackendE2ETest" → "PdBackendE2ETest".
func simpleClassName(chainID string) string {
	if i := strings.LastIndex(chainID, "."); i >= 0 && i < len(chainID)-1 {
		return chainID[i+1:]
	}
	return chainID
}

// chainDisplayName is the human name for a chain: its suite feature when
// present, else the simple class name.
func chainDisplayName(chainID string, es []LinkEntry) string {
	if f := firstFeature(es); f != "" {
		return f
	}
	return simpleClassName(chainID)
}

// chainPlanKey derives a stable, URL-safe plan key from a chain's fq class
// name, e.g. "api.pd.PdBackendE2ETest" → "PDBACKENDE2ETEST-8B1A47-CHAIN".
//
// The simple class name alone is NOT unique. "api.pd.SmokeTest" and
// "api.trading.SmokeTest" are different chains that would collide on
// "SMOKETEST-CHAIN" — and because the caller skips a plan whose key already
// exists, the second chain's plan would be silently never created. A shared
// simple class name across packages is ordinary in multi-module JVM suites.
//
// The fq name can't be used verbatim either: the server caps plan_key at 64
// chars and allows only [A-Za-z0-9_-] (val.ValidateRunKey), which a deep
// package name blows past — trading a silent collision for a rejected plan.
//
// So: the readable simple name, plus a short digest of the FULL chain id for
// uniqueness, truncated to fit. The digest is content-derived and therefore
// stable across runs, which idempotent re-import depends on — a key that moved
// between imports would duplicate every plan.
func chainPlanKey(chainID string) string {
	sum := sha256.Sum256([]byte(chainID))
	digest := strings.ToUpper(hex.EncodeToString(sum[:]))[:planKeyDigestLen]

	name := sanitizePlanKey(strings.ToUpper(simpleClassName(chainID)))
	// budget = 64 − "-CHAIN" − digest − the dash before the digest
	budget := planKeyMaxLen - len(planKeySuffix) - planKeyDigestLen - 1
	if len(name) > budget {
		name = name[:budget]
	}
	name = strings.Trim(name, "-")
	if name == "" {
		// A chain id of only punctuation still has to yield a valid key.
		return "CHAIN-" + digest + planKeySuffix
	}
	return name + "-" + digest + planKeySuffix
}

// sanitizePlanKey maps anything outside the server's plan_key charset
// ([A-Za-z0-9_-]) to a dash. Nested JVM classes ("Outer$Inner") and Kotlin
// backtick names reach here with characters the server rejects.
func sanitizePlanKey(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		default:
			return '-'
		}
	}, s)
}
