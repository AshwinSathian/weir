package weir

import (
	"net/http"
	"testing"
	"time"
)

// FR-NEG-2, RFC 9110 §10.2.3: Retry-After is delay-seconds or an HTTP-date;
// anything else, or a delay that is not positive, records nothing.
func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name, v string
		want    time.Duration
	}{
		{"empty records nothing", "", 0},
		{"delay-seconds", "7", 7 * time.Second},
		{"zero records nothing", "0", 0},
		{"negative records nothing", "-1", 0},
		{"overflowing delay records nothing", "9223372036854775807", 0},
		{"too many digits records nothing", "99999999999999999999", 0},
		{"leading plus is accepted (strconv)", "+7", 7 * time.Second},
		{"fraction records nothing", "1.5", 0},
		{"future date in whole seconds", now.Add(90*time.Second + 500*time.Millisecond).Format(http.TimeFormat), 90 * time.Second},
		{"past date records nothing", now.Add(-time.Minute).Format(http.TimeFormat), 0},
		{"garbage records nothing", "soon", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryAfter(tc.v, now); got != tc.want {
				t.Fatalf("retryAfter(%q) = %v, want %v", tc.v, got, tc.want)
			}
		})
	}
}

// FR-NEG-2, NFR-2: any origin Retry-After value yields a non-negative whole
// number of seconds and never panics.
func FuzzRetryAfter(f *testing.F) {
	for _, s := range []string{"7", "-1", "0", "99999999999999999999", "Thu, 01 Oct 2026 12:01:30 GMT", "Sunday, 06-Nov-94 08:49:37 GMT", ""} {
		f.Add(s)
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, v string) {
		d := retryAfter(v, now)
		if d < 0 || d%time.Second != 0 {
			t.Fatalf("retryAfter(%q) = %v", v, d)
		}
	})
}
