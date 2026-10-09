package weir

import "testing"

// FR-RNG-4, T-37: a Content-Range total decides whether a fill starts, so
// anything unparsable or negative must read as "no total".
func TestContentRangeTotal(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     int64
		ok       bool
	}{
		{"closed", "bytes 0-1/10", 10, true},
		{"zero total has no byte to span", "bytes 0-0/0", 0, false},
		{"unknown total", "bytes 0-1/*", 0, false},
		{"unsatisfied form is a 416 shape", "bytes */10", 0, false},
		{"first after last", "bytes 5-2/10", 0, false},
		{"last at or past total", "bytes 0-10/10", 0, false},
		{"garbage range", "bytes garbage/10", 0, false},
		{"wrong unit", "items 0-1/10", 0, false},
		{"no slash", "bytes 0-1", 0, false},
		{"negative", "bytes 0-1/-5", 0, false},
		{"overflow", "bytes 0-1/99999999999999999999", 0, false},
		{"plus sign", "bytes 0-1/+5", 0, false},
		{"empty", "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := contentRangeTotal(tc.in)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("contentRangeTotal(%q) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// NFR-2: parses origin bytes.
func FuzzContentRangeTotal(f *testing.F) {
	for _, s := range []string{"bytes 0-1/10", "bytes 0-1/*", "bytes */10", "", "/", "bytes /", "bytes 0-1/-1", "bytes 0-1/99999999999999999999"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if n, ok := contentRangeTotal(s); ok && n < 0 {
			t.Fatalf("negative total %d from %q", n, s)
		}
	})
}
