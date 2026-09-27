package keys

import (
	"slices"
	"strings"
	"testing"
)

func TestKeyedCookies(t *testing.T) {
	// FR-KEY-6, FR-VAL-3
	c := &Config{Cookies: []string{"lang", "sid"}, MaxKeyedHeaderBytes: 64}
	tests := []struct {
		name  string
		lines []string
		want  []Cookie
	}{
		{"no header", nil, nil},
		{"empty header", []string{""}, nil},
		{"config order not header order", []string{"sid=9; x=1; lang=en"}, []Cookie{{"lang", "en"}, {"sid", "9"}}},
		{"across lines", []string{"sid=9", "lang=en"}, []Cookie{{"lang", "en"}, {"sid", "9"}}},
		{"no space after semicolon", []string{"lang=en;sid=9"}, []Cookie{{"lang", "en"}, {"sid", "9"}}},
		{"empty value is present", []string{"lang="}, []Cookie{{"lang", ""}}},
		{"duplicate same value kept", []string{"lang=en; lang=en"}, []Cookie{{"lang", "en"}}},
		{"duplicate conflicting value absent", []string{"lang=en; sid=9", "lang=fr"}, []Cookie{{"sid", "9"}}},
		{"missing = skipped", []string{"lang; sid=9"}, []Cookie{{"sid", "9"}}},
		{"names are case-sensitive", []string{"LANG=en"}, nil},
		{"quoted value kept verbatim", []string{`lang="en"`}, []Cookie{{"lang", `"en"`}}},
		{"value with = kept", []string{"sid=a=b"}, []Cookie{{"sid", "a=b"}}},
		{"non-ASCII value absent", []string{"lang=\xe9n; sid=9"}, []Cookie{{"sid", "9"}}},
		{"value with inner space absent", []string{"lang=e n"}, nil},
		{"malformed duplicate makes name absent", []string{"lang=en; lang=\x01"}, nil},
		{"oversized header all absent", []string{"lang=en; pad=" + strings.Repeat("x", 64)}, nil},
		{"separators only", []string{";;; ;"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keyedCookies(tt.lines, c); !slices.Equal(got, tt.want) {
				t.Fatalf("keyedCookies(%q) = %q, want %q", tt.lines, got, tt.want)
			}
		})
	}
}

func TestCookieHeader(t *testing.T) {
	// FR-KEY-6: forwarded Cookie carries only keyed cookies, config order.
	if got := cookieHeader([]Cookie{{"lang", "en"}, {"sid", ""}}); got != "lang=en; sid=" {
		t.Fatalf("cookieHeader = %q", got)
	}
	if got := cookieHeader(nil); got != "" {
		t.Fatalf("cookieHeader(nil) = %q", got)
	}
}

func FuzzCookies(f *testing.F) {
	// FR-KEY-6, FR-VAL-3, NFR-2: no panic; output follows config order with
	// visible-ASCII values, and the forwarded header parses back to the same
	// cookies, so key and forward agree.
	f.Add("lang=en; sid=9", "lang=fr")
	f.Fuzz(func(t *testing.T, a, b string) {
		c := &Config{Cookies: []string{"lang", "sid", "x"}, MaxKeyedHeaderBytes: 1024}
		got := keyedCookies([]string{a, b}, c)
		last := -1
		for _, ck := range got {
			i := slices.Index(c.Cookies, ck.Name)
			if i <= last || !visibleASCII(ck.Value) || strings.Contains(ck.Value, ";") {
				t.Fatalf("keyedCookies(%q, %q) = %q", a, b, got)
			}
			last = i
		}
		h := cookieHeader(got)
		if back := keyedCookies([]string{h}, c); !slices.Equal(back, got) {
			t.Fatalf("forwarded %q parses to %q, want %q", h, back, got)
		}
	})
}
