package weir

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AshwinSathian/weir/internal/coalesce"
	"github.com/AshwinSathian/weir/internal/httpcc"
	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/internal/limiter"
	"github.com/AshwinSathian/weir/store"
)

var errOriginGoexit = errors.New("origin called runtime.Goexit") // a cause inside OriginError

// sharedError marks an error returned to a request that ran no fetch of its
// own: a follower that left while it waited, or one that took its flight's
// error (FR-COA-6). Serve unwraps it and counts the request like any other
// follower (FR-MR-1); it never reaches a caller.
type sharedError struct{ error }

func (s sharedError) Unwrap() error { return s.error }

// fetchSpec is what a fetch-and-store needs from the request that starts it.
type fetchSpec struct {
	c      *keys.Classified
	lk     lookupResult
	prior  *store.Entry    // the stale entry to validate (FR-SRV-3)
	found  *store.Entry    // the response the lookup returned; it may always be replaced
	purged *store.Entry    // an unusable response a marker may replace
	class  limiter.Class   // Background never queues, Warm waits on ctx only; neither takes the reserve (FR-LIM-4, FR-WRM-1)
	permit *limiter.Permit // a slot already held (Warm); fetch releases it
	// reentered marks a follower's second pass (FR-COA-5); a flight that
	// again cannot serve it sends it to fetchDirect.
	reentered bool
	// rangeFill marks the Background fetch a Range miss starts (FR-RNG-4).
	rangeFill bool
}

// flightResult is what a fetch-and-store produced, and what a flight
// publishes (04 §6.4).
type flightResult struct {
	fetchResult              // the creator's response; only the creator touches resp
	ci          CacheInfo    // for the creator's response
	entry       *store.Entry // the storable entry followers share; nil when not shareable
	vk          store.Key    // the variant key entry answers (Primary without Vary)
	errHeader   http.Header  // a buffered 5xx's header, which followers copy (04 §6.6)
	// fromStore: no fetch ran. The flight's creator found entry stored by
	// the flight before it (storedMeanwhile), and every waiter serves it as
	// a hit.
	fromStore bool
	// shareable: an over-size response that is storable but for its size,
	// so the flight's waiters may read it together (FR-COA-10); share is
	// that stream once runFlight has made it.
	shareable bool
	shareVary []string
	shareTags []store.Tag
	share     *sharedStream
}

// storedMeanwhile looks the request's key up once more, for a request that
// has just created a flight, and returns the entry when a fetch that ended
// in between stored a fresh one under the same key. It answers no when the
// client asked for validation, when the store is remote, and when the key the
// record lives under has changed (a vary spec appeared): those go on to the
// fetch as before.
func (e *Engine) storedMeanwhile(ctx context.Context, sp *fetchSpec) (ent *store.Entry, ttl time.Duration, ok bool) {
	if e.sg.remote || sp.lk.fwd == FwdRequest {
		return nil, 0, false
	}
	now := time.Now()
	lk := e.lookup(ctx, sp.c, now)
	if lk.entry == nil || lk.ck != sp.lk.ck || sameRecord(lk.entry, sp.found) {
		return nil, 0, false
	}
	st, staleness, _ := httpcc.Evaluate(lk.entry, lk.epoch, lk.epochOK, now)
	// FR-SRV-8: the first lookup found nothing, so nothing has judged the
	// client's own no-cache, max-age or min-fresh against this entry yet.
	if st != httpcc.Fresh || forcesValidation(&sp.c.ReqCC, httpcc.CurrentAge(lk.entry, now), -staleness) {
		return nil, 0, false
	}
	return lk.entry, -staleness, true
}

// live reports whether resp.Body still streams from the origin, so exactly
// one party must close it.
func (r *flightResult) live() bool { return r.err == nil && (r.over || r.stream || r.unbuffered) }

// fetchCoalesced runs the fetch for sp on a flight shared by every request
// with the same coalescing key (FR-COA-1). The caller waits at most
// FollowerMaxWait, bounded by its own ctx (FR-COA-4).
func (e *Engine) fetchCoalesced(ctx context.Context, sp *fetchSpec, origin Origin) (*Response, error) {
	c := sp.c
	f, created := e.flights.Join(sp.lk.ck, time.Now(), e.cfg.Coalesce.LeaderMaxAge)
	// FR-MR-1: an error a follower returns without a fetch of its own.
	shared := func(err error) error {
		if created || err == nil {
			return err
		}
		return sharedError{err}
	}
	if created {
		// FR-COA-1: between this request's lookup and its Join, the flight
		// it would have joined may have stored its response and gone. One
		// more look at the store, now that this request owns the key's
		// flight, turns that second fetch into a hit for it and for
		// whoever joins meanwhile. An in-process store only: there the read
		// costs nothing next to a fetch.
		if ent, ttl, ok := e.storedMeanwhile(ctx, sp); ok {
			f.Publish(&flightResult{entry: ent, vk: sp.lk.ck, fromStore: true})
			return e.fromEntry(c, ent, time.Now(), CacheInfo{Hit: true, TTL: ttl}), nil
		}
		if !e.goBackground(func(bg context.Context) { e.runFlight(bg, ctx, f, sp, origin) }) {
			f.Publish(&flightResult{fetchResult: fetchResult{err: ErrClosed}})
		}
	} else {
		emit(e.cfg.Observer, Event{Kind: EvCoalesceJoin, Time: time.Now(), Partition: c.Partition})
	}

	timer := time.NewTimer(e.cfg.Coalesce.FollowerMaxWait)
	defer timer.Stop()
	tc := timer.C
	waited := false     // FollowerMaxWait has passed
	for !published(f) { // a result published as the timer fired is still used
		select {
		case <-f.Done():
		case <-tc:
			if published(f) {
				continue
			}
			if waited {
				// The flight outlived its own bound: its origin ignores the
				// context, so this follower stops trusting it (T6.2a).
				emit(e.cfg.Observer, Event{Kind: EvCoalesceTimeout, Time: time.Now(), Partition: c.Partition, Reason: "direct"})
				leaveAsFollower(f) // only followers get here: the creator stops its timer
				return e.fetchDirect(ctx, sp, origin)
			}
			waited = true
			if r := e.staleOnTimeout(sp); r != nil {
				emit(e.cfg.Observer, Event{Kind: EvCoalesceTimeout, Time: time.Now(), Partition: c.Partition, Reason: "stale"})
				if created {
					e.leaveFlight(f)
				} else {
					leaveAsFollower(f)
				}
				return r, nil
			}
			if created {
				// The creator's fetch is the flight, already bounded by the
				// origin timeout; a second fetch would only race it. It keeps
				// waiting, which is neither outcome of 04 §9.2: no event.
				tc = nil
				continue
			}
			// FR-COA-4: a follower keeps a flight that is still inside its
			// bound: the wait for a half-open probe, the queue wait and the
			// origin timeout. Fetching now would turn one slow fetch into
			// MaxPerPartition of them and shed the rest (T-19).
			timer.Reset(e.cfg.Timeouts.Origin + 2*e.cfg.Limiter.MaxQueueWait - e.cfg.Coalesce.FollowerMaxWait)
		case <-ctx.Done():
			if created {
				e.leaveFlight(f)
			} else {
				leaveAsFollower(f)
			}
			return nil, shared(ctx.Err())
		}
	}

	fr := f.Result().(*flightResult)
	if fr.share != nil {
		return e.serveShared(ctx, sp, origin, fr, created)
	}
	// 04 §6.8: a rule for background work never sheds a request. Each
	// follower fetches directly, uncoalesced; the limiter bounds them.
	if fr.bgDropped {
		return e.reenter(ctx, sp, origin)
	}
	owner := created && (!fr.live() || f.ClaimStream())
	// FR-COA-6: every waiter gets the flight's error or buffered 5xx,
	// through its own lookup's stale entry (01 §7.2). Followers of an
	// over-size 5xx stream re-enter below and fetch once more.
	if (owner || !fr.live()) && failed(ctx, fr) {
		resp, err := e.onFetchError(sp, fr, owner)
		return resp, shared(err)
	}
	if fr.err != nil {
		return nil, shared(fr.err)
	}
	if owner {
		return e.respond(c, fr), nil
	}
	// FR-KEY-7: a follower shares the entry only when its own forwarded
	// request selects the same variant.
	if fr.entry != nil && keys.VariantKey(c.Primary, fr.entry.VaryNames, c.Forwarded.Header) == fr.vk {
		if fr.fromStore { // the flight found the entry already stored: a hit, nothing was forwarded
			now := time.Now()
			_, staleness, _ := httpcc.Evaluate(fr.entry, store.Epoch{}, false, now)
			return e.fromEntry(c, fr.entry, now, CacheInfo{Hit: true, TTL: -staleness}), nil
		}
		return e.fromEntry(c, fr.entry, time.Now(),
			CacheInfo{Fwd: sp.lk.fwd, FwdStatus: fr.ci.FwdStatus, Stored: fr.ci.Stored, Collapsed: true}), nil
	}
	return e.reenter(ctx, sp, origin)
}

// reenter answers a follower the flight could not serve: not storable,
// over-size, an event stream, purged during the flight, another variant, or
// a dropped background flight (FR-COA-5, 04 §6.8). It looks up once more:
// the flight may have left a marker, or a vary spec that moves the request
// to its own variant key, where it coalesces again. A second pass fetches
// directly, so followers never chain flights.
func (e *Engine) reenter(ctx context.Context, sp *fetchSpec, origin Origin) (*Response, error) {
	if sp.reentered {
		return e.fetchDirect(ctx, sp, origin)
	}
	ck := sp.lk.ck
	return e.cacheable(ctx, sp.c, origin, &ck, time.Now())
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
			fr = &flightResult{fetchResult: fetchResult{err: &OriginError{Err: fmt.Errorf("origin panic: %v", r)}}}
		} else if fr == nil {
			fr = &flightResult{fetchResult: fetchResult{err: &OriginError{Err: errOriginGoexit}}}
		}
		if !fr.live() {
			release()
		}
		if s := fr.share; s != nil { // FR-COA-10: every counted waiter attaches or releases
			f.PublishCounted(fr, s.setPending)
			s.closeIfUnused()
			return
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
		// FR-COA-10, FR-PRG-7: as for an entry, a purge since the request went
		// out means a waiter may have asked after it, so nobody shares.
		if fr.shareable && !e.purgedSince(ctx, &store.Entry{Tags: fr.shareTags, RequestTime: fr.reqTime}) {
			vk := keys.VariantKey(sp.c.Primary, fr.shareVary, sp.c.Forwarded.Header)
			fr.share = newSharedStream(fr.resp, storedHeader(fr.resp.Header, e.cfg.Storable.StripSetCookie), fr.shareVary, vk,
				e.cfg.Storable.MaxObjectBytes, e.resumer(bg, reqCtx, origin, fr.resp.Header))
		}
	}
}

// purgedSince reports whether an epoch newer than ent's request time
// applies to it. A lookup error fails open, as in lookup (T-9).
func (e *Engine) purgedSince(ctx context.Context, ent *store.Entry) bool {
	_, ok, err := e.sg.newestEpoch(ctx, ent.Tags, ent.RequestTime)
	return ok && err == nil
}

// hardPurgedSince reports whether a hard epoch newer than ent's request time
// applies to it. A lookup error fails open, as in lookup (T-9). Only a hard
// epoch counts: a soft or invalidating one leaves the entry open to
// validation (FR-PRG-2, FR-STL-5).
func (e *Engine) hardPurgedSince(ctx context.Context, ent *store.Entry) bool {
	ep, ok, err := e.sg.newestEpoch(ctx, ent.Tags, ent.RequestTime)
	return ok && err == nil && ep.Mode != store.EpochSoft && ep.Mode != store.EpochInvalid
}

// leaveFlight marks the creator gone. When the flight published first,
// runFlight may have checked too early, so the creator closes an unclaimed
// stream itself (04 §6.4).
func (e *Engine) leaveFlight(f *coalesce.Flight) {
	if f.CreatorGone() {
		fr := f.Result().(*flightResult)
		switch {
		case fr.share != nil:
			fr.share.release()
		case fr.live() && f.AbandonStream():
			closeBody(fr.resp)
		}
	}
}

// resumer returns how a reader moved off a shared stream fetches the rest of
// the body on its own (FR-COA-10): a range request from its offset with
// If-Range, through fetch like every origin call (P3), charged to that
// reader's own request (partition, host, breaker partition). Nil unless the
// origin advertised byte ranges and gave exactly one strong entity-tag,
// which is what makes "the rest of the same body" checkable; the reader is
// cut off then. The fetch takes the creator context's values, since the
// reader's own request context is not kept, and ends at Close (FR-COA-9).
func (e *Engine) resumer(bg, reqCtx context.Context, origin Origin, h http.Header) resumeFunc {
	etags := h["Etag"]
	if !acceptsByteRanges(h["Accept-Ranges"]) || len(etags) != 1 || etags[0] == "" || strings.HasPrefix(etags[0], "W/") {
		return nil
	}
	etag := etags[0]
	return func(c *keys.Classified, off int64) (io.ReadCloser, error) {
		ctx, cancel := context.WithCancel(context.WithoutCancel(reqCtx))
		stop := context.AfterFunc(bg, cancel)
		end := func() { stop(); cancel() }
		res := e.fetch(ctx, c.AsResume(off, etag), origin, limiter.Foreground, fetchOpts{}, nil, nil)
		if res.err != nil {
			end()
			return nil, res.err
		}
		// Anything but the rest of this body (a 200 after If-Range failed, a
		// range starting elsewhere) would be spliced onto bytes already sent.
		if res.resp.StatusCode != http.StatusPartialContent ||
			!strings.HasPrefix(res.resp.Header.Get("Content-Range"), "bytes "+strconv.FormatInt(off, 10)+"-") {
			closeBody(res.resp)
			end()
			return nil, errSharedTooSlow
		}
		return &cancelOnClose{ReadCloser: res.resp.Body, cancel: end}, nil
	}
}

// acceptsByteRanges reports an Accept-Ranges field that lists the bytes
// unit (RFC 9110 §14.3), compared as a token.
func acceptsByteRanges(lines []string) bool {
	for _, l := range lines {
		for tok := range strings.SplitSeq(l, ",") { // origin bytes on the miss path, not the hit path
			if strings.EqualFold(strings.TrimSpace(tok), "bytes") {
				return true
			}
		}
	}
	return false
}

// leaveAsFollower takes a follower that stopped waiting out of the flight's
// count, and releases its place on a shared stream when it was counted
// (FR-COA-10).
func leaveAsFollower(f *coalesce.Flight) {
	if res, published := f.Leave(); published {
		if fr := res.(*flightResult); fr.share != nil {
			fr.share.release()
		}
	}
}

// serveShared answers a waiter of a flight whose over-size response is
// shared (FR-COA-10): a reader from the first byte, or headers only for a
// HEAD. A follower that asked for another variant, or came too late to read
// from the first byte, fetches again.
func (e *Engine) serveShared(ctx context.Context, sp *fetchSpec, origin Origin, fr *flightResult, created bool) (*Response, error) {
	c, s := sp.c, fr.share
	if err := ctx.Err(); err != nil {
		s.release()
		if created {
			return nil, err
		}
		return nil, sharedError{err}
	}
	if !created && keys.VariantKey(c.Primary, s.vary, c.Forwarded.Header) != s.vk {
		s.release()
		return e.reenter(ctx, sp, origin)
	}
	h := s.header
	if !created {
		h = s.shared
	}
	resp := &Response{StatusCode: s.status, Header: maps.Clone(h), Body: http.NoBody}
	if c.Head {
		s.release()
	} else if r := s.attach(c); r != nil {
		resp.Body = r
	} else {
		return e.reenter(ctx, sp, origin)
	}
	ci := fr.ci
	if !created {
		ci = CacheInfo{Fwd: sp.lk.fwd, FwdStatus: fr.ci.FwdStatus, Detail: fr.ci.Detail, Collapsed: true}
	}
	return e.finish(resp, ci), nil
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
