package weir

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/AshwinSathian/weir/internal/httpcc"
	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/internal/limiter"
	"github.com/AshwinSathian/weir/store"
)

// Serve answers one request. It never returns (nil, nil). On a nil error the
// caller owns resp.Body and must close it. A non-nil error means no response
// could be produced; StatusCode(err) gives the status an adapter should send.
func (e *Engine) Serve(ctx context.Context, req *Request, origin Origin) (*Response, error) {
	var c keys.Classified
	if e.cfg.Observer == nil { // the hit path reads no clock for an event nobody receives
		return e.serve(ctx, req, origin, &c)
	}
	start := time.Now()
	resp, err := e.serve(ctx, req, origin, &c)
	// FR-OBS-1: one EvRequest per Serve return. Partition is empty when the
	// request was refused before classification.
	now := time.Now()
	ev := Event{Kind: EvRequest, Time: now, Partition: c.Partition, Duration: now.Sub(start), Reason: "error"}
	if err != nil {
		ev.Status = StatusCode(err)
	} else {
		ev.Status, ev.Info, ev.Reason = resp.StatusCode, resp.Cache, requestReason(&c, &resp.Cache)
	}
	emit(e.cfg.Observer, ev)
	return resp, err
}

// requestReason is the EvRequest reason of a response (04 §9.2). A follower
// that shared its flight's response sent nothing to the origin, so it is
// neither a miss nor a revalidation (FR-MR-1 counts it the same way). A
// Range request no entry answered was passed through (FR-SRV-5). A 304
// revalidates only a request that went forward for a stale entry: an origin
// answering 304 to a forward without validators validated nothing.
func requestReason(c *keys.Classified, ci *CacheInfo) string {
	switch {
	case ci.Detail == "negative":
		return "negative"
	case ci.Stale != StaleNone:
		return "stale"
	case ci.Hit:
		return "hit"
	case ci.Collapsed:
		return "collapsed"
	case ci.Fwd == FwdBypass:
		return "bypass"
	case c.Class == keys.ClassPass || c.Range:
		return "pass"
	case ci.FwdStatus == http.StatusNotModified && (ci.Fwd == FwdStale || ci.Fwd == FwdRequest):
		return "revalidated"
	}
	return "miss"
}

// serve is Serve without its event. It fills c, which stays zero when the
// request is refused before or by classification.
func (e *Engine) serve(ctx context.Context, req *Request, origin Origin, c *keys.Classified) (*Response, error) {
	if e.closed.Load() {
		return nil, ErrClosed
	}
	if req == nil {
		return nil, &RequestError{Reason: "nil request"}
	}
	cl, err := keys.Classify((*keys.Request)(req), &e.kcfg)
	if err != nil {
		return nil, e.rejected(err)
	}
	*c = cl
	if c.Class == keys.ClassPass {
		if c.FwdReason != keys.FwdBypass {
			return e.pass(ctx, c, origin, FwdMethod)
		}
		if c.ReqCC.OnlyIfCached { // FR-BYP-1, FR-SRV-6: the client forbids the origin and the rule forbids the cache
			return nil, ErrOnlyIfCached
		}
		return e.pass(ctx, c, origin, FwdBypass)
	}
	if e.currentMode() == ModeBypass { // FR-MODE-3
		if c.ReqCC.OnlyIfCached { // FR-SRV-6: the client forbade the origin
			return nil, ErrOnlyIfCached
		}
		return e.pass(ctx, c.AsBypass(), origin, FwdBypass)
	}
	e.cr.observe(req.Header)
	resp, err := e.cacheable(ctx, c, origin, nil)
	se, follower := errors.AsType[sharedError](err)
	if follower {
		err = se.error
	}
	// FR-MR-1: counted once the outcome is known. A miss is a request that
	// started a fetch of its own and was not answered from the store. One
	// that was shed or whose fetch failed is a miss, so a throttled flood
	// keeps its partition anomalous for as long as it lasts. A follower
	// shared another request's fetch, whatever came of it, and is counted
	// like a hit. Two refusals are not counted at all. Only-if-cached
	// (T-11): as a miss it would get a path flagged without one origin
	// call, as a hit it would let a flood dilute its own ratio. Circuit
	// open: the origin is down, which is the breaker's event, and counting
	// it would cap every busy path at one fetch just as the origin recovers.
	if !errors.Is(err, ErrOnlyIfCached) && !errors.Is(err, ErrCircuitOpen) {
		e.mr.Observe(c.PartitionH, c.Partition, !follower && (resp == nil || !resp.Cache.Hit && !resp.Cache.Collapsed))
	}
	return resp, err
}

// rejected maps a classification error to the public one (04 §6.2).
func (e *Engine) rejected(err error) error {
	if errors.Is(err, keys.ErrUpgrade) {
		// Not a validation failure: EvKeyRejected's vocabulary is
		// RequestError.Reason values only (04 §9.2), and FR-UPG-1 has
		// weirhttp and Caddy route CONNECT and upgrades around Serve, so
		// production traffic never reaches this path.
		return ErrUpgradeNotSupported
	}
	reason := "invalid"
	if re, ok := errors.AsType[*keys.RequestError](err); ok {
		reason = re.Reason
	}
	emit(e.cfg.Observer, Event{Kind: EvKeyRejected, Time: time.Now(), Reason: reason})
	return &RequestError{Reason: reason}
}

// pass forwards a request that is never stored, streaming the response.
func (e *Engine) pass(ctx context.Context, c *keys.Classified, origin Origin, fwd FwdReason) (*Response, error) {
	res := e.fetch(ctx, c, origin, limiter.Foreground, false, nil, nil)
	if res.err != nil {
		return nil, res.err
	}
	if c.Unsafe && res.resp.StatusCode >= 200 && res.resp.StatusCode < 400 {
		e.invalidate(ctx, c, res.received())
	}
	if c.Head { // a HEAD Range miss went forward as GET
		closeBody(res.resp)
		res.resp.Body = http.NoBody
	}
	return e.finish(res.resp, CacheInfo{Fwd: fwd, FwdStatus: res.resp.StatusCode}), nil
}

// lookupResult is what the store holds for a request (04 §6.3).
type lookupResult struct {
	ck      store.Key    // the key the records below live under: Primary, or the variant key under a vary spec
	entry   *store.Entry // a response record, not yet evaluated
	marker  bool         // a hit-for-miss marker
	neg     *store.Entry // a negative entry (FR-NEG-3)
	epoch   store.Epoch
	epochOK bool
	fwd     FwdReason // reason to report if the request goes forward
}

// lookup reads the primary key and, under a vary spec, the variant key
// (FR-KEY-7, ADR-2). Store errors read as a miss.
func (e *Engine) lookup(ctx context.Context, c *keys.Classified, now time.Time) lookupResult {
	lk := lookupResult{ck: c.Primary, fwd: FwdURIMiss}
	rec, err := e.sg.get(ctx, c.Primary)
	if err != nil || rec.Expires.Before(now) { // stores may return expired records lazily
		return lk
	}
	if rec.Kind == store.KindVarySpec {
		// Secondary values come from the forwarded request, which is what
		// the origin sees (FR-KEY-7, INV-1).
		lk.ck = keys.VariantKey(c.Primary, rec.VaryNames, c.Forwarded.Header)
		if rec, err = e.sg.get(ctx, lk.ck); err != nil || rec.Expires.Before(now) {
			lk.fwd = FwdVaryMiss
			return lk
		}
	}
	switch rec.Kind {
	case store.KindResponse:
		lk.entry = rec
		lk.fwd = FwdStale // used only if the request ends up forwarded
		// T-9: an epoch lookup error fails open, as "no epoch".
		lk.epoch, lk.epochOK, err = e.sg.newestEpoch(ctx, rec.Tags, rec.RequestTime)
		lk.epochOK = lk.epochOK && err == nil
	case store.KindHitForMiss:
		lk.marker = true
	case store.KindNegative:
		lk.neg = rec
	}
	return lk
}

// cacheable serves a GET or HEAD from the store or through a coalesced
// fetch (04 §6.2), validating a stale entry that has validators
// (FR-SRV-3). prevCK is the coalescing key of the flight a follower
// re-enters from (FR-COA-5), nil on the first pass.
func (e *Engine) cacheable(ctx context.Context, c *keys.Classified, origin Origin, prevCK *store.Key) (*Response, error) {
	now := time.Now()
	lk := e.lookup(ctx, c, now)
	found := lk.entry       // the response this request found; it may always be replaced
	var purged *store.Entry // an unusable response a marker may replace
	var prior *store.Entry  // the stale entry to validate
	if lk.entry != nil {
		st, staleness, _ := httpcc.Evaluate(lk.entry, lk.epoch, lk.epochOK, now)
		if (st == httpcc.Fresh || st == httpcc.StaleSWR) && !c.ReqCC.OnlyIfCached && forcesValidation(&c.ReqCC) {
			st, lk.fwd = httpcc.NeedsValidation, FwdRequest // RFC 9211 §2.2 fwd=request
		}
		switch st {
		case httpcc.Fresh:
			e.maybeEarlyRefresh(ctx, c, lk, -staleness, origin)
			return e.fromEntry(c, lk.entry, now, CacheInfo{Hit: true, TTL: -staleness}), nil
		case httpcc.StaleSWR: // FR-STL-1, 03 §2.3; before only-if-cached and Range, which it answers
			emit(e.cfg.Observer, Event{Kind: EvStaleServed, Time: now, Partition: c.Partition, Reason: "swr"})
			if !c.Authorized && !c.ReqCC.NoStore { // the early-refresh gate (T-8, T-31)
				e.backgroundRefresh(ctx, c, lk, origin)
			}
			return e.fromEntry(c, lk.entry, now, CacheInfo{Hit: true, Stale: StaleWhileRevalidate, TTL: -staleness}), nil
		case httpcc.Unusable: // FR-PRG-3: exactly a miss
			purged, lk.entry, lk.fwd = lk.entry, nil, FwdURIMiss
		default:
			if hasValidators(lk.entry) {
				prior = lk.entry
			}
		}
	}
	if lk.neg != nil { // FR-NEG-3: never stored next to a response, so no stale entry is servable
		return e.fromNegative(c, lk.neg, now), nil
	}
	if c.ReqCC.OnlyIfCached { // FR-SRV-6
		return nil, ErrOnlyIfCached
	}
	// FR-SRV-5, T-37: a Range request no entry answers passes through, even
	// with a stale entry: not stored, not coalesced, no marker.
	if c.Range {
		return e.pass(ctx, c.AsRangePass(), origin, lk.fwd)
	}
	sp := &fetchSpec{c: c, lk: lk, prior: prior, found: found, purged: purged, reentered: prevCK != nil}
	// FR-COA-8, FR-STO-12. A no-store request's response is never shared,
	// so leading a flight would only make its followers wait and refetch
	// (T-31: one client must not disable coalescing for everyone).
	// FR-COA-5: a re-entering follower coalesces again only under a new
	// key, so followers never wait on each other serially.
	if c.Authorized || c.ReqCC.NoStore || lk.marker || prevCK != nil && *prevCK == lk.ck {
		return e.fetchDirect(ctx, sp, origin)
	}
	return e.fetchCoalesced(ctx, sp, origin)
}

// fetchStored fetches sp's forwarded request, buffered, and stores the
// response when storable (04 §6.7). A flight runs it, or the request itself
// (fetchDirect).
func (e *Engine) fetchStored(ctx context.Context, sp *fetchSpec, origin Origin) *flightResult {
	c, prior := sp.c, sp.prior
	fr := &flightResult{fetchResult: e.fetch(ctx, c, origin, sp.class, true, prior, sp.permit)}
	res := &fr.fetchResult
	e.setNegative(ctx, sp, res) // once per flight, before any waiter reads the store
	if res.err != nil {
		return fr
	}
	fr.ci = CacheInfo{Fwd: sp.lk.fwd, FwdStatus: res.resp.StatusCode}
	if res.notMod { // FR-SRV-3: the freshened entry is stored and served like a full response
		res.resp, res.body = freshened(prior, res.resp.Header), prior.Body
		if res.recv != nil {
			res.recv = freshened(prior, res.recv).Header
		}
		if prior.Flags&store.FlagFromAuthorized != 0 && !c.Authorized {
			// FR-STO-5, T-8: the body answered an Authorization request, so
			// the merged headers still need a shared-cache permission.
			ac := *c
			ac.Authorized = true
			c = &ac
		}
	}
	if sp.lk.marker {
		fr.ci.Detail = "hit-for-miss"
	}
	switch {
	case res.stream: // FR-STR-1
		emit(e.cfg.Observer, Event{Kind: EvNotStored, Time: res.respTime, Partition: c.Partition, Reason: "stream"})
	case res.over:
		emit(e.cfg.Observer, Event{Kind: EvNotStored, Time: res.respTime, Partition: c.Partition, Reason: "too-large"})
	case serverError(res.resp.StatusCode):
		// Before Publish, so before the creator's respond writes to resp.
		fr.errHeader = maps.Clone(res.resp.Header)
		emit(e.cfg.Observer, Event{Kind: EvNotStored, Time: res.respTime, Partition: c.Partition, Reason: "status"})
	default:
		fr.entry, fr.ci.Stored = e.storeResponse(ctx, c, res, sp)
		if fr.entry != nil {
			fr.vk = keys.VariantKey(c.Primary, fr.entry.VaryNames, c.Forwarded.Header)
		}
	}
	return fr
}

// respond builds the response for the request whose fetch produced fr.
func (e *Engine) respond(c *keys.Classified, fr *flightResult) *Response {
	resp := fr.resp
	switch {
	case c.Head:
		closeBody(resp) // an over-size or event-stream body is canceled, not downloaded
		resp.Body = http.NoBody
	case !fr.over && !fr.stream && len(fr.body) > 0:
		resp.Body = io.NopCloser(bytes.NewReader(fr.body))
	}
	return e.finish(resp, fr.ci)
}

// forcesValidation reports the request directives that turn a fresh entry
// into one that needs validation. Classify clears them unless
// Client.HonorRevalidation is set (FR-SRV-8, D5).
func forcesValidation(cc *httpcc.RequestDirectives) bool {
	return cc.NoCache || cc.MaxAge.Set && !cc.MaxAge.Invalid && cc.MaxAge.V == 0
}

// serverError reports the statuses that never create a hit-for-miss
// marker: they are the origin failing, not the URI being uncacheable.
// Negative caching handles them (setNegative).
func serverError(status int) bool {
	switch status {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// storeResponse stores a fully read response when storable, else writes a
// hit-for-miss marker when the response itself is the reason (FR-STO-12,
// T-31). It returns the entry when storable, which followers may share, and
// reports whether it was stored. The client's own
// response keeps every origin field (FR-STO-6). sp.found is the response the
// lookup returned, which this request already judged not fresh.
func (e *Engine) storeResponse(ctx context.Context, c *keys.Classified, res *fetchResult, sp *fetchSpec) (*store.Entry, bool) {
	ctx = context.WithoutCancel(ctx) // a client leaving after the body arrived does not undo the store
	recv := res.received()           // T-8: a field Connection names still refuses storage
	d := storability(&e.cfg, c, recv, res.body, res.respTime)
	if !d.ok {
		emit(e.cfg.Observer, Event{Kind: EvNotStored, Time: res.respTime, Partition: c.Partition, Reason: d.reason})
		if d.responseDriven {
			now := res.respTime
			e.setUnlessResponse(ctx, sp.lk.ck, sp.purged,
				&store.Entry{Kind: store.KindHitForMiss, StoredAt: now, Expires: now.Add(e.cfg.Coalesce.HitForMissTTL)})
		}
		return nil, false
	}
	ent := buildEntry(&e.cfg, c, recv, res.body, res.reqTime, res.respTime, d)
	vk := keys.VariantKey(c.Primary, d.varyNames, c.Forwarded.Header)
	// RFC 9111 §4: a slow fetch never replaces a more recent response that
	// another fetch stored meanwhile (04 §6.7). The record this request
	// found is exempt: it is stale or unusable, and an origin clock that
	// once ran ahead would otherwise pin it until it expires. So is a
	// record past its Expires that a lazy store still returns.
	if cur, err := e.sg.get(ctx, vk); err == nil && cur.Kind == store.KindResponse &&
		cur.Expires.After(res.respTime) && !sameRecord(cur, sp.found) && newer(cur, ent) {
		return ent, false
	}
	if vk == c.Primary {
		return ent, e.sg.set(ctx, vk, ent) == nil
	}
	return ent, e.setVariant(ctx, c, vk, ent)
}

// setVariant stores ent under its variant key vk, then the vary spec under
// the primary key that lists it (04 §6.7). Variant first, so a reader that
// finds the spec usually finds the variant. A spec with other names is
// replaced and its variants age out. Refs past their Expires are dropped,
// and at the cap those without a record too, freeing their slots (D37); a new variant past
// MaxVariants live ones is not stored (FR-KEY-10, NFR-3).
func (e *Engine) setVariant(ctx context.Context, c *keys.Classified, vk store.Key, ent *store.Entry) bool {
	now := ent.StoredAt
	var refs []store.VariantRef // a new slice: the stored spec is immutable (P4)
	if cur, err := e.sg.get(ctx, c.Primary); err == nil && cur.Kind == store.KindVarySpec && slices.Equal(cur.VaryNames, ent.VaryNames) {
		for _, r := range cur.Variants {
			if r.Key != vk && r.Expires.After(now) {
				refs = append(refs, r)
			}
		}
	}
	if len(refs) >= e.cfg.Key.MaxVariants {
		// 04 §14 reclaim: a ref whose record the store evicted or dropped
		// holds no slot. One read per listed ref, only when the write
		// would otherwise be refused. Any other store error keeps the
		// ref, so a failing store cannot lift the cap (T-15).
		refs = slices.DeleteFunc(refs, func(r store.VariantRef) bool {
			_, err := e.sg.get(ctx, r.Key)
			return errors.Is(err, store.ErrNotFound)
		})
	}
	if len(refs) >= e.cfg.Key.MaxVariants {
		emit(e.cfg.Observer, Event{Kind: EvVaryOverflow, Time: now, Partition: c.Partition})
		return false
	}
	if e.sg.set(ctx, vk, ent) != nil {
		return false
	}
	refs = append(refs, store.VariantRef{Key: vk, Expires: ent.Expires})
	spec := &store.Entry{Kind: store.KindVarySpec, StoredAt: now, VaryNames: ent.VaryNames, Variants: refs}
	for _, r := range refs { // 04 §4.2: a spec lives as long as its longest variant
		spec.Expires = later(spec.Expires, r.Expires)
	}
	return e.sg.set(ctx, c.Primary, spec) == nil
}

// sameRecord reports whether a and b are the same stored response. Stores
// that decode a fresh copy per Get return a different pointer, so the
// request and response times identify it.
func sameRecord(a, b *store.Entry) bool {
	return a == b || b != nil && a.RequestTime.Equal(b.RequestTime) && a.ResponseTime.Equal(b.ResponseTime)
}

// newer reports whether a is a more recent response than b: a later Date,
// or on equal Dates a later ResponseTime. Date compares origin time with
// origin time, so origin clock skew does not matter (T-30). The read before
// the write is not atomic; two writes racing within one store round trip
// can still let the older win until the next refresh.
func newer(a, b *store.Entry) bool {
	if !a.Date.Equal(b.Date) {
		return a.Date.After(b.Date)
	}
	return a.ResponseTime.After(b.ResponseTime)
}

// setUnlessResponse writes a hit-for-miss marker or negative entry unless
// the key holds a response, which a concurrent fetch may just have stored
// (04 §6.7) and which may still be revalidated (FR-NEG-1), or a vary spec,
// whose variants a record at the primary key would hide. purged, the
// hard-purged response this request found, may be replaced: it can never be
// served or revalidated (FR-STO-12). Stores that decode a fresh copy per Get
// never match it, which only costs the record. A record past its Expires
// that a store still returns lazily is a miss to lookup, so it is replaced
// too, or a lazy store would never get a negative entry (T6.10).
func (e *Engine) setUnlessResponse(ctx context.Context, k store.Key, purged, rec *store.Entry) {
	if cur, err := e.sg.get(ctx, k); err == nil && (cur.Kind == store.KindResponse && cur != purged || cur.Kind == store.KindVarySpec) &&
		!cur.Expires.Before(time.Now()) {
		return
	}
	// A failed write only costs the record's benefit: the next miss refetches.
	_ = e.sg.set(ctx, k, rec)
}

// keysConfig compiles the key and forwarding settings for keys.Classify.
func keysConfig(c *Config) keys.Config {
	return keys.Config{
		MaxPathBytes:        c.Limits.MaxPathBytes,
		MaxQueryBytes:       c.Limits.MaxQueryBytes,
		MaxQueryParams:      c.Limits.MaxQueryParams,
		MaxKeyedHeaderBytes: c.Limits.MaxKeyedHeaderBytes,
		QueryDrop:           c.Key.QueryDrop,
		QueryKeep:           c.Key.QueryKeep,
		QuerySort:           c.Key.QuerySort,
		NormalizePath:       c.Key.NormalizePath,
		Headers:             c.Key.Headers,
		Cookies:             c.Key.Cookies,
		AcceptEncoding:      c.Key.AcceptEncoding,
		ForwardAll:          c.Forward.Mode == ForwardAll,
		Allow:               c.Forward.Allow,
		NoTraceHeaders:      c.Forward.NoTraceHeaders,
		BypassHeaders:       c.Bypass.Headers,
		BypassCookies:       c.Bypass.Cookies,
		HonorRevalidation:   c.Client.HonorRevalidation,
	}
}
