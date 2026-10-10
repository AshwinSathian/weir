// Package weircaddy is the Caddy adapter for Weir: the HTTP handler module
// http.handlers.weir (docs/08 §1).
package weircaddy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/store/memory"
	"github.com/AshwinSathian/weir/weirhttp"
)

func init() { caddy.RegisterModule(&Handler{}) }

// closeTimeout bounds the engine close in Cleanup, which takes no context
// (08 §3).
const closeTimeout = 5 * time.Second

// Handler is the http.handlers.weir module. JSON mirrors weir.Config with
// snake_case names (docs/08 §2).
type Handler struct {
	// Name is required: store identity across reloads, admin URL and
	// metrics label.
	Name        string        `json:"name,omitempty"`
	MaxBytes    ByteSize      `json:"max_bytes,omitempty"`
	SnapshotDir string        `json:"snapshot_dir,omitempty"`
	Key         KeyConfig     `json:"key,omitempty"`
	Forward     ForwardConfig `json:"forward,omitempty"`
	Bypass      BypassConfig  `json:"bypass,omitempty"`
	Limiter     LimiterConfig `json:"limiter,omitempty"`
	Stale       StaleConfig   `json:"stale,omitempty"`
	// MultiHost says the site serves more than one host or uses on-demand
	// TLS. It turns on the per-host fairness caps at 25% (FR-FAIR-3) and is
	// part of the store identity (08 §3, §4b).
	MultiHost bool `json:"multi_host,omitempty"`

	engine *weir.Engine
	// pool and release are set by a successful Provision and not changed
	// afterwards. release gives back the one store reference; closed makes
	// Cleanup run it at most once.
	pool    *pooledStore
	release func() error
	closed  *atomic.Bool // set by Provision; a pointer so Handler stays copyable
	// admin is the registry entry for the admin API (08 §7); set last by a
	// successful Provision, removed first by Cleanup.
	admin *adminEntry

	// Serving state, set by Provision. firstHost is the only host the handler
	// remembers (NFR-3); warnedHosts makes the multi_host warning one-shot.
	log         *slog.Logger
	httpApp     func() (any, error)
	chainOnce   *sync.Once
	firstHost   *atomic.Pointer[string]
	warnedHosts *atomic.Bool
}

// CaddyModule implements caddy.Module.
func (*Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.weir",
		New: func() caddy.Module { return new(Handler) },
	}
}

// Provision implements caddy.Provisioner. weir.New validates the engine
// config, so a bad one fails here and in caddy validate (08 §2). The memory
// store comes from the process-wide pool so it survives a reload (08 §3); a
// failed Provision releases whatever it took, so Cleanup has nothing to undo.
func (h *Handler) Provision(ctx caddy.Context) (err error) {
	if h.engine != nil {
		return errors.New("weir: handler is already provisioned")
	}
	if err := h.Validate(); err != nil {
		return err
	}
	cfg := h.weirConfig()
	cfg.Logger = ctx.Slogger()
	tap := newPurgeTap()
	cfg.Observer = tap
	load := any(ctx.GetMetricsRegistry())
	keyGen := keyGenHash(cfg)
	var built bool // the pool called the constructor: a new store, not a reuse
	p, keyChanged, err := stores.acquire(load, h.storeSpec(), keyGen,
		func() (*memory.Store, int64, error) {
			built = true
			size := int64(h.MaxBytes)
			if size == 0 {
				size = stores.autoSize(debug.SetMemoryLimit(-1), cfg.Logger, h.Name)
			}
			st, err := h.buildStore(keyGen, size)
			return st, size, err
		})
	if err != nil {
		return err
	}
	if built {
		stores.checkOvercommit(cfg.Logger, h.Name)
	}
	release := func() error {
		p.dropHolder(h)
		return stores.release(load, p)
	}
	defer func() {
		if err != nil {
			_ = release()
		}
	}()

	cfg.Store = p.store
	e, err := weir.New(cfg)
	if err != nil {
		return err // already prefixed "weir:"
	}
	if keyChanged {
		// R-3: serve nothing stored under the old forwarding rules. Failing
		// Provision is the safe side if the epoch cannot be written.
		pctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		if perr := e.Purge(pctx, weir.Purge{All: true, Mode: weir.PurgeHard}); perr != nil {
			cctx, ccancel := context.WithTimeout(context.Background(), closeTimeout)
			defer ccancel()
			_ = e.Close(cctx)
			return fmt.Errorf("weir: key-generation change: %w", perr)
		}
	}
	// Record the hash only now: a Provision that failed above must not
	// make a retry of the same config skip the purge.
	p.commitKeyGen(h, keyGen)
	h.engine, h.pool, h.release, h.closed = e, p, release, new(atomic.Bool)
	h.log, h.chainOnce = cfg.Logger, new(sync.Once)
	h.firstHost, h.warnedHosts = new(atomic.Pointer[string]), new(atomic.Bool)
	h.httpApp = func() (any, error) { return ctx.AppIfConfigured("http") }
	h.admin = &adminEntry{engine: e, tap: tap}
	engines.add(h.Name, h.admin)
	return nil
}

// Validate implements caddy.Validator. Caddy runs it after Provision, so it
// re-checks only the adapter's own fields (08 §2).
func (h *Handler) Validate() error {
	if err := validateName(h.Name); err != nil {
		return err
	}
	if h.MaxBytes != 0 && h.MaxBytes < minStoreBytes {
		return fmt.Errorf("weir: max_bytes must be at least %d MiB (the largest cacheable object is 10%% of a shard)", minStoreBytes>>20)
	}
	return validateSnapshotDir(h.SnapshotDir)
}

// Cleanup implements caddy.CleanerUpper. It is safe to call twice: the
// engine closes first, then the one store reference is released, which closes
// the store only when no other instance holds it (08 §3). h.engine stays set,
// because requests still running on the old config read it; the closed
// engine rejects them with ErrClosed.
func (h *Handler) Cleanup() error {
	if h.engine == nil || h.closed.Swap(true) {
		return nil
	}
	engines.remove(h.Name, h.admin) // before Close: no new admin call reaches a closing engine
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	err := h.engine.Close(ctx)
	return errors.Join(err, h.release())
}

// ServeHTTP implements caddyhttp.MiddlewareHandler (08 §4). CONNECT and
// upgrades go to next without touching the engine (FR-UPG-1). Engine errors
// come back as caddyhttp errors so the operator's handle_errors routes run
// (D34).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if keys.IsUpgrade(r.Method, r.Header) {
		return next.ServeHTTP(w, r)
	}
	h.chainOnce.Do(h.warnChain)
	h.noteHost(r.Host)
	resp, err := h.engine.Serve(r.Context(), requestFor(r), newNextOrigin(next, r, h.log))
	if err != nil {
		return serveError(w, err)
	}
	_ = weirhttp.WriteResponse(w, resp) // the client is gone; nothing left to tell it
	return nil
}

// requestFor builds the engine request. It prefers the bytes the client sent
// (RequestURI, T-6), but a rewrite earlier in the route (rewrite, handle_path,
// uri strip_prefix) changes only r.URL, and keying or forwarding the old path
// would undo it. Caddy keeps the unmodified request in the context, so a
// rewrite is detected by comparing against it.
func requestFor(r *http.Request) *weir.Request {
	req := weirhttp.RequestFrom(r)
	if orig, ok := r.Context().Value(caddyhttp.OriginalRequestCtxKey).(http.Request); ok && orig.URL != nil &&
		(orig.URL.Path != r.URL.Path || orig.URL.RawPath != r.URL.RawPath || orig.URL.RawQuery != r.URL.RawQuery) {
		req.Path, req.RawQuery = r.URL.EscapedPath(), r.URL.RawQuery
	}
	return req
}

// serveError sets Retry-After before returning the error: Caddy's error
// path writes the status on the same ResponseWriter, so the header survives
// (08 §4 step 4).
func serveError(w http.ResponseWriter, err error) error {
	code := weir.StatusCode(err)
	if code == 499 {
		return nil // the client is gone: no response, no handle_errors route
	}
	if d, ok := weir.RetryAfter(err); ok {
		w.Header().Set("Retry-After", strconv.FormatInt(int64(d/time.Second), 10))
	}
	return caddyhttp.Error(code, err)
}

// noteHost warns once when a second distinct Host arrives while multi_host is
// off (FR-FAIR-3): the per-host caps would be missing on a shared cache. Only
// the first host is kept, so request input never grows a set.
func (h *Handler) noteHost(host string) {
	if h.MultiHost {
		return
	}
	if hp, _, err := net.SplitHostPort(host); err == nil {
		host = hp // example.com and example.com:443 are one site
	}
	first := h.firstHost.Load()
	if first == nil {
		if h.firstHost.CompareAndSwap(nil, &host) {
			return
		}
		first = h.firstHost.Load()
	}
	if !strings.EqualFold(*first, host) && !h.warnedHosts.Swap(true) {
		h.log.Warn("weir: requests for more than one host reach this handler; set multi_host to enable the per-host fairness caps (docs/08 §2)",
			"name", h.Name)
	}
}

// warnChain logs the one-time 08 §6 warnings for the handlers behind weir.
// It runs on the first request because the route that holds this handler
// exists only after the http app finished provisioning.
func (h *Handler) warnChain() {
	if h.httpApp == nil {
		return
	}
	// Best effort (08 §6): a context without a config, as unit tests build,
	// makes ctx.App panic, and a warning must never fail a request.
	defer func() {
		if v := recover(); v != nil {
			h.log.Debug("weir: chain inspection skipped", "reason", v)
		}
	}()
	v, err := h.httpApp()
	if err != nil {
		return
	}
	app, ok := v.(*caddyhttp.App)
	if !ok {
		return
	}
	for _, msg := range appWarnings(app, h) {
		h.log.Warn(msg)
	}
}

// Interface guards.
var (
	_ caddy.Module                = (*Handler)(nil)
	_ caddy.Provisioner           = (*Handler)(nil)
	_ caddy.Validator             = (*Handler)(nil)
	_ caddy.CleanerUpper          = (*Handler)(nil)
	_ caddyhttp.MiddlewareHandler = (*Handler)(nil)
)
