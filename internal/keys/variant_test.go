package keys

import (
	"net/http"
	"slices"
	"testing"

	"github.com/AshwinSathian/weir/store"
)

// FR-KEY-8, 04 §3.3: names are canonical, sorted and deduplicated; "*"
// anywhere wins.
func TestVaryNames(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []string
		star  bool
	}{
		{"absent", nil, nil, false},
		{"one name", []string{"accept-language"}, []string{"Accept-Language"}, false},
		{"sorted across lines, deduplicated", []string{"Origin, accept", "ACCEPT ,origin"}, []string{"Accept", "Origin"}, false},
		{"empty members skipped", []string{" , ,x-a,"}, []string{"X-A"}, false},
		{"star in a list", []string{"Accept", "a, * "}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, star := VaryNames(tt.lines)
			if !slices.Equal(got, tt.want) || star != tt.star {
				t.Fatalf("VaryNames(%q) = %q, %v; want %q, %v", tt.lines, got, star, tt.want, tt.star)
			}
		})
	}
}

// FR-KEY-7, FR-KEY-11, T-3: equal normal forms give one key; anything else
// gives another.
func TestVariantKey(t *testing.T) {
	p1, p2 := store.Key{1}, store.Key{2}
	names := []string{"Accept-Language", "X-A"}
	k := func(p store.Key, h http.Header) store.Key { return VariantKey(p, names, h) }
	base := k(p1, http.Header{"Accept-Language": {"en, fr"}, "X-A": {"1"}})

	same := map[string]http.Header{
		"whitespace around commas and ends": {"Accept-Language": {" en ,\tfr "}, "X-A": {"1"}},
		"lines combined":                    {"Accept-Language": {"en", "fr"}, "X-A": {"1"}},
		"unnamed field ignored":             {"Accept-Language": {"en, fr"}, "X-A": {"1"}, "X-B": {"2"}},
	}
	for name, h := range same {
		if k(p1, h) != base {
			t.Errorf("%s: key differs", name)
		}
	}
	differ := map[string]http.Header{
		"other value":               {"Accept-Language": {"en"}, "X-A": {"1"}},
		"order of members":          {"Accept-Language": {"fr, en"}, "X-A": {"1"}},
		"absent is not empty":       {"Accept-Language": {"en, fr"}},
		"value moved between names": {"Accept-Language": {"en, fr1"}, "X-A": {""}},
	}
	for name, h := range differ {
		if k(p1, h) == base {
			t.Errorf("%s: key collides", name)
		}
	}
	if k(p2, http.Header{"Accept-Language": {"en, fr"}, "X-A": {"1"}}) == base {
		t.Error("primary key not in the variant key")
	}
	if VariantKey(p1, nil, http.Header{"X-A": {"1"}}) != p1 {
		t.Error("no names must give the primary key")
	}
	empty, absent := k(p1, http.Header{"X-A": {""}}), k(p1, http.Header{})
	if empty == absent {
		t.Error("an empty value matches absent")
	}
}

// FuzzVaryNames: NFR-2, T-15. Vary comes from the origin: no panic, and the
// names are sorted, unique, non-empty and never "*".
func FuzzVaryNames(f *testing.F) {
	f.Add("Accept-Encoding, accept-language", "*")
	f.Fuzz(func(t *testing.T, a, b string) {
		names, star := VaryNames([]string{a, b})
		if star && names != nil {
			t.Fatalf("star with names %q", names)
		}
		for i, n := range names {
			if n == "" || n == "*" || i > 0 && names[i-1] >= n {
				t.Fatalf("VaryNames(%q, %q) = %q", a, b, names)
			}
		}
		VariantKey(store.Key{}, names, http.Header{names0(names): {a, b}})
	})
}

func names0(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}
