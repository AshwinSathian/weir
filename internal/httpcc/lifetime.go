package httpcc

import (
	"net/http"
	"strings"
	"time"
)

// maxLifetime is the delta-seconds ceiling as a duration. Every lifetime and
// window returned here is clamped to it, operator defaults included, so later
// sums (retention, 04 §4.2) cannot overflow.
const maxLifetime = maxDelta * time.Second

// Config holds the freshness settings httpcc reads, with the engine's
// defaults already applied (weir.FreshnessConfig, 04 §1.1).
type Config struct {
	HeuristicFraction      float64
	HeuristicMax           time.Duration
	DefaultTTL             time.Duration
	DefaultSWR, DefaultSIE time.Duration
}

// Lifetime returns the freshness lifetime of a response received at respTime
// (FR-FRS-1 to FR-FRS-3) and whether it is heuristic. Origin dates are
// compared with each other; respTime stands in only when Date is absent or
// invalid (T-30).
func Lifetime(d ResponseDirectives, h http.Header, status int, respTime time.Time, cfg Config) (lt time.Duration, heuristic bool) {
	if d.Unusable() {
		return 0, false // FR-FRS-2
	}
	switch {
	case d.SMaxAge.Set:
		return time.Duration(d.SMaxAge.V) * time.Second, false
	case d.MaxAge.Set:
		return time.Duration(d.MaxAge.V) * time.Second, false
	}
	date, ok := ParseDate(h.Get("Date"))
	if !ok {
		date = respTime
	}
	if exp := h["Expires"]; len(exp) > 0 {
		// RFC 9111 §4.2.1 permits treating repeated Expires as stale.
		e, ok := ParseDate(exp[0])
		if !ok || len(exp) > 1 {
			return 0, false // FR-FRS-2: invalid Expires is the past
		}
		return clampLifetime(e.Sub(date)), false
	}
	if !heuristicStatus(status) {
		return 0, false
	}
	lm, ok := ParseDate(h.Get("Last-Modified"))
	if !ok {
		return clampLifetime(cfg.DefaultTTL), true
	}
	since := float64(clampLifetime(date.Sub(lm)))
	return clampLifetime(time.Duration(min(cfg.HeuristicFraction*since, float64(cfg.HeuristicMax)))), true
}

// Jitter shortens lt by frac×u (u in [0, 1)) when lt is at least minLT
// (FR-FRS-5). It never lengthens a lifetime, even for out-of-range inputs.
func Jitter(lt time.Duration, frac float64, minLT time.Duration, u float64) time.Duration {
	if lt < minLT || frac == 0 {
		return lt
	}
	cut := float64(lt) * frac * u
	switch {
	case !(cut > 0): // also NaN, which Go's min and max propagate
		return lt
	case cut >= float64(lt):
		return 0
	}
	return lt - time.Duration(cut)
}

// StaleWindows returns the SWR and SIE windows a response permits
// (FR-STL-1 to FR-STL-3). Each operator default applies only when the origin
// sent neither its own directive nor s-maxage; an invalid origin value is 0.
func StaleWindows(d ResponseDirectives, cfg Config) (swr, sie time.Duration) {
	if d.MustRevalidate || d.ProxyRevalidate || d.NoCache {
		return 0, 0
	}
	return window(d.SWR, d.SMaxAge.Set, cfg.DefaultSWR), window(d.SIE, d.SMaxAge.Set, cfg.DefaultSIE)
}

func window(s Seconds, sMaxAge bool, def time.Duration) time.Duration {
	switch {
	case s.Set:
		return time.Duration(s.V) * time.Second
	case sMaxAge:
		return 0
	}
	return clampLifetime(def)
}

// heuristicStatus reports the RFC 9110 §15.1 heuristically cacheable
// statuses. 302 and 307 are storable only with explicit freshness (D39).
func heuristicStatus(status int) bool {
	switch status {
	case 200, 203, 204, 300, 301, 308, 404, 405, 410, 414, 501:
		return true
	}
	return false
}

// ParseDate parses an HTTP-date in any of the three RFC 9110 §5.6.7 forms.
// It is http.ParseTime without its zone laxity: the RFC 850 layout accepts
// any abbreviation and resolves it against the host's TZ (PST is -8h in Los
// Angeles and +0 elsewhere), so the same bytes would give a different
// lifetime per deployment. RFC 850 dates must end in GMT; the IMF-fixdate
// layout has GMT as a literal and asctime has no zone.
// ponytail: RFC 850 two-digit years use Go's 1969 pivot, not RFC 9110's
// 50-years-ahead rule, so 70-75 read as the 1970s. The form is obsolete and
// only the origin sends it. Parse the year by hand if that matters.
func ParseDate(s string) (time.Time, bool) {
	for _, layout := range [...]string{http.TimeFormat, time.RFC850, time.ANSIC} {
		if layout == time.RFC850 && !strings.HasSuffix(s, " GMT") {
			continue
		}
		if !fixedWidth(layout, s) {
			continue
		}
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// fixedWidth rejects what time.Parse accepts beyond the RFC 9110 §5.6.7
// grammar: one-digit hours and days, and runs of spaces. An invalid date
// counts as a time in the past (FR-FRS-2), so reading one loosely would
// reuse a response the origin marked expired. The layouts with fixed-width
// fields are checked by length; RFC 850 has variable weekday names, so its
// day and hour positions are checked from the delimiters.
func fixedWidth(layout, s string) bool {
	switch layout {
	case http.TimeFormat:
		return len(s) == len(http.TimeFormat)
	case time.ANSIC: // asctime pads the day with a space, never a zero
		return len(s) == len(time.ANSIC) && s[8] != '0'
	}
	// RFC 850: "Sunday, 06-Nov-94 08:49:37 GMT"
	if strings.Contains(s, "  ") {
		return false
	}
	comma := strings.IndexByte(s, ',')
	colon := strings.IndexByte(s, ':')
	return comma >= 0 && colon >= 3 && len(s) > comma+4 && s[comma+4] == '-' && s[colon-3] == ' '
}

func clampLifetime(d time.Duration) time.Duration { return min(max(d, 0), maxLifetime) }
