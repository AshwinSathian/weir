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
		{"unkeyed bytes do not count toward the limit", []string{"lang=en; pad=" + strings.Repeat("x", 64)}, []Cookie{{"lang", "en"}}},
		{"keyed pairs over the limit all absent", []string{"lang=en; sid=" + strings.Repeat("x", 52)}, nil},
		{"keyed pairs at the limit kept", []string{"lang=en; sid=" + strings.Repeat("x", 51)}, []Cookie{{"lang", "en"}, {"sid", strings.Repeat("x", 51)}}},
		{"separators only", []string{";;; ;"}, nil},
		// T-2: a comma does not split pairs; the whole value is keyed, so an
		// origin that splits on ',' still parses only keyed bytes.
		{"comma stays inside value", []string{"lang=en,sid=x"}, []Cookie{{"lang", "en,sid=x"}}},
		{"quoted and unquoted conflict", []string{`lang="en"; lang=en`}, nil},
		{"trailing OWS equal values agree", []string{"lang=en; lang=en "}, []Cookie{{"lang", "en"}}},
		{"conflict persists after a repeat", []string{"lang=en", "lang=fr", "lang=en"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keyedCookies(tt.lines, c); !slices.Equal(got, tt.want) {
				t.Fatalf("keyedCookies(%q) = %q, want %q", tt.lines, got, tt.want)
			}
		})
	}
}

func TestKeyedCookieLimitCountsKeyedPairs(t *testing.T) {
	// FR-VAL-3, FR-KEY-6, T-13: MaxKeyedHeaderBytes bounds the forwarded keyed
	// pairs, not the raw lines, so a 4 KiB analytics cookie next to a keyed
	// lang keeps lang. The scan stays linear in the header size.
	c := &Config{Cookies: []string{"lang"}, MaxKeyedHeaderBytes: 1024}
	lines := []string{"_ga=" + strings.Repeat("a", 4096) + "; lang=en", "_gid=" + strings.Repeat("b", 4096)}
	if got := keyedCookies(lines, c); !slices.Equal(got, []Cookie{{"lang", "en"}}) {
		t.Fatalf("keyedCookies = %q, want lang=en", got)
	}
	lines = []string{"lang=" + strings.Repeat("e", 1020)}
	if got := keyedCookies(lines, c); got != nil {
		t.Fatalf("oversized keyed value: keyedCookies = %q, want absent", got)
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
		if h := cookieHeader(got); len(h) > c.MaxKeyedHeaderBytes {
			t.Fatalf("keyed pairs %d bytes over the limit", len(h))
		}
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
