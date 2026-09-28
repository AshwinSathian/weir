package weir

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/store"
)

var (
	errNilResponse = errors.New("weir: origin returned no response and no error")
	errBadStatus   = errors.New("weir: origin returned an informational or invalid status")
)

// fetchResult is what fetch produced: a response the caller owns, or an
// error. A buffered fetch also returns the body it read.
type fetchResult struct {
	resp     *Response
	recv     http.Header // resp.Header as received, when it had Connection; storability reads it (FR-FWD-7, T-8)
	body     []byte      // buffered fetches: the whole body; nil when over
	notMod   bool        // resp is a 304 validating the prior entry (FR-SRV-3)
	over     bool        // buffered fetches: the body exceeded MaxObjectBytes and resp.Body streams it all
	stream   bool        // an event-stream response; resp.Body streams it, never stored (FR-STR-1)
	reqTime  time.Time
	respTime time.Time // when the buffered body ended, or the headers arrived
	err      error
}

// fetch is the only caller of Origin.Fetch (P3, 04 §6.7). The limiter and
// breaker join it in their cards. A streamed fetch returns resp.Body for the
// caller to read; a buffered one reads up to MaxObjectBytes and, when the
// body is larger, returns a stream of the whole body instead of storing it.
// The origin timeout bounds the body read either way.
//
// A non-nil prior, which must have validators, makes the fetch conditional
// (FR-SRV-3). A 304 whose strong ETag differs from prior's is discarded and
// the request repeated without conditionals under the same timeout (and,
// from M4, the same limiter slot). A 304 to that retry is an origin
// misbehaving; it passes through unstored like any other 304.
func (e *Engine) fetch(ctx context.Context, req *Request, origin Origin, buffered bool, prior *store.Entry) fetchResult {
	tctx, cancel := context.WithTimeoutCause(ctx, e.cfg.Timeouts.Origin, ErrOriginTimeout)
	fwd := req
	if prior != nil {
		fwd = withValidators(req, prior)
	}
	reqTime := time.Now()
	resp, err := safeFetch(tctx, origin, fwd)
	if err == nil && prior != nil && resp.StatusCode == http.StatusNotModified && strongETagMismatch(resp.Header, prior.ETag) {
		closeBody(resp)
		prior = nil
		reqTime = time.Now()
		resp, err = safeFetch(tctx, origin, req)
	}
	if err != nil {
		cancel()
		return fetchResult{err: timeoutOrOrigin(ctx, tctx, err)}
	}
	resp.Cache = CacheInfo{} // ignored on origin responses (01 §4); Serve sets it
	// Serve adds Cache-Status; an Origin may reuse its header map (INV-4).
	// Value slices stay shared, and only full slice expressions append to them.
	resp.Header = maps.Clone(resp.Header)
	res := fetchResult{resp: resp, reqTime: reqTime, notMod: prior != nil && resp.StatusCode == http.StatusNotModified}
	if len(resp.Header["Connection"]) > 0 {
		res.recv = maps.Clone(resp.Header)
	}
	// FR-FWD-7: strip once here, on the single origin path (P3), so no
	// adapter or stored entry sees the origin connection's fields.
	keys.DropHopByHop(resp.Header, resp.Header["Connection"])
	if res.notMod { // a 304 body means nothing; never read or stream it (04 §6.7)
		closeBody(resp)
		cancel()
		resp.Body = http.NoBody
		res.respTime = time.Now()
		return res
	}
	if !buffered || isEventStream(res.received().Header, e.cfg.Storable.StreamTypes) {
		res.respTime = time.Now()
		res.stream = buffered
		if resp.Body == http.NoBody {
			cancel() // nothing left to bound; keeps NoBody visible to adapters
		} else {
			resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
		}
		return res
	}
	limit := e.cfg.Storable.MaxObjectBytes
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	res.respTime = time.Now()
	if rerr != nil { // a truncated body is never stored or served
		closeBody(resp)
		cancel()
		return fetchResult{err: timeoutOrOrigin(ctx, tctx, rerr)}
	}
	if int64(len(body)) > limit {
		rest := resp.Body
		resp.Body = &cancelOnClose{ReadCloser: readCloser{io.MultiReader(bytes.NewReader(body), rest), rest}, cancel: cancel}
		res.over = true
		return res
	}
	closeBody(resp)
	cancel()
	resp.Body = http.NoBody
	res.body = body
	return res
}

// received returns resp with its header as the origin sent it. Engine
// decisions read it; the served and stored copies lose the hop-by-hop
// fields (FR-FWD-7).
func (r *fetchResult) received() *Response {
	if r.recv == nil {
		return r.resp
	}
	c := *r.resp
	c.Header = r.recv
	return &c
}

// isEventStream reports whether h names text/event-stream or a type in
// extra as its Content-Type (FR-STR-1). Parameters and case are ignored
// (RFC 9110 §8.3.1); extra is already lowercased (config.go canonicalize).
func isEventStream(h http.Header, extra []string) bool {
	ct := h.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	return ct == "text/event-stream" || slices.Contains(extra, ct)
}

// readCloser joins a reader with the closer of the body it reads.
type readCloser struct {
	io.Reader
	io.Closer
}

// safeFetch calls origin.Fetch and turns misbehavior into errors: a panic,
// (nil, nil), and a status net/http would reject or treat as interim (NFR-2).
// A nil Header or Body on a valid response is filled in.
func safeFetch(ctx context.Context, origin Origin, req *Request) (resp *Response, err error) {
	defer func() {
		if r := recover(); r != nil {
			resp, err = nil, fmt.Errorf("weir: origin panic: %v", r)
		}
	}()
	resp, err = origin.Fetch(ctx, req)
	switch {
	case err != nil:
		closeBody(resp)
		return nil, err
	case resp == nil:
		return nil, errNilResponse
	case resp.StatusCode < 200 || resp.StatusCode > 999:
		closeBody(resp)
		return nil, errBadStatus
	}
	normalizeResponse(resp)
	return resp, nil
}

func closeBody(r *Response) {
	if r != nil && r.Body != nil {
		_ = r.Body.Close() // the response is discarded; its close error changes nothing
	}
}

// timeoutOrOrigin maps a fetch error from the contexts, never from err, so an
// origin cannot pose as a caller cancellation or timeout (04 §1.3). ctx is
// the request context; flights will map its end to ErrClosed.
func timeoutOrOrigin(ctx, tctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if errors.Is(context.Cause(tctx), ErrOriginTimeout) {
		return ErrOriginTimeout
	}
	return &OriginError{Err: err}
}

// cancelOnClose keeps the origin timeout context alive until the consumer
// closes the body.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}
