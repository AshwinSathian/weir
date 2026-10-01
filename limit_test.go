package weir_test

import (
	"errors"
	"fmt"
	"io"
	"math"
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
// stays within MaxPerPartition in flight and queues at most MaxPerPartition
// more, so it can fill neither the slots nor the queue. 50 requests to other
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
		if ok != 32 {
			t.Fatalf("/search served %d, want 32 (16 in flight, 16 queued)", ok)
		}
		if n := o.MaxInflightPartition(); n > 16 {
			t.Fatalf("/search max in-flight = %d, want <= 16", n)
		}
	})
}

// FR-LIM-1, FR-TMO-2, T-18: 200 pass-through responses whose consumers never
// read the body hold no limiter slot, so the next request never queues; the
// streams still end at Timeouts.Origin.
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
		synctest.Wait() // the deadlines fire at this same instant
		for i, b := range bodies {
			if _, err := io.ReadAll(b); err == nil {
				t.Fatalf("stream %d read after Timeouts.Origin succeeded", i)
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
