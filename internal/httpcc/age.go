package httpcc

import (
	"math"
	"strings"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// CorrectedInitialAge returns age_value + response_delay (RFC 9111 §4.2.3,
// FR-FRS-4). Date is deliberately not an input: apparent_age would compare
// the origin's clock with ours (T-30). A list-based Age uses its first
// member; an invalid one is ignored (RFC 9111 §5.1).
func CorrectedInitialAge(ageHdr string, reqTime, respTime time.Time) time.Duration {
	first, _, _ := strings.Cut(ageHdr, ",")
	first = trimOWS(first)
	var age time.Duration
	if first != "" && first[0] != '"' { // Age is a bare token, never quoted
		if v, ok := parseDelta(directive{arg: first, hasArg: true}); ok {
			age = time.Duration(v) * time.Second
		}
	}
	return saturatingAdd(age, max(respTime.Sub(reqTime), 0))
}

// CurrentAge returns the entry's age at now (FR-FRS-4). Both times carry
// monotonic readings in process, so a wall-clock step cannot change it
// (FR-FRS-8). Negative intermediate ages clamp to zero (a decoded entry can
// carry any value); the sum saturates.
func CurrentAge(e *store.Entry, now time.Time) time.Duration {
	initial := max(e.CorrectedInitialAge, 0)
	return saturatingAdd(initial, max(now.Sub(e.ResponseTime), 0))
}

// saturatingAdd adds two non-negative durations, stopping at the maximum: a
// wrapped age would read as brand new, the fail-open direction.
func saturatingAdd(a, b time.Duration) time.Duration {
	if b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}
