package weir

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/internal/limiter"
	"github.com/AshwinSathian/weir/store"
)

// healthStatus returns the status a negative entry records for res when it
// is an origin-health failure (01 §2): a transport error, an origin timeout,
// or a 502, 503 or 504 response. Anything else, including a fetch whose
// caller left, returns 0.
func healthStatus(ctx context.Context, res *fetchResult) int {
	if res.err != nil {
		if ctx.Err() == nil && (errors.Is(res.err, ErrOriginTimeout) || errors.Is(res.err, ErrOrigin)) {
			return StatusCode(res.err)
		}
		return 0
	}
	if s := res.resp.StatusCode; s != http.StatusInternalServerError && serverError(s) {
		return s
	}
	return 0
}

// setNegative records a negative entry for a foreground fetch that ended in
// an origin-health failure on a key holding no response (FR-NEG-1). The
// request exclusions are FR-NEG-4 and T-31: an input the origin saw but the
// key does not (Authorization, unkeyed fields) must not fail the key for
// everyone. Bypassed, unsafe and Range requests never reach here today
// (cacheable sends them to pass); the Range check keeps that true if the
// routing changes.
func (e *Engine) setNegative(ctx context.Context, sp *fetchSpec, res *fetchResult) {
	c := sp.c
	if e.cfg.Negative.Disable || sp.class != limiter.Foreground || sp.lk.entry != nil ||
		c.Authorized || c.ReqCC.NoStore || c.Unkeyed || c.Range {
		return
	}
	st := healthStatus(ctx, res)
	if st == 0 {
		return
	}
	now := time.Now()
	neg := &store.Entry{Kind: store.KindNegative, Status: st, StoredAt: now, Expires: now.Add(e.cfg.Negative.TTL)}
	if res.err == nil {
		neg.RetryAfter = retryAfter(res.resp.Header.Get("Retry-After"), now)
	}
	e.setUnlessResponse(context.WithoutCancel(ctx), c.Primary, sp.purged, neg)
}

// retryAfter parses a Retry-After value (RFC 9110 §10.2.3), delay-seconds or
// an HTTP-date, in whole seconds. Invalid or past values give 0, which
// serves no Retry-After.
func retryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		if n <= 0 || n > math.MaxInt64/int64(time.Second) {
			return 0
		}
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now).Truncate(time.Second)
	}
	return 0
}

// fromNegative synthesizes the response for a live negative entry: its
// status, its Retry-After, nothing from the origin (FR-NEG-2, FR-NEG-3).
func (e *Engine) fromNegative(c *keys.Classified, neg *store.Entry, now time.Time) *Response {
	h := http.Header{}
	if ra := neg.RetryAfter; ra > 0 {
		h["Retry-After"] = []string{strconv.FormatInt(int64(ra/time.Second), 10)}
	}
	emit(e.cfg.Observer, Event{Kind: EvNegativeServed, Time: now, Partition: c.Partition})
	return e.finish(&Response{StatusCode: neg.Status, Header: h, Body: http.NoBody},
		CacheInfo{Hit: true, TTL: neg.Expires.Sub(now), Detail: "negative"})
}
