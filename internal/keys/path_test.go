package keys

import (
	"strings"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	// FR-KEY-4: RFC 3986 §6.2.2.1-6.2.2.2, dot-segments never resolved.
	tests := []struct {
		name, in, want string
	}{
		{"plain path unchanged", "/a/b", "/a/b"},
		{"unreserved letter decoded", "/%41b", "/Ab"},
		{"unreserved marks decoded", "/%2d%2E%5f%7E", "/-._~"},
		{"lowercase escape uppercased", "/a%2fb", "/a%2Fb"},
		{"uppercase reserved escape kept", "/a%2Fb", "/a%2Fb"},
		{"space stays escaped", "/a%20b", "/a%20b"},
		{"percent sign never double decoded", "/%2541", "/%2541"},
		{"non-ASCII escape uppercased", "/%e9", "/%E9"},
		{"dot segments decoded but not resolved", "/a/%2e%2E/b", "/a/../b"},
		{"literal dot segments kept", "/a/../b", "/a/../b"},
		{"digits decoded", "/%31%32", "/12"},
		{"trailing percent leaves path unchanged", "/%41%", "/%41%"},
		{"short escape leaves path unchanged", "/%41%4", "/%41%4"},
		{"bad hex leaves path unchanged", "/%zz%41", "/%zz%41"},
		{"stray percent cannot pair with decoded digits", "/%%3441", "/%%3441"},
		{"asterisk form", "*", "*"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizePath(tt.in); got != tt.want {
				t.Fatalf("normalizePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizePathNoAllocWhenCanonical(t *testing.T) {
	// Hot path: an already canonical path is returned as is.
	p := "/a/%2F/b"
	if n := testing.AllocsPerRun(100, func() { normalizePath(p) }); n != 0 {
		t.Fatalf("normalizePath allocates %v times on a canonical path", n)
	}
}

func FuzzNormalizePath(f *testing.F) {
	// FR-KEY-4, NFR-2: idempotent, never longer, and only unreserved bytes
	// are decoded, so no new "%", "/", "?" or "#" can appear (P2).
	f.Fuzz(func(t *testing.T, p string) {
		n := normalizePath(p)
		if normalizePath(n) != n {
			t.Fatalf("not idempotent: %q -> %q -> %q", p, n, normalizePath(n))
		}
		if len(n) > len(p) {
			t.Fatalf("normalizePath(%q) = %q grew", p, n)
		}
		for _, s := range []string{"%", "/", "?", "#"} {
			if strings.Count(n, s) > strings.Count(p, s) {
				t.Fatalf("normalizePath(%q) = %q introduced %q", p, n, s)
			}
		}
	})
}
