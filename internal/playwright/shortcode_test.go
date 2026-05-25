package playwright

import "testing"

func TestResolveShortCode(t *testing.T) {
	cases := []struct {
		name        string
		tags        []string
		titles      []string
		attachments []string
		want        string
	}{
		{
			name: "explicit @observo tag wins over title",
			tags: []string{"@smoke", "@observo:OB-7"},
			// Title also has a code, but the tag is authoritative.
			titles: []string{"Login redirect from OB-99"},
			want:   "OB-7",
		},
		{
			name:        "tag with trailing whitespace still matches",
			tags:        []string{"  @observo:OB-12  "},
			titles:      nil,
			attachments: nil,
			want:        "OB-12",
		},
		{
			name:        "tag without @observo prefix is ignored",
			tags:        []string{"OB-99"}, // bare token, not the annotation form
			titles:      []string{"Some title without code"},
			attachments: nil,
			want:        "",
		},
		{
			name:   "title fallback when no tag",
			tags:   nil,
			titles: []string{"Registration — OB-1 happy path"},
			want:   "OB-1",
		},
		{
			name:   "title with code in parent describe block",
			tags:   nil,
			titles: []string{"signup happy path", "Registration OB-1"}, // spec + parent
			want:   "OB-1",
		},
		{
			name:        "attachment dir fallback when no tag and no code in title",
			tags:        nil,
			titles:      []string{"login redirect"},
			attachments: []string{"test-results/auth-login-OB-7-chromium/video.webm"},
			want:        "OB-7",
		},
		{
			name:        "attachment filename containing OB-X is not enough (must be in dir)",
			tags:        nil,
			titles:      []string{"login redirect"},
			attachments: []string{"test-results/auth-other/screenshot-OB-2.png"},
			want:        "",
		},
		{
			name:        "nothing matches → empty (caller will skip)",
			tags:        []string{"@smoke"},
			titles:      []string{"unrelated title"},
			attachments: []string{"test-results/something/video.webm"},
			want:        "",
		},
		{
			name:   "first code in title wins when multiple",
			tags:   nil,
			titles: []string{"OB-3 vs OB-9 reorder check"},
			want:   "OB-3",
		},
		{
			name:        "boundary-only match — OB-1 inside word does not match",
			tags:        nil,
			titles:      []string{"FOOB-1 should not match"},
			attachments: nil,
			want:        "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveShortCode(tc.tags, tc.titles, tc.attachments)
			if got != tc.want {
				t.Errorf("ResolveShortCode(tags=%v, titles=%v, attachments=%v) = %q; want %q",
					tc.tags, tc.titles, tc.attachments, got, tc.want)
			}
		})
	}
}
