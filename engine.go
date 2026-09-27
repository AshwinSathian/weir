package weir

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

// Origin produces responses for forwarded requests. Fetch must honor ctx:
// return promptly once ctx is done, and make body reads fail after that.
// Close depends on it (FR-LCY-2). Fetch may be called after the request that
// triggered it has finished (background refresh), and concurrently.
type Origin interface {
	Fetch(ctx context.Context, req *Request) (*Response, error)
}

// OriginFunc adapts a function to the Origin interface.
type OriginFunc func(ctx context.Context, req *Request) (*Response, error)

// Fetch calls f.
func (f OriginFunc) Fetch(ctx context.Context, req *Request) (*Response, error) { return f(ctx, req) }

// Engine makes shared-cache decisions per request (04 §6.1). It is safe for
// concurrent use (FR-LCY-3).
type Engine struct {
	cfg      Config // defaults applied, immutable after New
	store    store.Store
	ownStore bool

	mu        sync.Mutex  // orders setting closed against wg.Add in goBackground
	closed    atomic.Bool // written under mu; read without it on the Serve path
	closeDone chan struct{}
	bgCtx     context.Context // canceled after Close's grace period
	bgCancel  context.CancelFunc
	wg        sync.WaitGroup // every engine-owned goroutine (04 §12)
}

// New validates cfg, applies defaults and returns a ready engine. It returns
// an error wrapping ErrInvalidConfig for any invalid field (FR-LCY-1).
func New(cfg Config) (*Engine, error) {
	c, err := prepareConfig(cfg)
	if err != nil {
		return nil, err
	}
	own := c.Store == nil
	if own {
		// ponytail: fixed 256 MiB until FR-MEM-1 sizing from GOMEMLIMIT (M1-15).
		m, err := memory.New(memory.Config{})
		if err != nil {
			return nil, err
		}
		c.Store = m
	}
	if sz, ok := c.Store.(store.Sizer); ok && c.Storable.MaxObjectBytes > sz.MaxObjectBytes() {
		if own {
			_ = c.Store.Close()
		}
		return nil, invalid("Storable.MaxObjectBytes", fmt.Sprintf("above the store's limit of %d", sz.MaxObjectBytes()))
	}
	warnForwarding(c)
	e := &Engine{cfg: c, store: c.Store, ownStore: own, closeDone: make(chan struct{})}
	e.bgCtx, e.bgCancel = context.WithCancel(context.Background())
	return e, nil
}

// warnForwarding logs forwarding settings that send unkeyed credentials to
// the origin (R-3).
func warnForwarding(c Config) {
	if c.Forward.Mode == ForwardAll {
		c.Logger.Warn("weir: Forward.Mode is ForwardAll; unkeyed request headers reach the origin")
	}
	for _, h := range c.Forward.Allow {
		switch h {
		case "Cookie", "Authorization", "Proxy-Authorization":
			c.Logger.Warn("weir: Forward.Allow forwards a credential header without keying it", "header", h)
		}
	}
}

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
	// ponytail: every request passes through unchanged until classification
	// and forwarding land (M1-07).
	fwd := *req
	res := e.fetch(ctx, &fwd, origin)
	if res.err != nil {
		return nil, res.err
	}
	return res.resp, nil
}

// goBackground runs f on an engine-owned goroutine with the background
// context, which Close cancels after its grace period. It reports false,
// without running f, once the engine is closed.
func (e *Engine) goBackground(f func(context.Context)) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed.Load() {
		return false
	}
	e.wg.Go(func() { f(e.bgCtx) })
	return true
}

// Close rejects new Serve calls with ErrClosed, waits for engine goroutines
// until ctx is done, then cancels the rest and waits for them (FR-LCY-2). It
// returns ctx's error when the grace period ran out. It closes the store only
// if the engine created it. Later calls wait for the first until their own
// ctx is done and return nil or ctx's error.
func (e *Engine) Close(ctx context.Context) error {
	e.mu.Lock()
	first := !e.closed.Swap(true)
	e.mu.Unlock()
	if !first {
		select {
		case <-e.closeDone:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	defer close(e.closeDone)
	// No wg.Add can follow: goBackground checks closed under mu.
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	var graceErr error
	select {
	case <-done:
	case <-ctx.Done():
		graceErr = ctx.Err()
	}
	e.bgCancel()
	<-done
	var storeErr error
	if e.ownStore {
		storeErr = e.store.Close()
	}
	return errors.Join(graceErr, storeErr)
}

// normalizeResponse fills a nil Header or Body, which adapters rely on,
// and canonicalizes header keys, merging duplicates.
func normalizeResponse(r *Response) {
	if r.Header == nil {
		r.Header = http.Header{}
	}
	// INV-4, T-8: storability and ParseResponse read canonical keys, so a
	// custom Origin's "set-cookie" or "cache-control" must not slip past
	// them. Deleting during range is safe; added keys are canonical.
	for k, v := range r.Header {
		if ck := http.CanonicalHeaderKey(k); ck != k {
			r.Header[ck] = append(r.Header[ck], v...)
			delete(r.Header, k)
		}
	}
	if r.Body == nil {
		r.Body = http.NoBody
	}
}
