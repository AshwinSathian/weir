package weir_test

import (
	"errors"
	"net/http"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

func queryReq(path string, i int) *weir.Request {
	r := getReq(path)
	r.RawQuery = "q=" + strconv.Itoa(i)
	return r
}

// FR-CB-2, T-16: one path that answers 504, asked with distinct query
// strings so negative caching does not absorb it, does not open the breaker
// while another path succeeds. Before v1.1, 20 such requests answered every
// other path with 503.
func TestOneBadRouteLeavesBreakerClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(cacheable("ok"))
		o.Route("/report", testorigin.Behavior{Status: http.StatusGatewayTimeout})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/first"), o)
		for i := range 200 {
			resp, err := e.Serve(t.Context(), queryReq("/report", i), o)
			if err != nil {
				t.Fatalf("request %d: %v", i, err)
			}
			resp.Body.Close()
		}
		if s := e.Stats().BreakerState; s != weir.BreakerClosed {
			t.Fatalf("breaker = %v after 200 failures on one path, want closed", s)
		}
		if _, body := serve(t, e, getReq("/home"), o); body != "ok" {
			t.Fatalf("/home = %q, want ok", body)
		}
	})
}

// FR-CB-2: with Breaker.MinPartitions 1 the breaker behaves as before v1.1.
func TestBreakerMinPartitionsOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(cacheable("ok"))
		o.Route("/report", testorigin.Behavior{Status: http.StatusGatewayTimeout})
		cfg := cacheCfg
		cfg.Breaker.MinPartitions = 1
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/first"), o)
		for i := range 20 {
			if resp, err := e.Serve(t.Context(), queryReq("/report", i), o); err == nil {
				resp.Body.Close()
			}
		}
		if _, err := e.Serve(t.Context(), getReq("/home"), o); !errors.Is(err, weir.ErrCircuitOpen) {
			t.Fatalf("/home: %v, want ErrCircuitOpen", err)
		}
	})
}

// FR-CB-2, FR-CB-4: two failing paths still open the breaker, and once it is
// open the path whose probe failed leaves the next probe to another request.
func TestBreakerProbeNotCapturedByFailingRoute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(cacheable("ok"))
		bad := testorigin.Behavior{Status: http.StatusGatewayTimeout}
		o.Route("/report", bad)
		o.Route("/export", bad)
		cfg := cacheCfg
		cfg.Rand = func() float64 { return 0.5 } // open periods without jitter
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for i := range 10 {
			for _, p := range []string{"/report", "/export"} {
				if resp, err := e.Serve(t.Context(), queryReq(p, i), o); err == nil {
					resp.Body.Close()
				}
			}
		}
		if s := e.Stats().BreakerState; s != weir.BreakerOpen {
			t.Fatalf("breaker = %v after two paths failed, want open", s)
		}
		o.Route("/export", cacheable("ok")) // the origin recovers, /report stays broken
		time.Sleep(5*time.Second + time.Millisecond)
		// /report takes the first probe and fails it.
		if resp, err := e.Serve(t.Context(), queryReq("/report", 100), o); err == nil {
			resp.Body.Close()
		}
		time.Sleep(10*time.Second + time.Millisecond) // the doubled open period
		// /report arrives first again and is refused the probe.
		if _, err := e.Serve(t.Context(), queryReq("/report", 101), o); !errors.Is(err, weir.ErrCircuitOpen) {
			t.Fatalf("/report took the probe twice in a row: %v", err)
		}
		if _, body := serve(t, e, getReq("/home"), o); body != "ok" {
			t.Fatalf("/home = %q, want the probe to succeed", body)
		}
		if s := e.Stats().BreakerState; s != weir.BreakerClosed {
			t.Fatalf("breaker = %v after a successful probe, want closed", s)
		}
	})
}
