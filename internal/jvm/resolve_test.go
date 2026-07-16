package jvm

import "testing"

// TestResolve_CrossLanguageParity is the OB-543 AC1 test: the canonical
// key must resolve identically from all four native forms.
func TestResolve_CrossLanguageParity(t *testing.T) {
	const want = "PD-101"
	cases := []struct {
		name     string
		tags     []string
		tmsLinks []string
	}{
		{"JUnit5 @Tag", []string{"observo:PD-101"}, nil},
		{"TestNG groups", []string{"observo:PD-101"}, nil},
		{"Playwright title-tag", []string{"@observo:PD-101"}, nil},
		{"Allure @TmsLink fallback", nil, []string{"PD-101"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(tc.tags, tc.tmsLinks); got != want {
				t.Fatalf("Resolve(%v, %v) = %q, want %q", tc.tags, tc.tmsLinks, got, want)
			}
		})
	}
}

func TestFromTag(t *testing.T) {
	cases := []struct {
		in       string
		wantCode string
		wantOK   bool
	}{
		{"observo:PD-101", "PD-101", true},
		{"@observo:PD-101", "PD-101", true},
		{"observo:OB-543", "OB-543", true}, // not hard-coded to PD
		{"observo:ABC123-7", "ABC123-7", true},
		{"  observo:PD-9  ", "PD-9", true}, // trimmed
		// Non-matches:
		{"PD-101", "", false},         // bare code is not a tag
		{"observo:pd-101", "", false}, // lowercase prefix rejected
		{"Observo:PD-101", "", false}, // wrong-case keyword rejected
		{"observo:PD-", "", false},    // no digits
		{"observo:PD101", "", false},  // no dash
		{"@smoke", "", false},         // unrelated tag
		{"observo:PD-101 extra", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		gotCode, gotOK := FromTag(tc.in)
		if gotCode != tc.wantCode || gotOK != tc.wantOK {
			t.Errorf("FromTag(%q) = (%q, %v), want (%q, %v)", tc.in, gotCode, gotOK, tc.wantCode, tc.wantOK)
		}
	}
}

func TestFromTmsLink(t *testing.T) {
	cases := []struct {
		in       string
		wantCode string
		wantOK   bool
	}{
		{"PD-101", "PD-101", true},         // bare code accepted here
		{"observo:PD-101", "PD-101", true}, // redundant prefix tolerated
		{"OB-9999", "OB-9999", true},
		{"", "", false},
		{"not-a-code", "", false},
		{"PD 101", "", false},
	}
	for _, tc := range cases {
		gotCode, gotOK := FromTmsLink(tc.in)
		if gotCode != tc.wantCode || gotOK != tc.wantOK {
			t.Errorf("FromTmsLink(%q) = (%q, %v), want (%q, %v)", tc.in, gotCode, gotOK, tc.wantCode, tc.wantOK)
		}
	}
}

func TestResolve_TagBeatsTmsLink(t *testing.T) {
	// An authoritative tag wins over a conflicting @TmsLink fallback.
	if got := Resolve([]string{"observo:PD-1"}, []string{"PD-2"}); got != "PD-1" {
		t.Errorf("tag should win over tmsLink: got %q want PD-1", got)
	}
}

func TestResolve_NoLinkReturnsEmpty(t *testing.T) {
	if got := Resolve([]string{"@smoke", "regression"}, []string{"not-a-code"}); got != "" {
		t.Errorf("expected empty for no resolvable link, got %q", got)
	}
}
