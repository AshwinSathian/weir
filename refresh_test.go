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
		o := testorigin.NewChecked(t, 64, 16)
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
		o := testorigin.NewChecked(t, 64, 16)
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
				o := testorigin.NewChecked(t, 64, 16)
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
		o := testorigin.NewChecked(t, 64, 16)
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
		o := testorigin.NewChecked(t, 64, 16)
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
		o := testorigin.NewChecked(t, 64, 16)
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

// swr is a response that may be served stale for 30s after 60s of life.
func swr(body string) testorigin.Behavior {
	return testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60, stale-while-revalidate=30"}}, Body: []byte(body)}
}

// FR-STL-1, FR-STL-6, 03 §2.3: requests inside the SWR window get the stale
// entry at once, and only one background refresh runs however many arrive.
func TestSWRServesAndRefreshesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(swr("a"))
		obs := &eventCounter{}
		cfg := cacheCfg
		cfg.Observer = obs
		cfg.Freshness.NoEarlyRefresh = true
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/k"), o)
		gate := make(chan struct{})
		next := swr("b")
		next.Gate = gate
		o.Default(next)
		time.Sleep(70 * time.Second) // 10s stale, inside the 30s window
		for i := range 10 {
			resp, body := serve(t, e, getReq("/k"), o)
			if body != "a" || !resp.Cache.Hit || resp.Cache.Stale != weir.StaleWhileRevalidate || resp.Cache.TTL != -10*time.Second {
				t.Fatalf("request %d: body %q cache %+v; want the stale entry, ttl=-10s", i, body, resp.Cache)
			}
		}
		synctest.Wait()
		if n := o.Calls("/k"); n != 2 {
			t.Fatalf("origin calls = %d, want 2 (fill and one refresh)", n)
		}
		if n := obs.count("stale-served/swr"); n != 10 { // 04 §12
			t.Fatalf("EvStaleServed swr = %d, want 10", n)
		}
		close(gate)
		synctest.Wait()
		if resp, body := serve(t, e, getReq("/k"), o); body != "b" || !resp.Cache.Hit || resp.Cache.Stale != weir.StaleNone {
			t.Fatalf("after refresh: body %q cache %+v; want the fresh entry", body, resp.Cache)
		}
	})
}

// FR-STL-1, FR-SRV-5, FR-SRV-6, T-7, T-8, T-31, FR-SRV-8: an Authorization or no-store request gets
// the stale entry but starts no refresh; a client no-cache honored under
// HonorRevalidation validates in the foreground instead. Only-if-cached
// and Range requests are answered from the stale entry.
func TestSWRGates(t *testing.T) {
	cases := []struct {
		name  string
		hdr   []string
		honor bool
		body  string
		calls int
	}{
		{name: "authorization request serves stale without refresh", hdr: []string{"Authorization", "Bearer x"}, body: "a", calls: 1},
		{name: "no-store request serves stale without refresh", hdr: []string{"Cache-Control", "no-store"}, body: "a", calls: 1},
		{name: "honored no-cache fetches in the foreground", hdr: []string{"Cache-Control", "no-cache"}, honor: true, body: "b", calls: 2},
		{name: "only-if-cached serves stale and refreshes", hdr: []string{"Cache-Control", "only-if-cached"}, body: "a", calls: 2},
		{name: "range request serves the stale entry and refreshes without range", hdr: []string{"Range", "bytes=0-0"}, body: "a", calls: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.NewChecked(t, 64, 16)
				fill := swr("a")
				fill.Header.Set("Cache-Control", "public, max-age=60, stale-while-revalidate=30")
				o.Default(fill)
				cfg := cacheCfg
				cfg.Freshness.NoEarlyRefresh = true
				cfg.Client.HonorRevalidation = tc.honor
				e := newEngine(t, cfg)
				defer closeEngine(t, e)

				serve(t, e, getReq("/k"), o)
				o.Default(swr("b"))
				time.Sleep(70 * time.Second)
				req := getReq("/k")
				req.Header.Set(tc.hdr[0], tc.hdr[1])
				if _, body := serve(t, e, req, o); body != tc.body {
					t.Fatalf("body %q, want %q", body, tc.body)
				}
				synctest.Wait()
				if n := o.Calls("/k"); n != tc.calls {
					t.Fatalf("origin calls = %d, want %d", n, tc.calls)
				}
				if reqs := o.Requests(); reqs[len(reqs)-1].Header.Get("Range") != "" { // T-7
					t.Fatal("refresh forwarded the client's Range")
				}
			})
		})
	}
}

// FR-LIM-4, FR-STL-1: a burst of SWR refreshes takes at most
// MaxConcurrent - ReserveForeground slots; the rest are dropped and counted,
// and every request is still served stale.
func TestRefreshNeverExceedsReserve(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const keys = 20
		o := testorigin.NewChecked(t, 3, 3) // MaxConcurrent - ReserveForeground
		o.Default(swr("a"))
		obs := &eventCounter{}
		cfg := cacheCfg
		cfg.Observer = obs
		cfg.Freshness.NoEarlyRefresh = true
		cfg.Limiter = weir.LimiterConfig{MaxConcurrent: 4, ReserveForeground: 1}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for i := range keys {
			serve(t, e, getReq("/k"+strconv.Itoa(i)), o)
		}
		gate := make(chan struct{})
		next := swr("b")
		next.Gate = gate
		o.Default(next)
		time.Sleep(70 * time.Second)
		for i := range keys {
			resp, body := serve(t, e, getReq("/k"+strconv.Itoa(i)), o)
			if body != "a" || resp.Cache.Stale != weir.StaleWhileRevalidate {
				t.Fatalf("/k%d: body %q stale %v; want the stale entry", i, body, resp.Cache.Stale)
			}
		}
		synctest.Wait()
		if n := o.MaxInflight(); n != 3 {
			t.Fatalf("origin max in-flight = %d, want 3", n)
		}
		if n := obs.count("refresh-dropped/no-slot"); n != keys-3 {
			t.Fatalf("EvRefreshDropped no-slot = %d, want %d", n, keys-3)
		}
		close(gate)
	})
}

// FR-LCY-2, 04 §12: an SWR hit racing Close is still served, and its refresh
// is dropped with EvRefreshDropped closed instead of starting a goroutine
// Close no longer waits for.
func TestSWRRefreshDroppedOnClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(swr("a"))
		// The request blocks in its stale-served event, before the refresh.
		obs := &holdObserver{key: "stale-served/swr", hold: make(chan struct{})}
		cfg := cacheCfg
		cfg.Observer = obs
		cfg.Freshness.NoEarlyRefresh = true
		e := newEngine(t, cfg)

		serve(t, e, getReq("/k"), o)
		time.Sleep(70 * time.Second)
		ch := serveAsync(t.Context(), e, getReq("/k"), o)
		synctest.Wait()
		closeEngine(t, e)
		close(obs.hold)
		if s := <-ch; s.err != nil || s.body != "a" {
			t.Fatalf("racing request: %v, %q; want the stale entry", s.err, s.body)
		}
		if n := obs.count("refresh-dropped/closed"); n != 1 {
			t.Fatalf("EvRefreshDropped closed = %d, want 1", n)
		}
		if n := o.Calls("/k"); n != 1 {
			t.Fatalf("origin calls = %d, want 1", n)
		}
	})
}
