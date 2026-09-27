package httpcc

import (
	"net/http"
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
	if d.Duplicates || d.MaxAge.Invalid || d.SMaxAge.Invalid || d.SWR.Invalid || d.SIE.Invalid {
		return 0, false // FR-FRS-2
	}
	switch {
	case d.SMaxAge.Set:
		return time.Duration(d.SMaxAge.V) * time.Second, false
	case d.MaxAge.Set:
		return time.Duration(d.MaxAge.V) * time.Second, false
	}
	date, ok := parseDate(h.Get("Date"))
	if !ok {
		date = respTime
	}
	if exp := h["Expires"]; len(exp) > 0 {
		// RFC 9111 §4.2.1 permits treating repeated Expires as stale.
		e, ok := parseDate(exp[0])
		if !ok || len(exp) > 1 {
			return 0, false // FR-FRS-2: invalid Expires is the past
		}
		return clampLifetime(e.Sub(date)), false
	}
	if !heuristicStatus(status) {
		return 0, false
	}
	lm, ok := parseDate(h.Get("Last-Modified"))
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
	return lt - time.Duration(min(max(float64(lt)*frac*u, 0), float64(lt)))
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

// parseDate parses an HTTP-date in any of the three RFC 9110 §5.6.7 forms.
// http.ParseTime accepts any zone abbreviation in the RFC 850 form and
// resolves it against the host's TZ, so the same bytes would give a
// different lifetime per deployment. Only a zero-offset GMT or UTC zone is
// accepted (asctime and the IMF-fixdate literal parse as UTC).
func parseDate(s string) (time.Time, bool) {
	t, err := http.ParseTime(s)
	if err != nil {
		return time.Time{}, false
	}
	name, off := t.Zone()
	return t, off == 0 && (name == "GMT" || name == "UTC")
}

func clampLifetime(d time.Duration) time.Duration { return min(max(d, 0), maxLifetime) }
