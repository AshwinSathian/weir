package weir

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/AshwinSathian/weir/internal/httpcc"
	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/store"
)

// Serve answers one request. It never returns (nil, nil). On a nil error the
// caller owns resp.Body and must close it. A non-nil error means no response
// could be produced; StatusCode(err) gives the status an adapter should send.
func (e *Engine) Serve(ctx context.Context, req *Request, origin Origin) (*Response, error) {
	if e.closed.Load() {
		return nil, ErrClosed
	}
	if req == nil {
		return nil, &RequestError{Reason: "nil request"}
	}
	c, err := keys.Classify((*keys.Request)(req), &e.kcfg)
	if err != nil {
		return nil, e.rejected(err)
	}
	if c.Class == keys.ClassPass {
		return e.pass(ctx, &c, origin, FwdMethod)
	}
	return e.cacheable(ctx, &c, origin)
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
	res := e.fetch(ctx, (*Request)(&c.Forwarded), origin, false, nil)
	if res.err != nil {
		return nil, res.err
	}
	if c.Unsafe && res.resp.StatusCode >= 200 && res.resp.StatusCode < 400 {
		e.invalidate(ctx, c, res.resp)
	}
	if c.Head { // a HEAD Range miss went forward as GET
		closeBody(res.resp)
		res.resp.Body = http.NoBody
	}
	return e.finish(res.resp, CacheInfo{Fwd: fwd, FwdStatus: res.resp.StatusCode}), nil
}

// lookupResult is what the store holds for a request (04 §6.3).
type lookupResult struct {
	entry   *store.Entry // a response record, not yet evaluated
	marker  bool         // a hit-for-miss marker
	epoch   store.Epoch
	epochOK bool
	fwd     FwdReason // reason to report if the request goes forward
}

// lookup reads the primary key. Store errors read as a miss. Vary specs
// (M7-01) and negative records (M6) are not written yet, so they read as a
// miss too.
func (e *Engine) lookup(ctx context.Context, c *keys.Classified, now time.Time) lookupResult {
	lk := lookupResult{fwd: FwdURIMiss}
	rec, err := e.store.Get(ctx, c.Primary)
	if err != nil || rec.Expires.Before(now) { // stores may return expired records lazily
		return lk
	}
	switch rec.Kind {
	case store.KindResponse:
		lk.entry = rec
		lk.fwd = FwdStale // used only if the request ends up forwarded
		// T-9: an epoch lookup error fails open, as "no epoch".
		lk.epoch, lk.epochOK, err = e.store.NewestEpoch(ctx, rec.Tags, rec.RequestTime)
		lk.epochOK = lk.epochOK && err == nil
	case store.KindHitForMiss:
		lk.marker = true
	}
	return lk
}

// cacheable serves a GET or HEAD from the store or through an uncoalesced
// fetch (04 §6.2), validating a stale entry that has validators
// (FR-SRV-3). Coalescing arrives in M2.
func (e *Engine) cacheable(ctx context.Context, c *keys.Classified, origin Origin) (*Response, error) {
	now := time.Now()
	lk := e.lookup(ctx, c, now)
	found := lk.entry       // the response this request found; it may always be replaced
	var purged *store.Entry // an unusable response a marker may replace
	var prior *store.Entry  // the stale entry to validate
	if lk.entry != nil {
		st, staleness, _ := httpcc.Evaluate(lk.entry, lk.epoch, lk.epochOK, now)
		if st == httpcc.Fresh && !c.ReqCC.OnlyIfCached && forcesValidation(&c.ReqCC) {
			st, lk.fwd = httpcc.NeedsValidation, FwdRequest // RFC 9211 §2.2 fwd=request
		}
		switch st {
		case httpcc.Fresh:
			return e.fromEntry(c, lk.entry, now, CacheInfo{Hit: true, TTL: -staleness}), nil
		case httpcc.Unusable: // FR-PRG-3: exactly a miss
			purged, lk.entry, lk.fwd = lk.entry, nil, FwdURIMiss
		default:
			// ponytail: StaleSWR validates in the foreground until
			// background refresh (M5) lands.
			if hasValidators(lk.entry) {
				prior = lk.entry
			}
		}
	}
	// ponytail: a StaleSWR entry fails only-if-cached until M5 serves it.
	if c.ReqCC.OnlyIfCached { // FR-SRV-6
		return nil, ErrOnlyIfCached
	}
	// FR-SRV-5, T-37: a Range request no entry answers passes through, even
	// with a stale entry: not stored, not coalesced, no marker.
	// ponytail: StaleSWR entries pass through too until M5 serves them.
	if c.Range {
		return e.pass(ctx, c.AsRangePass(), origin, lk.fwd)
	}
	res := e.fetch(ctx, (*Request)(&c.Forwarded), origin, true, prior)
	if res.err != nil {
		return nil, res.err
	}
	ci := CacheInfo{Fwd: lk.fwd, FwdStatus: res.resp.StatusCode}
	if res.notMod { // FR-SRV-3: the freshened entry is stored and served like a full response
		res.resp, res.body = freshened(prior, res.resp), prior.Body
		if prior.Flags&store.FlagFromAuthorized != 0 && !c.Authorized {
			// FR-STO-5, T-8: the body answered an Authorization request, so
			// the merged headers still need a shared-cache permission.
			ac := *c
			ac.Authorized = true
			c = &ac
		}
	}
	resp := res.resp
	if lk.marker {
		ci.Detail = "hit-for-miss"
	}
	if !res.over && !res.stream && !serverError(resp.StatusCode) {
		ci.Stored = e.storeResponse(ctx, c, &res, found, purged)
	}
	switch {
	case c.Head:
		closeBody(resp) // an over-size or event-stream body is canceled, not downloaded
		resp.Body = http.NoBody
	case !res.over && !res.stream && len(res.body) > 0:
		resp.Body = io.NopCloser(bytes.NewReader(res.body))
	}
	return e.finish(resp, ci), nil
}

// forcesValidation reports the request directives that turn a fresh entry
// into one that needs validation. Classify clears them unless
// Client.HonorRevalidation is set (FR-SRV-8, D5).
func forcesValidation(cc *httpcc.RequestDirectives) bool {
	return cc.NoCache || cc.MaxAge.Set && !cc.MaxAge.Invalid && cc.MaxAge.V == 0
}

// serverError reports the statuses that never create a hit-for-miss
// marker: they are the origin failing, not the URI being uncacheable.
// Negative caching (M6) handles them.
func serverError(status int) bool {
	switch status {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// storeResponse stores a fully read response when storable, else writes a
// hit-for-miss marker when the response itself is the reason (FR-STO-12,
// T-31). It reports whether the entry was stored. The client's own
// response keeps every origin field (FR-STO-6). found is the response the
// lookup returned, which this request already judged not fresh.
func (e *Engine) storeResponse(ctx context.Context, c *keys.Classified, res *fetchResult, found, purged *store.Entry) bool {
	ctx = context.WithoutCancel(ctx) // a client leaving after the body arrived does not undo the store
	d := storability(&e.cfg, c, res.resp, res.body, res.respTime)
	if !d.ok {
		emit(e.cfg.Observer, Event{Kind: EvNotStored, Time: res.respTime, Partition: c.Partition, Reason: d.reason})
		if d.responseDriven {
			e.setMarker(ctx, c.Primary, res.respTime, purged)
		}
		return false
	}
	ent := buildEntry(&e.cfg, c, res.resp, res.body, res.reqTime, res.respTime, d)
	// RFC 9111 §4: a slow fetch never replaces a more recent response that
	// another fetch stored meanwhile (04 §6.7). The record this request
	// found is exempt: it is stale or unusable, and an origin clock that
	// once ran ahead would otherwise pin it until it expires. So is a
	// record past its Expires that a lazy store still returns.
	if cur, err := e.store.Get(ctx, c.Primary); err == nil && cur.Kind == store.KindResponse &&
		cur.Expires.After(res.respTime) && !sameRecord(cur, found) && newer(cur, ent) {
		return false
	}
	return e.store.Set(ctx, c.Primary, ent) == nil
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

// setMarker writes a hit-for-miss marker unless the key holds a response,
// which a concurrent fetch may just have stored (04 §6.7). purged, the
// hard-purged response this request found, may be replaced: it can never be
// served or revalidated (FR-STO-12). Stores that decode a fresh copy per Get
// never match it, which only costs the marker.
func (e *Engine) setMarker(ctx context.Context, k store.Key, now time.Time, purged *store.Entry) {
	if cur, err := e.store.Get(ctx, k); err == nil && cur.Kind == store.KindResponse && cur != purged {
		return
	}
	// A failed write only costs the marker's benefit: the next miss refetches.
	_ = e.store.Set(ctx, k, &store.Entry{Kind: store.KindHitForMiss, StoredAt: now, Expires: now.Add(e.cfg.Coalesce.HitForMissTTL)})
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
		Cookies:             c.Key.Cookies,
		AcceptEncoding:      c.Key.AcceptEncoding,
		ForwardAll:          c.Forward.Mode == ForwardAll,
		Allow:               c.Forward.Allow,
		NoTraceHeaders:      c.Forward.NoTraceHeaders,
		HonorRevalidation:   c.Client.HonorRevalidation,
	}
}
