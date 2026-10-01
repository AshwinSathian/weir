package weir

import (
	"context"
	"iter"
	"sync"
	"time"

	"github.com/AshwinSathian/weir/internal/coalesce"
	"github.com/AshwinSathian/weir/internal/httpcc"
	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/internal/limiter"
)

// Warm fetches each request through the normal path at background priority
// and stores what is storable (FR-WRM-1, T6.4). Warm.Concurrency workers
// fetch at once; each waits for a limiter slot outside the foreground
// reserve until ctx ends. A request with a fresh entry, or whose key
// another request's flight fetches and stores, counts as skipped (FR-WRM-2). Requests Weir
// would never store (unsafe methods, Range, only-if-cached) are not sent
// and count as not stored. Warm stops at the first ctx cancellation and
// returns ctx's error, or ErrClosed when Close ends it.
func (e *Engine) Warm(ctx context.Context, reqs iter.Seq[*Request], origin Origin) (WarmStats, error) {
	if e.closed.Load() {
		return WarmStats{}, ErrClosed
	}
	// Workers are engine goroutines (04 §12): Close cancels them through
	// bgCtx after its grace period, and a caller leaving cancels them too.
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan *Request)
	var (
		mu    sync.Mutex
		total WarmStats
		wg    sync.WaitGroup
	)
	for range e.cfg.Warm.Concurrency {
		wg.Add(1)
		started := e.goBackground(func(bg context.Context) {
			defer wg.Done()
			defer context.AfterFunc(bg, cancel)()
			var st WarmStats
			defer func() {
				mu.Lock()
				total.Fetched += st.Fetched
				total.Skipped += st.Skipped
				total.NotStored += st.NotStored
				total.Failed += st.Failed
				mu.Unlock()
			}()
			// Close must not wait on a caller's iterator that never
			// yields again (FR-LCY-2), so wctx ends the loop too.
			for {
				select {
				case req, ok := <-work:
					if !ok {
						return
					}
					e.warmOne(wctx, req, origin, &st)
				case <-wctx.Done():
					return
				}
			}
		})
		if !started {
			wg.Done()
			cancel()
		}
	}
feed:
	for req := range reqs {
		select {
		case work <- req:
		case <-wctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	switch {
	case ctx.Err() != nil:
		return total, ctx.Err()
	case wctx.Err() != nil:
		return total, ErrClosed
	}
	return total, nil
}

// warmOne runs one request's lookup and, unless its entry is fresh, a
// stored fetch of class Warm, and counts the outcome in st.
func (e *Engine) warmOne(ctx context.Context, req *Request, origin Origin, st *WarmStats) {
	if ctx.Err() != nil {
		return // the caller left; a request not tried is not counted
	}
	if req == nil {
		st.Failed++
		return
	}
	c, err := keys.Classify((*keys.Request)(req), &e.kcfg)
	if err != nil {
		_ = e.rejected(err) // emits EvKeyRejected
		st.Failed++
		return
	}
	if c.Class == keys.ClassPass || c.Range || c.ReqCC.OnlyIfCached {
		st.NotStored++
		return
	}
	now := time.Now()
	lk := e.lookup(ctx, &c, now)
	sp := &fetchSpec{c: &c, lk: lk, found: lk.entry, class: limiter.Warm}
	if lk.entry != nil {
		switch state, _, _ := httpcc.Evaluate(lk.entry, lk.epoch, lk.epochOK, now); state {
		case httpcc.Fresh:
			st.Skipped++
			return
		case httpcc.Unusable: // FR-PRG-3: exactly a miss
			sp.purged, sp.lk.entry, sp.lk.fwd = lk.entry, nil, FwdURIMiss
		default:
			if hasValidators(lk.entry) {
				sp.prior = lk.entry
			}
		}
	}
	// ponytail: the lookup above may be stale after a long wait for a slot,
	// so a foreground fetch meanwhile costs one more origin call; look up
	// again after Acquire if warm calls overlap live traffic heavily.
	// Acquire before joining: a warm fetch queued for a slot must not hold
	// a flight that foreground requests join and then wait on (FR-LIM-4).
	permit, err := e.limFor(&c).Acquire(ctx, limiter.Warm, c.PartitionH)
	if err != nil {
		if ctx.Err() == nil {
			e.shed(&c, limiter.Warm, err)
			st.Failed++
		}
		return
	}
	defer permit.Release() // idempotent; fetch releases it first
	sp.permit = permit
	f := coalesce.NewFlight() // the requests cacheable never lets lead a flight
	if !c.Authorized && !c.ReqCC.NoStore && !lk.marker {
		var created bool
		if f, created = e.flights.Join(c.Primary, time.Now(), e.cfg.Coalesce.LeaderMaxAge); !created {
			permit.Release()
			e.warmFollow(ctx, f, st)
			return
		}
	}
	f.CreatorGone() // no one claims an over-size stream; runFlight closes it
	// runFlight on its own goroutine: it turns an origin panic or Goexit
	// into a published error, which this worker cannot survive (NFR-2).
	// ctx is both its cancellation and its values (FR-COA-9).
	if !e.goBackground(func(context.Context) { e.runFlight(ctx, ctx, f, sp, origin) }) {
		f.Publish(&flightResult{fetchResult: fetchResult{err: ErrClosed}})
	}
	<-f.Done() // ctx ending cancels the fetch, so this returns promptly
	fr := f.Result().(*flightResult)
	switch {
	case ctx.Err() != nil: // the caller left mid-fetch
	case fr.err != nil:
		st.Failed++
	case fr.ci.Stored:
		st.Fetched++
	default:
		st.NotStored++
	}
}

// warmFollow waits for a flight another request leads and counts its
// outcome: a stored response is Skipped, since Warm sent nothing.
func (e *Engine) warmFollow(ctx context.Context, f *coalesce.Flight, st *WarmStats) {
	select {
	case <-f.Done():
	case <-ctx.Done():
		return
	}
	switch fr := f.Result().(*flightResult); {
	case fr.err != nil:
		st.Failed++
	case fr.ci.Stored:
		st.Skipped++
	default:
		st.NotStored++
	}
}
