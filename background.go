package weir

import (
	"context"
	"math"
	"time"

	"github.com/AshwinSathian/weir/internal/keys"
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
func (e *Engine) backgroundRefresh(ctx context.Context, c *keys.Classified, lk lookupResult, origin Origin) {
	if !e.tryAcquireBackground() {
		emit(e.cfg.Observer, Event{Kind: EvRefreshDropped, Time: time.Now(), Partition: c.Partition})
		return
	}
	f, created := e.flights.Join(c.Primary, time.Now(), e.cfg.Coalesce.LeaderMaxAge)
	if !created {
		return
	}
	f.CreatorGone() // no requester claims an over-size stream; runFlight closes it
	var prior *store.Entry
	if hasValidators(lk.entry) {
		prior = lk.entry
	}
	sp := &fetchSpec{c: c, lk: lk, prior: prior, found: lk.entry}
	if !e.goBackground(func(bg context.Context) { e.runFlight(bg, ctx, f, sp, origin) }) {
		f.Publish(&flightResult{fetchResult: fetchResult{err: ErrClosed}})
	}
}

// tryAcquireBackground reports whether background work may take an origin
// slot now.
// ponytail: always true until M4-02 wires the limiter's Background class.
// Until then nothing bounds refresh fetches below one per distinct key near
// expiry (P5). M4-02 must acquire inside fetch, after Join, as 04 §6.8 says:
// acquiring here holds a slot for every hit that finds a flight running.
func (e *Engine) tryAcquireBackground() bool { return true }
