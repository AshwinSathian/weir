package weir_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store/memory"
)

// missWindow is the default MissRate.Window.
const missWindow = 10 * time.Second

// anomalyLog keeps the EvMissRateAnomaly events; Observe runs on engine
// goroutines.
type anomalyLog struct {
	mu  sync.Mutex
	evs []weir.Event
}

func (a *anomalyLog) Observe(ev weir.Event) {
	if ev.Kind != weir.EvMissRateAnomaly {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.evs = append(a.evs, ev)
}

func (a *anomalyLog) events() []weir.Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]weir.Event(nil), a.evs...)
}

// floodReq is the i-th request of a cache-busting flood on path: the same
// path with a query string no other request has (T-11).
func floodReq(path string, i int) *weir.Request {
	r := getReq(path)
	r.RawQuery = fmt.Sprintf("r=%d", i)
	return r
}

// flood sends n such requests at once and waits for all of them. Each ends
// served or shed. seq keeps the query strings distinct across calls.
func flood(t *testing.T, e *weir.Engine, o weir.Origin, path string, seq *int, n int) {
	t.Helper()
	chs := make([]<-chan timedServe, n)
	for i := range chs {
		chs[i] = serveTimed(t, e, floodReq(path, *seq), o)
		*seq++
	}
	for i, ch := range chs {
		if r := <-ch; r.err != nil && !isShed(r.err, 2*time.Second) {
			t.Fatalf("flood request %d: %v, want success or a shed", i, r.err)
		}
	}
}

// slowOrigin is a checked origin whose every response is cacheable and
// takes 50 ms, so concurrent misses overlap.
func slowOrigin(t *testing.T) *testorigin.Origin {
	o := testorigin.NewChecked(t, 64, 16)
	b := cacheable("v")
	b.Delay = 50 * time.Millisecond
	o.Default(b)
	return o
}

// FR-LIM-3, T-11, T6.8: 10 000 requests to one path with random query
// strings keep at most MaxPerPartition fetches in flight, and keys on other
// paths stay hits while the flood runs.
func TestRandomQueryFloodBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := slowOrigin(t)
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		for i := range 10 {
			serve(t, e, getReq(fmt.Sprintf("/hot%d", i)), o)
		}

		chs := make([]<-chan timedServe, 10000)
		for i := range chs {
			chs[i] = serveTimed(t, e, floodReq("/p", i), o)
		}
		synctest.Wait() // the flood holds its slots and its queue places
		for i := range 10 {
			if resp, _ := serve(t, e, getReq(fmt.Sprintf("/hot%d", i)), o); !resp.Cache.Hit {
				t.Fatalf("/hot%d during the flood: not a hit", i)
			}
		}
		for i, ch := range chs {
			if r := <-ch; r.err != nil && !isShed(r.err, 2*time.Second) {
				t.Fatalf("/p %d: %v, want success or a shed", i, r.err)
			}
		}
		if n := o.MaxInflightPartition(); n != 16 {
			t.Fatalf("/p max in-flight = %d, want exactly MaxPerPartition (16)", n)
		}
	})
}

// FR-MR-1, FR-MR-2, T-11, T6.8: the query flood raises exactly one
// EvMissRateAnomaly for its path per window, with a warning log. A path
// with as many misses at a 50% miss ratio raises none.
func TestMissRateAnomaly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := slowOrigin(t)
		o.Route("/mixed", cacheable("m")) // no delay: 1 000 serial misses fit in the window
		o.Route("/tick", cacheable("t"))
		obs := &anomalyLog{}
		var logs bytes.Buffer
		cfg := cacheCfg
		cfg.Observer = obs
		cfg.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
		start := time.Now()
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		var seq int
		for window := 1; window <= 2; window++ {
			flood(t, e, o, "/p", &seq, 10000)
			// 1 000 misses, then 1 000 hits on the same keys.
			for pass := range 2 {
				for i := range 1000 {
					if resp, _ := serve(t, e, floodReq("/mixed", seq+i), o); resp.Cache.Hit != (pass == 1) {
						t.Fatalf("/mixed %d pass %d: hit = %v", i, pass, resp.Cache.Hit)
					}
				}
			}
			seq += 1000
			if n := len(obs.events()); n != window-1 {
				t.Fatalf("window %d still open: %d anomaly events, want %d", window, n, window-1)
			}
			time.Sleep(missWindow)
			serve(t, e, getReq("/tick"), o) // rotation is lazy: this request closes the window

			evs := obs.events()
			if len(evs) != window {
				t.Fatalf("after window %d: %d anomaly events, want %d: %+v", window, len(evs), window, evs)
			}
			if ev := evs[window-1]; ev.Partition != "https://example.com/p" || ev.Reason != "flag" {
				t.Fatalf("anomaly event = %+v, want partition https://example.com/p, reason flag", ev)
			}
			// The event is dated by its window, not by the request that
			// happened to close it (04 §8.4).
			if end := start.Add(missWindow); window == 1 && !evs[0].Time.Equal(end) {
				t.Fatalf("anomaly event time = %v, want the window's end %v", evs[0].Time, end)
			}
		}
		// A quiet window reports nothing.
		time.Sleep(missWindow)
		serve(t, e, getReq("/tick"), o)
		if n := len(obs.events()); n != 2 {
			t.Fatalf("after a quiet window: %d anomaly events, want 2", n)
		}
		if n := strings.Count(logs.String(), "partition=https://example.com/p"); n != 2 {
			t.Fatalf("%d warnings name the partition, want 2:\n%s", n, logs.String())
		}
	})
}

// FR-MR-3, T-11, T6.8: with Throttle, the anomalous partition runs one
// fetch at a time in the window after the one that reported it. Without a
// report the cap ends by itself two windows after the anomalous window's
// end, a report that arrives after its window is not applied, and a report
// of an earlier window delivered late does not lift a newer one (04 §8.4).
func TestMissRateThrottle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := slowOrigin(t)
		obs := &eventCounter{}
		cfg := cacheCfg
		cfg.Observer = obs
		cfg.MissRate.Throttle = true
		e := newEngine(t, cfg)
		defer closeEngine(t, e)
		var seq int
		inflight := func(n int) int {
			t.Helper()
			o.Reset()
			flood(t, e, o, "/p", &seq, n)
			return o.MaxInflightPartition()
		}

		if n := inflight(1000); n != 16 {
			t.Fatalf("window 1: /p max in-flight = %d, want 16", n)
		}
		time.Sleep(missWindow)
		serve(t, e, getReq("/tick"), o) // closes window 1
		if n := obs.count("miss-rate-anomaly/throttle"); n != 1 {
			t.Fatalf("%d throttle events after window 1, want 1", n)
		}
		if n := inflight(1000); n != 1 {
			t.Fatalf("window 2: /p max in-flight = %d, want 1", n)
		}
		// Other paths keep their cap.
		o.Reset()
		flood(t, e, o, "/other", &seq, 100)
		if n := o.MaxInflightPartition(); n != 16 {
			t.Fatalf("window 2: /other max in-flight = %d, want 16", n)
		}

		// Window 2 was a flood too, but nothing closes it for an hour. The
		// throttle of window 1 has ended by then without a report, and the
		// report of window 2, an hour late, must not start one.
		time.Sleep(time.Hour)
		if n := inflight(1000); n != 16 {
			t.Fatalf("after an idle hour: /p max in-flight = %d, want 16", n)
		}
		if n := inflight(1000); n != 16 {
			t.Fatalf("after the hour-old report: /p max in-flight = %d, want 16", n)
		}
		if got, want := obs.count("miss-rate-anomaly/throttle"), 1; got != want {
			t.Fatalf("%d throttle events, want %d: the late report throttled", got, want)
		}
		if n := obs.count("miss-rate-anomaly/flag"); n != 1 {
			t.Fatalf("%d flag events, want 1: the late report still names its anomaly", n)
		}

		// Two callers stalled a window apart deliver out of order: the
		// window that just closed names /p, then the one before it arrives
		// with nothing.
		now := time.Now()
		weir.MissWindow(e, now, getReq("/p"))
		weir.MissWindow(e, now.Add(-missWindow))
		if n := inflight(200); n != 1 {
			t.Fatalf("after out-of-order reports: /p max in-flight = %d, want 1", n)
		}
	})
}

// T6.11, ADR-6: a flood of one-hit keys far larger than the store does not
// push a hot-key workload's hit ratio below 90% of its ratio without the
// flood.
func TestOneHitWondersDoNotEvictHot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const hot = 100
		o := testorigin.New()
		o.Default(cacheable(strings.Repeat("x", 1024)))
		// About 500 entries: five times the hot set, a twentieth of the flood.
		st, err := memory.New(memory.Config{MaxBytes: 512 << 10, Shards: 1})
		if err != nil {
			t.Fatal(err)
		}
		cfg := cacheCfg
		cfg.Store = st
		cfg.Storable.MaxObjectBytes = 8 << 10
		e := newEngine(t, cfg)
		defer closeEngine(t, e)
		defer st.Close()

		hits := func(round int) (n int) {
			if resp, _ := serve(t, e, getReq(fmt.Sprintf("/hot%d", round%hot)), o); resp.Cache.Hit {
				n = 1
			}
			return n
		}
		for i := range 2 * hot { // fill, then one read each
			hits(i)
		}
		// Two reads of each hot key on either side, so a key the flood
		// evicts costs at least half its reads: with small-to-main
		// promotion broken this scored 26 of 200, where 1 000 reads hid
		// the same fault at 892 of 1 000.
		const reads = 2 * hot
		var base int
		for i := range reads {
			base += hits(i)
		}
		var during int
		for i := range 10000 {
			serve(t, e, floodReq("/p", i), o)
			if i%(10000/reads) == 0 {
				during += hits(i / (10000 / reads))
			}
		}
		if base != reads {
			t.Fatalf("baseline: %d of %d hot reads hit", base, reads)
		}
		if during*10 < base*9 {
			t.Fatalf("during the flood: %d of %d hot reads hit, want at least 90%% of the baseline %d", during, reads, base)
		}
	})
}

// T-12, FR-LIM-1, FR-LIM-5, T6.8: 10 000 requests to distinct paths keep at
// most MaxConcurrent fetches in flight, and a request the limiter refuses
// gets ErrShed within MaxQueueWait. No path is heavy, so the tracker
// reports nothing: the limiter is what bounds this flood (04 §8.4).
func TestPathFloodOriginBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const wait = 2 * time.Second
		o := slowOrigin(t)
		obs := &anomalyLog{}
		cfg := cacheCfg
		cfg.Observer = obs
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		chs := make([]<-chan timedServe, 10000)
		for i := range chs {
			chs[i] = serveTimed(t, e, getReq(fmt.Sprintf("/d%d", i)), o)
		}
		var shed int
		for i, ch := range chs {
			r := <-ch
			switch {
			case r.err == nil:
			case !isShed(r.err, wait):
				t.Fatalf("/d%d: %v, want success or a shed", i, r.err)
			case r.elapsed > wait:
				t.Fatalf("/d%d: shed after %v, want at most %v", i, r.elapsed, wait)
			default:
				shed++
			}
		}
		if shed == 0 {
			t.Fatal("no request was shed: the flood did not exceed the limiter")
		}
		if n := o.MaxInflight(); n != 64 {
			t.Fatalf("origin max in-flight = %d, want exactly MaxConcurrent (64)", n)
		}
		time.Sleep(missWindow)
		serve(t, e, getReq("/tick"), o)
		if evs := obs.events(); len(evs) != 0 {
			t.Fatalf("anomaly events for a flood with no heavy path: %+v", evs)
		}
	})
}

// FR-MR-1, FR-MR-3, T-11: requests that cost the origin nothing are not
// misses. 1 000 requests that share one fetch of a cold key, and 600
// only-if-cached requests for a URL that is not cached, raise no anomaly;
// counted as misses they would let a client throttle a path for free.
func TestMissRateIgnoresFreeRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := slowOrigin(t)
		obs := &anomalyLog{}
		cfg := cacheCfg
		cfg.Observer = obs
		cfg.MissRate.Throttle = true
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		chs := make([]<-chan served, 1000)
		for i := range chs {
			chs[i] = serveAsync(t.Context(), e, getReq("/cold"), o)
		}
		for i, ch := range chs {
			if s := <-ch; s.err != nil {
				t.Fatalf("/cold %d: %v", i, s.err)
			}
		}
		if n := o.Calls("/cold"); n != 1 {
			t.Fatalf("/cold origin calls = %d, want 1", n)
		}
		for i := range 600 {
			req := withHeader(getReq("/victim"), "Cache-Control", "only-if-cached")
			if _, err := e.Serve(t.Context(), req, o); !errors.Is(err, weir.ErrOnlyIfCached) {
				t.Fatalf("/victim %d: %v, want ErrOnlyIfCached", i, err)
			}
		}
		time.Sleep(missWindow)
		serve(t, e, getReq("/tick"), o)
		if evs := obs.events(); len(evs) != 0 {
			t.Fatalf("anomaly events for requests that never reached the origin: %+v", evs)
		}
	})
}

// FR-MR-3, T-11: while the flood lasts the cap holds from one report to the
// next. A window is closed by the first request after its end, so the next
// report always comes a little after end + Window; a throttle that ended
// exactly there would let 16 fetches through at every boundary. The origin
// delay does not divide the window, so no fetch completes on a boundary.
func TestMissRateThrottleHoldsAcrossWindows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		b := cacheable("v")
		b.Delay = 37 * time.Millisecond
		o.Default(b)
		cfg := cacheCfg
		cfg.MissRate.Throttle = true
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		start, seq := time.Now(), 0
		for tick := 0; time.Since(start) < 45*time.Second; tick++ {
			for range 7 { // 1 000 requests a second
				req := floodReq("/p", seq)
				seq++
				go func() {
					if resp, err := e.Serve(t.Context(), req, o); err == nil {
						resp.Body.Close()
					}
				}()
			}
			time.Sleep(7 * time.Millisecond)
			if tick%20 == 19 {
				// Window 1 ends at 10 s; 2 s more lets its 16 fetches drain.
				if n := o.MaxInflightPartition(); time.Since(start) > 12*time.Second && n > 1 {
					t.Fatalf("at %v: /p in-flight = %d under throttle, want 1", time.Since(start), n)
				}
				o.Reset()
			}
		}
		time.Sleep(5 * time.Second) // queued requests time out before Close
	})
}

// FR-MR-1, FR-MR-3, T-11: only a request that started a fetch of its own
// can be a miss. Followers that leave before their flight ends, followers
// handed their flight's error, and requests the open breaker refused cost
// the origin one call or none and raise no anomaly; a client that sends
// distinct keys and leaves at once still does, because each of its flights
// fetches without it (FR-COA-2).
func TestMissRateCountsOwnFetchesOnly(t *testing.T) {
	const n = 600 // over MinMisses
	run := func(name string, want int, f func(t *testing.T, e *weir.Engine, o *testorigin.Origin)) {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := slowOrigin(t)
				obs := &anomalyLog{}
				cfg := cacheCfg
				cfg.Observer = obs
				cfg.MissRate.Throttle = true
				e := newEngine(t, cfg)
				defer closeEngine(t, e)
				f(t, e, o)
				time.Sleep(missWindow)
				serve(t, e, getReq("/tick"), o)
				if evs := obs.events(); len(evs) != want {
					t.Fatalf("%d anomaly events, want %d: %+v", len(evs), want, evs)
				}
			})
		})
	}
	run("followers that leave are not misses", 0, func(t *testing.T, e *weir.Engine, o *testorigin.Origin) {
		ctx, cancel := context.WithCancel(t.Context())
		chs := make([]<-chan served, n)
		for i := range chs {
			chs[i] = serveAsync(ctx, e, getReq("/victim"), o)
		}
		time.Sleep(10 * time.Millisecond) // the flight's fetch takes 50 ms
		cancel()
		for i, ch := range chs {
			// %T: the engine's follower mark must not reach the caller.
			if s := <-ch; !errors.Is(s.err, context.Canceled) || fmt.Sprintf("%T", s.err) != fmt.Sprintf("%T", context.Canceled) {
				t.Fatalf("/victim %d: %T %v, want context.Canceled itself", i, s.err, s.err)
			}
		}
		time.Sleep(time.Second)
		if c := o.Calls("/victim"); c != 1 {
			t.Fatalf("/victim origin calls = %d, want 1", c)
		}
	})
	run("followers of a failed flight are not misses", 0, func(t *testing.T, e *weir.Engine, o *testorigin.Origin) {
		o.Route("/victim", testorigin.Behavior{Delay: 50 * time.Millisecond, Err: errors.New("boom")})
		chs := make([]<-chan served, n)
		for i := range chs {
			chs[i] = serveAsync(t.Context(), e, getReq("/victim"), o)
		}
		for i, ch := range chs {
			s := <-ch
			if got := fmt.Sprintf("%T", s.err); got != "*weir.OriginError" {
				t.Fatalf("/victim %d: %s %v, want the flight's *weir.OriginError unwrapped", i, got, s.err)
			}
		}
		if c := o.Calls("/victim"); c != 1 {
			t.Fatalf("/victim origin calls = %d, want 1", c)
		}
	})
	run("requests the open breaker refused are not counted", 0, func(t *testing.T, e *weir.Engine, o *testorigin.Origin) {
		o.Route("/down", testorigin.Behavior{Err: errors.New("boom")})
		for i := range 40 { // opens the breaker: 20 requests, half failed (FR-CB-1)
			_, _ = e.Serve(t.Context(), floodReq("/down", i), o)
		}
		for i := range n {
			if _, err := e.Serve(t.Context(), floodReq("/victim", i), o); !errors.Is(err, weir.ErrCircuitOpen) {
				t.Fatalf("/victim %d: %v, want ErrCircuitOpen", i, err)
			}
		}
	})
	run("creators that leave are misses", 1, func(t *testing.T, e *weir.Engine, o *testorigin.Origin) {
		ctx, cancel := context.WithCancel(t.Context())
		chs := make([]<-chan served, n)
		for i := range chs {
			chs[i] = serveAsync(ctx, e, floodReq("/p", i), o)
		}
		synctest.Wait()
		cancel()
		for _, ch := range chs {
			<-ch
		}
		time.Sleep(3 * time.Second) // the flights fetch without their creators
		if c := o.Calls("/p"); c < 16 {
			t.Fatalf("/p origin calls = %d, want the abandoned flights to fetch", c)
		}
	})
}

// FR-MR-3, 04 §8.4: a report that arrives a window or more after its
// window's end is not applied, although the caps it would set have not
// reached their own deadline of two windows.
func TestMissRateThrottleSkipsLateReport(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := slowOrigin(t)
		obs := &eventCounter{}
		cfg := cacheCfg
		cfg.Observer = obs
		cfg.MissRate.Throttle = true
		start := time.Now()
		e := newEngine(t, cfg)
		defer closeEngine(t, e)
		var seq int
		flood(t, e, o, "/p", &seq, 1000)
		// Window 1 ends at 10 s. At 22 s its report is 2 s too late to
		// apply and 8 s short of the 30 s at which its caps would end.
		time.Sleep(22*time.Second - time.Since(start))
		flood(t, e, o, "/p", &seq, 1000)
		if n := obs.count("miss-rate-anomaly/throttle"); n != 0 {
			t.Fatalf("%d throttle events, want 0: the late report throttled", n)
		}
		if n := obs.count("miss-rate-anomaly/flag"); n != 1 {
			t.Fatalf("%d flag events, want 1", n)
		}
	})
}
