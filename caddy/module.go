// Package weircaddy is the Caddy adapter for Weir: the HTTP handler module
// http.handlers.weir (docs/08 §1).
package weircaddy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/store/memory"
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
	if h.MaxBytes == 0 {
		// ponytail: auto-sizing lands in P2-04; until then the store default
		// (256 MiB) applies.
		cfg.Logger.Debug("weir: max_bytes unset, using the store default", "name", h.Name)
	}

	load := any(ctx.GetMetricsRegistry())
	keyGen := keyGenHash(cfg)
	p, keyChanged, err := stores.acquire(load, h.storeSpec(), keyGen,
		func() (*memory.Store, error) { return h.buildStore(keyGen) })
	if err != nil {
		return err
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
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	err := h.engine.Close(ctx)
	return errors.Join(err, h.release())
}

// ServeHTTP implements caddyhttp.MiddlewareHandler. Serving arrives with
// P2-03; until then the handler passes every request to the next handler so
// a provisioned module never blocks traffic.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	return next.ServeHTTP(w, r)
}

// Interface guards.
var (
	_ caddy.Module                = (*Handler)(nil)
	_ caddy.Provisioner           = (*Handler)(nil)
	_ caddy.Validator             = (*Handler)(nil)
	_ caddy.CleanerUpper          = (*Handler)(nil)
	_ caddyhttp.MiddlewareHandler = (*Handler)(nil)
)
