package weir

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"time"

	"github.com/AshwinSathian/weir/internal/coalesce"
	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/internal/limiter"
	"github.com/AshwinSathian/weir/store"
)

var errOriginGoexit = errors.New("weir: origin called runtime.Goexit")

// fetchSpec is what a fetch-and-store needs from the request that starts it.
type fetchSpec struct {
	c      *keys.Classified
	lk     lookupResult
	prior  *store.Entry    // the stale entry to validate (FR-SRV-3)
	found  *store.Entry    // the response the lookup returned; it may always be replaced
	purged *store.Entry    // an unusable response a marker may replace
	class  limiter.Class   // Background never queues, Warm waits on ctx only; neither takes the reserve (FR-LIM-4, FR-WRM-1)
	permit *limiter.Permit // a slot already held (Warm); fetch releases it
}

// flightResult is what a fetch-and-store produced, and what a flight
// publishes (04 §6.4).
type flightResult struct {
	fetchResult              // the creator's response; only the creator touches resp
	ci          CacheInfo    // for the creator's response
	entry       *store.Entry // the storable entry followers share; nil when not shareable
	errHeader   http.Header  // a buffered 5xx's header, which followers copy (04 §6.6)
}

// live reports whether resp.Body still streams from the origin, so exactly
// one party must close it.
func (r *flightResult) live() bool { return r.err == nil && (r.over || r.stream) }

// fetchCoalesced runs the fetch for sp on a flight shared by every request
// with the same coalescing key (FR-COA-1). The caller waits at most
// FollowerMaxWait, bounded by its own ctx (FR-COA-4).
func (e *Engine) fetchCoalesced(ctx context.Context, sp *fetchSpec, origin Origin) (*Response, error) {
	c := sp.c
	f, created := e.flights.Join(c.Primary, time.Now(), e.cfg.Coalesce.LeaderMaxAge)
	if created {
		if !e.goBackground(func(bg context.Context) { e.runFlight(bg, ctx, f, sp, origin) }) {
			f.Publish(&flightResult{fetchResult: fetchResult{err: ErrClosed}})
		}
	} else {
		emit(e.cfg.Observer, Event{Kind: EvCoalesceJoin, Time: time.Now(), Partition: c.Partition})
	}

	timer := time.NewTimer(e.cfg.Coalesce.FollowerMaxWait)
	defer timer.Stop()
	tc := timer.C
	for !published(f) { // a result published as the timer fired is still used
		select {
		case <-f.Done():
		case <-tc:
			if published(f) {
				continue
			}
			emit(e.cfg.Observer, Event{Kind: EvCoalesceTimeout, Time: time.Now(), Partition: c.Partition})
			if r := e.staleOnTimeout(sp); r != nil {
				if created {
					e.leaveFlight(f)
				}
				return r, nil
			}
			if !created {
				return e.fetchDirect(ctx, sp, origin)
			}
			// The creator's fetch is the flight, already bounded by the
			// origin timeout; a second fetch would only race it.
			tc = nil
		case <-ctx.Done():
			if created {
				e.leaveFlight(f)
			}
			return nil, ctx.Err()
		}
	}

	fr := f.Result().(*flightResult)
	// 04 §6.8: a rule for background work never sheds a request. Each
	// follower fetches directly, uncoalesced; the limiter bounds them.
	if fr.bgDropped {
		ck := c.Primary
		return e.cacheable(ctx, c, origin, &ck)
	}
	owner := created && (!fr.live() || f.ClaimStream())
	// FR-COA-6: every waiter gets the flight's error or buffered 5xx,
	// through its own lookup's stale entry (01 §7.2). Followers of an
	// over-size 5xx stream re-enter below and fetch once more.
	if (owner || !fr.live()) && failed(ctx, fr) {
		return e.onFetchError(sp, fr, owner)
	}
	if fr.err != nil {
		return nil, fr.err
	}
	if owner {
		return e.respond(c, fr), nil
	}
	if fr.entry != nil {
		return e.fromEntry(c, fr.entry, time.Now(),
			CacheInfo{Fwd: sp.lk.fwd, FwdStatus: fr.ci.FwdStatus, Stored: fr.ci.Stored, Collapsed: true}), nil
	}
	// FR-COA-5: not storable, over-size, an event stream, or purged during
	// the flight. Re-enter lookup once: the flight may have left a marker
	// or, under Vary, moved this request to another key. Until M7-01 the
	// key never changes, so this equals fetchDirect and no test can tell
	// them apart (TestVaryFollowersRecoalesce, M7-01).
	// ponytail: M7-01 must compare the lookup's coalescing key, not
	// c.Primary, cap re-entry at one attempt (a second unshareable flight
	// under a new key would re-enter again), and add keys.VaryMatches above
	// or a follower gets another variant (storability refuses Vary today).
	ck := c.Primary
	return e.cacheable(ctx, c, origin, &ck)
}

// published reports whether f's result is ready without waiting.
func published(f *coalesce.Flight) bool {
	select {
	case <-f.Done():
		return true
	default:
		return false
	}
}

// runFlight is a flight's goroutine. The fetch context carries the values
// of the creator's request context but none of its cancellation; only Close
// cancels it, through bg (FR-COA-2, FR-COA-9).
func (e *Engine) runFlight(bg, reqCtx context.Context, f *coalesce.Flight, sp *fetchSpec, origin Origin) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(reqCtx))
	stop := context.AfterFunc(bg, cancel)
	release := func() { stop(); cancel() }
	var fr *flightResult
	defer func() {
		// NFR-2, FR-COA-6: a panic reading the origin body happens outside
		// safeFetch's recover; on this goroutine it would end the process.
		// runtime.Goexit in the origin leaves fr nil without a panic; the
		// waiters must still get a result.
		if r := recover(); r != nil {
			fr = &flightResult{fetchResult: fetchResult{err: &OriginError{Err: fmt.Errorf("weir: origin panic: %v", r)}}}
		} else if fr == nil {
			fr = &flightResult{fetchResult: fetchResult{err: &OriginError{Err: errOriginGoexit}}}
		}
		if !fr.live() {
			release()
		}
		f.Publish(fr)
		if fr.live() && f.CreatorIsGone() && f.AbandonStream() {
			closeBody(fr.resp)
		}
	}()
	fr = e.fetchStored(ctx, sp, origin)
	if fr.err != nil && ctx.Err() != nil { // only Close, or a Warm caller leaving, cancels a flight
		fr.err = ErrClosed
		// Followers of a warm flight fetch for themselves (04 §6.8).
		fr.bgDropped = sp.class == limiter.Warm
	}
	if fr.entry != nil && e.purgedSince(ctx, fr.entry) {
		// FR-PRG-7, T-10: a follower may have arrived after the purge. Every
		// follower arrived before Publish, so checking once here covers
		// them all, up to this one store round trip.
		fr.entry = nil
	}
	if fr.live() { // the body streams past this goroutine; Close still cancels it
		fr.resp.Body = &cancelOnClose{ReadCloser: fr.resp.Body, cancel: release}
	}
}

// purgedSince reports whether an epoch newer than ent's request time
// applies to it. A lookup error fails open, as in lookup (T-9).
func (e *Engine) purgedSince(ctx context.Context, ent *store.Entry) bool {
	_, ok, err := e.sg.newestEpoch(ctx, ent.Tags, ent.RequestTime)
	return ok && err == nil
}

// leaveFlight marks the creator gone. When the flight published first,
// runFlight may have checked too early, so the creator closes an unclaimed
// stream itself (04 §6.4).
func (e *Engine) leaveFlight(f *coalesce.Flight) {
	if f.CreatorGone() {
		if fr := f.Result().(*flightResult); fr.live() && f.AbandonStream() {
			closeBody(fr.resp)
		}
	}
}

// staleOnTimeout serves the stale entry when stale-if-error permits it at
// the current time (FR-COA-4), else returns nil.
func (e *Engine) staleOnTimeout(sp *fetchSpec) *Response {
	ent := sp.lk.entry
	if ent == nil {
		return nil
	}
	now := time.Now()
	if staleness, ok := e.staleOK(sp, now); ok {
		emit(e.cfg.Observer, Event{Kind: EvStaleServed, Time: now, Partition: sp.c.Partition, Reason: "coalesce-timeout"})
		return e.fromEntry(sp.c, ent, now, CacheInfo{Hit: true, Stale: StaleCoalesceTimeout, TTL: -staleness})
	}
	return nil
}

// failed reports whether fr is an error condition for stale serving (01
// §7.2): an error or a 5xx response. A caller that left gets nothing. It
// reads ci, not resp, which the creator's caller may be writing to.
func failed(ctx context.Context, fr *flightResult) bool {
	return ctx.Err() == nil && (fr.err != nil || serverError(fr.ci.FwdStatus))
}

// onFetchError answers a request whose fetch failed (04 §6.6): the stale
// entry when stale-if-error permits it, ErrMustRevalidate when the entry
// forbids serving it and the origin gave no response (FR-STL-4), else the
// error or the 5xx. own is true when the request owns fr.resp. The flight
// already wrote any negative entry (fetchStored), once for all waiters.
func (e *Engine) onFetchError(sp *fetchSpec, fr *flightResult, own bool) (*Response, error) {
	if ent := sp.lk.entry; ent != nil {
		now := time.Now()
		if staleness, ok := e.staleOK(sp, now); ok {
			if own {
				closeBody(fr.resp)
			}
			reason, ev := staleReason(fr.err)
			emit(e.cfg.Observer, Event{Kind: EvStaleServed, Time: now, Partition: sp.c.Partition, Reason: ev})
			return e.fromEntry(sp.c, ent, now, CacheInfo{Hit: true, Stale: reason, TTL: -staleness}), nil
		}
		if fr.err != nil && ent.Flags&(store.FlagMustRevalidate|store.FlagProxyRevalidate) != 0 {
			return nil, ErrMustRevalidate
		}
	}
	switch {
	case fr.err != nil:
		return nil, fr.err
	case own:
		return e.respond(sp.c, fr), nil
	}
	r := &Response{StatusCode: fr.ci.FwdStatus, Header: maps.Clone(fr.errHeader), Body: http.NoBody}
	if !sp.c.Head && len(fr.body) > 0 {
		r.Body = io.NopCloser(bytes.NewReader(fr.body))
	}
	return e.finish(r, CacheInfo{Fwd: sp.lk.fwd, FwdStatus: r.StatusCode, Collapsed: true}), nil
}

// staleReason maps a fetch error to the reason a stale entry is served for
// it, and the EvStaleServed reason (04 §6.6, §9.2).
func staleReason(err error) (StaleReason, string) {
	if _, re := classify(err); re != nil {
		if is(re.Err, ErrCircuitOpen) {
			return StaleCircuitOpen, "circuit-open"
		}
		return StaleShed, "shed"
	}
	return StaleIfError, "sie"
}

// fetchDirect fetches and stores on the request goroutine, without a flight,
// so it never blocks anyone else (04 §6.5).
func (e *Engine) fetchDirect(ctx context.Context, sp *fetchSpec, origin Origin) (*Response, error) {
	fr := e.fetchStored(ctx, sp, origin)
	if failed(ctx, fr) {
		return e.onFetchError(sp, fr, true)
	}
	if fr.err != nil {
		return nil, fr.err
	}
	return e.respond(sp.c, fr), nil
}
