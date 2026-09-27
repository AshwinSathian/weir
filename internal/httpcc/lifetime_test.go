package httpcc

import (
	"math"
	"math/rand/v2"
	"net/http"
	"testing"
	"time"

	"github.com/AshwinSathian/weir/store"
)

var (
	respTime = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	testCfg  = Config{HeuristicFraction: 0.1, HeuristicMax: time.Hour}
)

func httpDate(t time.Time) string { return t.Format(http.TimeFormat) }

// hdr builds a header from key/value pairs; repeated keys add lines.
func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

func TestLifetimePrecedence(t *testing.T) {
	// FR-FRS-1, FR-FRS-2, FR-FRS-3; T-30
	slowDate := respTime.Add(-10 * time.Minute) // origin clock 10 minutes slow
	tests := []struct {
		name      string
		h         http.Header
		status    int
		want      time.Duration
		heuristic bool
	}{
		{"s-maxage beats max-age", hdr("Cache-Control", "max-age=60, s-maxage=30"), 200, 30 * time.Second, false},
		{"max-age beats Expires", hdr("Cache-Control", "max-age=60", "Expires", httpDate(respTime.Add(time.Hour))), 200, time.Minute, false},
		{"Expires minus Date ignores the local clock",
			hdr("Date", httpDate(slowDate), "Expires", httpDate(slowDate.Add(5*time.Minute))), 200, 5 * time.Minute, false},
		{"Expires without Date uses the response time", hdr("Expires", httpDate(respTime.Add(2*time.Minute))), 200, 2 * time.Minute, false},
		{"invalid Date uses the response time",
			hdr("Date", "yesterday", "Expires", httpDate(respTime.Add(2*time.Minute))), 200, 2 * time.Minute, false},
		{"Expires 0 is the past and blocks the heuristic",
			hdr("Expires", "0", "Last-Modified", httpDate(respTime.Add(-10*time.Hour))), 200, 0, false},
		{"Expires before Date is zero", hdr("Date", httpDate(respTime), "Expires", httpDate(respTime.Add(-time.Minute))), 200, 0, false},
		{"non-GMT Expires is invalid whatever the host zone",
			hdr("Date", "Sunday, 06-Nov-94 08:49:37 GMT", "Expires", "Sunday, 06-Nov-94 09:49:37 PST"), 200, 0, false},
		{"GMT-offset Expires is invalid",
			hdr("Date", "Sunday, 06-Nov-94 08:49:37 GMT", "Expires", "Sunday, 06-Nov-94 09:49:37 GMT-8"), 200, 0, false},
		{"asctime Expires has no zone and is GMT",
			hdr("Date", "Sun Nov  6 08:49:37 1994", "Expires", "Sun Nov  6 09:49:37 1994"), 200, time.Hour, false},
		{"conflicting Expires lines are stale",
			hdr("Expires", httpDate(respTime.Add(time.Hour)), "Expires", httpDate(respTime.Add(2*time.Hour))), 200, 0, false},
		{"far-future Expires clamps to the delta-seconds ceiling",
			hdr("Date", "Mon, 01 Jan 0001 00:00:00 GMT", "Expires", "Fri, 31 Dec 9999 23:59:59 GMT"), 200, maxDelta * time.Second, false},
		{"negative max-age zeroes the lifetime", hdr("Cache-Control", "max-age=-1", "Expires", httpDate(respTime.Add(time.Hour))), 200, 0, false},
		{"invalid s-maxage zeroes a valid max-age", hdr("Cache-Control", "s-maxage=x, max-age=60"), 200, 0, false},
		{"invalid stale-while-revalidate zeroes the lifetime", hdr("Cache-Control", "max-age=60, stale-while-revalidate=1.5"), 200, 0, false},
		{"conflicting max-age zeroes the lifetime", hdr("Cache-Control", "max-age=60, max-age=120"), 200, 0, false},
		{"max-age=0 is explicit, not heuristic",
			hdr("Cache-Control", "max-age=0", "Last-Modified", httpDate(respTime.Add(-10*time.Hour))), 200, 0, false},
		{"heuristic from Last-Modified", hdr("Date", httpDate(respTime), "Last-Modified", httpDate(respTime.Add(-5*time.Hour))), 200, 30 * time.Minute, true},
		{"no freshness information on a non-heuristic status", hdr("Last-Modified", httpDate(respTime.Add(-5*time.Hour))), 302, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lt, heur := Lifetime(ParseResponse(tt.h), tt.h, tt.status, respTime, testCfg)
			if lt != tt.want || heur != tt.heuristic {
				t.Errorf("Lifetime = (%v, %v), want (%v, %v)", lt, heur, tt.want, tt.heuristic)
			}
		})
	}
}

func TestParseDateIgnoresHostZone(t *testing.T) {
	// FR-FRS-1, FR-FRS-2; T-30: the same date bytes mean the same instant on
	// every host. London shows "GMT" dates in summer as BST (+1h).
	for _, name := range []string{"UTC", "Europe/London", "America/Los_Angeles", "Asia/Kolkata"} {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Skipf("no tzdata for %s: %v", name, err)
		}
		t.Run(name, func(t *testing.T) {
			saved := time.Local
			time.Local = loc
			defer func() { time.Local = saved }()
			want := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
			for _, s := range []string{"Sun, 05 Jul 2026 08:00:00 GMT", "Sunday, 05-Jul-26 08:00:00 GMT", "Sun Jul  5 08:00:00 2026"} {
				if got, ok := parseDate(s); !ok || !got.Equal(want) {
					t.Errorf("parseDate(%q) = (%v, %v), want (%v, true)", s, got, ok, want)
				}
			}
			for _, s := range []string{"Sunday, 05-Jul-26 08:00:00 PST", "Sunday, 05-Jul-26 08:00:00 BST",
				"Sunday, 05-Jul-26 08:00:00 GMT-8", "Sunday, 05-Jul-26 08:00:00 UTC", "Sun, 05 Jul 2026 08:00:00 +0000"} {
				if got, ok := parseDate(s); ok {
					t.Errorf("parseDate(%q) = %v, want invalid", s, got)
				}
			}
		})
	}
}

func TestHeuristicLimits(t *testing.T) {
	// FR-FRS-3, FR-STO-2 (302 and 307 never heuristic, D39)
	cfg := testCfg
	cfg.DefaultTTL = 2 * time.Minute
	lm := func(ago time.Duration) string { return httpDate(respTime.Add(-ago)) }
	date := httpDate(respTime)
	tests := []struct {
		name      string
		h         http.Header
		status    int
		want      time.Duration
		heuristic bool
	}{
		{"fraction of Date minus Last-Modified", hdr("Date", date, "Last-Modified", lm(5*time.Hour)), 200, 30 * time.Minute, true},
		{"capped at HeuristicMax", hdr("Date", date, "Last-Modified", lm(100*time.Hour)), 200, time.Hour, true},
		{"Last-Modified after Date is zero", hdr("Date", date, "Last-Modified", lm(-time.Hour)), 200, 0, true},
		{"Date absent measures from the response time", hdr("Last-Modified", lm(5*time.Hour)), 200, 30 * time.Minute, true},
		{"no Last-Modified uses DefaultTTL", hdr("Date", date), 200, 2 * time.Minute, true},
		{"invalid Last-Modified uses DefaultTTL", hdr("Date", date, "Last-Modified", "0"), 200, 2 * time.Minute, true},
		{"non-GMT Last-Modified uses DefaultTTL", hdr("Date", date, "Last-Modified", "Sunday, 06-Nov-94 08:49:37 PST"), 200, 2 * time.Minute, true},
		{"404 is heuristically cacheable", hdr("Date", date, "Last-Modified", lm(5*time.Hour)), 404, 30 * time.Minute, true},
		{"302 is never heuristic", hdr("Date", date, "Last-Modified", lm(5*time.Hour)), 302, 0, false},
		{"307 is never heuristic", hdr("Date", date, "Last-Modified", lm(5*time.Hour)), 307, 0, false},
		{"206 is never heuristic", hdr("Date", date, "Last-Modified", lm(5*time.Hour)), 206, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lt, heur := Lifetime(ParseResponse(tt.h), tt.h, tt.status, respTime, cfg)
			if lt != tt.want || heur != tt.heuristic {
				t.Errorf("Lifetime = (%v, %v), want (%v, %v)", lt, heur, tt.want, tt.heuristic)
			}
		})
	}
}

func TestCorrectedInitialAge(t *testing.T) {
	// FR-FRS-4, FR-FRS-8; T-30. RFC 9111 §4.2.3 corrected_age_value form.
	req := respTime.Add(-2 * time.Second) // response_delay = 2s
	tests := []struct {
		name string
		age  string
		req  time.Time
		want time.Duration
	}{
		{"age_value plus response_delay", "60", req, 62 * time.Second},
		{"absent Age is response_delay", "", req, 2 * time.Second},
		{"negative Age is ignored", "-5", req, 2 * time.Second},
		{"non-digit Age is ignored", "abc", req, 2 * time.Second},
		{"list-based Age uses the first member", "30, 40", req, 32 * time.Second},
		{"huge Age clamps to the delta-seconds ceiling", "99999999999999", req, maxDelta*time.Second + 2*time.Second},
		{"response before request clamps the delay to zero", "10", respTime.Add(time.Second), 10 * time.Second},
		{"zero request time saturates instead of overflowing", "5", time.Time{}, math.MaxInt64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CorrectedInitialAge(tt.age, tt.req, respTime); got != tt.want {
				t.Errorf("CorrectedInitialAge = %v, want %v", got, tt.want)
			}
		})
	}

	// Date is ignored: an origin clock 10 minutes slow must not age the entry.
	e := &store.Entry{ResponseTime: respTime, Date: respTime.Add(-10 * time.Minute), CorrectedInitialAge: 5 * time.Second}
	t.Run("current age adds resident time and ignores Date", func(t *testing.T) {
		if got := CurrentAge(e, respTime.Add(time.Minute)); got != 65*time.Second {
			t.Errorf("CurrentAge = %v, want 65s", got)
		}
	})
	t.Run("current age clamps negative resident time", func(t *testing.T) {
		if got := CurrentAge(e, respTime.Add(-time.Hour)); got != 5*time.Second {
			t.Errorf("CurrentAge = %v, want 5s", got)
		}
	})
	t.Run("current age saturates instead of overflowing", func(t *testing.T) {
		old := &store.Entry{CorrectedInitialAge: maxDelta * time.Second}
		if got := CurrentAge(old, respTime); got != math.MaxInt64 {
			t.Errorf("CurrentAge = %v, want saturated", got)
		}
	})
}

func TestJitterNeverLengthens(t *testing.T) {
	// FR-FRS-5, T6.1
	const minLT = 10 * time.Second
	lts := []time.Duration{0, 1, time.Second, minLT - 1, minLT, 300 * time.Second, 24 * time.Hour, maxDelta * time.Second}
	// Out-of-range u and frac (NaN, negative, above 1, infinite) cannot come
	// from a validated config, but must still not lengthen: Go's max returns
	// NaN when either operand is NaN.
	us := []float64{0, 1e-9, 0.25, 0.5, 0.75, 0.999999, math.Nextafter(1, 0), -1, 2, math.NaN(), math.Inf(1)}
	for _, lt := range lts {
		for _, frac := range []float64{0, 0.1, 0.5, -0.5, math.NaN(), math.Inf(1)} {
			for _, u := range us {
				got := Jitter(lt, frac, minLT, u)
				if got > lt || got < 0 {
					t.Fatalf("Jitter(%v, %v, %v, %v) = %v, outside [0, lt]", lt, frac, minLT, u, got)
				}
				if (lt < minLT || frac == 0) && got != lt {
					t.Fatalf("Jitter(%v, %v, %v, %v) = %v, want unchanged", lt, frac, minLT, u, got)
				}
			}
		}
	}
}

func TestJitterSpread(t *testing.T) {
	// FR-FRS-5, T6.1: 10 000 draws of 300 s in [270 s, 300 s], uniform by
	// chi-square over 10 buckets (df 9, p = 0.001 critical value 27.877).
	const n, buckets = 10000, 10
	r := rand.New(rand.NewPCG(1, 2))
	var counts [buckets]int
	for range n {
		got := Jitter(300*time.Second, 0.1, 10*time.Second, r.Float64())
		if got < 270*time.Second || got > 300*time.Second {
			t.Fatalf("Jitter = %v, outside [270s, 300s]", got)
		}
		counts[min(int((300*time.Second-got)/(3*time.Second)), buckets-1)]++
	}
	var chi2 float64
	for _, c := range counts {
		d := float64(c) - n/buckets
		chi2 += d * d / (n / buckets)
	}
	if chi2 >= 27.877 {
		t.Errorf("chi-square = %.2f over %v, rejects uniformity at p = 0.001", chi2, counts)
	}
}

func TestStaleWindows(t *testing.T) {
	// FR-STL-1, FR-STL-2, FR-STL-3; D6
	cfg := testCfg
	cfg.DefaultSWR, cfg.DefaultSIE = time.Minute, time.Hour
	tests := []struct {
		name     string
		cc       string
		swr, sie time.Duration
	}{
		{"defaults when the origin sends neither", "max-age=60", time.Minute, time.Hour},
		{"origin values win", "max-age=60, stale-while-revalidate=5, stale-if-error=7", 5 * time.Second, 7 * time.Second},
		{"origin SIE alone keeps the SWR default", "max-age=60, stale-if-error=7", time.Minute, 7 * time.Second},
		{"origin SWR alone keeps the SIE default", "max-age=60, stale-while-revalidate=5", 5 * time.Second, time.Hour},
		{"s-maxage blocks defaults", "s-maxage=60", 0, 0},
		{"s-maxage keeps explicit origin values", "s-maxage=60, stale-while-revalidate=5", 5 * time.Second, 0},
		{"must-revalidate forbids stale", "max-age=60, must-revalidate, stale-if-error=7", 0, 0},
		{"proxy-revalidate forbids stale", "max-age=60, proxy-revalidate, stale-while-revalidate=5", 0, 0},
		{"no-cache forbids stale", "no-cache, stale-if-error=7", 0, 0},
		{"qualified no-cache forbids stale", `no-cache="Set-Cookie", stale-if-error=7`, 0, 0},
		{"invalid origin value is zero, not the default", "max-age=60, stale-while-revalidate=x", 0, time.Hour},
	}
	t.Run("operator defaults clamp to the delta-seconds ceiling", func(t *testing.T) {
		big := Config{DefaultSWR: math.MaxInt64, DefaultSIE: math.MaxInt64}
		if swr, sie := StaleWindows(ResponseDirectives{}, big); swr != maxLifetime || sie != maxLifetime {
			t.Errorf("StaleWindows = (%v, %v), want (%v, %v)", swr, sie, maxLifetime, maxLifetime)
		}
		h := hdr("Date", httpDate(respTime))
		if lt, _ := Lifetime(ParseResponse(h), h, 200, respTime, Config{DefaultTTL: math.MaxInt64}); lt != maxLifetime {
			t.Errorf("Lifetime with huge DefaultTTL = %v, want %v", lt, maxLifetime)
		}
	})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			swr, sie := StaleWindows(ParseResponse(hdr("Cache-Control", tt.cc)), cfg)
			if swr != tt.swr || sie != tt.sie {
				t.Errorf("StaleWindows = (%v, %v), want (%v, %v)", swr, sie, tt.swr, tt.sie)
			}
		})
	}
}

func FuzzHTTPDate(f *testing.F) {
	// FR-FRS-2, FR-FRS-4, NFR-2: origin date and Age bytes never panic or
	// give a negative lifetime or age.
	f.Add("0", httpDate(respTime), "0", "60")
	f.Add("Sunday, 06-Nov-94 08:49:37 GMT", "Sun Nov  6 08:49:37 1994", "Sun, 06 Nov 1994 08:49:37 PST", `"5", 7`)
	f.Fuzz(func(t *testing.T, expires, date, lastMod, age string) {
		if a := CorrectedInitialAge(age, respTime.Add(-time.Second), respTime); a < 0 {
			t.Fatalf("negative age %v for Age=%q", a, age)
		}
		h := hdr("Date", date, "Last-Modified", lastMod)
		for _, withExpires := range []bool{false, true} {
			if withExpires {
				h.Set("Expires", expires)
			}
			lt, _ := Lifetime(ParseResponse(h), h, 200, respTime, testCfg)
			if lt < 0 {
				t.Fatalf("negative lifetime %v for Expires=%q Date=%q Last-Modified=%q", lt, expires, date, lastMod)
			}
		}
	})
}
