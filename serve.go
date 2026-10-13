package weir

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
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
		resp, err := e.serve(ctx, req, origin, &c)
		return e.experimentResponse(&c, resp), err
	}
	start := time.Now()
	resp, err := e.serve(ctx, req, origin, &c)
	resp = e.experimentResponse(&c, resp)
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
	// The forwarded header is built when a path first needs it (shared):
	// a hit reads only the key (NFR-5).
	cl, err := keys.ClassifyDeferred((*keys.Request)(req), &e.kcfg)
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
	e.cr.observe(c.CanonicalHeader())
	now := time.Now() // read once for the lookup, freshness, and the miss-rate window
	resp, err := e.cacheable(ctx, c, origin, nil, now)
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
		e.mr.ObserveAt(now, c.PartitionH, c.Partition, !follower && (resp == nil || !resp.Cache.Hit && !resp.Cache.Collapsed))
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

// admit takes the client's place for a request that is about to go to the
// origin or wait for another request's fetch (FR-FAIR-4). It returns the
// release to call when that ends, or the shed result when the client's
// places stayed taken for MaxQueueWait. A request that named no client, and
// every request with MaxPerClient off, is admitted at once.
func (e *Engine) admit(ctx context.Context, c *keys.Classified) (release func(), shed *fetchResult) {
	if e.gate == nil {
		return noRelease, nil
	}
	ch := c.ClientHash()
	if ch == 0 {
		return noRelease, nil
	}
	p, err := e.gate.Acquire(ctx, limiter.Foreground, ch, 0)
	if err != nil {
		res := e.shed(c, limiter.Foreground, err)
		return noRelease, &res
	}
	return p.Release, nil
}

func noRelease() {}

// maxForwards applies RFC 9110 §7.6.2 to a TRACE or OPTIONS request
// (FR-FWD-9). With Max-Forwards 0 Weir is the final recipient and answers
// itself: 204 for OPTIONS, and 405 for TRACE, whose answer would echo the
// request's fields back and which a server may refuse. Above 0 the forward
// carries the value less one. A field that is absent, repeated or not a
// number is left alone, as the RFC allows. c's forwarded header is the
// request's own copy (keys.pass), so the write stays in this request.
func (e *Engine) maxForwards(c *keys.Classified) *Response {
	m := c.Forwarded.Method
	if m != http.MethodTrace && m != http.MethodOptions {
		return nil
	}
	mf := c.Forwarded.Header["Max-Forwards"]
	if len(mf) != 1 {
		return nil
	}
	n, ok := digits(mf[0])
	if !ok {
		return nil
	}
	if n > 0 {
		c.Forwarded.Header["Max-Forwards"] = []string{strconv.FormatInt(n-1, 10)}
		return nil
	}
	status := http.StatusNoContent
	if m == http.MethodTrace {
		status = http.StatusMethodNotAllowed
	}
	return e.finish(&Response{StatusCode: status, Header: http.Header{"Allow": {"GET, HEAD, OPTIONS"}}, Body: http.NoBody}, CacheInfo{})
}

// shared returns a copy of c on the heap, with its forwarded header built,
// for the paths that hand the request to the origin or to another goroutine
// (a flight, a background fetch). Serve's own Classified then stays on the
// stack for a hit: the copy is made here, never by taking c's address, so
// nothing a hit does makes it escape (NFR-5). The copy shares c's maps and
// slices, which nothing writes after classification.
func shared(c *keys.Classified) *keys.Classified {
	c.Forward()
	h := *c
	return &h
}

// pass forwards a request that is never stored, streaming the response.
func (e *Engine) pass(ctx context.Context, rc *keys.Classified, origin Origin, fwd FwdReason) (*Response, error) {
	c := shared(rc)
	if resp := e.maxForwards(c); resp != nil {
		return resp, nil
	}
	release, shed := e.admit(ctx, c)
	if shed != nil {
		return nil, shed.err
	}
	defer release()
	res := e.fetch(ctx, c, origin, limiter.Foreground, fetchOpts{patient: true}, nil, nil)
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

// rangeMiss passes a Range request through and, when the origin's 206 shows
// a whole object the cache would store, starts one background fetch of it
// (FR-RNG-4, 04 §13.1). The client never waits on that fetch (T-37).
func (e *Engine) rangeMiss(ctx context.Context, c *keys.Classified, lk lookupResult, origin Origin) (*Response, error) {
	resp, err := e.pass(ctx, c.AsRangePass(), origin, lk.fwd)
	if err != nil {
		return nil, err
	}
	// The same requests that never lead a flight (cacheable) never start a
	// fill, and a hit-for-miss marker says the response is not shareable.
	// A HEAD never fills: it forwards as a GET (FR-FWD-4) and a metadata probe
	// must not cost a full-object fetch (T-37).
	if c.Head || lk.marker || c.Authorized || c.ReqCC.NoStore || !e.fillable(c, resp) {
		return resp, nil
	}
	// ponytail: a fill that errors repeats on the next Range miss; see 04 §13.1.
	e.background(ctx, c, lk, origin, true) // c's forwarded request has no Range (T-7)
	return resp, nil
}

// fillable reports a 206 whose Content-Range declares a total within
// MaxObjectBytes and that would pass storability if it were a 200 (T-37).
func (e *Engine) fillable(c *keys.Classified, resp *Response) bool {
	if resp.StatusCode != http.StatusPartialContent {
		return false
	}
	total, ok := contentRangeTotal(resp.Header.Get("Content-Range"))
	// The limit counts headers too (storability), so the 200 the fill will
	// read must fit total plus its headers.
	if !ok || total+headerBytes(resp.Header) > e.cfg.Storable.MaxObjectBytes {
		return false
	}
	as200 := &Response{StatusCode: http.StatusOK, Header: resp.Header}
	return storability(&e.cfg, c, as200, nil, time.Now()).ok
}

// contentRangeTotal returns the complete length of a 206's
// "bytes first-last/total" Content-Range. Anything else reports false: "*"
// totals, the 416 form "bytes */total", signs, overflow, last < first or
// last >= total (RFC 9110 §14.4).
func contentRangeTotal(v string) (int64, bool) {
	rest, ok := strings.CutPrefix(v, "bytes ")
	if !ok {
		return 0, false
	}
	span, totalS, ok := strings.Cut(rest, "/")
	if !ok {
		return 0, false
	}
	firstS, lastS, ok := strings.Cut(span, "-")
	if !ok {
		return 0, false
	}
	first, ok1 := digits(firstS)
	last, ok2 := digits(lastS)
	total, ok3 := digits(totalS)
	if !ok1 || !ok2 || !ok3 || first > last || last >= total {
		return 0, false
	}
	return total, true
}

// digits parses a non-empty run of ASCII digits; ParseInt alone would take a
// sign.
func digits(s string) (int64, bool) {
	if s == "" || s[0] < '0' || s[0] > '9' {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// lookupResult is what the store holds for a request (04 §6.3).
type lookupResult struct {
	ck     store.Key    // the key the records below live under: Primary, or the variant key under a vary spec
	entry  *store.Entry // a response record, not yet evaluated
	marker bool         // a hit-for-miss marker
	// oversize: the marker was written for an object over MaxObjectBytes.
	oversize bool
	neg      *store.Entry // a negative entry (FR-NEG-3)
	epoch    store.Epoch
	epochOK  bool
	fwd      FwdReason // reason to report if the request goes forward
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
		c.Forward()
		lk.ck = keys.VariantKey(c.Primary, rec.VaryNames, c.Forwarded.Header)
		spec := rec
		if rec, err = e.sg.get(ctx, lk.ck); err != nil || rec.Expires.Before(now) {
			lk.fwd = FwdVaryMiss
			return lk
		}
		// FR-INV-1: a response under a variant key counts only while the spec
		// lists it. One left there by a spec that has since gone (evicted, or
		// replaced by another) is unreachable, so the primary key leads to
		// every servable response of the URI, which is what lets an
		// invalidation ask the primary key alone whether anything is stored.
		// Markers and negative entries are written without a reference and
		// carry nothing an invalidation must reach.
		if rec.Kind == store.KindResponse && !lists(spec, lk.ck) {
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
		lk.marker, lk.oversize = true, rec.Status == markerOversize
	case store.KindNegative:
		lk.neg = rec
	}
	return lk
}

// lists reports whether the vary spec has a reference to the variant key k.
// At most Key.MaxVariants references are compared.
func lists(spec *store.Entry, k store.Key) bool {
	for i := range spec.Variants {
		if spec.Variants[i].Key == k {
			return true
		}
	}
	return false
}

// cacheable serves a GET or HEAD from the store or through a coalesced
// fetch (04 §6.2), validating a stale entry that has validators
// (FR-SRV-3). prevCK is the coalescing key of the flight a follower
// re-enters from (FR-COA-5), nil on the first pass. now is the request time.
func (e *Engine) cacheable(ctx context.Context, c *keys.Classified, origin Origin, prevCK *store.Key, now time.Time) (*Response, error) {
	lk := e.lookup(ctx, c, now)
	found := lk.entry       // the response this request found; it may always be replaced
	var purged *store.Entry // an unusable response a marker may replace
	var prior *store.Entry  // the stale entry to validate
	if lk.entry != nil {
		st, staleness, _ := httpcc.Evaluate(lk.entry, lk.epoch, lk.epochOK, now)
		if (st == httpcc.Fresh || st == httpcc.StaleSWR) && !c.ReqCC.OnlyIfCached &&
			forcesValidation(&c.ReqCC, httpcc.CurrentAge(lk.entry, now), -staleness) {
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
		return e.rangeMiss(ctx, c, lk, origin)
	}
	return e.miss(ctx, shared(c), origin, lk, prior, found, purged, prevCK)
}

// miss is the rest of cacheable for a request nothing in the store answers:
// it takes the client's place, then fetches through a flight or directly.
// c is the shared copy (shared), since the fetch may outlive this call.
func (e *Engine) miss(ctx context.Context, c *keys.Classified, origin Origin, lk lookupResult, prior, found, purged *store.Entry, prevCK *store.Key) (*Response, error) {
	sp := &fetchSpec{c: c, lk: lk, prior: prior, found: found, purged: purged, reentered: prevCK != nil}
	if prevCK == nil { // a re-entering follower already holds its place
		// FR-FAIR-4: before any flight is joined or created, so a client
		// over its cap never leads a fetch that other clients wait on.
		release, shed := e.admit(ctx, c)
		if shed != nil {
			fr := &flightResult{fetchResult: *shed}
			if failed(ctx, fr) {
				return e.onFetchError(sp, fr, false) // the stale entry, when stale-if-error permits it
			}
			return nil, fr.err
		}
		defer release()
	}
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
	// FR-INV-1: counted from before the request is sent until anything it
	// stores is in the store, so an unsafe request that arrives in between
	// knows the URI has a response on its way (invalidate). The count is the
	// store's, so engines that share the store see each other's fetches.
	if ft := e.sg.fetches; ft != nil {
		ft.FetchBegin(c.Primary)
		defer ft.FetchEnd(c.Primary)
	}
	// FR-STO-12, T-18: a marker for an over-size object says the body will
	// not fit, so the fetch behind it streams instead of holding
	// MaxObjectBytes that nothing will keep. The next attempt to store comes
	// when the marker has expired. Behind any other marker the fetch is
	// buffered, and a response that has become storable is stored.
	// FR-CB-4: a request with a stale response to fall back on takes it at
	// once; any other waits for a half-open probe.
	patient := sp.lk.entry == nil
	if !patient {
		_, stale := e.staleOK(sp, time.Now())
		patient = !stale
	}
	fo := fetchOpts{buffered: !sp.lk.oversize, patient: patient, marked: sp.lk.marker}
	fr := &flightResult{fetchResult: e.fetch(ctx, c, origin, sp.class, fo, prior, sp.permit)}
	if fr.notMod && e.hardPurgedSince(ctx, prior) {
		// FR-PRG-3, FR-PRG-7: a hard purge after prior was stored makes it
		// unreachable, and it landed while this request waited for a slot,
		// so the validation went out after the epoch and the 304 would store
		// the purged body as fresh. Fetch again without conditionals.
		closeBody(fr.resp)
		prior = nil
		fo.buffered = true
		fr = &flightResult{fetchResult: e.fetch(ctx, c, origin, sp.class, fo, nil, nil)}
	}
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
	case res.unbuffered: // fetched behind an over-size marker: nothing to store
		emit(e.cfg.Observer, Event{Kind: EvNotStored, Time: res.respTime, Partition: c.Partition, Reason: "too-large"})
	case res.over:
		emit(e.cfg.Observer, Event{Kind: EvNotStored, Time: res.respTime, Partition: c.Partition, Reason: "too-large"})
		// FR-STO-12, T-37: without a marker every later request would wait
		// for a flight to read MaxObjectBytes and then fetch again. T-31: the
		// response's size is the response's own, unless an unkeyed input
		// could have chosen it.
		// A 5xx is the origin failing, not the object being large: no
		// marker, and the negative entry setNegative may have written stays.
		// Any other error status leaves the response the request found.
		if status := res.resp.StatusCode; !serverError(status) && !c.Authorized && !c.ReqCC.NoStore && !c.Unkeyed {
			e.setMarker(context.WithoutCancel(ctx), sp, supersedes(sp, status, false), res.respTime, true)
		}
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
	// FR-SRV-2: a response built from the entry just stored is judged
	// against the client's own preconditions, like a hit. fr.entry is the
	// final entry, never the discarded 304 of an entity-tag mismatch.
	// FR-STO-6: the creator's own response keeps a Set-Cookie the stored
	// entry dropped, so that response is not replaced by the stored 304.
	if fr.entry != nil && len(resp.Header["Set-Cookie"]) == 0 && clientNotModified(&c.ClientCond, fr.entry) {
		closeBody(resp)
		return e.fromEntry(c, fr.entry, time.Now(), fr.ci)
	}
	switch {
	case c.Head:
		closeBody(resp) // an over-size or event-stream body is canceled, not downloaded
		resp.Body = http.NoBody
	case !fr.over && !fr.stream && len(fr.body) > 0:
		resp.Body = io.NopCloser(bytes.NewReader(fr.body))
	}
	return e.finish(resp, fr.ci)
}

// forcesValidation reports the request directives that turn an entry of the
// given age, with remaining freshness left (negative when stale), into one
// that needs validation: no-cache, a max-age the entry is older than, and a
// min-fresh it cannot meet (RFC 9111 §5.2.1). Classify clears them unless
// Client.HonorRevalidation is set (FR-SRV-8, D5). An argument that is not
// delta-seconds asks for nothing. max-stale is not honored: it loosens, and
// a shared cache may ignore it.
func forcesValidation(cc *httpcc.RequestDirectives, age, remaining time.Duration) bool {
	switch {
	case cc.NoCache:
		return true
	// max-age=0 forces validation of a response of age 0 too.
	case cc.MaxAge.Set && !cc.MaxAge.Invalid && (cc.MaxAge.V == 0 || age > time.Duration(cc.MaxAge.V)*time.Second):
		return true
	case cc.MinFresh.Set && !cc.MinFresh.Invalid && remaining < time.Duration(cc.MinFresh.V)*time.Second:
		return true
	}
	return false
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
			// A full response replaces what the request found (RFC 9111
			// §4.3.3), and this one says the URI is no longer storable. A 304
			// only confirmed the stored body, so it leaves the response in
			// place: revalidating it costs the origin less than a 200.
			e.setMarker(ctx, sp, supersedes(sp, recv.StatusCode, res.notMod), res.respTime, false)
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

// varyCASAttempts bounds the compare-and-set retries of one setVariant. Each
// lost swap means another writer changed the spec, and at most MaxVariants
// such changes can add a reference before the spec is full, so the bound only
// matters when writers keep replacing refs of the same variant.
const varyCASAttempts = 16

// setVariant stores ent under its variant key vk, then the vary spec under
// the primary key that lists it (04 §6.7). Variant first, so a reader that
// finds the spec usually finds the variant. A spec with other names is
// replaced and its variants age out. Refs past their Expires are dropped,
// and at the cap those without a record too, freeing their slots (D37); a new variant past
// MaxVariants live ones is not stored (FR-KEY-10, NFR-3). With a store that
// has store.VarySetter the spec write is a compare-and-set, so concurrent
// writers cannot lose each other's refs.
func (e *Engine) setVariant(ctx context.Context, c *keys.Classified, vk store.Key, ent *store.Entry) bool {
	if e.sg.vary != nil {
		return e.setVariantCAS(ctx, c, vk, ent)
	}
	cur, _ := e.sg.get(ctx, c.Primary)
	spec := e.nextSpec(ctx, c, vk, ent, cur)
	if spec == nil || e.sg.set(ctx, vk, ent) != nil {
		return false
	}
	return e.sg.set(ctx, c.Primary, spec) == nil
}

// setVariantCAS is setVariant for a store with store.VarySetter. The variant
// is written once, before the first swap. Whenever the variant ends up not
// listed (the spec is full, a store call failed, 16 swaps were lost), it is
// deleted again, best effort: left in place it would be found by variant key
// although no spec lists it, and the cap would not hold. The delete can remove
// a record that a concurrent writer of the same variant has since listed; the
// spec then lists a ref without a record, which the reclaim path (D37) frees
// and a request turns into one extra miss.
//
// ponytail: the reclaim reads in nextSpec repeat on every attempt, so a full
// spec costs up to 16 * MaxVariants reads; the ceiling is ctx, and the upgrade
// is to reclaim on the first attempt only.
func (e *Engine) setVariantCAS(ctx context.Context, c *keys.Classified, vk store.Key, ent *store.Entry) (stored bool) {
	written := false
	defer func() {
		if written && !stored {
			// The caller's context may be the reason for the failure.
			dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.cfg.Timeouts.Store)
			defer cancel()
			_ = e.sg.del(dctx, vk)
		}
	}()
	for range varyCASAttempts {
		cur, err := e.sg.get(ctx, c.Primary)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return false
		}
		spec := e.nextSpec(ctx, c, vk, ent, cur)
		if spec == nil {
			return false
		}
		if !written {
			if e.sg.set(ctx, vk, ent) != nil {
				return false
			}
			written = true
		}
		swapped, err := e.sg.setVarySpec(ctx, c.Primary, cur, spec)
		if err != nil {
			return false
		}
		if swapped {
			return true
		}
	}
	return false
}

// nextSpec builds the vary spec that lists ent's variant on top of cur, the
// record read at the primary key (nil: none). It returns nil, after emitting
// EvVaryOverflow, when the cap leaves no slot. cur is not modified (P4).
func (e *Engine) nextSpec(ctx context.Context, c *keys.Classified, vk store.Key, ent, cur *store.Entry) *store.Entry {
	now := ent.StoredAt
	var refs []store.VariantRef // a new slice: the stored spec is immutable (P4)
	if cur != nil && cur.Kind == store.KindVarySpec && slices.Equal(cur.VaryNames, ent.VaryNames) {
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
		return nil
	}
	refs = append(refs, store.VariantRef{Key: vk, Expires: ent.Expires})
	spec := &store.Entry{Kind: store.KindVarySpec, StoredAt: now, VaryNames: ent.VaryNames, Variants: refs}
	for _, r := range refs { // 04 §4.2: a spec lives as long as its longest variant
		spec.Expires = later(spec.Expires, r.Expires)
	}
	return spec
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

// supersedes returns the stored response that a marker for an unstorable
// answer of the given status may replace (FR-STO-12). A successful or
// redirecting full response is a new representation and supersedes the
// response the request found. A 304 only confirmed that response, and an
// error status (a 429, a 507, any 4xx or 5xx the configuration does not
// store) says nothing about it: both leave it in place, where it can still
// be revalidated or served under stale-if-error, and the marker may replace
// only a hard-purged one.
func supersedes(sp *fetchSpec, status int, notModified bool) *store.Entry {
	if notModified || status >= 400 {
		return sp.purged
	}
	return sp.found
}

// markerOversize is the Status of a hit-for-miss marker written because the
// object exceeded MaxObjectBytes. A marker has no status of its own, and an
// engine that does not know this one reads the marker as any other.
const markerOversize = http.StatusRequestEntityTooLarge

// setMarker writes a hit-for-miss marker under sp's coalescing key at now
// (FR-STO-12), unless the key holds a response other than replace.
func (e *Engine) setMarker(ctx context.Context, sp *fetchSpec, replace *store.Entry, now time.Time, oversize bool) {
	m := &store.Entry{Kind: store.KindHitForMiss, StoredAt: now, Expires: now.Add(e.cfg.Coalesce.HitForMissTTL)}
	if oversize {
		m.Status = markerOversize
	}
	e.setUnlessResponse(ctx, sp.lk.ck, replace, m)
}

// setUnlessResponse writes a hit-for-miss marker or negative entry unless
// the key holds a response, which a concurrent fetch may just have stored
// (04 §6.7) and which may still be revalidated (FR-NEG-1), or a vary spec,
// whose variants a record at the primary key would hide. replace is the one
// response that may go: the hard-purged response this request found, which
// can never be served or revalidated, or for a marker the response the
// request found and a full response has now superseded (FR-STO-12). It is
// matched by its request and response times, so a store that decodes a fresh
// copy per Get matches too. A record past its Expires that a store still
// returns lazily is a miss to lookup, so it is replaced too, or a lazy store
// would never get a negative entry (T6.10).
func (e *Engine) setUnlessResponse(ctx context.Context, k store.Key, replace, rec *store.Entry) {
	if cur, err := e.sg.get(ctx, k); err == nil && (cur.Kind == store.KindResponse && !sameRecord(cur, replace) || cur.Kind == store.KindVarySpec) &&
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
