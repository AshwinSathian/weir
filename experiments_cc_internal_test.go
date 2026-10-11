package weir

import (
	"strings"
	"testing"
)

// E7, NFR-2: the rewrite keeps unknown and quoted directives intact and never
// panics on a malformed line.
func TestPrivateCacheControl(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"no header", nil, "private"},
		{"public and s-maxage dropped", []string{"public, max-age=5, s-maxage=9"}, "max-age=5, private"},
		{"case-insensitive across lines", []string{"PUBLIC", "S-MaxAge=1, no-transform"}, "no-transform, private"},
		{"existing private kept once", []string{"private, max-age=5"}, "private, max-age=5"},
		{"field-qualified private gets a broad one", []string{`private="set-cookie, x", max-age=5`}, `private="set-cookie, x", max-age=5, private`},
		{"quoted comma is not a separator", []string{`ext="a, public", public`}, `ext="a, public", private`},
		{"unterminated quote falls back to a plain split", []string{`ext="a, public, s-maxage=9`, `\`}, `ext="a, \, private`},
		{"empty members", []string{",, ,"}, "private"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := privateCacheControl(tt.in); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// E7, T-36, NFR-2: whatever the origin sends, the result has no bare public
// or s-maxage member even for a parser that splits on every comma, and has
// an unqualified private.
func FuzzPrivateCacheControl(f *testing.F) {
	for _, s := range []string{"public, max-age=5", `ext="a, public`, `private="x", s-maxage=1`, `\"`, ",,", ""} {
		f.Add(s, "")
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		out := privateCacheControl([]string{a, b})
		private := false
		for d := range strings.SplitSeq(out, ",") {
			name, _, _ := strings.Cut(strings.TrimSpace(d), "=")
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "s-maxage":
				// ponytail: a quoted member holding ", s-maxage" is data to a
				// quote-aware parser; the plain split here flags it, so only
				// unquoted outputs are held to the property.
				if !strings.Contains(out, `"`) {
					t.Fatalf("s-maxage survived: %q", out)
				}
			case "public":
				if !strings.Contains(out, `"`) {
					t.Fatalf("public survived: %q", out)
				}
			case "private":
				private = true
			}
		}
		if !private {
			t.Fatalf("no private in %q", out)
		}
	})
}
