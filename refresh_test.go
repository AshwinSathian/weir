package weir_test

import (
	"context"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// lockedRand is a seeded Config.Rand that is safe for concurrent use, as
// Config.Rand must be.
func lockedRand(seed uint64) func() float64 {
	var mu sync.Mutex
	r := rand.New(rand.NewPCG(seed, seed))
	return func() float64 {
		mu.Lock()
		defer mu.Unlock()
		return r.Float64()
	}
}

// FR-FRS-5, FR-FRS-6, T6.1: 1 000 keys stored in the same instant do not
// all expire in the same second.
func TestBatchWriteExpirySpread(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=300"}}, Body: []byte("x")})
		e := newEngine(t, weir.Config{Rand: lockedRand(1)})
		defer closeEngine(t, e)

		const keys = 1000
		paths := make([]string, keys)
		for i := range paths {
			paths[i] = "/k" + strconv.Itoa(i)
			serve(t, e, getReq(paths[i]), o)
		}
		worst := 0
		for range 300 {
			time.Sleep(time.Second)
			before := o.TotalCalls()
			for _, p := range paths {
				serve(t, e, getReq(p), o)
			}
			synctest.Wait() // count early refreshes in the step that started them
			worst = max(worst, o.TotalCalls()-before)
		}
		if worst > 150 {
			t.Fatalf("max origin calls in one 1s step = %d, want <= 150", worst)
		}
	})
}

// FR-FRS-6, FR-COA-1, T6.1: concurrent fresh hits near expiry start one
// background refresh between them, and every one is served from the entry.
func TestEarlyRefreshSingleFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		b := cacheable("v")
		b.Delay = time.Second // Δ = 1s; with u = 1e-9 the trigger horizon is about 20.7s
		o.Default(b)
		cfg := cacheCfg
		cfg.Rand = func() float64 { return 1 - 1e-9 }
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/hot"), o)
		time.Sleep(49 * time.Second) // about 10s left of max-age=60
		chs := make([]<-chan served, 500)
		for i := range chs {
			chs[i] = serveAsync(t.Context(), e, getReq("/hot"), o)
		}
		for _, ch := range chs {
			if s := <-ch; s.err != nil || !s.resp.Cache.Hit || s.body != "v" {
				t.Fatalf("got %v, %q; want a hit", s.err, s.body)
			}
		}
		synctest.Wait()
		if n := o.TotalCalls(); n != 2 {
			t.Fatalf("origin calls = %d, want 2 (fill and one refresh)", n)
		}
		// The refresh stored a new entry: past the old expiry it still hits.
		time.Sleep(30 * time.Second)
		if resp, _ := serve(t, e, getReq("/hot"), o); !resp.Cache.Hit || o.TotalCalls() != 2 {
			t.Fatalf("hit=%v calls=%d after the old expiry; want the refreshed entry", resp.Cache.Hit, o.TotalCalls())
		}
	})
}

// FR-FRS-6, FR-COA-8, T-8, T-31: early refresh never starts when disabled, for
// lifetimes below JitterMinLifetime, or for a request whose credentials or
// no-store must not decide a shared entry.
func TestEarlyRefreshGates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cc    string
		off   bool
		req   func() *weir.Request
		calls int
	}{
		{"control refreshes", "max-age=60", false, func() *weir.Request { return getReq("/k") }, 2},
		{"NoEarlyRefresh", "max-age=60", true, func() *weir.Request { return getReq("/k") }, 1},
		{"lifetime below JitterMinLifetime", "max-age=9", false, func() *weir.Request { return getReq("/k") }, 1},
		{"Authorization request", "max-age=60, public", false, func() *weir.Request {
			return withHeader(getReq("/k"), "Authorization", "Bearer x")
		}, 1},
		{"no-store request", "max-age=60", false, func() *weir.Request {
			return withHeader(getReq("/k"), "Cache-Control", "no-store")
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.New()
				o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {tc.cc}}, Body: []byte("v"), Delay: 2 * time.Second})
				cfg := cacheCfg
				cfg.Freshness.NoEarlyRefresh = tc.off
				// u = 2^-53 and Δ = 2s put the trigger horizon at about 73s,
				// so every entry here refreshes unless a gate stops it.
				cfg.Rand = func() float64 { return math.Nextafter(1, 0) }
				e := newEngine(t, cfg)
				defer closeEngine(t, e)

				serve(t, e, getReq("/k"), o)
				time.Sleep(time.Second)
				if resp, _ := serve(t, e, tc.req(), o); !resp.Cache.Hit {
					t.Fatalf("not a hit: %+v", resp.Cache)
				}
				synctest.Wait()
				if n := o.TotalCalls(); n != tc.calls {
					t.Fatalf("origin calls = %d, want %d", n, tc.calls)
				}
			})
		})
	}
}

// FR-FRS-6, NFR-1: a hit far from expiry does not draw from Config.Rand,
// which keeps the draw and math.Log off the hit path.
func TestEarlyRefreshNoDrawFarFromExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(cacheable("v"))
		var draws atomic.Int64
		cfg := cacheCfg
		cfg.Rand = func() float64 { draws.Add(1); return 0.5 }
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o) // Δ = 0, clamped to 1 ms; 60 s left
		draws.Store(0)               // the fill drew once for jitter
		for range 10 {
			serve(t, e, getReq("/a"), o)
		}
		if n := draws.Load(); n != 0 {
			t.Fatalf("Rand drawn %d times on hits with 60s left, want 0", n)
		}
	})
}

// FR-LCY-2, FR-FRS-6: Close cancels a background refresh stuck on the
// origin once its grace period runs out, and the hit that started it was
// already served.
func TestCloseCancelsEarlyRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		fill := cacheable("v")
		fill.Delay = 2 * time.Second // Δ = 2s: the trigger horizon is about 73s
		o.Default(fill)
		cfg := cacheCfg
		cfg.Rand = func() float64 { return math.Nextafter(1, 0) }
		e := newEngine(t, cfg)

		serve(t, e, getReq("/a"), o)
		stuck := cacheable("v2")
		stuck.Gate = make(chan struct{}) // never opened; released by ctx only
		o.Default(stuck)
		time.Sleep(50 * time.Second)
		if resp, _ := serve(t, e, getReq("/a"), o); !resp.Cache.Hit {
			t.Fatal("near-expiry request not served as a hit")
		}
		synctest.Wait()
		if n := o.TotalCalls(); n != 2 {
			t.Fatalf("origin calls = %d, want the refresh in flight", n)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := e.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close = %v, want the grace period to run out", err)
		}
	})
}

// trackedBody reports when it is closed.
type trackedBody struct {
	io.Reader
	closed chan struct{}
}

func (b *trackedBody) Close() error { close(b.closed); return nil }

// FR-FRS-6, FR-STR-1, P5: a refresh answered by an event stream has no
// request to hand the stream to, so the flight closes it rather than
// holding the origin connection open.
func TestEarlyRefreshClosesUnclaimedStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		fill := cacheable("v")
		fill.Delay = 2 * time.Second // Δ = 2s: the trigger horizon is about 73s
		o.Default(fill)
		cfg := cacheCfg
		cfg.Rand = func() float64 { return math.Nextafter(1, 0) }
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		body := &trackedBody{Reader: strings.NewReader("data: x\n\n"), closed: make(chan struct{})}
		o.Default(testorigin.Behavior{Func: func(*weir.Request) (*weir.Response, error) {
			return &weir.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}, nil
		}})
		time.Sleep(50 * time.Second)
		if resp, _ := serve(t, e, getReq("/a"), o); !resp.Cache.Hit {
			t.Fatal("near-expiry request not served as a hit")
		}
		synctest.Wait()
		select {
		case <-body.closed:
		default:
			t.Fatalf("stream body not closed after the refresh (origin calls %d)", o.TotalCalls())
		}
	})
}
