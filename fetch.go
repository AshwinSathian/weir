package weir

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

var (
	errNilResponse = errors.New("weir: origin returned no response and no error")
	errBadStatus   = errors.New("weir: origin returned an informational or invalid status")
)

// fetchResult is what fetch produced: a response the caller owns, or an error.
type fetchResult struct {
	resp *Response
	err  error
}

// fetch is the only caller of Origin.Fetch (P3, 04 §6.7). The limiter,
// breaker and buffered storage paths join it in their cards; today it
// streams the response and the origin timeout bounds the body read.
func (e *Engine) fetch(ctx context.Context, req *Request, origin Origin) fetchResult {
	tctx, cancel := context.WithTimeoutCause(ctx, e.cfg.Timeouts.Origin, ErrOriginTimeout)
	resp, err := safeFetch(tctx, origin, req)
	if err != nil {
		cancel()
		return fetchResult{err: timeoutOrOrigin(ctx, tctx, err)}
	}
	resp.Cache = CacheInfo{} // ignored on origin responses (01 §4); Serve sets it
	if resp.Body == http.NoBody {
		cancel() // nothing left to bound; keeps NoBody visible to adapters
	} else {
		resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	}
	return fetchResult{resp: resp}
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
