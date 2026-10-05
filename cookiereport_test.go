package weir_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// reportLines returns the stripped-cookie report lines in a text log.
func reportLines(log string) []string {
	var out []string
	for l := range strings.Lines(log) {
		if strings.Contains(l, "cookies stripped") {
			out = append(out, l)
		}
	}
	return out
}

// FR-OBS-5, D31: after the report window one log line lists the most
// frequent cookie names strict forwarding stripped, never a value, and the
// report never repeats.
func TestStrippedCookieReport(t *testing.T) {
	cookieReq := func(method, cookie string) *weir.Request {
		r := getReq("/a")
		r.Method = method
		r.Header["Cookie"] = []string{cookie}
		return r
	}
	run := func(t *testing.T, mut func(*weir.Config), f func(t *testing.T, e *weir.Engine, o *testorigin.Origin, log *bytes.Buffer)) {
		synctest.Test(t, func(t *testing.T) {
			var buf bytes.Buffer
			o := testorigin.NewChecked(t, 64, 16)
			o.Default(cacheable("v"))
			cfg := cacheCfg
			cfg.Logger = slog.New(slog.NewTextHandler(&buf, nil))
			cfg.Key.Cookies = []string{"lang"}
			cfg.Bypass.ReportStrippedCookies = time.Minute
			if mut != nil {
				mut(&cfg)
			}
			e := newEngine(t, cfg)
			defer closeEngine(t, e)
			f(t, e, o, &buf)
		})
	}

	t.Run("names by frequency, no values, logged once", func(t *testing.T) {
		run(t, nil, func(t *testing.T, e *weir.Engine, o *testorigin.Origin, log *bytes.Buffer) {
			for range 3 {
				serve(t, e, cookieReq(http.MethodGet, "sid=SECRETVALUE; lang=en; cart=SECRETVALUE"), o)
			}
			serve(t, e, cookieReq(http.MethodGet, "sid=SECRETVALUE; theme=SECRETVALUE"), o)
			// Never logged: a name outside the
			// token grammar, a pair without "=", a name over 64 bytes.
			serve(t, e, cookieReq(http.MethodGet, `ba d=SECRETVALUE; "q"=1; novalue; `+strings.Repeat("n", 65)+"=1"), o)
			// A request forwarded as received strips nothing.
			serve(t, e, cookieReq(http.MethodPost, "posted=SECRETVALUE"), o)
			if got := reportLines(log.String()); len(got) != 0 {
				t.Fatalf("report before the window ended: %q", got)
			}
			time.Sleep(time.Minute)
			serve(t, e, cookieReq(http.MethodGet, "late=1"), o)
			serve(t, e, cookieReq(http.MethodGet, "sid=1"), o)
			time.Sleep(time.Hour)
			serve(t, e, cookieReq(http.MethodGet, "sid=1"), o)

			got := reportLines(log.String())
			if len(got) != 1 {
				t.Fatalf("%d report lines, want 1: %q", len(got), got)
			}
			if !strings.Contains(got[0], `names="[sid cart theme]"`) {
				t.Errorf("report %q, want names sid, cart, theme in that order", got[0])
			}
			if strings.Contains(log.String(), "SECRETVALUE") {
				t.Errorf("a cookie value reached the log: %q", log.String())
			}
		})
	})

	t.Run("summary holds 32 names and keeps the heavy one", func(t *testing.T) { // P5, NFR-3
		run(t, nil, func(t *testing.T, e *weir.Engine, o *testorigin.Origin, log *bytes.Buffer) {
			for i := range 500 {
				serve(t, e, cookieReq(http.MethodGet, fmt.Sprintf("heavy=1; junk%d=1", i)), o)
			}
			time.Sleep(time.Minute)
			serve(t, e, cookieReq(http.MethodGet, "x=1"), o)
			got := reportLines(log.String())
			if len(got) != 1 {
				t.Fatalf("%d report lines, want 1", len(got))
			}
			if n := strings.Count(got[0], "junk"); n != 31 {
				t.Errorf("%d junk names logged, want 31 (32 counters, one for heavy)", n)
			}
			if !strings.Contains(got[0], `names="[heavy `) {
				t.Errorf("heavy is not first: %q", got[0])
			}
		})
	})

	t.Run("one request counts at most 32 pairs", func(t *testing.T) { // P5: bounded work under the lock
		run(t, nil, func(t *testing.T, e *weir.Engine, o *testorigin.Origin, log *bytes.Buffer) {
			var b strings.Builder
			for i := range 40 {
				fmt.Fprintf(&b, "c%02d=1; ", i)
			}
			serve(t, e, cookieReq(http.MethodGet, b.String()), o)
			time.Sleep(time.Minute)
			serve(t, e, cookieReq(http.MethodGet, "x=1"), o)
			got := reportLines(log.String())
			if len(got) != 1 || !strings.Contains(got[0], "c31") || strings.Contains(got[0], "c32") {
				t.Errorf("report %q, want c00 to c31 only", got)
			}
		})
	})

	t.Run("cookie header key in any case, name of 64 bytes", func(t *testing.T) {
		run(t, nil, func(t *testing.T, e *weir.Engine, o *testorigin.Origin, log *bytes.Buffer) {
			r := getReq("/a")
			r.Header = http.Header{"cookie": {"sid=1; " + strings.Repeat("n", 64) + "=1"}} // a library caller, not net/http
			serve(t, e, r, o)
			time.Sleep(time.Minute)
			serve(t, e, cookieReq(http.MethodGet, "x=1"), o)
			got := reportLines(log.String())
			if len(got) != 1 || !strings.Contains(got[0], "sid") || !strings.Contains(got[0], strings.Repeat("n", 64)) {
				t.Errorf("report %q, want sid and the 64-byte name", got)
			}
		})
	})

	t.Run("concurrent requests across the deadline log once", func(t *testing.T) {
		run(t, nil, func(t *testing.T, e *weir.Engine, o *testorigin.Origin, log *bytes.Buffer) {
			serve(t, e, cookieReq(http.MethodGet, "sid=1"), o) // fill the cache, count sid
			burst := func() {
				var wg sync.WaitGroup
				for range 16 {
					wg.Go(func() {
						for range 50 {
							serve(t, e, cookieReq(http.MethodGet, "sid=1; cart=2"), o)
						}
					})
				}
				wg.Wait()
			}
			burst()
			time.Sleep(time.Minute)
			burst()
			got := reportLines(log.String())
			if len(got) != 1 || !strings.Contains(got[0], "sid") {
				t.Errorf("report %q, want one line naming sid", got)
			}
		})
	})

	t.Run("nothing stripped logs nothing", func(t *testing.T) {
		run(t, nil, func(t *testing.T, e *weir.Engine, o *testorigin.Origin, log *bytes.Buffer) {
			serve(t, e, cookieReq(http.MethodGet, "lang=en"), o)
			time.Sleep(time.Minute)
			serve(t, e, cookieReq(http.MethodGet, "sid=1"), o)
			if got := reportLines(log.String()); len(got) != 0 {
				t.Errorf("report with no names: %q", got)
			}
		})
	})

	t.Run("default window is 5 minutes", func(t *testing.T) {
		run(t, func(c *weir.Config) { c.Bypass.ReportStrippedCookies = 0 }, func(t *testing.T, e *weir.Engine, o *testorigin.Origin, log *bytes.Buffer) {
			serve(t, e, cookieReq(http.MethodGet, "sid=1"), o)
			time.Sleep(5*time.Minute - time.Second)
			serve(t, e, cookieReq(http.MethodGet, "sid=1"), o)
			if got := reportLines(log.String()); len(got) != 0 {
				t.Fatalf("report before 5 minutes: %q", got)
			}
			time.Sleep(time.Second)
			serve(t, e, cookieReq(http.MethodGet, "sid=1"), o)
			if got := reportLines(log.String()); len(got) != 1 {
				t.Errorf("%d report lines at 5 minutes, want 1", len(got))
			}
		})
	})

	for name, mut := range map[string]func(*weir.Config){
		"negative window disables":     func(c *weir.Config) { c.Bypass.ReportStrippedCookies = -1 },
		"ForwardAll strips no cookies": func(c *weir.Config) { c.Forward.Mode = weir.ForwardAll },
	} {
		t.Run(name, func(t *testing.T) {
			run(t, mut, func(t *testing.T, e *weir.Engine, o *testorigin.Origin, log *bytes.Buffer) {
				serve(t, e, cookieReq(http.MethodGet, "sid=1"), o)
				time.Sleep(time.Hour)
				serve(t, e, cookieReq(http.MethodGet, "sid=1"), o)
				if got := reportLines(log.String()); len(got) != 0 {
					t.Errorf("report logged: %q", got)
				}
			})
		})
	}
}
