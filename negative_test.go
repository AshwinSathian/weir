package weir_test

import (
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

// FR-NEG-1, FR-NEG-3, T6.10: a burst against a failing key costs one origin
// call per Negative.TTL; the rest get a synthesized 503.
func TestNegativeCacheBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Status: 503, Header: http.Header{"Retry-After": {"7"}, "X-Origin": {"secret"}}, Body: []byte("down")})
		obs := &eventCounter{}
		cfg := cacheCfg
		cfg.Observer = obs
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for i := range 200 {
			resp, body := serve(t, e, getReq("/a"), o)
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("request %d: status %d, want 503", i, resp.StatusCode)
			}
			if i == 0 {
				continue
			}
			// FR-NEG-2: status and Retry-After only, never the origin's body or headers.
			if cs := resp.Header.Get("Cache-Status"); cs != "Weir; hit; ttl=1; detail=negative" && cs != "Weir; hit; ttl=2; detail=negative" {
				t.Fatalf("request %d: Cache-Status = %q", i, cs)
			}
			if body != "" || resp.Header.Get("X-Origin") != "" || resp.Header.Get("Retry-After") != "7" {
				t.Fatalf("request %d: body %q header %v", i, body, resp.Header)
			}
			time.Sleep(500 * time.Microsecond)
		}
		if n := o.Calls("/a"); n != 1 {
			t.Fatalf("origin calls = %d, want 1", n)
		}
		if n := obs.count("negative-served/"); n != 199 {
			t.Fatalf("EvNegativeServed = %d, want 199", n)
		}
		time.Sleep(2 * time.Second)
		serve(t, e, getReq("/a"), o)
		if n := o.Calls("/a"); n != 2 {
			t.Fatalf("after Negative.TTL origin calls = %d, want 2", n)
		}
	})
}

// FR-NEG-1: transport errors and timeouts are origin-health failures too;
// the negative entry carries the status the error maps to.
func TestNegativeFromTransportFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		b      testorigin.Behavior
		status int
	}{
		{"transport error is 502", testorigin.Behavior{Err: errors.New("refused")}, http.StatusBadGateway},
		{"timeout is 504", testorigin.Behavior{Delay: time.Hour}, http.StatusGatewayTimeout},
		{"502 response", testorigin.Behavior{Status: 502}, http.StatusBadGateway},
		{"504 response", testorigin.Behavior{Status: 504}, http.StatusGatewayTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.NewChecked(t, 64, 16)
				o.Default(tc.b)
				e := newEngine(t, cacheCfg)
				defer closeEngine(t, e)

				_, _, _ = serveResult(t, e, getReq("/a"), o)
				resp, _, err := serveResult(t, e, getReq("/a"), o)
				if err != nil || resp.StatusCode != tc.status || resp.Cache.Detail != "negative" {
					t.Fatalf("second request: %v %v, want negative %d", resp, err, tc.status)
				}
				if o.Calls("/a") != 1 {
					t.Fatalf("origin calls = %d, want 1", o.Calls("/a"))
				}
			})
		})
	}
}

// FR-NEG-4, T-31: 500, 4xx and failures of excluded requests never create a
// negative entry.
func TestNegativeNotFor500(t *testing.T) {
	allow := cacheCfg
	allow.Forward.Allow = []string{"X-Tenant"}
	all := cacheCfg
	all.Forward.Mode = weir.ForwardAll
	off := cacheCfg
	off.Negative.Disable = true
	for _, tc := range []struct {
		name   string
		cfg    weir.Config
		status int
		req    *weir.Request
	}{
		{"500", cacheCfg, 500, getReq("/a")},
		{"404", cacheCfg, 404, getReq("/a")},
		{"429", cacheCfg, 429, getReq("/a")},
		{"Authorization", cacheCfg, 503, withHeader(getReq("/a"), "Authorization", "Bearer junk")},
		{"request no-store", cacheCfg, 503, withHeader(getReq("/a"), "Cache-Control", "no-store")},
		{"Range", cacheCfg, 503, withHeader(getReq("/a"), "Range", "bytes=0-1")},
		{"Forward.Allow field", allow, 503, withHeader(getReq("/a"), "X-Tenant", "evil")},
		{"ForwardAll", all, 503, getReq("/a")},
		{"Negative.Disable", off, 503, getReq("/a")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.NewChecked(t, 64, 16)
				o.Default(testorigin.Behavior{Status: tc.status})
				e := newEngine(t, tc.cfg)
				defer closeEngine(t, e)

				_, _, _ = serveResult(t, e, tc.req, o)
				resp, _, _ := serveResult(t, e, getReq("/a"), o)
				if o.Calls("/a") != 2 || resp != nil && resp.Cache.Detail == "negative" {
					t.Fatalf("origin calls = %d, want 2 (no negative entry)", o.Calls("/a"))
				}
			})
		})
	}
}

// FR-NEG-3, FR-STL-2: a stale entry stale-if-error permits is served on an
// origin failure, and the failure leaves no negative entry behind.
func TestNegativePrefersStale(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(ccBehavior(200, "max-age=10, stale-if-error=60", "a"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(20 * time.Second)
		o.Default(testorigin.Behavior{Status: 503})
		for range 3 {
			resp, body := serve(t, e, getReq("/a"), o)
			if body != "a" || resp.Cache.Stale != weir.StaleIfError {
				t.Fatalf("%d %q %+v, want stale-if-error", resp.StatusCode, body, resp.Cache)
			}
		}
		if o.Calls("/a") != 4 {
			t.Fatalf("origin calls = %d, want 4 (each failure tried, none cached)", o.Calls("/a"))
		}
	})
}

// T-17: a failure one client induces sits on that client's own key; other
// keys of the same path still reach the origin.
func TestNegativeScopedToKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			if r.RawQuery == "boom=1" {
				return &weir.Response{StatusCode: 503, Header: http.Header{}, Body: http.NoBody}, nil
			}
			return &weir.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: http.NoBody}, nil
		}})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		evil := getReq("/a")
		evil.RawQuery = "boom=1"
		serve(t, e, evil, o)
		evil = getReq("/a")
		evil.RawQuery = "boom=1"
		if resp, _ := serve(t, e, evil, o); resp.Cache.Detail != "negative" {
			t.Fatalf("attacker key: %+v, want negative", resp.Cache)
		}
		if resp, _ := serve(t, e, getReq("/a"), o); resp.StatusCode != http.StatusOK || resp.Cache.Detail == "negative" {
			t.Fatalf("other key: %d %+v, want origin 200", resp.StatusCode, resp.Cache)
		}
	})
}

// FR-NEG-1: a failure never overwrites a stored response, even one that is
// stale and not servable, so it can still be revalidated later.
func TestNegativeNeverReplacesResponse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		b := ccBehavior(200, "max-age=10", "a")
		b.Header.Set("ETag", `"1"`)
		o.Default(b)
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(20 * time.Second)
		o.Default(testorigin.Behavior{Status: 503})
		if resp, _ := serve(t, e, getReq("/a"), o); resp.StatusCode != http.StatusServiceUnavailable || resp.Cache.Detail == "negative" {
			t.Fatalf("validation failure: %d %+v, want origin 503", resp.StatusCode, resp.Cache)
		}
		o.Default(testorigin.Behavior{Status: 304, Header: http.Header{"ETag": {`"1"`}, "Cache-Control": {"max-age=10"}}})
		resp, body := serve(t, e, getReq("/a"), o)
		if body != "a" || resp.Cache.FwdStatus != 304 {
			t.Fatalf("revalidation: %d %q %+v, want 304-freshened entry", resp.StatusCode, body, resp.Cache)
		}
	})
}

// FR-NEG-1, T6.10, 04 §6.7: an expired response a store still returns lazily
// does not block the negative entry; lookup already treats it as a miss.
func TestNegativeReplacesExpiredRecord(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, err := memory.New(memory.Config{})
		if err != nil {
			t.Fatal(err)
		}
		defer m.Close()
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(ccBehavior(200, "max-age=1", "a"))
		e := newEngine(t, weir.Config{Store: &lazyStore{Store: m, m: map[store.Key]*store.Entry{}}, Freshness: weir.FreshnessConfig{NoJitter: true}})
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(time.Hour)
		o.Default(testorigin.Behavior{Status: 503})
		serve(t, e, getReq("/a"), o)
		if resp, _ := serve(t, e, getReq("/a"), o); resp.Cache.Detail != "negative" {
			t.Fatalf("second failure: %d %+v, want negative hit", resp.StatusCode, resp.Cache)
		}
		if n := o.Calls("/a"); n != 2 {
			t.Fatalf("origin calls = %d, want 2", n)
		}
	})
}
