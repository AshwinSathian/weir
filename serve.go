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
		return e.pass(ctx, &c, origin)
	}
	return e.cacheable(ctx, &c, origin)
}

// rejected maps a classification error to the public one (04 §6.2).
func (e *Engine) rejected(err error) error {
	if errors.Is(err, keys.ErrUpgrade) {
		return ErrUpgradeNotSupported // not a validation failure; M1-16 owns its event
	}
	reason := "invalid"
	if re, ok := errors.AsType[*keys.RequestError](err); ok {
		reason = re.Reason
	}
	emit(e.cfg.Observer, Event{Kind: EvKeyRejected, Time: time.Now(), Reason: reason})
	return &RequestError{Reason: reason}
}

// pass forwards a request that is never stored, streaming the response.
func (e *Engine) pass(ctx context.Context, c *keys.Classified, origin Origin) (*Response, error) {
	res := e.fetch(ctx, (*Request)(&c.Forwarded), origin, false)
	if res.err != nil {
		return nil, res.err
	}
	return e.finish(res.resp, CacheInfo{Fwd: FwdMethod, FwdStatus: res.resp.StatusCode}), nil
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
// fetch (04 §6.2). Coalescing arrives in M2, validation in M1-13.
func (e *Engine) cacheable(ctx context.Context, c *keys.Classified, origin Origin) (*Response, error) {
	now := time.Now()
	lk := e.lookup(ctx, c, now)
	var purged *store.Entry // an unusable response a marker may replace
	if lk.entry != nil {
		st, staleness, _ := httpcc.Evaluate(lk.entry, lk.epoch, lk.epochOK, now)
		switch st {
		case httpcc.Fresh:
			return e.fromEntry(c, lk.entry, now, CacheInfo{Hit: true, TTL: -staleness}), nil
		case httpcc.Unusable: // FR-PRG-3: exactly a miss
			purged, lk.entry, lk.fwd = lk.entry, nil, FwdURIMiss
		}
		// ponytail: StaleSWR and NeedsValidation refetch in the foreground,
		// unconditionally, until validation (M1-13) and background refresh
		// (M5) land; Unusable is a plain miss either way.
	}
	res := e.fetch(ctx, (*Request)(&c.Forwarded), origin, true)
	if res.err != nil {
		return nil, res.err
	}
	resp := res.resp
	ci := CacheInfo{Fwd: lk.fwd, FwdStatus: resp.StatusCode}
	if lk.marker {
		ci.Detail = "hit-for-miss"
	}
	if !res.over && !serverError(resp.StatusCode) {
		ci.Stored = e.storeResponse(ctx, c, &res, purged)
	}
	switch {
	case c.Head:
		closeBody(resp) // an over-size stream is canceled, not downloaded
		resp.Body = http.NoBody
	case !res.over && len(res.body) > 0:
		resp.Body = io.NopCloser(bytes.NewReader(res.body))
	}
	return e.finish(resp, ci), nil
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
// response keeps every origin field (FR-STO-6).
func (e *Engine) storeResponse(ctx context.Context, c *keys.Classified, res *fetchResult, purged *store.Entry) bool {
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
	return e.store.Set(ctx, c.Primary, ent) == nil
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
