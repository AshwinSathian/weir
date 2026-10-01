package weir

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AshwinSathian/weir/internal/coalesce"
	"github.com/AshwinSathian/weir/internal/httpcc"
	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/store"
)

var errOriginGoexit = errors.New("weir: origin called runtime.Goexit")

// fetchSpec is what a fetch-and-store needs from the request that starts it.
type fetchSpec struct {
	c      *keys.Classified
	lk     lookupResult
	prior  *store.Entry // the stale entry to validate (FR-SRV-3)
	found  *store.Entry // the response the lookup returned; it may always be replaced
	purged *store.Entry // an unusable response a marker may replace
}

// flightResult is what a fetch-and-store produced, and what a flight
// publishes (04 §6.4).
type flightResult struct {
	fetchResult              // the creator's response; only the creator touches resp
	ci          CacheInfo    // for the creator's response
	entry       *store.Entry // the storable entry followers share; nil when not shareable
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
	if fr.err != nil { // FR-COA-6: every waiter gets the flight's error
		return nil, fr.err
	}
	if created && (!fr.live() || f.ClaimStream()) {
		return e.respond(c, fr), nil
	}
	if fr.entry != nil {
		return e.fromEntry(c, fr.entry, time.Now(),
			CacheInfo{Fwd: sp.lk.fwd, FwdStatus: fr.ci.FwdStatus, Stored: fr.ci.Stored, Collapsed: true}), nil
	}
	// ponytail: M2-03 adds the re-entry rule (FR-COA-5); until then a
	// follower of an unshareable result fetches on its own. Safe without
	// keys.VaryMatches only while storability refuses Vary; M7-01 must add
	// the check above or a follower gets another variant.
	return e.fetchDirect(ctx, sp, origin)
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
	if fr.err != nil && ctx.Err() != nil { // only Close cancels a flight
		fr.err = ErrClosed
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
	_, ok, err := e.store.NewestEpoch(ctx, ent.Tags, ent.RequestTime)
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
	if _, staleness, sieOK := httpcc.Evaluate(ent, sp.lk.epoch, sp.lk.epochOK, now); sieOK {
		return e.fromEntry(sp.c, ent, now, CacheInfo{Hit: true, Stale: StaleCoalesceTimeout, TTL: -staleness})
	}
	return nil
}

// fetchDirect fetches and stores on the request goroutine, without a flight,
// so it never blocks anyone else (04 §6.5).
func (e *Engine) fetchDirect(ctx context.Context, sp *fetchSpec, origin Origin) (*Response, error) {
	fr := e.fetchStored(ctx, sp, origin)
	if fr.err != nil {
		return nil, fr.err
	}
	return e.respond(sp.c, fr), nil
}
