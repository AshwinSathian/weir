package keys

import (
	"slices"
	"strings"
	"testing"
)

func TestRewriteQuery(t *testing.T) {
	// FR-KEY-5; T-2
	drop := &Config{QueryDrop: []string{"utm_*", "fbclid"}}
	keep := &Config{QueryKeep: []string{"id", "page*"}}
	sorted := &Config{QuerySort: true}
	tests := []struct {
		name string
		cfg  *Config
		raw  string
		want string
	}{
		{"empty stays empty", drop, "", ""},
		{"no rules keeps bytes", &Config{}, "b=2&a=1", "b=2&a=1"},
		{"empty segments dropped", &Config{}, "&&a=1&&b=2&", "a=1&b=2"},
		{"only separators become empty", &Config{}, "&&&", ""},
		{"exact name dropped", drop, "a=1&fbclid=x", "a=1"},
		{"exact pattern does not match longer name", drop, "fbclid2=x", "fbclid2=x"},
		{"prefix pattern drops matches", drop, "utm_source=a&x=1&utm_medium=b", "x=1"},
		{"name without = is matched", drop, "utm_x&x", "x"},
		{"encoded name is a different name", drop, "utm%5Fsource=a", "utm%5Fsource=a"},
		{"name matching is case-sensitive", drop, "UTM_source=a", "UTM_source=a"},
		{"everything dropped leaves empty", drop, "utm_a=1&fbclid=2", ""},
		{"keep removes non-matching", keep, "id=1&x=2&page_size=3&pagex", "id=1&page_size=3&pagex"},
		{"keep matches exact only without star", keep, "idx=1", ""},
		{"sort orders raw bytes", sorted, "b=2&a=2&a=1&B=0", "B=0&a=1&a=2&b=2"},
		{"sort keeps duplicates", sorted, "a=1&a=1", "a=1&a=1"},
		{"values are not decoded", &Config{QuerySort: true}, "a=%2F&a=/", "a=%2F&a=/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rewriteQuery(tt.raw, tt.cfg); got != tt.want {
				t.Fatalf("rewriteQuery(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParameterCloakingSemicolon(t *testing.T) {
	// FR-KEY-5; T-2: ';' is not a separator, so a cloaked parameter stays
	// inside a kept segment and is keyed and forwarded byte-for-byte.
	c := &Config{QueryDrop: []string{"utm_*"}}
	tests := []struct {
		name, raw, want string
	}{
		{"cloaked parameter kept inside segment a", "a=1;utm_x=2", "a=1;utm_x=2"},
		{"segment named utm_x dropped whole", "utm_x=2;a=1&b=3", "b=3"},
		{"second segment cloaking kept", "b=3&a=1;utm_x=2", "b=3&a=1;utm_x=2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rewriteQuery(tt.raw, c); got != tt.want {
				t.Fatalf("rewriteQuery(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func FuzzQueryRewrite(f *testing.F) {
	// FR-KEY-5, NFR-2; T-2: no panic, every output segment is an input
	// segment, no dropped name survives, and rewriting is idempotent.
	f.Add("a=1;b=2&utm_x=3", false, false)
	f.Fuzz(func(t *testing.T, raw string, useKeep, sort bool) {
		c := &Config{QueryDrop: []string{"utm_*", "fbclid"}, QuerySort: sort}
		if useKeep {
			c.QueryKeep = []string{"a", "b*"}
		}
		got := rewriteQuery(raw, c)
		if got == "" {
			return
		}
		in := strings.Split(raw, "&")
		out := strings.Split(got, "&")
		// Oracle independent of match: utm_ prefix, fbclid exact, keep a or b*.
		dropped := func(name string) bool {
			return strings.HasPrefix(name, "utm_") || name == "fbclid" ||
				useKeep && name != "a" && !strings.HasPrefix(name, "b")
		}
		rest := slices.Clone(in)
		j := 0
		for _, s := range out {
			// Each output segment consumes one input segment; without sort
			// it must also come later than the previous one.
			i := slices.Index(rest, s)
			if s == "" || i < 0 {
				t.Fatalf("rewriteQuery(%q) = %q: segment %q not in input", raw, got, s)
			}
			rest = slices.Delete(rest, i, i+1)
			if !sort {
				k := slices.Index(in[j:], s)
				if k < 0 {
					t.Fatalf("rewriteQuery(%q) = %q: segment %q out of order", raw, got, s)
				}
				j += k + 1
			}
			name, _, _ := strings.Cut(s, "=")
			if dropped(name) {
				t.Fatalf("rewriteQuery(%q) = %q: kept %q", raw, got, s)
			}
		}
		if sort && !slices.IsSorted(out) {
			t.Fatalf("rewriteQuery(%q) = %q: not sorted", raw, got)
		}
		if again := rewriteQuery(got, c); again != got {
			t.Fatalf("not idempotent: %q -> %q -> %q", raw, got, again)
		}
	})
}
