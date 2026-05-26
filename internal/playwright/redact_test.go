package playwright

import (
	"strings"
	"testing"
)

func TestNewRedactor_InvalidExtraPattern(t *testing.T) {
	if _, err := NewRedactor("[invalid("); err == nil {
		t.Fatalf("expected error for malformed regex")
	}
}

func TestRedactor_Apply(t *testing.T) {
	cases := []struct {
		name  string
		extra string
		in    string
		want  string
	}{
		{
			name: "default redacts Authorization header line",
			in:   "Authorization: Bearer abc123\nContent-Type: json",
			want: "<redacted by observo>\nContent-Type: json",
		},
		{
			name: "default redacts JSON password field line",
			in:   `{"email":"a@b.com","password":"hunter2"}`,
			want: "<redacted by observo>",
		},
		{
			name: "default redacts api_key with snake_case",
			in:   `api_key: deadbeef`,
			want: "<redacted by observo>",
		},
		{
			name: "default redacts API-KEY with dash and uppercase",
			in:   `X-API-KEY: cafef00d`,
			want: "<redacted by observo>",
		},
		{
			name: "default lets innocuous body through unchanged",
			in:   `{"items": [1, 2, 3]}`,
			want: `{"items": [1, 2, 3]}`,
		},
		{
			name:  "user --redact extends the default",
			extra: `(?i)session_id`,
			in:    "session_id=xyz\nauthorization: Bearer x",
			want:  "<redacted by observo>\n<redacted by observo>",
		},
		{
			name:  "user pattern only triggers when default would not",
			extra: `card_number`,
			in:    "card_number: 4111\nuser: alice",
			want:  "<redacted by observo>\nuser: alice",
		},
		{
			name: "empty input passes through",
			in:   "",
			want: "",
		},
		{
			name: "multiline preserves non-matching lines verbatim",
			in:   "ok\nAuthorization: x\nfine",
			want: "ok\n<redacted by observo>\nfine",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r, err := NewRedactor(tc.extra)
			if err != nil {
				t.Fatalf("NewRedactor(%q): %v", tc.extra, err)
			}
			got := r.Apply(tc.in)
			if got != tc.want {
				t.Errorf("Apply mismatch\n in:   %s\n got:  %s\n want: %s",
					strings.ReplaceAll(tc.in, "\n", `\n`),
					strings.ReplaceAll(got, "\n", `\n`),
					strings.ReplaceAll(tc.want, "\n", `\n`))
			}
		})
	}
}

func TestRedactor_NilSafeApply(t *testing.T) {
	// Defensive: callers that skip NewRedactor and use a zero-value
	// Redactor (e.g. tests that pass a nil pointer through layers) must
	// not panic. Returning input unchanged is the safest no-op.
	var r *Redactor
	if got := r.Apply("Authorization: leak"); got != "Authorization: leak" {
		t.Errorf("nil Redactor must pass through unchanged, got %q", got)
	}
}
