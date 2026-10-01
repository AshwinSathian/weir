package weir_test

import (
	"context"
	"errors"
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

// serveAsync runs Serve on its own goroutine and delivers the response with
// its body read, or the error.
func serveAsync(ctx context.Context, e *weir.Engine, req *weir.Request, o weir.Origin) <-chan served {
	ch := make(chan served, 1)
	go func() {
		resp, err := e.Serve(ctx, req, o)
		s := served{resp: resp, err: err}
		if err == nil {
			b, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			s.body, s.err = string(b), rerr
		}
		ch <- s
	}()
	return ch
}

type served struct {
	resp *weir.Response
	body string
	err  error
}

// FR-COA-1, FR-COA-2, T6.2: one flight serves every concurrent request for a
// cold key.
func TestCoalesceColdKey1000(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		b := cacheable("shared")
		b.Delay = 200 * time.Millisecond
		o.Default(b)
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		chs := make([]<-chan served, 1000)
		for i := range chs {
			chs[i] = serveAsync(t.Context(), e, getReq("/hot"), o)
		}
		collapsed := 0
		for _, ch := range chs {
			s := <-ch
			if s.err != nil || s.resp.StatusCode != http.StatusOK || s.body != "shared" {
				t.Fatalf("got %v, %q; want 200 shared", s.err, s.body)
			}
			if s.resp.Cache.Collapsed {
				collapsed++
			}
		}
		if n := o.TotalCalls(); n != 1 {
			t.Fatalf("origin calls = %d, want 1", n)
		}
		if collapsed != 999 {
			t.Fatalf("collapsed = %d, want 999", collapsed)
		}
	})
}

// FR-COA-2, FR-COA-9: canceling the creator does not cancel the shared fetch,
// and the fetch still carries the creator's context values.
func TestCoalesceCreatorCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		o := testorigin.New()
		b := cacheable("shared")
		b.Gate = gate
		o.Default(b)
		type ctxKey struct{}
		var seen atomic.Value
		via := weir.OriginFunc(func(ctx context.Context, req *weir.Request) (*weir.Response, error) {
			seen.Store(ctx.Value(ctxKey{}))
			return o.Fetch(ctx, req)
		})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		ctx, cancel := context.WithCancel(context.WithValue(t.Context(), ctxKey{}, "creator"))
		creator := serveAsync(ctx, e, getReq("/a"), via)
		synctest.Wait()
		followers := make([]<-chan served, 5)
		for i := range followers {
			followers[i] = serveAsync(t.Context(), e, getReq("/a"), via)
		}
		synctest.Wait()
		cancel()
		if s := <-creator; !errors.Is(s.err, context.Canceled) {
			t.Fatalf("creator: %v, want context.Canceled", s.err)
		}
		close(gate)
		for _, ch := range followers {
			if s := <-ch; s.err != nil || s.body != "shared" {
				t.Fatalf("follower: %v, %q", s.err, s.body)
			}
		}
		if n := o.TotalCalls(); n != 1 {
			t.Fatalf("origin calls = %d, want 1", n)
		}
		if v := seen.Load(); v != "creator" {
			t.Fatalf("origin saw context value %v, want the creator's", v)
		}
	})
}

// FR-COA-4, T6.2a: a stuck flight holds no follower past FollowerMaxWait.
// Followers serve stale when stale-if-error permits, else fetch themselves.
// ponytail: MaxPerPartition is not asserted until the limiter lands (M4).
func TestCoalesceStuckLeader(t *testing.T) {
	const wait = 2 * time.Second
	cfg := cacheCfg
	cfg.Coalesce.FollowerMaxWait = wait

	t.Run("no stale entry: each follower fetches", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			gate := make(chan struct{})
			o := testorigin.New()
			o.Default(testorigin.Behavior{Gate: gate, Header: http.Header{"Cache-Control": {"max-age=60"}}})
			e := newEngine(t, cfg)
			defer closeEngine(t, e)

			chs := make([]<-chan served, 100)
			for i := range chs {
				chs[i] = serveAsync(t.Context(), e, getReq("/a"), o)
			}
			synctest.Wait()
			if n := o.TotalCalls(); n != 1 {
				t.Fatalf("calls before the wait = %d, want 1", n)
			}
			time.Sleep(wait + time.Millisecond)
			synctest.Wait()
			// The creator keeps waiting on its own flight instead of fetching twice.
			if n := o.TotalCalls(); n != 100 {
				t.Fatalf("calls after FollowerMaxWait = %d, want 1 flight + 99 follower fetches", n)
			}
			close(gate)
			for _, ch := range chs {
				if s := <-ch; s.err != nil {
					t.Fatal(s.err)
				}
			}
		})
	})

	t.Run("stale-if-error entry: followers serve stale", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := testorigin.New()
			o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=1, stale-if-error=600"}}, Body: []byte("old")})
			e := newEngine(t, cfg)
			defer closeEngine(t, e)
			serve(t, e, getReq("/a"), o)
			time.Sleep(5 * time.Second)

			gate := make(chan struct{})
			defer close(gate)
			o.Default(testorigin.Behavior{Gate: gate})
			start := time.Now()
			chs := make([]<-chan served, 100)
			for i := range chs {
				chs[i] = serveAsync(t.Context(), e, getReq("/a"), o)
			}
			for _, ch := range chs {
				s := <-ch
				if s.err != nil {
					t.Fatal(s.err)
				}
				if s.body != "old" || s.resp.Cache.Stale != weir.StaleCoalesceTimeout {
					t.Fatalf("got %q, stale=%v; want old served as coalesce-timeout", s.body, s.resp.Cache.Stale)
				}
			}
			if d := time.Since(start); d > wait+time.Millisecond {
				t.Fatalf("followers waited %v, want at most %v", d, wait+time.Millisecond)
			}
			if n := o.TotalCalls(); n != 2 {
				t.Fatalf("origin calls = %d, want the priming call and 1 flight", n)
			}
		})
	})
}

// FR-COA-3, T6.2a: a request after LeaderMaxAge starts a second flight
// instead of joining the aged one.
func TestCoalesceLeaderAging(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		o := testorigin.New()
		b := cacheable("x")
		b.Gate = gate
		o.Default(b)
		cfg := cacheCfg
		cfg.Coalesce.LeaderMaxAge = time.Second
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		var chs []<-chan served
		for _, at := range []time.Duration{0, 500 * time.Millisecond, 500 * time.Millisecond, 200 * time.Millisecond} {
			time.Sleep(at)
			chs = append(chs, serveAsync(t.Context(), e, getReq("/a"), o))
			synctest.Wait()
		}
		if n := o.TotalCalls(); n != 2 {
			t.Fatalf("origin calls = %d, want 2 (one per LeaderMaxAge)", n)
		}
		close(gate)
		for _, ch := range chs {
			if s := <-ch; s.err != nil {
				t.Fatal(s.err)
			}
		}
	})
}

// panicBody panics on Read, outside Origin.Fetch's own recover.
type panicBody struct{}

func (panicBody) Read([]byte) (int, error) { panic("body read") }
func (panicBody) Close() error             { return nil }

// FR-COA-6, NFR-2: a panic in the origin reaches every waiter as
// *OriginError and the flight is released, so the next request refetches.
func TestCoalescePanic(t *testing.T) {
	for _, tt := range []struct {
		name string
		b    testorigin.Behavior
	}{
		{"panic in Fetch", testorigin.Behavior{Delay: 100 * time.Millisecond, Panic: true}},
		{"panic in body read", testorigin.Behavior{Delay: 100 * time.Millisecond, Func: func(*weir.Request) (*weir.Response, error) {
			return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: panicBody{}}, nil
		}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.New()
				o.Default(tt.b)
				e := newEngine(t, cacheCfg)
				defer closeEngine(t, e)

				var wg sync.WaitGroup
				for range 10 {
					wg.Go(func() {
						_, err := e.Serve(t.Context(), getReq("/a"), o)
						if _, ok := errors.AsType[*weir.OriginError](err); !ok {
							t.Errorf("err = %v, want *OriginError", err)
						}
					})
				}
				wg.Wait()
				if n := o.TotalCalls(); n != 1 {
					t.Fatalf("origin calls = %d, want 1", n)
				}
				o.Default(cacheable("ok"))
				if _, body := serve(t, e, getReq("/a"), o); body != "ok" {
					t.Fatalf("next request got %q, want a fresh fetch", body)
				}
			})
		})
	}
}

// FR-COA-8, FR-STO-12: a request carrying Authorization, or one that finds
// a hit-for-miss marker, fetches on its own instead of joining a flight.
// M2-03 refines both paths; this pins the skip.
func TestCoalesceSkipsDirectRequests(t *testing.T) {
	for _, tt := range []struct {
		name   string
		prime  bool // store a marker first with a private response
		header string
	}{
		{"authorization", false, "Authorization"},
		{"hit-for-miss marker", true, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.New()
				e := newEngine(t, cacheCfg)
				defer closeEngine(t, e)
				if tt.prime {
					o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"private"}}})
					serve(t, e, getReq("/a"), o)
					o.Reset()
				}
				gate := make(chan struct{})
				o.Default(testorigin.Behavior{Gate: gate, Header: http.Header{"Cache-Control": {"private"}}})
				chs := make([]<-chan served, 5)
				for i := range chs {
					req := getReq("/a")
					if tt.header != "" {
						req.Header.Set(tt.header, "Bearer x")
					}
					chs[i] = serveAsync(t.Context(), e, req, o)
				}
				synctest.Wait()
				if n := o.TotalCalls(); n != 5 {
					t.Fatalf("origin calls = %d, want 5 independent fetches", n)
				}
				close(gate)
				for _, ch := range chs {
					if s := <-ch; s.err != nil {
						t.Fatal(s.err)
					}
				}
			})
		})
	}
}

// FR-COA-9, FR-LCY-2: only Close cancels a flight. Its waiters get
// ErrClosed, even when the origin itself reports context.Canceled.
func TestCoalesceCanceledByClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(testorigin.Behavior{Gate: make(chan struct{})})
		e := newEngine(t, cacheCfg)
		chs := make([]<-chan served, 3)
		for i := range chs {
			chs[i] = serveAsync(t.Context(), e, getReq("/a"), o)
		}
		synctest.Wait()
		ctx, cancel := context.WithCancel(t.Context())
		cancel() // no grace period: cancel the flight at once
		if err := e.Close(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Close: %v", err)
		}
		for _, ch := range chs {
			if s := <-ch; !errors.Is(s.err, weir.ErrClosed) {
				t.Fatalf("waiter: %v, want ErrClosed", s.err)
			}
		}
	})

	t.Run("origin's own context.Canceled stays an origin error", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := testorigin.New()
			o.Default(testorigin.Behavior{Err: context.Canceled})
			e := newEngine(t, cacheCfg)
			defer closeEngine(t, e)
			_, err := e.Serve(t.Context(), getReq("/a"), o)
			if _, ok := errors.AsType[*weir.OriginError](err); !ok {
				t.Fatalf("err = %v, want *OriginError", err)
			}
		})
	})
}
