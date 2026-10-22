package weir

import (
	"context"
	"math"
	"time"

	"github.com/AshwinSathian/weir/internal/httpcc"
	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/internal/limiter"
	"github.com/AshwinSathian/weir/store"
)

// maybeEarlyRefresh starts a background refresh of a fresh entry with
// remaining lifetime left, with the XFetch probability (FR-FRS-6, 04 §6.8).
func (e *Engine) maybeEarlyRefresh(ctx context.Context, c *keys.Classified, lk lookupResult, remaining time.Duration, origin Origin) {
	f := &e.cfg.Freshness
	if f.NoEarlyRefresh || lk.entry.Lifetime < f.JitterMinLifetime {
		return
	}
	// The same requests that never lead a flight (cacheable) never start a
	// refresh: their credentials or no-store must not decide a shared entry.
	if c.Authorized || c.ReqCC.NoStore {
		return
	}
	// Hot path: for any Rand in [0, 1), u >= 2^-53, so -ln(u) < 37 and an
	// entry with more than 37·Δ·β left can never trigger. Skipping the draw
	// keeps Rand and math.Log off most hits.
	if 37*float64(clampDelta(lk.entry.FetchDuration))*f.EarlyRefreshBeta < float64(remaining) {
		return
	}
	if xfetch(remaining, lk.entry.FetchDuration, f.EarlyRefreshBeta, 1-e.cfg.Rand()) {
		e.backgroundRefresh(ctx, c, lk, origin)
	}
}

// xfetch is the FR-FRS-6 trigger: -Δ·β·ln(u) >= remaining, with Δ clamped
// to [1 ms, 10 s] and u in (0, 1].
func xfetch(remaining, delta time.Duration, beta, u float64) bool {
	return -float64(clampDelta(delta))*beta*math.Log(u) >= float64(remaining)
}

func clampDelta(d time.Duration) time.Duration { return min(max(d, time.Millisecond), 10*time.Second) }

// backgroundRefresh fetches c's key on a flight no request waits on, unless
// one is already fetching it (04 §6.8). ctx lends its values only (FR-COA-9).
// The flight takes its Background slot inside fetch, after Join, so a hit
// that finds a flight running holds nothing; with no slot outside the
// reserve the refresh is dropped (FR-LIM-4).
func (e *Engine) backgroundRefresh(ctx context.Context, c *keys.Classified, lk lookupResult, origin Origin) {
	e.background(ctx, c, lk, origin, false)
}

// background is backgroundRefresh; rangeFill marks the fill of a Range miss.
func (e *Engine) background(ctx context.Context, c *keys.Classified, lk lookupResult, origin Origin, rangeFill bool) {
	f, created := e.flights.Join(lk.ck, time.Now(), e.cfg.Coalesce.LeaderMaxAge)
	if !created {
		leaveAsFollower(f) // Join counted it; it never takes the result (FR-COA-10)
		return
	}
	f.CreatorGone() // no requester claims an over-size stream; runFlight closes it
	var prior *store.Entry
	if lk.entry != nil && hasValidators(lk.entry) { // nil: a Range miss fills a key with no entry (FR-RNG-4)
		prior = lk.entry
	}
	// The fetch outlives this request, so it gets its own copy (shared).
	sp := &fetchSpec{c: shared(c), lk: lk, prior: prior, found: lk.entry, class: limiter.Background, rangeFill: rangeFill}
	if !e.goBackground(func(bg context.Context) { e.runFlight(bg, ctx, f, sp, origin) }) {
		emit(e.cfg.Observer, Event{Kind: EvRefreshDropped, Time: time.Now(), Partition: c.Partition, Reason: "closed"})
		f.Publish(&flightResult{fetchResult: fetchResult{err: ErrClosed}})
	}
}

// owedRefreshWait bounds how long a refresh that stale serving owes waits
// for a slot. A refresh still waiting when its entry's stale window ends is
// harmless (the next request fetches in the foreground and the refresh,
// once granted, finds a fresh entry), so the bound only limits how long a
// parked goroutine lives.
const owedRefreshWait = time.Minute

// owedRefresh is the refresh a stale-while-revalidate hit starts (FR-STL-1).
// Unlike an early refresh it is owed: the entry is already stale, and
// dropping the refresh leaves it stale until some later request happens to
// find a slot. So it waits for one, outside the foreground reserve and
// behind every waiting client (limiter.Refresh, FR-LIM-4).
//
// The wait holds no flight, as a warm fetch's does not (FR-WRM-1): a
// request that needs the origin for the same key must not join a fetch that
// has not started. One refresh waits per key (e.owed), and at most
// Limiter.MaxQueue wait in all, a quarter of that for one partition; past
// that the refresh is dropped and counted as before.
func (e *Engine) owedRefresh(ctx context.Context, c *keys.Classified, lk lookupResult, origin Origin) {
	ck := lk.ck
	// ponytail: one lock for every stale hit. The flight table it stands in
	// for has 64 shards; shard this map the same way if stale hits ever
	// show in a mutex profile.
	e.owedMu.Lock()
	if _, waiting := e.owed[ck]; waiting {
		e.owedMu.Unlock()
		return
	}
	if e.owed == nil {
		e.owed = make(map[store.Key]struct{})
	}
	e.owed[ck] = struct{}{}
	e.owedMu.Unlock()
	done := func() {
		e.owedMu.Lock()
		delete(e.owed, ck)
		e.owedMu.Unlock()
	}
	sc := shared(c) // the wait and the fetch outlive this request
	if !e.goBackground(func(bg context.Context) {
		defer done()
		// The wait ends after owedRefreshWait, or when Close begins
		// (waitCtx), which is before bg ends.
		wctx, cancel := context.WithTimeout(bg, owedRefreshWait)
		stop := context.AfterFunc(e.waitCtx, cancel) //nolint:contextcheck // a second, earlier end for the same wait, not a new lineage
		permit, err := e.limFor(sc).Acquire(wctx, limiter.Refresh, sc.PartitionH, sc.HostH)
		stop()
		cancel()
		if err == nil && e.closed.Load() { // granted as Close began: start nothing new
			permit.Release()
			err = ErrClosed
		}
		if err != nil {
			reason := "no-slot"
			if e.closed.Load() {
				reason = "closed"
			}
			emit(e.cfg.Observer, Event{Kind: EvRefreshDropped, Time: time.Now(), Partition: sc.Partition, Reason: reason})
			return
		}
		// Look again: a request may have refreshed the entry, or a purge
		// removed it, while this one waited.
		now := time.Now()
		cur := e.lookup(bg, sc, now)
		if cur.entry == nil {
			permit.Release()
			return
		}
		// Only an entry still inside its stale-while-revalidate window is
		// owed a refresh. One that is fresh has had it; one past the window
		// or unusable is the next request's to fetch.
		if st, _, _ := httpcc.Evaluate(cur.entry, cur.epoch, cur.epochOK, now); st != httpcc.StaleSWR {
			permit.Release()
			return
		}
		f, created := e.flights.Join(cur.ck, now, e.cfg.Coalesce.LeaderMaxAge)
		if !created { // someone is fetching it now
			leaveAsFollower(f) // Join counted it; it never takes the result (FR-COA-10)
			permit.Release()
			return
		}
		f.CreatorGone()
		var prior *store.Entry
		if hasValidators(cur.entry) {
			prior = cur.entry
		}
		sp := &fetchSpec{c: sc, lk: cur, prior: prior, found: cur.entry, class: limiter.Background, permit: permit}
		e.runFlight(bg, ctx, f, sp, origin)
	}) {
		done()
		emit(e.cfg.Observer, Event{Kind: EvRefreshDropped, Time: time.Now(), Partition: c.Partition, Reason: "closed"})
	}
}
