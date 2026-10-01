package weir_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

// FR-MODE-1: SetMode requires a ttl in (0, 24 h] and a known mode.
func TestSetModeRejectsInvalid(t *testing.T) {
	e := newEngine(t, cacheCfg)
	defer closeEngine(t, e)
	for _, tc := range []struct {
		name string
		m    weir.Mode
		ttl  time.Duration
		ok   bool
	}{
		{"zero ttl rejected", weir.ModeBypass, 0, false},
		{"negative ttl rejected", weir.ModeStaleOnError, -time.Second, false},
		{"ttl over 24 h rejected", weir.ModeBypass, 24*time.Hour + 1, false},
		{"unknown mode rejected", weir.Mode(9), time.Minute, false},
		{"24 h accepted", weir.ModeStaleOnError, 24 * time.Hour, true},
		{"normal accepted", weir.ModeNormal, time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := e.SetMode(tc.m, tc.ttl)
			if tc.ok != (err == nil) || !tc.ok && !errors.Is(err, weir.ErrInvalidConfig) {
				t.Fatalf("SetMode(%d, %v) = %v", tc.m, tc.ttl, err)
			}
		})
	}
}

// FR-MODE-1, T-42: a mode reverts to normal when its ttl runs out, and both
// the change and the expiry emit EvMode.
func TestModeExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(cacheable("a"))
		obs := &eventCounter{}
		cfg := cacheCfg
		cfg.Observer = obs
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		if err := e.SetMode(weir.ModeBypass, time.Minute); err != nil {
			t.Fatal(err)
		}
		serve(t, e, getReq("/a"), o)
		serve(t, e, getReq("/a"), o)
		if n := o.Calls("/a"); n != 2 {
			t.Fatalf("calls in bypass = %d, want 2", n)
		}
		time.Sleep(time.Minute + time.Second)
		serve(t, e, getReq("/a"), o)
		resp, _ := serve(t, e, getReq("/a"), o)
		if n := o.Calls("/a"); n != 3 || !resp.Cache.Hit {
			t.Fatalf("after expiry: calls = %d, hit = %v; want 3 and a hit", n, resp.Cache.Hit)
		}
		if b, n := obs.count("mode/bypass"), obs.count("mode/normal"); b != 1 || n != 1 {
			t.Fatalf("EvMode bypass = %d, normal = %d; want 1 each", b, n)
		}
	})
}

// FR-MODE-2, T-42: stale-on-error serves a stored entry without an SIE
// window up to 24 h stale, never a hard-purged, invalidated, must-revalidate,
// proxy-revalidate, no-cache or s-maxage entry (RFC 9111 §4.2.4, §5.2.2.10).
func TestModeStaleOnErrorLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cc      string
		mode    bool
		epoch   store.EpochMode
		stale   time.Duration
		wantErr error // nil: the stale entry is served
	}{
		{"no sie window served", "max-age=10", true, 0, time.Hour, nil},
		{"24 h stale served", "max-age=10", true, 0, 24 * time.Hour, nil},
		{"past 24 h not served", "max-age=10", true, 0, 24*time.Hour + time.Second, weir.ErrOrigin},
		{"normal mode not served", "max-age=10", false, 0, time.Hour, weir.ErrOrigin},
		{"must-revalidate not served", "max-age=10, must-revalidate", true, 0, time.Hour, weir.ErrMustRevalidate},
		{"proxy-revalidate not served", "max-age=10, proxy-revalidate", true, 0, time.Hour, weir.ErrMustRevalidate},
		{"no-cache not served", "max-age=10, no-cache", true, 0, time.Hour, weir.ErrOrigin},
		{"s-maxage not served", "s-maxage=10", true, 0, time.Hour, weir.ErrOrigin},
		{"hard-purged not served", "max-age=10", true, store.EpochHard, time.Hour, weir.ErrOrigin},
		{"invalidated not served", "max-age=10", true, store.EpochInvalid, time.Hour, weir.ErrOrigin},
		{"soft-purged served", "max-age=10", true, store.EpochSoft, time.Hour, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, err := memory.New(memory.Config{MaxRetention: 48 * time.Hour})
				if err != nil {
					t.Fatal(err)
				}
				defer m.Close()
				o := testorigin.NewChecked(t, 64, 16)
				o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {tc.cc}, "Etag": {`"v1"`}}, Body: []byte("a")})
				cfg := cacheCfg
				cfg.Store = m
				cfg.Freshness.Keep = 48 * time.Hour // with MaxRetention, keeps the entry stored past 24 h
				e := newEngine(t, cfg)
				defer closeEngine(t, e)

				serve(t, e, getReq("/a"), o)
				stored := time.Now()
				if tc.epoch != 0 {
					time.Sleep(time.Second)
					if err := m.SetEpoch(t.Context(), store.TagGlobal(), store.Epoch{At: time.Now(), Mode: tc.epoch}); err != nil {
						t.Fatal(err)
					}
				}
				time.Sleep(time.Until(stored.Add(10*time.Second + tc.stale)))
				if tc.mode {
					if err := e.SetMode(weir.ModeStaleOnError, 24*time.Hour); err != nil {
						t.Fatal(err)
					}
				}
				o.SetDown(true)
				resp, body, err := serveResult(t, e, getReq("/a"), o)
				if tc.wantErr != nil {
					if !errors.Is(err, tc.wantErr) {
						t.Fatalf("Serve = %v, want %v", err, tc.wantErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("Serve = %v, want the stale entry", err)
				}
				if body != "a" || resp.Cache.Stale != weir.StaleIfError {
					t.Fatalf("Serve = %q, stale=%v; want the stale entry", body, resp.Cache.Stale)
				}
			})
		})
	}
}

// FR-MODE-3, FR-FWD-3: bypass forwards every request as received without
// storing it, and leaves existing entries untouched.
func TestModeBypass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(cacheable("old"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		if err := e.SetMode(weir.ModeBypass, time.Hour); err != nil {
			t.Fatal(err)
		}
		o.Default(cacheable("new"))
		resp, body := serve(t, e, getReq("/a"), o)
		if body != "new" || resp.Cache.Hit || resp.Cache.Fwd != weir.FwdBypass {
			t.Fatalf("bypass: %q hit=%v fwd=%v; want origin's new body, fwd=bypass", body, resp.Cache.Hit, resp.Cache.Fwd)
		}
		serve(t, e, getReq("/b"), o)
		head := getReq("/c")
		head.Method, head.RawQuery = http.MethodHead, "z=1&a=2"
		head.Header["If-None-Match"] = []string{`"v1"`}
		head.Header["X-Debug"] = []string{"1"}
		head.Header["Connection"] = []string{"X-Hop"}
		head.Header["X-Hop"] = []string{"1"}
		serve(t, e, head, o)
		reqs := o.Requests()
		got := reqs[len(reqs)-1]
		if got.Method != http.MethodHead || got.RawQuery != "z=1&a=2" || got.Header.Get("If-None-Match") != `"v1"` ||
			got.Header.Get("X-Debug") != "1" || got.Header["X-Hop"] != nil {
			t.Fatalf("bypass forwarded %s ?%s %v; want the client request minus hop-by-hop fields", got.Method, got.RawQuery, got.Header)
		}
		if err := e.SetMode(weir.ModeNormal, time.Second); err != nil {
			t.Fatal(err)
		}
		if resp, body := serve(t, e, getReq("/a"), o); !resp.Cache.Hit || body != "old" {
			t.Fatalf("after bypass /a: %q hit=%v; want the untouched old entry", body, resp.Cache.Hit)
		}
		serve(t, e, getReq("/b"), o)
		if n := o.Calls("/b"); n != 2 {
			t.Fatalf("calls /b = %d, want 2 (bypass stored nothing)", n)
		}
	})
}

// FR-MODE-2, RFC 9111 §5.2.1.4: a fresh entry the client asked to validate
// is not served when validation fails; stale-on-error widens only stale
// entries.
func TestModeStaleOnErrorFreshForcedValidation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(cacheable("a"))
		cfg := cacheCfg
		cfg.Client.HonorRevalidation = true
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		if err := e.SetMode(weir.ModeStaleOnError, time.Hour); err != nil {
			t.Fatal(err)
		}
		o.SetDown(true)
		req := getReq("/a")
		req.Header["Cache-Control"] = []string{"no-cache"}
		if _, _, err := serveResult(t, e, req, o); !errors.Is(err, weir.ErrOrigin) {
			t.Fatalf("Serve = %v, want %v", err, weir.ErrOrigin)
		}
	})
}

// FR-MODE-3, FR-CB-1: bypass traffic still goes through the breaker, which
// opens after 20 gateway failures and keeps the rest from the origin.
func TestModeBypassThroughBreaker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Status: http.StatusServiceUnavailable})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		if err := e.SetMode(weir.ModeBypass, time.Hour); err != nil {
			t.Fatal(err)
		}
		for i := range 30 {
			if _, _, err := serveResult(t, e, getReq(fmt.Sprintf("/f%d", i)), o); err != nil && !errors.Is(err, weir.ErrCircuitOpen) {
				t.Fatalf("request %d: %v", i, err)
			}
			time.Sleep(2 * time.Second / 30)
		}
		if n := o.TotalCalls(); n != 20 {
			t.Fatalf("origin calls = %d, want 20 (the breaker opens at the 20th failure)", n)
		}
	})
}
