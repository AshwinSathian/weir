package httpcc

import (
	"strings"
	"testing"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// FR-RNG-2, FR-RNG-3
func TestParseRange(t *testing.T) {
	tests := []struct {
		name       string
		h          string
		size       int64
		start, end int64
		kind       RangeKind
	}{
		{"closed range", "bytes=0-4", 10, 0, 4, RangeOK},
		{"open end", "bytes=5-", 10, 5, 9, RangeOK},
		{"suffix", "bytes=-3", 10, 7, 9, RangeOK},
		{"suffix longer than body is the whole body", "bytes=-99", 10, 0, 9, RangeOK},
		{"end past the body is clamped", "bytes=8-99", 10, 8, 9, RangeOK},
		{"unit is case-insensitive", "Bytes=1-2", 10, 1, 2, RangeOK},
		{"surrounding whitespace is ignored", " bytes=1-2 ", 10, 1, 2, RangeOK},
		{"start at the size is unsatisfiable", "bytes=10-", 10, 0, 0, RangeUnsatisfiable},
		{"start past the size is unsatisfiable", "bytes=20-30", 10, 0, 0, RangeUnsatisfiable},
		{"zero suffix is unsatisfiable", "bytes=-0", 10, 0, 0, RangeUnsatisfiable},
		{"any range of an empty body is unsatisfiable", "bytes=0-", 0, 0, 0, RangeUnsatisfiable},
		{"huge position saturates", "bytes=99999999999999999999-", 10, 0, 0, RangeUnsatisfiable},
		{"huge end saturates", "bytes=0-99999999999999999999", 10, 0, 9, RangeOK},
		{"absent", "", 10, 0, 0, RangeNone},
		{"unknown unit", "items=0-1", 10, 0, 0, RangeNone},
		{"multi-range gets 200", "bytes=0-1,3-4", 10, 0, 0, RangeNone},
		{"last before first is invalid", "bytes=5-2", 10, 0, 0, RangeNone},
		{"missing dash", "bytes=5", 10, 0, 0, RangeNone},
		{"only a dash", "bytes=-", 10, 0, 0, RangeNone},
		{"signed number", "bytes=+1-2", 10, 0, 0, RangeNone},
		{"letters", "bytes=a-b", 10, 0, 0, RangeNone},
		{"no equals", "bytes", 10, 0, 0, RangeNone},
		{"oversize header", "bytes=0-" + strings.Repeat("1", 300), 10, 0, 0, RangeNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, e, k := ParseRange(tc.h, tc.size)
			if k != tc.kind || (k == RangeOK && (s != tc.start || e != tc.end)) {
				t.Fatalf("ParseRange(%q, %d) = %d, %d, %d; want %d, %d, %d", tc.h, tc.size, s, e, k, tc.start, tc.end, tc.kind)
			}
		})
	}
}

// FR-RNG-1
func TestIfRangeStrongOnly(t *testing.T) {
	date := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	lm := date.Add(-time.Hour)
	tests := []struct {
		name    string
		ifRange string
		ent     store.Entry
		want    bool
	}{
		{"empty value never applies", "", store.Entry{ETag: `"v1"`}, false},
		{"strong etag match", `"v1"`, store.Entry{ETag: `"v1"`}, true},
		{"strong etag mismatch", `"v2"`, store.Entry{ETag: `"v1"`}, false},
		{"weak client tag never matches", `W/"v1"`, store.Entry{ETag: `W/"v1"`}, false},
		{"weak stored tag never matches", `"v1"`, store.Entry{ETag: `W/"v1"`}, false},
		{"no stored tag", `"v1"`, store.Entry{}, false},
		{"date equal to a strong last-modified", httpDate(lm), store.Entry{LastModified: lm, Date: date}, true},
		{"date that differs", httpDate(lm.Add(time.Second)), store.Entry{LastModified: lm, Date: date}, false},
		{"last-modified within a second of date is weak", httpDate(date), store.Entry{LastModified: date, Date: date.Add(500 * time.Millisecond)}, false},
		{"last-modified exactly one second before date is strong", httpDate(date.Add(-time.Second)), store.Entry{LastModified: date.Add(-time.Second), Date: date}, true},
		{"last-modified 999ms before date is weak", httpDate(date), store.Entry{LastModified: date.Add(-999 * time.Millisecond).Truncate(time.Second), Date: date}, false},
		{"no stored last-modified", httpDate(lm), store.Entry{Date: date}, false},
		{"not a date or tag", "tomorrow", store.Entry{LastModified: lm, Date: date}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IfRangeApplies(tc.ifRange, &tc.ent); got != tc.want {
				t.Fatalf("IfRangeApplies(%q) = %v, want %v", tc.ifRange, got, tc.want)
			}
		})
	}
}

// NFR-2: no input panics; RangeOK stays inside the representation.
func FuzzRange(f *testing.F) {
	for _, s := range []string{"bytes=0-4", "bytes=-3", "bytes=5-", "bytes=0-1,3-4", "bytes=-0", "bytes=99999999999999999999-", "items=1-2", "bytes=", "", "bytes=5-2", " bytes = 1 - 2 "} {
		f.Add(s, int64(10))
	}
	f.Add("bytes=0-", int64(0))
	f.Fuzz(func(t *testing.T, h string, size int64) {
		s, e, k := ParseRange(h, size)
		if k == RangeOK && (s < 0 || e < s || e >= size) {
			t.Fatalf("ParseRange(%q, %d) = %d, %d outside the body", h, size, s, e)
		}
		IfRangeApplies(h, &store.Entry{ETag: h, LastModified: time.Unix(0, 0), Date: time.Unix(10, 0)})
	})
}
