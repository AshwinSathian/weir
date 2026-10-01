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
// another request's flight fetches and stores, counts as skipped
// (FR-WRM-2). Requests Weir would never store (unsafe methods, Range,
// only-if-cached) are not sent and count as not stored. Warm stops at the
// first ctx cancellation and returns ctx's error. Once Close starts, it
// takes no new request, lets running fetches finish within Close's grace,
// and returns ErrClosed.
func (e *Engine) Warm(ctx context.Context, reqs iter.Seq[*Request], origin Origin) (WarmStats, error) {
	if e.closed.Load() {
		return WarmStats{}, ErrClosed
	}
	// Workers are engine goroutines (04 §12): Close cancels them through
	// bgCtx after its grace period, and a caller leaving cancels them too.
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan *Request)
	// closing is closed by the first worker that sees Close start. Running
	// fetches finish within Close's grace; no new request is taken.
	closing := make(chan struct{})
	var (
		mu       sync.Mutex
		total    WarmStats
		wg       sync.WaitGroup
		stopOnce sync.Once
	)
	stop := func() { stopOnce.Do(func() { close(closing) }) }
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
					if e.closed.Load() {
						stop()
						return
					}
					e.warmOne(wctx, req, origin, &st)
					if e.closed.Load() { // warmOne may have stopped on it
						stop()
						return
					}
				case <-wctx.Done():
					return
				}
			}
		})
		if !started {
			wg.Done()
			stop()
		}
	}
feed:
	for req := range reqs {
		select {
		case work <- req:
		case <-wctx.Done():
			break feed
		case <-closing:
			break feed
		}
	}
	close(work)
	wg.Wait()
	switch {
	case ctx.Err() != nil:
		return total, ctx.Err()
	case wctx.Err() != nil, isClosed(closing):
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
	if e.warmSpec(ctx, &c) == nil {
		st.Skipped++
		return
	}
	// Two attempts: a flight joined below may be dropped by its own caller
	// leaving (bgDropped), or store another variant (FR-KEY-7), and then
	// this request is still cold.
	for range 2 {
		// Acquire before joining: a warm fetch queued for a slot must not
		// hold a flight that foreground requests join and then wait on
		// (FR-LIM-4).
		permit, err := e.limFor(&c).Acquire(ctx, limiter.Warm, c.PartitionH)
		if err != nil {
			if ctx.Err() == nil {
				e.shed(&c, limiter.Warm, err)
				st.Failed++
			}
			return
		}
		// Look again: live traffic may have stored the key while this
		// request waited (T6.4 cold starts overlap it).
		sp := e.warmSpec(ctx, &c)
		if sp == nil {
			permit.Release()
			st.Skipped++
			return
		}
		sp.permit = permit
		f := coalesce.NewFlight() // the requests cacheable never lets lead a flight
		if !c.Authorized && !c.ReqCC.NoStore && !sp.lk.marker {
			var created bool
			if f, created = e.flights.Join(sp.lk.ck, time.Now(), e.cfg.Coalesce.LeaderMaxAge); !created {
				permit.Release()
				if e.warmFollow(ctx, f, &c, st) {
					continue
				}
				return
			}
		}
		e.warmLead(ctx, f, sp, origin, st)
		return
	}
	st.Failed++ // two joined flights in a row left this request cold
}

// warmSpec looks c up and returns the fetch to run, or nil when the entry is
// fresh. no-cache and max-age=0 do not force validation here.
func (e *Engine) warmSpec(ctx context.Context, c *keys.Classified) *fetchSpec {
	now := time.Now()
	lk := e.lookup(ctx, c, now)
	sp := &fetchSpec{c: c, lk: lk, found: lk.entry, class: limiter.Warm}
	if lk.entry != nil {
		switch state, _, _ := httpcc.Evaluate(lk.entry, lk.epoch, lk.epochOK, now); state {
		case httpcc.Fresh:
			return nil
		case httpcc.Unusable: // FR-PRG-3: exactly a miss
			sp.purged, sp.lk.entry, sp.lk.fwd = lk.entry, nil, FwdURIMiss
		default:
			if hasValidators(lk.entry) {
				sp.prior = lk.entry
			}
		}
	}
	return sp
}

// warmLead runs f's fetch and counts its outcome. runFlight runs on its own
// goroutine: it turns an origin panic or Goexit into a published error,
// which this worker cannot survive (NFR-2). ctx is both its cancellation
// and its values (FR-COA-9).
func (e *Engine) warmLead(ctx context.Context, f *coalesce.Flight, sp *fetchSpec, origin Origin, st *WarmStats) {
	f.CreatorGone() // no one claims an over-size stream; runFlight closes it
	if !e.goBackground(func(context.Context) { e.runFlight(ctx, ctx, f, sp, origin) }) {
		sp.permit.Release()
		f.Publish(&flightResult{fetchResult: fetchResult{err: ErrClosed}})
		return // Close started after the worker checked; Warm reports ErrClosed
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
// outcome: a stored response is Skipped, since Warm sent nothing. It
// reports true, counting nothing, when the flight was dropped or stored
// another variant (FR-KEY-7), and the request should be tried again.
func (e *Engine) warmFollow(ctx context.Context, f *coalesce.Flight, c *keys.Classified, st *WarmStats) (retry bool) {
	select {
	case <-f.Done():
	case <-ctx.Done():
		return false
	}
	switch fr := f.Result().(*flightResult); {
	case fr.bgDropped, fr.entry != nil && keys.VariantKey(c.Primary, fr.entry.VaryNames, c.Forwarded.Header) != fr.vk:
		return ctx.Err() == nil
	case fr.err != nil:
		st.Failed++
	case fr.ci.Stored:
		st.Skipped++
	default:
		st.NotStored++
	}
	return false
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
