package weir_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// serveResult runs one request and returns the response with its body read,
// or the error.
func serveResult(t *testing.T, e *weir.Engine, req *weir.Request, o weir.Origin) (*weir.Response, string, error) {
	t.Helper()
	resp, err := e.Serve(t.Context(), req, o)
	if err != nil {
		return nil, "", err
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(b), nil
}

func ccBehavior(status int, cc, body string) testorigin.Behavior {
	return testorigin.Behavior{Status: status, Header: http.Header{"Cache-Control": {cc}}, Body: []byte(body)}
}

// FR-STL-2, 01 §7.2, T6.6: within its stale-if-error window a stale entry is
// served when the origin is down; past the window the origin's error is.
func TestStaleIfErrorOnOriginDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(ccBehavior(200, "max-age=10, stale-if-error=60", "a"))
		obs := &eventCounter{}
		cfg := cacheCfg
		cfg.Observer = obs
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(20 * time.Second)
		o.SetDown(true)
		time.Sleep(10 * time.Second)
		for range 3 {
			resp, body := serve(t, e, getReq("/a"), o)
			if body != "a" || resp.StatusCode != http.StatusOK || resp.Cache.Stale != weir.StaleIfError {
				t.Fatalf("at 30s: %d %q stale=%v, want stale 200", resp.StatusCode, body, resp.Cache.Stale)
			}
			if cs := resp.Header.Get("Cache-Status"); cs != "Weir; hit; ttl=-20; detail=stale-if-error" {
				t.Fatalf("Cache-Status = %q", cs)
			}
		}
		if n := obs.count("stale-served/sie"); n != 3 {
			t.Fatalf("EvStaleServed sie = %d, want 3", n)
		}
		time.Sleep(50 * time.Second)
		if _, _, err := serveResult(t, e, getReq("/a"), o); !errors.Is(err, weir.ErrOrigin) || weir.StatusCode(err) != http.StatusBadGateway {
			t.Fatalf("at 80s: err = %v, want ErrOrigin (502)", err)
		}
	})
}

// FR-STL-4, 01 §7.2, T6.6: a must-revalidate or proxy-revalidate entry that
// cannot be validated is ErrMustRevalidate, even with stale-if-error; an
// origin 5xx response is passed through instead.
func TestMustRevalidate504(t *testing.T) {
	for _, cc := range []string{"max-age=10, must-revalidate", "max-age=10, proxy-revalidate", "max-age=10, must-revalidate, stale-if-error=60"} {
		t.Run(cc, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.NewChecked(t, 64, 16)
				b := ccBehavior(200, cc, "a")
				b.Header.Set("ETag", `"v1"`) // kept past its lifetime for validation
				o.Default(b)
				e := newEngine(t, cacheCfg)
				defer closeEngine(t, e)

				serve(t, e, getReq("/a"), o)
				time.Sleep(20 * time.Second)
				o.SetDown(true)
				_, _, err := serveResult(t, e, getReq("/a"), o)
				if !errors.Is(err, weir.ErrMustRevalidate) || weir.StatusCode(err) != http.StatusGatewayTimeout {
					t.Fatalf("origin down: err = %v, want ErrMustRevalidate (504)", err)
				}
				o.SetDown(false)
				o.Default(ccBehavior(http.StatusServiceUnavailable, "", "busy"))
				resp, body := serve(t, e, getReq("/a"), o)
				if resp.StatusCode != http.StatusServiceUnavailable || body != "busy" {
					t.Fatalf("origin 503: got %d %q, want the 503 passed through", resp.StatusCode, body)
				}
			})
		})
	}
}

// D6, FR-STL-2, T6.6: with no stale directives from the origin and the
// default zero stale windows, nothing stale is ever served.
func TestDefaultStaleWindowsOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(ccBehavior(200, "max-age=10", "a"))
		cfg := cacheCfg
		cfg.Negative.Disable = true // the expired entry is gone, so each failure would leave one
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(11 * time.Second)
		o.SetDown(true)
		if _, _, err := serveResult(t, e, getReq("/a"), o); !errors.Is(err, weir.ErrOrigin) {
			t.Fatalf("origin down: err = %v, want ErrOrigin", err)
		}
		o.SetDown(false)
		for _, st := range []int{500, 502, 503, 504} {
			o.Default(ccBehavior(st, "", "fail"))
			resp, body := serve(t, e, getReq("/a"), o)
			if resp.StatusCode != st || body != "fail" || resp.Cache.Stale != weir.StaleNone {
				t.Fatalf("origin %d: got %d %q stale=%v, want the %d passed through", st, resp.StatusCode, body, resp.Cache.Stale, st)
			}
		}
	})
}

// FR-COA-5, 04 §6.4, §6.6: followers of a flight whose origin answered with a
// 5xx receive that 5xx, each with its own body, instead of refetching.
func TestFollowersShareFlight5xx(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		gate := make(chan struct{})
		b := ccBehavior(http.StatusBadGateway, "", "down")
		b.Gate = gate
		o.Default(b)
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		const n = 20
		type result struct {
			status int
			body   string
		}
		results := make(chan result, n)
		for range n {
			go func() {
				resp, body := serve(t, e, getReq("/a"), o)
				results <- result{resp.StatusCode, body}
			}()
		}
		synctest.Wait()
		close(gate)
		for range n {
			if r := <-results; r.status != http.StatusBadGateway || r.body != "down" {
				t.Fatalf("got %d %q, want 502 \"down\"", r.status, r.body)
			}
		}
		if c := o.Calls("/a"); c != 1 {
			t.Fatalf("origin calls = %d, want 1", c)
		}
	})
}

// FR-CB-1..6, FR-STL-2, T6.6: 30 gateway failures in 2 s open the breaker;
// while open no request reaches the origin, a stale-if-error entry is served
// with detail=circuit-open and the rest get ErrCircuitOpen with a Retry-After
// hint. After OpenFor (±20%) one probe goes through and its success closes
// the breaker; a failed probe reopens it for twice as long.
func TestBreakerOpensHalfOpenCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Route("/stale", ccBehavior(200, "max-age=1, stale-if-error=600", "s"))
		o.Route("/swr", ccBehavior(200, "max-age=1, stale-while-revalidate=600", "w"))
		obs := &breakerStates{}
		cfg := cacheCfg
		cfg.Observer = obs
		cfg.Rand = func() float64 { return 0.5 } // no jitter: open for exactly OpenFor
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/stale"), o)
		serve(t, e, getReq("/swr"), o)
		time.Sleep(11 * time.Second) // the success leaves the 10 s window
		o.Default(ccBehavior(http.StatusServiceUnavailable, "", "down"))
		for i := range 30 {
			if _, _, err := serveResult(t, e, getReq(fmt.Sprintf("/f%d", i)), o); err != nil && !errors.Is(err, weir.ErrCircuitOpen) {
				t.Fatalf("failure %d: %v", i, err)
			}
			time.Sleep(2 * time.Second / 30)
		}
		calls := o.TotalCalls()
		if calls != 2+20 {
			t.Fatalf("origin calls = %d, want 22 (the breaker opens at the 20th failure)", calls)
		}
		_, _, err := serveResult(t, e, getReq("/x"), o)
		if !errors.Is(err, weir.ErrCircuitOpen) || weir.StatusCode(err) != http.StatusServiceUnavailable {
			t.Fatalf("while open: err = %v, want ErrCircuitOpen (503)", err)
		}
		if after, ok := weir.RetryAfter(err); !ok || after <= 0 || after > 5*time.Second {
			t.Fatalf("RetryAfter = %v, %v, want the remaining open time", after, ok)
		}
		resp, body := serve(t, e, getReq("/stale"), o)
		if body != "s" || resp.Cache.Stale != weir.StaleCircuitOpen {
			t.Fatalf("stale while open: %q stale=%v, want detail=circuit-open", body, resp.Cache.Stale)
		}
		// FR-CB-5: the background refresh an SWR hit starts is dropped.
		if resp, body = serve(t, e, getReq("/swr"), o); body != "w" || resp.Cache.Stale != weir.StaleWhileRevalidate {
			t.Fatalf("swr while open: %q stale=%v", body, resp.Cache.Stale)
		}
		synctest.Wait()
		if n := obs.dropped.Load(); n != 1 {
			t.Fatalf("EvRefreshDropped circuit-open = %d, want 1", n)
		}
		if o.TotalCalls() != calls {
			t.Fatalf("origin calls while open = %d, want none", o.TotalCalls()-calls)
		}

		// Opened at the 20th failure, 19/30 of 2 s in; 30/30 have passed.
		time.Sleep(5*time.Second - 11*(2*time.Second/30) - time.Millisecond)
		if _, _, err := serveResult(t, e, getReq("/x"), o); !errors.Is(err, weir.ErrCircuitOpen) || o.TotalCalls() != calls {
			t.Fatalf("1 ms before OpenFor ends: err = %v, calls +%d; want still open", err, o.TotalCalls()-calls)
		}
		time.Sleep(time.Millisecond)

		// A failed probe reopens it for 10 s.
		if _, _, err := serveResult(t, e, getReq("/x"), o); err != nil || o.TotalCalls() != calls+1 {
			t.Fatalf("probe: err = %v, calls +%d; want one probe passed through", err, o.TotalCalls()-calls)
		}
		time.Sleep(10*time.Second - time.Millisecond)
		if _, _, err := serveResult(t, e, getReq("/x"), o); !errors.Is(err, weir.ErrCircuitOpen) {
			t.Fatalf("reopened: err = %v, want ErrCircuitOpen for the doubled period", err)
		}
		time.Sleep(time.Millisecond)

		// A probe that succeeds closes it.
		o.Default(ccBehavior(200, "max-age=60", "ok"))
		if _, body := serve(t, e, getReq("/y"), o); body != "ok" {
			t.Fatalf("probe body = %q", body)
		}
		for i := range 5 {
			if _, body := serve(t, e, getReq(fmt.Sprintf("/z%d", i)), o); body != "ok" {
				t.Fatalf("after close: body = %q", body)
			}
		}
		obs.mu.Lock()
		got := fmt.Sprint(obs.states)
		obs.mu.Unlock()
		if want := "[open half-open open half-open closed]"; got != want {
			t.Fatalf("EvBreakerState = %s, want %s", got, want)
		}
	})
}

// breakerStates records EvBreakerState reasons in order and counts
// refreshes dropped for the open breaker.
type breakerStates struct {
	mu      sync.Mutex
	states  []string
	dropped atomic.Int32 // EvRefreshDropped circuit-open
}

func (b *breakerStates) Observe(ev weir.Event) {
	if ev.Kind == weir.EvRefreshDropped && ev.Reason == "circuit-open" {
		b.dropped.Add(1)
	}
	if ev.Kind == weir.EvBreakerState {
		b.mu.Lock()
		b.states = append(b.states, ev.Reason)
		b.mu.Unlock()
	}
}

// FR-COA-6, 04 §6.4: followers never read the creator's response, which its
// caller may already be writing to.
func TestFollowersDoNotReadCreatorResponse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		gate := make(chan struct{})
		b := ccBehavior(200, "no-store", "x")
		b.Gate = gate
		o.Default(b)
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				resp, err := e.Serve(t.Context(), getReq("/a"), o)
				if err != nil {
					t.Errorf("Serve: %v", err)
					return
				}
				resp.StatusCode = http.StatusTeapot
				resp.Body.Close()
			})
		}
		synctest.Wait()
		close(gate)
		wg.Wait()
	})
}

// FR-STL-5, T6.6: an entry invalidated by an unsafe method is never served
// stale, even within stale-if-error.
func TestInvalidatedEntryNotServedStaleOnError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(ccBehavior(200, "max-age=10, stale-if-error=600", "a"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		post := getReq("/a")
		post.Method = http.MethodPost
		serve(t, e, post, o)
		time.Sleep(20 * time.Second)
		o.SetDown(true)
		if _, _, err := serveResult(t, e, getReq("/a"), o); !errors.Is(err, weir.ErrOrigin) {
			t.Fatalf("err = %v, want ErrOrigin, not the invalidated entry", err)
		}
	})
}

// FR-CB-1, FR-CB-2: a body that fails after its headers is a failed fetch
// for the breaker, so an origin that always truncates opens it.
func TestBreakerCountsBodyFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: []byte("xxxx"), Truncate: 1})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		for i := range 20 {
			if _, _, err := serveResult(t, e, getReq(fmt.Sprintf("/t%d", i)), o); !errors.Is(err, weir.ErrOrigin) {
				t.Fatalf("truncated %d: err = %v, want ErrOrigin", i, err)
			}
		}
		if _, _, err := serveResult(t, e, getReq("/x"), o); !errors.Is(err, weir.ErrCircuitOpen) {
			t.Fatalf("after 20 truncated bodies: err = %v, want ErrCircuitOpen", err)
		}
	})
}

// FR-CB-4, FR-CB-5: a request refused while half-open, with the probe place
// taken, still gets a Retry-After hint of at least a second, so rejected
// clients do not all retry at once.
func TestBreakerHalfOpenRetryHint(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(ccBehavior(http.StatusServiceUnavailable, "", "down"))
		cfg := cacheCfg
		cfg.Rand = func() float64 { return 0.5 }
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for i := range 20 {
			if _, _, err := serveResult(t, e, getReq(fmt.Sprintf("/f%d", i)), o); err != nil && !errors.Is(err, weir.ErrCircuitOpen) {
				t.Fatalf("failure %d: %v", i, err)
			}
		}
		time.Sleep(5 * time.Second)
		gate := make(chan struct{})
		b := ccBehavior(200, "max-age=60", "ok")
		b.Gate = gate
		o.Default(b)
		probe := make(chan error, 1)
		go func() { _, _, err := serveResult(t, e, getReq("/probe"), o); probe <- err }()
		synctest.Wait()
		_, _, err := serveResult(t, e, getReq("/x"), o)
		if after, ok := weir.RetryAfter(err); !errors.Is(err, weir.ErrCircuitOpen) || !ok || after < time.Second {
			t.Fatalf("half-open, probe in flight: err = %v, RetryAfter = %v, %v; want ErrCircuitOpen with a hint of at least 1s", err, after, ok)
		}
		close(gate)
		if err := <-probe; err != nil {
			t.Fatalf("probe: %v", err)
		}
	})
}
