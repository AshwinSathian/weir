package weir

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/AshwinSathian/weir/internal/keys"
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
	cfg      Config      // defaults applied, immutable after New
	kcfg     keys.Config // compiled from cfg
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
	e := &Engine{cfg: c, kcfg: keysConfig(&c), store: c.Store, ownStore: own, closeDone: make(chan struct{})}
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
	r.Header = canonicalHeader(r.Header)
	if r.Body == nil {
		r.Body = http.NoBody
	}
}

// canonicalHeader returns h with every key canonical, merging duplicates
// canonical-first and then in sorted key order. storability and
// ParseResponse read canonical keys, so a custom Origin's "set-cookie" or
// "cache-control: private" must not slip past them (INV-4, T-8). h is
// returned as is when already canonical; otherwise a new map is built and
// no value array of h is written, since an Origin may reuse them.
func canonicalHeader(h http.Header) http.Header {
	var odd []string
	for k := range h {
		if http.CanonicalHeaderKey(k) != k {
			odd = append(odd, k)
		}
	}
	if len(odd) == 0 {
		return h
	}
	out := make(http.Header, len(h))
	for k, v := range h {
		if http.CanonicalHeaderKey(k) == k {
			out[k] = v
		}
	}
	slices.Sort(odd)
	for _, k := range odd {
		ck := http.CanonicalHeaderKey(k)
		out[ck] = append(slices.Clip(out[ck]), h[k]...)
	}
	return out
}
