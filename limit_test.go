package weir_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// eventCounter counts events by kind and reason; Observe runs on engine
// goroutines.
type eventCounter struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *eventCounter) Observe(ev weir.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == nil {
		c.n = map[string]int{}
	}
	c.n[ev.Kind.String()+"/"+ev.Reason]++
}

func (c *eventCounter) count(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[key]
}

// timedServe is a Serve result with the time it took.
type timedServe struct {
	err     error
	elapsed time.Duration
}

func serveTimed(t *testing.T, e *weir.Engine, req *weir.Request, o weir.Origin) <-chan timedServe {
	ch := make(chan timedServe, 1)
	go func() {
		start := time.Now()
		resp, err := e.Serve(t.Context(), req, o)
		if err == nil {
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		ch <- timedServe{err, time.Since(start)}
	}()
	return ch
}

// isShed reports whether err is ErrShed in a *RetryError carrying the
// MaxQueueWait hint (FR-LIM-5).
func isShed(err error, wait time.Duration) bool {
	re, ok := errors.AsType[*weir.RetryError](err)
	after, hint := weir.RetryAfter(err)
	return ok && errors.Is(re, weir.ErrShed) && hint && after == wait
}

// FR-LIM-1, FR-LIM-2, FR-LIM-6, T6.3, INV-7: 5 000 cold keys on distinct
// paths at once reach the origin at most MaxConcurrent at a time, and all
// succeed when the queue is large enough to hold them.
func TestLimiterCap5000Keys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const slots = 64
		o := testorigin.NewChecked(t, slots, 16)
		b := cacheable("v")
		b.Delay = 50 * time.Millisecond
		o.Default(b)
		cfg := cacheCfg
		cfg.Limiter = weir.LimiterConfig{MaxConcurrent: slots, MaxQueue: 10000, MaxQueueWait: time.Minute}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		chs := make([]<-chan served, 5000)
		for i := range chs {
			chs[i] = serveAsync(t.Context(), e, getReq(fmt.Sprintf("/k%d", i)), o)
		}
		for i, ch := range chs {
			if s := <-ch; s.err != nil || s.body != "v" {
				t.Fatalf("request %d: %v, %q", i, s.err, s.body)
			}
		}
		if n := o.MaxInflight(); n != slots {
			t.Fatalf("origin max in-flight = %d, want exactly %d", n, slots)
		}
		if n := o.TotalCalls(); n != 5000 {
			t.Fatalf("origin calls = %d, want 5000", n)
		}
	})
}

// FR-LIM-2, FR-LIM-5, T6.3: with a short queue, requests the limiter cannot
// hold get ErrShed in a *RetryError with a MaxQueueWait hint, and none
// waits longer than MaxQueueWait plus its own origin time.
// ponytail: the stale half (SIE entries serve stale with detail=shed)
// completes in M5-03.
func TestLimiterShedsWithStale(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const delay, wait = 50 * time.Millisecond, 2 * time.Second
		o := testorigin.NewChecked(t, 64, 16)
		b := cacheable("v")
		b.Delay = delay
		o.Default(b)
		obs := &eventCounter{}
		cfg := cacheCfg
		cfg.Observer = obs
		cfg.Limiter = weir.LimiterConfig{MaxConcurrent: 64, MaxQueue: 100, MaxQueueWait: wait}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		chs := make([]<-chan timedServe, 5000)
		for i := range chs {
			chs[i] = serveTimed(t, e, getReq(fmt.Sprintf("/k%d", i)), o)
		}
		var ok, shed int
		for i, ch := range chs {
			r := <-ch
			switch {
			case r.err == nil:
				ok++
			case isShed(r.err, wait):
				shed++
			default:
				t.Fatalf("request %d: %v, want success or a shed", i, r.err)
			}
			if r.elapsed > wait+delay {
				t.Fatalf("request %d took %v, more than MaxQueueWait %v plus the origin delay", i, r.elapsed, wait)
			}
		}
		if ok < 164 || shed == 0 {
			t.Fatalf("ok = %d, shed = %d; want at least the 164 slots and queue places served, and some shed", ok, shed)
		}
		if n := obs.count("shed/queue-full"); n != shed {
			t.Fatalf("EvShed queue-full = %d, want %d", n, shed)
		}
	})
}

// FR-LIM-3, T-11, T6.3, T6.8: a flood of unique query strings on /search
// stays within MaxPerPartition in flight and queues at most
// max(MaxPerPartition, MaxQueue/4) more, so it can fill neither the slots
// nor the queue. 50 requests to other
// paths, arriving after the flood, complete without shedding.
func TestPartitionFairness(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const wait = 2 * time.Second
		o := testorigin.NewChecked(t, 64, 16)
		b := cacheable("v")
		b.Delay = 50 * time.Millisecond
		o.Default(b)
		cfg := cacheCfg
		// The flood alone would fill a queue shared without a partition cap.
		cfg.Limiter = weir.LimiterConfig{MaxConcurrent: 64, MaxQueue: 100, MaxQueueWait: wait}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		flood := make([]<-chan timedServe, 1000)
		for i := range flood {
			r := getReq("/search")
			r.RawQuery = fmt.Sprintf("q=%d", i)
			flood[i] = serveTimed(t, e, r, o)
		}
		synctest.Wait()
		others := make([]<-chan timedServe, 50)
		for i := range others {
			others[i] = serveTimed(t, e, getReq(fmt.Sprintf("/p%d", i)), o)
		}
		for i, ch := range others {
			if r := <-ch; r.err != nil {
				t.Fatalf("other path %d: %v", i, r.err)
			}
		}
		var ok int
		for i, ch := range flood {
			r := <-ch
			switch {
			case r.err == nil:
				ok++
			case !isShed(r.err, wait):
				t.Fatalf("/search %d: %v, want success or a shed", i, r.err)
			}
		}
		if ok != 41 {
			t.Fatalf("/search served %d, want 41 (16 in flight, 25 queued)", ok)
		}
		if n := o.MaxInflightPartition(); n > 16 {
			t.Fatalf("/search max in-flight = %d, want <= 16", n)
		}
	})
}

// FR-LIM-1, FR-TMO-2, T-18: 200 pass-through responses whose consumers never
// read the body hold no limiter slot, so the next request never queues. The
// streams have no total deadline, so they still read after Timeouts.Origin.
func TestSlowReaderDoesNotPinSlots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Body: []byte("streamed body")})
		cfg := cacheCfg
		cfg.Timeouts.Origin = 5 * time.Second
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		start := time.Now()
		bodies := make([]io.ReadCloser, 200)
		for i := range bodies {
			resp, err := e.Serve(t.Context(), postReq("/upload"), o)
			if err != nil {
				t.Fatalf("request %d: %v", i, err)
			}
			bodies[i] = resp.Body
		}
		if d := time.Since(start); d != 0 {
			t.Fatalf("200 unread streams took %v; a request queued for a pinned slot", d)
		}
		time.Sleep(cfg.Timeouts.Origin)
		synctest.Wait() // a total deadline would fire at this same instant
		for i, b := range bodies {
			if _, err := io.ReadAll(b); err != nil {
				t.Fatalf("stream %d read after Timeouts.Origin: %v", i, err)
			}
			b.Close()
		}
	})
}

// FR-LIM-1, FR-COA-1, T6.4: an empty store under 2 000 requests over 500
// hot keys sends one fetch per key, at most MaxConcurrent at a time.
func TestColdStartBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		b := cacheable("v")
		b.Delay = 50 * time.Millisecond
		o.Default(b)
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		chs := make([]<-chan served, 2000)
		for i := range chs {
			chs[i] = serveAsync(t.Context(), e, getReq(fmt.Sprintf("/k%d", i%500)), o)
		}
		for i, ch := range chs {
			if s := <-ch; s.err != nil || s.body != "v" {
				t.Fatalf("request %d: %v, %q", i, s.err, s.body)
			}
		}
		if n := o.TotalCalls(); n != 500 {
			t.Fatalf("origin calls = %d, want 500", n)
		}
		if n := o.MaxInflight(); n > 64 {
			t.Fatalf("origin max in-flight = %d, want <= 64", n)
		}
	})
}

// FR-LIM-4, FR-FRS-6: an early refresh finding no slot outside the
// foreground reserve is dropped (EvRefreshDropped no-slot), never queued,
// and the hit is still served from the entry.
func TestBackgroundRefreshDroppedWithoutSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 4, 4)
		o.Default(cacheable("v"))
		gate := make(chan struct{})
		o.Route("/busy", testorigin.Behavior{Gate: gate})
		obs := &eventCounter{}
		cfg := cacheCfg
		cfg.Observer = obs
		cfg.Rand = func() float64 { return math.Nextafter(1, 0) }
		cfg.Limiter = weir.LimiterConfig{MaxConcurrent: 4, ReserveForeground: 1}
		cfg.Timeouts.Origin = 2 * time.Minute // the busy fetches outlast the entry
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/hot"), o)
		busy := make([]<-chan timedServe, 3) // MaxConcurrent - ReserveForeground
		for i := range busy {
			busy[i] = serveTimed(t, e, postReq("/busy"), o)
		}
		synctest.Wait()
		time.Sleep(60*time.Second - time.Millisecond) // 1ms left: the refresh triggers
		if resp, body := serve(t, e, getReq("/hot"), o); !resp.Cache.Hit || body != "v" {
			t.Fatalf("hit=%v body=%q; want the entry", resp.Cache.Hit, body)
		}
		synctest.Wait()
		if n := o.Calls("/hot"); n != 1 {
			t.Fatalf("/hot origin calls = %d, want 1 (refresh dropped)", n)
		}
		if n := obs.count("refresh-dropped/no-slot"); n != 1 {
			t.Fatalf("EvRefreshDropped no-slot = %d, want 1", n)
		}
		close(gate)
		for _, ch := range busy {
			if r := <-ch; r.err != nil {
				t.Fatalf("busy request: %v", r.err)
			}
		}
	})
}

// holdObserver counts events and blocks the goroutine emitting kind/reason
// until hold is closed.
type holdObserver struct {
	eventCounter
	key  string
	hold chan struct{}
}

func (o *holdObserver) Observe(ev weir.Event) {
	o.eventCounter.Observe(ev)
	if ev.Kind.String()+"/"+ev.Reason == o.key {
		<-o.hold
	}
}

// FR-LIM-4, FR-COA-1, 04 §6.8: a request that joined a background flight
// which found no slot fetches as a foreground request instead of getting a
// shed meant only for background work.
func TestBackgroundDroppedFollowerFetches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 4, 4)
		o.Default(cacheable("v"))
		gate := make(chan struct{})
		o.Route("/busy", testorigin.Behavior{Gate: gate})
		// The flight blocks in its drop event, before it publishes.
		obs := &holdObserver{key: "refresh-dropped/no-slot", hold: make(chan struct{})}
		cfg := cacheCfg
		cfg.Observer = obs
		cfg.Rand = func() float64 { return math.Nextafter(1, 0) }
		cfg.Limiter = weir.LimiterConfig{MaxConcurrent: 4, ReserveForeground: 1}
		cfg.Timeouts.Origin = 2 * time.Minute
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/hot"), o)
		busy := make([]<-chan timedServe, 3)
		for i := range busy {
			busy[i] = serveTimed(t, e, postReq("/busy"), o)
		}
		synctest.Wait()
		time.Sleep(60*time.Second - time.Millisecond)
		serve(t, e, getReq("/hot"), o) // starts the refresh, which blocks in its drop event
		synctest.Wait()
		time.Sleep(time.Second) // the entry is stale now
		follower := serveAsync(t.Context(), e, getReq("/hot"), o)
		synctest.Wait()
		if n := obs.count("coalesce-join/"); n != 1 {
			t.Fatalf("EvCoalesceJoin = %d, want the follower on the refresh flight", n)
		}
		close(obs.hold)
		if s := <-follower; s.err != nil || s.body != "v" {
			t.Fatalf("follower: %v, %q; want a foreground fetch", s.err, s.body)
		}
		if n := o.Calls("/hot"); n != 2 {
			t.Fatalf("/hot origin calls = %d, want 2 (fill and the follower's fetch)", n)
		}
		close(gate)
		for _, ch := range busy {
			if r := <-ch; r.err != nil {
				t.Fatalf("busy request: %v", r.err)
			}
		}
	})
}

// P5, FR-LIM-6, FR-COA-2, T6.4: under a cold-start flood of distinct keys
// whose clients all leave, the flight table holds at most MaxConcurrent +
// MaxQueue flights: a flight the limiter sheds publishes at once, and
// leaving clients do not end the flights they started.
func TestFlightTableBoundedUnderFlood(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const slots, queue = 64, 100
		o := testorigin.NewChecked(t, slots, 16)
		gate := make(chan struct{})
		b := cacheable("v")
		b.Gate = gate
		o.Default(b)
		cfg := cacheCfg
		cfg.Limiter = weir.LimiterConfig{MaxConcurrent: slots, MaxQueue: queue}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		ctx, cancel := context.WithCancel(t.Context())
		chs := make([]<-chan served, 5000)
		for i := range chs {
			chs[i] = serveAsync(ctx, e, getReq(fmt.Sprintf("/k%d", i)), o)
		}
		synctest.Wait()
		if n := weir.Flights(e); n != slots+queue {
			t.Fatalf("flights = %d, want %d (in flight plus queued)", n, slots+queue)
		}
		cancel()
		for _, ch := range chs {
			<-ch
		}
		synctest.Wait()
		if n := weir.Flights(e); n > slots+queue {
			t.Fatalf("flights after clients left = %d, want <= %d", n, slots+queue)
		}
		close(gate)
		synctest.Wait()
		if n := weir.Flights(e); n != 0 {
			t.Fatalf("flights after the origin answered = %d, want 0", n)
		}
	})
}

// stallBody is a request or response body that never ends on its own: Read
// blocks until stop closes (then EOF) or ctx ends.
type stallBody struct {
	ctx  context.Context
	stop <-chan struct{}
}

func (b stallBody) Read([]byte) (int, error) {
	select {
	case <-b.stop:
		return 0, io.EOF
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	}
}

func (stallBody) Close() error { return nil }

// dripBody sends burst at once, then one byte per tick until n bytes are
// sent, then EOF. It never sends anything after stallAfter bytes (0 = no
// stall). Reads fail once ctx ends, like an http.Client body.
type dripBody struct {
	ctx        context.Context
	burst      []byte
	tick       time.Duration
	n, sent    int
	stallAfter int
}

func (b *dripBody) Read(p []byte) (int, error) {
	if len(b.burst) > 0 {
		n := copy(p, b.burst)
		b.burst = b.burst[n:]
		return n, nil
	}
	if b.sent >= b.n {
		return 0, io.EOF
	}
	d := b.tick
	if b.stallAfter > 0 && b.sent >= b.stallAfter {
		d = math.MaxInt64
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	}
	p[0] = 'x'
	b.sent++
	return 1, nil
}

func (*dripBody) Close() error { return nil }

func uploadReq(ctx context.Context, path string, stop <-chan struct{}) *weir.Request {
	r := postReq(path)
	r.Body = stallBody{ctx, stop}
	return r
}

// uploadOrigin reads each request body to the end, as a transport sends
// it, then answers from o. It records how many bodies were read at once.
type uploadOrigin struct {
	o            *testorigin.Origin
	mu           sync.Mutex
	reading, max int
}

func (u *uploadOrigin) Fetch(ctx context.Context, req *weir.Request) (*weir.Response, error) {
	if req.Body != nil {
		u.mu.Lock()
		u.reading++
		u.max = max(u.max, u.reading)
		u.mu.Unlock()
		_, err := io.Copy(io.Discard, req.Body)
		u.mu.Lock()
		u.reading--
		u.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	return u.o.Fetch(ctx, req)
}

// FR-LIM-7, T-39: 200 POSTs whose bodies never finish saturate the upload
// pool alone; cacheable misses on the main pool are not delayed.
func TestSlowUploadsDoNotStarveMisses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const slots, upload = 64, 16
		o := testorigin.NewChecked(t, slots+upload, 16)
		gate := make(chan struct{})
		miss := cacheable("v")
		miss.Gate = gate // every miss must hold a main slot at once
		o.Default(miss)
		origin := &uploadOrigin{o: o}
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		stop := make(chan struct{})
		uploads := make([]<-chan timedServe, 200)
		for i := range uploads {
			uploads[i] = serveTimed(t, e, uploadReq(t.Context(), "/upload", stop), origin)
		}
		synctest.Wait()
		misses := make([]<-chan timedServe, slots)
		for i := range misses {
			misses[i] = serveTimed(t, e, getReq(fmt.Sprintf("/k%d", i)), origin)
		}
		synctest.Wait()
		if n := o.TotalCalls(); n != slots {
			t.Fatalf("misses at the origin = %d, want all %d", n, slots)
		}
		close(gate)
		for i, ch := range misses {
			if r := <-ch; r.err != nil || r.elapsed != 0 {
				t.Fatalf("miss %d: err %v after %v; want served at once", i, r.err, r.elapsed)
			}
		}
		close(stop)
		for i, ch := range uploads {
			if r := <-ch; r.err != nil {
				t.Fatalf("upload %d: %v", i, r.err)
			}
		}
		if n := origin.max; n != upload {
			t.Fatalf("uploads in flight at once = %d, want the upload pool's %d", n, upload)
		}
	})
}

// FR-LIM-7: requests without a body (nil, http.NoBody, or a declared
// Content-Length: 0) take main-pool slots even when the upload pool is full.
func TestBodylessPassUsesMainPool(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 5, 4)
		origin := &uploadOrigin{o: o}
		cfg := cacheCfg
		cfg.Limiter = weir.LimiterConfig{MaxConcurrent: 4, MaxUpload: 1}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		stop := make(chan struct{})
		held := serveTimed(t, e, uploadReq(t.Context(), "/upload", stop), origin)
		queued := serveTimed(t, e, uploadReq(t.Context(), "/upload", stop), origin)
		synctest.Wait()

		for _, tc := range []struct {
			name string
			req  func() *weir.Request
		}{
			{"DELETE with nil body", func() *weir.Request { r := getReq("/a"); r.Method = http.MethodDelete; return r }},
			{"OPTIONS with NoBody", func() *weir.Request {
				r := getReq("/a")
				r.Method, r.Body = http.MethodOptions, http.NoBody
				return r
			}},
			{"POST declaring Content-Length 0", func() *weir.Request {
				r := postReq("/a")
				r.Header.Set("Content-Length", "0")
				r.Body = io.NopCloser(strings.NewReader(""))
				return r
			}},
		} {
			if r := <-serveTimed(t, e, tc.req(), origin); r.err != nil || r.elapsed != 0 {
				t.Fatalf("%s: err %v after %v; want served at once from the main pool", tc.name, r.err, r.elapsed)
			}
		}
		close(stop)
		for _, ch := range []<-chan timedServe{held, queued} {
			if r := <-ch; r.err != nil {
				t.Fatalf("upload: %v", r.err)
			}
		}
	})
}

// FR-TMO-1, T-41: an origin sending a 10 KiB cacheable body at 1 byte per
// second fails at Timeouts.Origin and releases its slot.
func TestDripOriginReleasesSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 1, 1)
		o.Default(cacheable("v"))
		origin := weir.OriginFunc(func(ctx context.Context, req *weir.Request) (*weir.Response, error) {
			if req.Path != "/drip" {
				return o.Fetch(ctx, req)
			}
			return &weir.Response{StatusCode: http.StatusOK,
				Header: http.Header{"Cache-Control": {"max-age=60"}},
				Body:   &dripBody{ctx: ctx, tick: time.Second, n: 10 << 10}}, nil
		})
		cfg := cacheCfg
		cfg.Limiter = weir.LimiterConfig{MaxConcurrent: 1}
		cfg.Timeouts.Origin = 5 * time.Second
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		r := <-serveTimed(t, e, getReq("/drip"), origin)
		if !errors.Is(r.err, weir.ErrOriginTimeout) || r.elapsed != cfg.Timeouts.Origin {
			t.Fatalf("drip: err %v after %v; want ErrOriginTimeout after %v", r.err, r.elapsed, cfg.Timeouts.Origin)
		}
		if r := <-serveTimed(t, e, getReq("/next"), origin); r.err != nil || r.elapsed != 0 {
			t.Fatalf("next: err %v after %v; want the released slot at once", r.err, r.elapsed)
		}
	})
}

// FR-TMO-2: streamed bodies (pass-through and oversized) have no total
// deadline. A 2-minute stream that keeps sending survives Timeouts.Origin;
// one that stalls ends StreamIdle after its last byte.
func TestStreamIdleTimeout(t *testing.T) {
	const limit = 1 << 10
	for _, tc := range []struct {
		name  string
		req   func() *weir.Request
		burst int
	}{
		{"pass-through", func() *weir.Request { return postReq("/s") }, 0},
		{"oversized", func() *weir.Request { return getReq("/s") }, limit + 1},
	} {
		for _, stall := range []bool{false, true} {
			name := tc.name + " sending"
			if stall {
				name = tc.name + " stalled"
			}
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					origin := weir.OriginFunc(func(ctx context.Context, _ *weir.Request) (*weir.Response, error) {
						b := &dripBody{ctx: ctx, burst: make([]byte, tc.burst), tick: time.Second, n: 120}
						if stall {
							b.stallAfter = 1
						}
						return &weir.Response{StatusCode: http.StatusOK,
							Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: b}, nil
					})
					cfg := cacheCfg
					cfg.Storable.MaxObjectBytes = limit
					e := newEngine(t, cfg)
					defer closeEngine(t, e)

					start := time.Now()
					resp, err := e.Serve(t.Context(), tc.req(), origin)
					if err != nil {
						t.Fatalf("Serve: %v", err)
					}
					defer resp.Body.Close()
					n, err := io.Copy(io.Discard, resp.Body)
					elapsed := time.Since(start)
					if !stall {
						if err != nil || n != int64(tc.burst+120) || elapsed != 120*time.Second {
							t.Fatalf("read %d bytes in %v, err %v; want %d in 2m0s", n, elapsed, err, tc.burst+120)
						}
						return
					}
					if want := time.Second + 60*time.Second; !errors.Is(err, weir.ErrOriginTimeout) || elapsed != want {
						t.Fatalf("stalled stream: err %v after %v; want ErrOriginTimeout after %v", err, elapsed, want)
					}
				})
			})
		}
	}
}
