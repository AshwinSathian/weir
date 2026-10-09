package httpcc

import (
	"math"
	"strings"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// RangeKind is the outcome of ParseRange.
type RangeKind uint8

// Range outcomes (04 §13.1).
const (
	RangeNone          RangeKind = iota // absent, invalid, unknown unit or multi-range: serve the full 200
	RangeOK                             // one satisfiable range
	RangeUnsatisfiable                  // one well-formed range outside the representation: 416
)

// maxRangeHeader bounds the header ParseRange looks at (04 §13.1); a longer
// one is ignored, which RFC 9110 §14.2 permits.
const maxRangeHeader = 256

// ParseRange evaluates a Range header value against a representation of size
// bytes (RFC 9110 §14.1.2, FR-RNG-2, FR-RNG-3). For RangeOK it returns the
// inclusive byte positions to serve.
func ParseRange(h string, size int64) (start, end int64, kind RangeKind) {
	if len(h) > maxRangeHeader || size < 0 {
		return 0, 0, RangeNone
	}
	h = strings.Trim(h, " \t")
	unit, spec, ok := strings.Cut(h, "=")
	if !ok || !strings.EqualFold(strings.TrimRight(unit, " \t"), "bytes") {
		return 0, 0, RangeNone
	}
	spec = strings.Trim(spec, " \t")
	if spec == "" || strings.IndexByte(spec, ',') >= 0 { // multi-range: Weir serves 200
		return 0, 0, RangeNone
	}
	first, last, ok := strings.Cut(spec, "-")
	if !ok {
		return 0, 0, RangeNone
	}
	if first == "" { // suffix-range: -n
		n, ok := parseRangeInt(last)
		if !ok {
			return 0, 0, RangeNone
		}
		if n == 0 || size == 0 {
			return 0, 0, RangeUnsatisfiable
		}
		return max(size-n, 0), size - 1, RangeOK
	}
	a, ok := parseRangeInt(first)
	if !ok {
		return 0, 0, RangeNone
	}
	b := int64(math.MaxInt64)
	if last != "" {
		if b, ok = parseRangeInt(last); !ok || b < a { // last < first is invalid, not unsatisfiable
			return 0, 0, RangeNone
		}
	}
	if a >= size {
		return 0, 0, RangeUnsatisfiable
	}
	return a, min(b, size-1), RangeOK
}

// parseRangeInt reads 1*DIGIT, saturating at MaxInt64 so a huge position is
// still "past the end" instead of an error (NFR-2).
func parseRangeInt(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var n int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		d := int64(c - '0')
		if n > (math.MaxInt64-d)/10 {
			n = math.MaxInt64
			continue
		}
		n = n*10 + d
	}
	return n, true
}

// IfRangeApplies reports whether a request's If-Range lets its Range apply
// to ent (FR-RNG-1, RFC 9110 §13.1.5). Only a strong entity-tag equal to the
// stored strong ETag, or an HTTP-date equal to a Last-Modified that is a
// strong validator, qualifies; anything else, an empty value included, means
// the full 200. Callers skip this when the request has no If-Range.
func IfRangeApplies(ifRange string, ent *store.Entry) bool {
	ifRange = strings.Trim(ifRange, " \t")
	if ifRange == "" {
		return false
	}
	if ifRange[0] == '"' || strings.HasPrefix(ifRange, "W/") { // an entity-tag, never a date
		return ent.ETag != "" && !strings.HasPrefix(ent.ETag, "W/") &&
			!strings.HasPrefix(ifRange, "W/") && ifRange == ent.ETag
	}
	t, ok := ParseDate(ifRange)
	if !ok || ent.LastModified.IsZero() || ent.Date.IsZero() {
		return false
	}
	// RFC 9110 §8.8.2.2: a Last-Modified within one second of Date may still
	// change, so it is not a strong validator.
	return ent.Date.Sub(ent.LastModified) >= time.Second && t.Equal(ent.LastModified.Truncate(time.Second))
}
