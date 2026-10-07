//go:build load

package loadtest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

// eventLog counts engine events by kind and reason.
type eventLog struct {
	mu sync.Mutex
	n  map[string]int
}

func (l *eventLog) Observe(ev weir.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n == nil {
		l.n = map[string]int{}
	}
	l.n[ev.Kind.String()+"/"+ev.Reason]++
}

func (l *eventLog) count(kind, reason string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n[kind+"/"+reason]
}

func pathN(prefix string, i int) string { return fmt.Sprintf("/%s/%d", prefix, i) }

// TestSteadyHits: 64 goroutines over 10 000 hot keys for 60 s.
// 07 §9; NFR-1 (hit path latency).
func TestSteadyHits(t *testing.T) {
	const keys = 10000
	base := baseline()
	org := newCapOrigin(64, func(string) time.Duration { return svcTime(time.Millisecond) }, cacheControl("max-age=3600"))
	e := newEngine(t, weir.Config{Observer: nil})
	var warm counters
	for i := range keys { // fill the store; not measured
		serveOnce(e, org, pathN("hot", i), "", nil, &warm)
	}
	// Closed-loop goroutines at full speed measure run-queue wait once they
	// outnumber the cores, not the engine. Calibrate the saturated rate, then
	// hold the real run to half of it (07 §9 note on pacing).
	var cal counters
	stop := make(chan struct{})
	time.AfterFunc(2*time.Second, func() { close(stop) })
	closedLoop(64, stop, func(r *rand.Rand, h *hist) { serveOnce(e, org, pathN("hot", r.IntN(keys)), "", nil, &cal) })
	rate := int(cal.total.Load()/2) / 2

	s := startSampler(e, org)
	var c counters
	// Six windows: one scheduler hiccup or neighbor on a shared host moves
	// one window's p99, so the assertion is on the median window.
	const windows = 6
	win := dur(60*time.Second, 5*time.Second) / windows
	h := &hist{}
	var p99s []time.Duration
	t0 := time.Now()
	for range windows {
		stop = make(chan struct{})
		time.AfterFunc(win, func() { close(stop) })
		wh := closedLoopPaced(64, rate, stop, func(r *rand.Rand, h *hist) {
			serveOnce(e, org, pathN("hot", r.IntN(keys)), "", h, &c)
		})
		p99s = append(p99s, wh.quantile(0.99))
		h.merge(wh)
	}
	elapsed := time.Since(t0)
	s.close()
	settleThenClose(t, e, base)

	ratio := float64(c.hits.Load()) / float64(c.total.Load())
	sorted := slices.Sorted(slices.Values(p99s))
	p99 := sorted[len(sorted)/2]
	table(t, "steady hits", row("requests", c.total.Load()), row("paced target (50% of saturated)", rate), row("achieved rate", int(float64(c.total.Load())/elapsed.Seconds())), row("hit ratio", fmt.Sprintf("%.4f", ratio)),
		row("p50", h.quantile(0.5)), row("p99 per window", p99s), row("p99 median window", p99), row("p99 overall", h.quantile(0.99)), row("max", h.max), row("origin calls after warm", org.calls.Load()-int64(keys)))
	if ratio <= 0.99 {
		t.Errorf("hit ratio %.4f, want > 0.99", ratio)
	}
	if p99 >= 200*time.Microsecond {
		t.Errorf("median window p99 %v, want < 200µs", p99)
	}
}

// TestSynchronizedExpiry: 10 000 keys stored within 1 s with the same
// max-age, then read continuously through the expiry. FR-FRS-4, FR-LIM-1.
func TestSynchronizedExpiry(t *testing.T) {
	const keys = 10000
	const maxConc = 32
	// Not scaled: the 20% bound depends on the jitter spreading 10 000
	// expiries over 10% of the lifetime, which a shorter max-age shrinks.
	const maxAge = 60 * time.Second
	base := baseline()
	org := newCapOrigin(maxConc, func(string) time.Duration { return svcTime(5 * time.Millisecond) },
		cacheControl(fmt.Sprintf("max-age=%d", int(maxAge.Seconds()))))
	e := newEngine(t, weir.Config{Limiter: weir.LimiterConfig{MaxConcurrent: maxConc, MaxPerPartition: maxConc}})
	var c counters
	var wg sync.WaitGroup
	t0 := time.Now()
	next := atomic.Int64{}
	for range 32 { // storing phase: all keys inside about a second
		wg.Go(func() {
			for {
				i := int(next.Add(1)) - 1
				if i >= keys {
					return
				}
				serveOnce(e, org, pathN("sync", i), "", nil, &c)
			}
		})
	}
	wg.Wait()
	stored := time.Now()
	storeTook := stored.Sub(t0)
	s := startSampler(e, org)
	stop := make(chan struct{})
	time.AfterFunc(maxAge+maxAge/3, func() { close(stop) })
	closedLoopPaced(64, 4000, stop, func(r *rand.Rand, h *hist) {
		serveOnce(e, org, pathN("sync", r.IntN(keys)), "", h, &c)
	})
	s.close()
	settleThenClose(t, e, base)

	peak := s.peakCalls()
	refetched := org.calls.Load() - keys
	table(t, "synchronized expiry", row("keys", keys), row("max-age", maxAge), row("storing phase", storeTook.Round(10*time.Millisecond)), row("elapsed", time.Since(stored).Round(time.Second)),
		row("origin peak in-flight", org.peakInflight()), row("origin peak calls/s", peak), row("limit calls/s (20%)", keys/5),
		row("refetched after storing", refetched), row("client errors", c.errs.Load()), row("peak queue", s.peakQ))
	if refetched < keys*9/10 {
		t.Errorf("only %d of %d keys were refetched; the expiry never happened, so the bounds below prove nothing", refetched, keys)
	}
	if c.errs.Load() != 0 {
		t.Errorf("%d client errors during expiry, want 0", c.errs.Load())
	}
	if got := org.peakInflight(); got > maxConc {
		t.Errorf("origin peak in-flight %d, want <= MaxConcurrent %d", got, maxConc)
	}
	if peak > keys/5 {
		t.Errorf("origin calls in the busiest second %d, want <= %d", peak, keys/5)
	}
}

// closedLoopPaced is closedLoop capped at about rps requests a second in total.
func closedLoopPaced(n, rps int, stop <-chan struct{}, fn func(r *rand.Rand, h *hist)) *hist {
	gap := time.Second * time.Duration(n) / time.Duration(rps)
	// Absolute deadlines: a sleep that overshoots is made up by the next
	// request, so the achieved rate approaches rps instead of falling short.
	var mu sync.Mutex
	next := map[*rand.Rand]time.Time{}
	return closedLoop(n, stop, func(r *rand.Rand, h *hist) {
		fn(r, h)
		mu.Lock()
		at := next[r].Add(gap)
		if at.Before(time.Now().Add(-gap)) {
			at = time.Now() // never bank more than one gap of lag
		}
		next[r] = at
		mu.Unlock()
		if d := time.Until(at); d > 0 {
			time.Sleep(d)
		}
	})
}

// TestBustingFloodWithNormalTraffic: 5 000 rps of unique queries on one path
// beside 500 rps of normal traffic. FR-LIM-3, FR-MR-1 to FR-MR-3, T-6.
func TestBustingFloodWithNormalTraffic(t *testing.T) {
	const maxPart = 8
	phase := dur(20*time.Second, 4*time.Second)
	base := baseline()
	org := newCapOrigin(64, func(string) time.Duration { return svcTime(5 * time.Millisecond) }, cacheControl("max-age=3600"))
	e := newEngine(t, weir.Config{Limiter: weir.LimiterConfig{MaxConcurrent: 64, MaxPerPartition: maxPart}})
	var warm counters
	for i := range 200 {
		serveOnce(e, org, pathN("normal", i), "", nil, &warm)
	}
	normal := func(c *counters) func(*rand.Rand, *hist, int) {
		return func(r *rand.Rand, h *hist, _ int) { serveOnce(e, org, pathN("normal", r.IntN(200)), "", h, c) }
	}
	var baseC, floodC, flood counters
	baseH := openLoop(500, phase, 500, normal(&baseC), &baseC)

	s := startSampler(e, org)
	var wg sync.WaitGroup
	wg.Go(func() {
		var n atomic.Int64
		openLoop(5000, phase, 2000, func(_ *rand.Rand, h *hist, _ int) {
			serveOnce(e, org, "/flood", fmt.Sprintf("bust=%d", n.Add(1)), h, &flood)
		}, &flood)
	})
	floodH := openLoop(500, phase, 500, normal(&floodC), &floodC)
	wg.Wait()
	s.close()
	settleThenClose(t, e, base)

	b, f := baseH.quantile(0.99), floodH.quantile(0.99)
	table(t, "busting flood plus normal traffic",
		row("normal p99 alone", b), row("normal p99 under flood", f), row("normal p50 alone / flood", fmt.Sprint(baseH.quantile(0.5), " / ", floodH.quantile(0.5))),
		row("normal hit ratio under flood", fmt.Sprintf("%.4f", float64(floodC.hits.Load())/float64(floodC.total.Load()))),
		row("flood requests / shed errors", fmt.Sprint(flood.total.Load(), " / ", flood.errs.Load())), row("flood client drops", flood.dropped.Load()), row("normal offered (target ~500/s)", fmt.Sprint(floodC.offered.Load()/int64(phase.Seconds()), "/s, drops ", baseC.dropped.Load()+floodC.dropped.Load())),
		row("flood peak in-flight", org.peakPartition("/flood")), row("MaxPerPartition", maxPart))
	if got := org.peakPartition("/flood"); got > maxPart {
		t.Errorf("flooded path peak in-flight %d, want <= MaxPerPartition %d", got, maxPart)
	}
	if n := floodC.offered.Load(); n < int64(0.95*500*phase.Seconds()) || baseC.dropped.Load()+floodC.dropped.Load() != 0 {
		t.Errorf("normal traffic offered %d requests (drops %d), want >= 95%% of 500 rps and no drops", n, baseC.dropped.Load()+floodC.dropped.Load())
	}
	// Both p99 are bucket upper bounds (12.5% apart), and at tens of
	// microseconds one bucket is scheduler noise, so 20% gets a 50 µs floor.
	if f > b+max(b/5, 50*time.Microsecond) {
		t.Errorf("normal p99 under flood %v vs %v alone, want within 20%% (or 50µs)", f, b)
	}
}

// TestOriginBrownout: the origin's latency is multiplied by 20 for 30 s.
// FR-CB-1 to FR-CB-5, FR-STL, FR-LIM-4.
func TestOriginBrownout(t *testing.T) {
	const keys = 200
	brown := dur(30*time.Second, 6*time.Second)
	base := baseline()
	var slow atomic.Bool
	org := newCapOrigin(32, func(string) time.Duration {
		d := svcTime(5 * time.Millisecond)
		if slow.Load() {
			d *= 20
		}
		return d
	}, cacheControl("max-age=2, stale-if-error=600"))
	var ev eventLog
	e := newEngine(t, weir.Config{
		Observer:  &ev,
		Timeouts:  weir.TimeoutsConfig{Origin: 80 * time.Millisecond},
		Limiter:   weir.LimiterConfig{MaxConcurrent: 32, MaxQueueWait: 100 * time.Millisecond},
		Breaker:   weir.BreakerConfig{Window: 2 * time.Second, MinRequests: 20, OpenFor: time.Second, MaxOpenFor: 2 * time.Second},
		Freshness: weir.FreshnessConfig{NoEarlyRefresh: true},
	})
	var warm counters
	for i := range keys {
		serveOnce(e, org, pathN("brown", i), "", nil, &warm)
	}
	s := startSampler(e, org)
	var pre, during, post counters
	run := func(c *counters, d time.Duration) {
		stop := make(chan struct{})
		time.AfterFunc(d, func() { close(stop) })
		closedLoopPaced(32, 2000, stop, func(r *rand.Rand, h *hist) {
			serveOnce(e, org, pathN("brown", r.IntN(keys)), "", h, c)
		})
	}
	run(&pre, dur(6*time.Second, 3*time.Second))
	slow.Store(true)
	run(&during, brown)
	slow.Store(false)
	run(&post, dur(10*time.Second, 6*time.Second))
	s.close()
	final := e.Stats().BreakerState
	settleThenClose(t, e, base)

	ok := func(c *counters) string {
		return fmt.Sprintf("%d / %d", c.statuses[2].Load(), c.total.Load())
	}
	table(t, "origin brownout (x20 latency)",
		row("2xx / total before", ok(&pre)), row("2xx / total during", ok(&during)), row("2xx / total after", ok(&post)),
		row("stale served during", during.stale.Load()), row("client errors during", during.errs.Load()),
		row("breaker open events", ev.count("breaker-state", "open")),
		row("breaker state at end", map[weir.BreakerState]string{weir.BreakerClosed: "closed", weir.BreakerHalfOpen: "half-open", weir.BreakerOpen: "open"}[final]), row("peak goroutines", s.peakGoroutines()), row("origin shed events", ev.count("shed", "")))
	if ev.count("breaker-state", "open") == 0 {
		t.Error("breaker never opened during the brownout")
	}
	if during.stale.Load() == 0 {
		t.Error("no stale response served during the brownout despite stale-if-error")
	}
	// Every key was stored before the brownout, so stale-if-error covers all of them.
	if bad := during.total.Load() - during.statuses[2].Load(); bad > during.total.Load()/100 {
		t.Errorf("%d of %d requests during the brownout were not 2xx, want <= 1%%", bad, during.total.Load())
	}
	if final != weir.BreakerClosed {
		t.Errorf("breaker state after recovery %v, want closed", final)
	}
}

// flapStore is a remote-flagged store failing a fraction of calls: half of
// the failures return at once, half hang until the caller's deadline, as a
// network partition does (05 S-2).
type flapStore struct {
	store.Store
	failing atomic.Bool
	calls   atomic.Int64
}

func (s *flapStore) Info() store.Info { return store.Info{Name: "flap", Remote: true} }

func (s *flapStore) trip(ctx context.Context) error {
	s.calls.Add(1)
	if !s.failing.Load() || rand.IntN(2) == 0 {
		return nil
	}
	if rand.IntN(2) == 0 {
		return store.ErrUnavailable
	}
	<-ctx.Done()
	return store.ErrUnavailable
}

func (s *flapStore) Get(ctx context.Context, k store.Key) (*store.Entry, error) {
	if err := s.trip(ctx); err != nil {
		return nil, err
	}
	return s.Store.Get(ctx, k)
}

func (s *flapStore) Set(ctx context.Context, k store.Key, e *store.Entry) error {
	if err := s.trip(ctx); err != nil {
		return err
	}
	return s.Store.Set(ctx, k, e)
}

func (s *flapStore) NewestEpoch(ctx context.Context, tags []store.Tag, since time.Time) (store.Epoch, bool, error) {
	if err := s.trip(ctx); err != nil {
		return store.Epoch{}, false, err
	}
	return s.Store.NewestEpoch(ctx, tags, since)
}

// TestStoreFlap: a remote-flagged store fails half its calls for 30 s.
// FR-STF-1, FR-STF-2.
func TestStoreFlap(t *testing.T) {
	const keys = 500
	const storeTimeout = 20 * time.Millisecond
	flapFor := dur(30*time.Second, 6*time.Second)
	base := baseline()
	mem, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	fs := &flapStore{Store: mem}
	org := newCapOrigin(32, func(string) time.Duration { return svcTime(5 * time.Millisecond) }, cacheControl("max-age=30"))
	var ev eventLog
	e := newEngine(t, weir.Config{Store: fs, Observer: &ev, Timeouts: weir.TimeoutsConfig{Store: storeTimeout}})

	var c counters
	var slowest atomic.Int64
	loop := func(d time.Duration) {
		stop := make(chan struct{})
		time.AfterFunc(d, func() { close(stop) })
		h := closedLoop(32, stop, func(r *rand.Rand, h *hist) {
			serveOnce(e, org, pathN("flap", r.IntN(keys)), "", h, &c)
		})
		for { // keep the largest across phases
			old := slowest.Load()
			if int64(h.max) <= old || slowest.CompareAndSwap(old, int64(h.max)) {
				break
			}
		}
	}
	loop(dur(5*time.Second, 2*time.Second))
	fs.failing.Store(true)
	s := startSampler(e, org)
	loop(flapFor)
	fs.failing.Store(false)
	loop(dur(8*time.Second, 6*time.Second))
	s.close()
	settleThenClose(t, e, base)
	_ = mem.Close()

	// A request touches the store a few times (lookup, epoch, set), each
	// bounded by Timeouts.Store, plus one origin fetch; the origin's slowest
	// service time is observed, with 100 ms for scheduling.
	bound := 4*storeTimeout + time.Duration(org.maxSvc.Load()) + 100*time.Millisecond
	open, closed := ev.count("store-breaker", "open"), ev.count("store-breaker", "closed")
	table(t, "store flap (50% failing)", row("requests", c.total.Load()), row("client errors", c.errs.Load()),
		row("slowest request", time.Duration(slowest.Load())), row("bound", bound),
		row("store breaker open / closed", fmt.Sprint(open, " / ", closed)), row("store errors", ev.count("store-error", "get")+ev.count("store-error", "set")+ev.count("store-error", "epoch")+ev.count("store-error", "set-epoch")))
	if got := time.Duration(slowest.Load()); got > bound {
		t.Errorf("slowest request %v, want <= %v (a few Timeouts.Store plus origin time)", got, bound)
	}
	if open == 0 || closed == 0 {
		t.Errorf("store breaker did not cycle: %d open, %d closed events", open, closed)
	}
	if c.errs.Load() != 0 {
		t.Errorf("%d requests failed; a failing store must degrade to a miss, not an error (FR-STF-3)", c.errs.Load())
	}
}

// settleThenClose checks the goroutine count while the engine is still open
// (a leak Close would hide), then closes it and checks again.
func settleThenClose(t *testing.T, e *weir.Engine, base int) {
	t.Helper()
	settle(t, base)
	closeEngine(t, e)
	settle(t, base)
}
