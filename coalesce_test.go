package weir_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
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
		o := testorigin.NewChecked(t, 64, 16)
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
		o := testorigin.NewChecked(t, 64, 16)
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

// wideSlots lifts the limiter's caps above the concurrency of the tests
// below, which check that requests are not serialized by coalescing.
const wideSlots = 128

func wideLimiter(cfg weir.Config) weir.Config {
	cfg.Limiter.MaxConcurrent, cfg.Limiter.MaxPerPartition = wideSlots, wideSlots
	return cfg
}

// FR-COA-4, T6.2a: a stuck flight holds no follower past FollowerMaxWait.
// Followers serve stale when stale-if-error permits, else fetch themselves.
// The limiter is widened so its partition cap does not bind; the cap itself
// is TestPartitionFairness.
func TestCoalesceStuckLeader(t *testing.T) {
	const wait = 2 * time.Second
	cfg := wideLimiter(cacheCfg)
	cfg.Coalesce.FollowerMaxWait = wait

	t.Run("no stale entry: each follower fetches", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			gate := make(chan struct{})
			o := testorigin.NewChecked(t, wideSlots, wideSlots)
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
			o := testorigin.NewChecked(t, wideSlots, wideSlots)
			o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=1, stale-if-error=600"}}, Body: []byte("old")})
			obs := &eventCounter{}
			cfg := cfg
			cfg.Observer = obs
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
			if n := obs.count("stale-served/coalesce-timeout"); n != 100 {
				t.Fatalf("EvStaleServed coalesce-timeout = %d, want 100", n)
			}
		})
	})
}

// FR-COA-3, T6.2a: a request after LeaderMaxAge starts a second flight
// instead of joining the aged one.
func TestCoalesceLeaderAging(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		o := testorigin.NewChecked(t, 64, 16)
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
		{"Goexit in Fetch", testorigin.Behavior{Delay: 100 * time.Millisecond, Func: func(*weir.Request) (*weir.Response, error) {
			runtime.Goexit()
			return nil, nil
		}}},
		{"panic in body read", testorigin.Behavior{Delay: 100 * time.Millisecond, Func: func(*weir.Request) (*weir.Response, error) {
			return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: panicBody{}}, nil
		}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.NewChecked(t, 64, 16)
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
				o := testorigin.NewChecked(t, 64, 16)
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
		o := testorigin.NewChecked(t, 64, 16)
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
			o := testorigin.NewChecked(t, 64, 16)
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

// FR-PRG-7, T-10: a request that arrives after a purge never gets the
// response of a flight whose request was sent before it.
func TestCoalescePurgeDuringFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, err := memory.New(memory.Config{})
		if err != nil {
			t.Fatal(err)
		}
		defer m.Close()
		gate := make(chan struct{})
		o := testorigin.NewChecked(t, 64, 16)
		old := cacheable("old")
		old.Gate = gate
		o.Default(old)
		cfg := cacheCfg
		cfg.Store = m
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		creator := serveAsync(t.Context(), e, getReq("/a"), o)
		synctest.Wait()
		time.Sleep(time.Second)
		if err := m.SetEpoch(t.Context(), store.TagGlobal(), store.Epoch{At: time.Now(), Mode: store.EpochHard}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		follower := serveAsync(t.Context(), e, getReq("/a"), o)
		synctest.Wait()
		o.Default(cacheable("new"))
		close(gate)
		if s := <-creator; s.err != nil || s.body != "old" {
			t.Fatalf("creator: %v, %q; want its own old response", s.err, s.body)
		}
		if s := <-follower; s.err != nil || s.body != "new" {
			t.Fatalf("follower: %v, %q; want a fetch after the purge", s.err, s.body)
		}
	})
}

// T-31, FR-COA-8: a request whose response can never be shared (request
// no-store) does not lead a flight, so one client cannot make every
// concurrent request wait for an unusable result and then fetch again.
func TestCoalesceNoStoreRequestNotLeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		o := testorigin.NewChecked(t, 64, 16)
		b := cacheable("x")
		b.Gate = gate
		o.Default(b)
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		ns := serveAsync(t.Context(), e, withHeader(getReq("/a"), "Cache-Control", "no-store"), o)
		synctest.Wait()
		chs := make([]<-chan served, 5)
		for i := range chs {
			chs[i] = serveAsync(t.Context(), e, getReq("/a"), o)
		}
		synctest.Wait()
		close(gate)
		if s := <-ns; s.err != nil {
			t.Fatal(s.err)
		}
		collapsed := 0
		for _, ch := range chs {
			s := <-ch
			if s.err != nil {
				t.Fatal(s.err)
			}
			if s.resp.Cache.Collapsed {
				collapsed++
			}
		}
		if n := o.TotalCalls(); n != 2 || collapsed != 4 {
			t.Fatalf("origin calls = %d, collapsed = %d; want 2 (no-store direct, one flight) and 4", n, collapsed)
		}
	})
}

// T6.2, FR-COA-5, FR-STO-12: a flight whose response is not storable does
// not hold its followers in line. They re-enter, find the marker the flight
// left and fetch concurrently; later requests skip coalescing entirely.
func TestUncacheableNotSerialized(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, wideSlots, wideSlots)
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"private"}}, Body: []byte("p"), Delay: 100 * time.Millisecond})
		e := newEngine(t, wideLimiter(cacheCfg))
		defer closeEngine(t, e)

		run := func(gate chan struct{}) (collapsed int) {
			chs := make([]<-chan served, 50)
			for i := range chs {
				chs[i] = serveAsync(t.Context(), e, getReq("/p"), o)
			}
			if gate != nil {
				synctest.Wait()
				if n := o.TotalCalls(); n != 50 {
					t.Fatalf("origin calls with the marker = %d, want 50 at once", n)
				}
				close(gate)
			}
			for _, ch := range chs {
				s := <-ch
				if s.err != nil || s.body != "p" {
					t.Fatalf("got %v, %q; want p", s.err, s.body)
				}
				if s.resp.Cache.Collapsed {
					collapsed++
				}
			}
			return collapsed
		}
		if c := run(nil); c != 0 || o.TotalCalls() != 50 {
			t.Fatalf("first wave: collapsed = %d, origin calls = %d; want 0 and 50", c, o.TotalCalls())
		}
		if m := o.MaxInflight(); m <= 1 {
			t.Fatalf("origin max in flight = %d, want followers fetching concurrently", m)
		}
		resp, _ := serve(t, e, getReq("/p"), o)
		if resp.Cache.Detail != "hit-for-miss" {
			t.Fatalf("no marker after the flight: %+v", resp.Cache)
		}
		o.Reset()
		gate := make(chan struct{})
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"private"}}, Body: []byte("p"), Gate: gate})
		if c := run(gate); c != 0 || o.TotalCalls() != 50 {
			t.Fatalf("second wave: collapsed = %d, origin calls = %d; want 0 and 50", c, o.TotalCalls())
		}
	})
}

// FR-COA-8, T-8: concurrent Authorization requests on a cold key each fetch
// with their own credentials; no response crosses between them.
func TestAuthorizedNotCoalesced(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		o := testorigin.NewChecked(t, wideSlots, wideSlots)
		o.Default(testorigin.Behavior{Gate: gate, Func: func(r *weir.Request) (*weir.Response, error) {
			return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{"Cache-Control": {"max-age=60"}},
				Body: io.NopCloser(strings.NewReader("for " + r.Header.Get("Authorization")))}, nil
		}})
		e := newEngine(t, wideLimiter(cacheCfg))
		defer closeEngine(t, e)

		chs := make([]<-chan served, 50)
		for i := range chs {
			chs[i] = serveAsync(t.Context(), e, withHeader(getReq("/me"), "Authorization", "Bearer "+strconv.Itoa(i)), o)
		}
		synctest.Wait()
		if n := o.TotalCalls(); n != 50 {
			t.Fatalf("origin calls = %d, want 50", n)
		}
		close(gate)
		for i, ch := range chs {
			s := <-ch
			if want := "for Bearer " + strconv.Itoa(i); s.err != nil || s.body != want || s.resp.Cache.Collapsed {
				t.Fatalf("request %d: %v, %q, collapsed=%v; want its own %q", i, s.err, s.body, s.resp.Cache.Collapsed, want)
			}
		}
	})
}

// markerProbe sends one request with header set, whose response is a 401
// (refused for storage whatever the request), then 100 concurrent anonymous
// ones, and returns the origin calls the anonymous ones made.
func markerProbe(t *testing.T, name, value string) int {
	var calls atomic.Int32
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(testorigin.Behavior{Delay: 100 * time.Millisecond, Func: func(*weir.Request) (*weir.Response, error) {
		if calls.Add(1) == 1 {
			return &weir.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{}, Body: http.NoBody}, nil
		}
		return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{"Cache-Control": {"max-age=60"}},
			Body: io.NopCloser(strings.NewReader("pub"))}, nil
	}})
	e := newEngine(t, cacheCfg)
	defer closeEngine(t, e)

	serve(t, e, withHeader(getReq("/a"), name, value), o)
	o.Reset()
	chs := make([]<-chan served, 100)
	for i := range chs {
		chs[i] = serveAsync(t.Context(), e, getReq("/a"), o)
	}
	for _, ch := range chs {
		if s := <-ch; s.err != nil || s.body != "pub" {
			t.Fatalf("got %v, %q; want pub", s.err, s.body)
		}
	}
	return o.TotalCalls()
}

// FR-STO-12, T-31: junk credentials on a cold URL get a 401 that is not
// storable, but plant no marker that would switch coalescing off.
func TestMarkerNotFromAuthorizedRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		if n := markerProbe(t, "Authorization", "junk"); n != 1 {
			t.Fatalf("origin calls = %d, want 1", n)
		}
	})
}

// FR-STO-12, T-31: a request no-store keeps the response out of the store
// but plants no marker either.
func TestMarkerNotFromRequestNoStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		if n := markerProbe(t, "Cache-Control", "no-store"); n != 1 {
			t.Fatalf("origin calls = %d, want 1", n)
		}
	})
}

// zeros reads as an endless run of zero bytes without allocating.
type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// FR-COA-5, FR-STO-9, NFR-3: a body over MaxObjectBytes streams to the
// creator without being buffered, and followers re-enter and fetch it
// themselves instead of sharing the single-consumer stream.
func TestOversizedStreamedNotBuffered(t *testing.T) {
	const size = 50 << 20
	// big serves the body; the first call waits for first, later ones for rest.
	big := func(first, rest chan struct{}) testorigin.Behavior {
		var calls atomic.Int32
		return testorigin.Behavior{Func: func(*weir.Request) (*weir.Response, error) {
			if calls.Add(1) == 1 {
				<-first
			} else {
				<-rest
			}
			return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{"Cache-Control": {"max-age=60"}},
				Body: io.NopCloser(io.LimitReader(zeros{}, size))}, nil
		}}
	}

	t.Run("creator's stream stays below 2 x MaxObjectBytes of heap", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			open := make(chan struct{})
			close(open)
			o := testorigin.NewChecked(t, 64, 16)
			o.Default(big(open, open))
			e := newEngine(t, cacheCfg) // MaxObjectBytes defaults to 1 MiB
			defer closeEngine(t, e)

			var before, during runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			resp, err := e.Serve(t.Context(), getReq("/big"), o)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if _, err := io.CopyN(io.Discard, resp.Body, 10<<20); err != nil {
				t.Fatal(err)
			}
			runtime.GC()
			runtime.ReadMemStats(&during)
			if grew := int64(during.HeapAlloc) - int64(before.HeapAlloc); grew >= 2<<20 {
				t.Fatalf("heap grew %d bytes mid-stream, want < %d", grew, 2<<20)
			}
			n, err := io.Copy(io.Discard, resp.Body)
			if err != nil || n+10<<20 != size {
				t.Fatalf("read %d more bytes, %v; want the whole %d", n, err, size)
			}
		})
	})

	t.Run("followers re-enter and fetch themselves", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			first, rest := make(chan struct{}), make(chan struct{})
			o := testorigin.NewChecked(t, 64, 16)
			o.Default(big(first, rest))
			e := newEngine(t, cacheCfg)
			defer closeEngine(t, e)

			type result struct {
				n         int64
				collapsed bool
				err       error
			}
			chs := make([]chan result, 4)
			for i := range chs {
				chs[i] = make(chan result, 1)
				go func() {
					resp, err := e.Serve(t.Context(), getReq("/big"), o)
					if err != nil {
						chs[i] <- result{err: err}
						return
					}
					n, err := io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					chs[i] <- result{n, resp.Cache.Collapsed, err}
				}()
			}
			synctest.Wait()
			if n := o.TotalCalls(); n != 1 {
				t.Fatalf("origin calls before release = %d, want 1 flight", n)
			}
			close(first)
			synctest.Wait()
			// Followers never wait on each other serially: all three are at
			// the origin at once.
			if n := o.TotalCalls(); n != 4 {
				t.Fatalf("origin calls after the flight = %d, want 4 (one flight, three re-entries)", n)
			}
			close(rest)
			for _, ch := range chs {
				if r := <-ch; r.err != nil || r.n != size || r.collapsed {
					t.Fatalf("got %d bytes, %v, collapsed=%v; want %d of its own", r.n, r.err, r.collapsed, size)
				}
			}
		})
	})
}

// FR-STO-12, 04 §6.7: a marker never replaces a stored response. A stale
// entry kept for stale-if-error is refetched; the origin now answers
// private, and the stale response stays rather than a marker taking its
// place.
func TestMarkerNeverReplacesResponse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=1, stale-if-error=60"}}, Body: []byte("a")})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(2 * time.Second)
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"private"}}, Body: []byte("p")})
		serve(t, e, getReq("/a"), o)
		if resp, _ := serve(t, e, getReq("/a"), o); resp.Cache.Detail == "hit-for-miss" {
			t.Fatalf("a marker replaced the stored response: %+v", resp.Cache)
		}
	})
}

// FR-COA-5 (decided 2026-10-01): a storable flight response that needs
// validation before its next reuse (no-cache) is still shared with the
// followers waiting on it.
func TestCoalesceSharesNoCacheResponse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Gate: gate, Header: http.Header{"Cache-Control": {"no-cache"}, "Etag": {`"1"`}}, Body: []byte("n")})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		chs := make([]<-chan served, 5)
		for i := range chs {
			chs[i] = serveAsync(t.Context(), e, getReq("/n"), o)
		}
		synctest.Wait()
		close(gate)
		collapsed := 0
		for _, ch := range chs {
			s := <-ch
			if s.err != nil || s.body != "n" {
				t.Fatalf("got %v, %q; want n", s.err, s.body)
			}
			if s.resp.Cache.Collapsed {
				collapsed++
			}
		}
		if n := o.TotalCalls(); n != 1 || collapsed != 4 {
			t.Fatalf("origin calls = %d, collapsed = %d; want 1 and 4", n, collapsed)
		}
	})
}
