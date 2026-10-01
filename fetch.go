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
	"github.com/AshwinSathian/weir/internal/limiter"
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
	// bgDropped: a background fetch found no limiter slot. Foreground
	// requests that joined its flight fetch again rather than fail (04 §6.8).
	bgDropped bool
}

// fetch is the only caller of Origin.Fetch (P3, 04 §6.7). It forwards
// c.Forwarded holding a limiter slot of class for c's partition, from before
// the request is sent until it returns: after the buffered body, or at the
// headers of a stream (FR-LIM-1, T-18). Requests with a body take the slot
// from the upload pool (FR-LIM-7, T-39). The breaker joins it in M5. A
// streamed fetch returns resp.Body for the caller to read; a buffered one
// reads up to MaxObjectBytes and, when the body is larger, returns a stream
// of the whole body instead of storing it. The origin timeout starts once
// the slot is held and bounds the headers and a buffered body (FR-TMO-1,
// T-41). A stream has no total deadline; a read that makes no progress for
// StreamIdle fails it (FR-TMO-2).
//
// A non-nil prior, which must have validators, makes the fetch conditional
// (FR-SRV-3). A 304 whose strong ETag differs from prior's is discarded and
// the request repeated without conditionals under the same timeout and
// limiter slot. A 304 to that retry is an origin misbehaving; it passes
// through unstored like any other 304.
func (e *Engine) fetch(ctx context.Context, c *keys.Classified, origin Origin, class limiter.Class, buffered bool, prior *store.Entry) fetchResult {
	lim := e.lim
	if c.HasBody {
		lim = e.upl
	}
	permit, err := lim.Acquire(ctx, class, c.PartitionH)
	if err != nil {
		return e.shed(c, class, err)
	}
	defer permit.Release()
	if err := ctx.Err(); err != nil { // granted as the caller left (04 §8.2): send nothing
		return fetchResult{err: err}
	}
	req := (*Request)(&c.Forwarded)
	// A timer, not WithTimeout, so a stream can drop the deadline at its
	// headers and keep the context (04 §14).
	tctx, cancelCause := context.WithCancelCause(ctx)
	deadline := time.AfterFunc(e.cfg.Timeouts.Origin, func() { cancelCause(ErrOriginTimeout) })
	cancel := func() { deadline.Stop(); cancelCause(nil) }
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
			deadline.Stop()
			resp.Body = newIdleBody(tctx, resp.Body, cancelCause, e.cfg.Timeouts.StreamIdle)
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
		deadline.Stop()
		resp.Body = newIdleBody(tctx, readCloser{io.MultiReader(bytes.NewReader(body), rest), rest}, cancelCause, e.cfg.Timeouts.StreamIdle)
		res.over = true
		return res
	}
	closeBody(resp)
	cancel()
	resp.Body = http.NoBody
	res.body = body
	return res
}

// shed maps a limiter refusal to the fetch result (FR-LIM-5, 04 §8.2). A
// context error is the caller's own and passes through.
func (e *Engine) shed(c *keys.Classified, class limiter.Class, err error) fetchResult {
	if !errors.Is(err, limiter.ErrShed) {
		return fetchResult{err: err}
	}
	now := time.Now()
	reason := "background"
	switch {
	case errors.Is(err, limiter.ErrQueueFull):
		reason = "queue-full"
	case errors.Is(err, limiter.ErrQueueTimeout):
		reason = "queue-timeout"
	}
	emit(e.cfg.Observer, Event{Kind: EvShed, Time: now, Partition: c.Partition, Reason: reason})
	if class == limiter.Background {
		emit(e.cfg.Observer, Event{Kind: EvRefreshDropped, Time: now, Partition: c.Partition, Reason: "no-slot"})
	}
	return fetchResult{err: &RetryError{Err: ErrShed, After: e.cfg.Limiter.MaxQueueWait}, bgDropped: class == limiter.Background}
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

// idleBody is a streamed origin body (FR-TMO-2). Each Read arms a timer of
// idle; a Read still blocked when it fires cancels the fetch context with
// ErrOriginTimeout. Time the consumer spends between reads does not count.
// Close cancels the context.
type idleBody struct {
	io.ReadCloser
	ctx    context.Context
	cancel context.CancelCauseFunc
	idle   time.Duration
	timer  *time.Timer
}

func newIdleBody(ctx context.Context, rc io.ReadCloser, cancel context.CancelCauseFunc, idle time.Duration) *idleBody {
	b := &idleBody{ReadCloser: rc, ctx: ctx, cancel: cancel, idle: idle}
	b.timer = time.AfterFunc(idle, func() { cancel(ErrOriginTimeout) })
	b.timer.Stop()
	return b
}

func (b *idleBody) Read(p []byte) (int, error) {
	b.timer.Reset(b.idle)
	n, err := b.ReadCloser.Read(p)
	b.timer.Stop()
	if err != nil && !errors.Is(err, io.EOF) && b.ctx.Err() != nil {
		err = context.Cause(b.ctx) // the origin's own error cannot pose as the timeout (04 §1.3)
	}
	return n, err
}

func (b *idleBody) Close() error {
	b.timer.Stop()
	err := b.ReadCloser.Close()
	b.cancel(nil)
	return err
}

// cancelOnClose runs cancel after the body closes.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}
