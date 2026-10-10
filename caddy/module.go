// Package weircaddy is the Caddy adapter for Weir: the HTTP handler module
// http.handlers.weir (docs/08 §1).
package weircaddy

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"

	"github.com/AshwinSathian/weir"
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

	engine *weir.Engine
}

// CaddyModule implements caddy.Module.
func (*Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.weir",
		New: func() caddy.Module { return new(Handler) },
	}
}

// Provision implements caddy.Provisioner. weir.New validates the engine
// config, so a bad one fails here and in caddy validate (08 §2). A failed
// weir.New leaves nothing to close.
func (h *Handler) Provision(ctx caddy.Context) error {
	if h.engine != nil {
		return errors.New("weir: handler is already provisioned")
	}
	if err := h.Validate(); err != nil {
		return err
	}
	cfg := h.weirConfig()
	cfg.Logger = ctx.Slogger()
	// ponytail: the store pool and serving land in P2-02 and P2-03; say so
	// instead of silently ignoring what the operator set.
	if h.MaxBytes != 0 || h.SnapshotDir != "" {
		cfg.Logger.Warn("weir: max_bytes and snapshot_dir are parsed but not applied yet (P2-02)", "name", h.Name)
	}
	e, err := weir.New(cfg)
	if err != nil {
		return err // already prefixed "weir:"
	}
	h.engine = e
	return nil
}

// Validate implements caddy.Validator. Caddy runs it after Provision, so it
// re-checks only the adapter's own fields (08 §2).
func (h *Handler) Validate() error { return validateName(h.Name) }

// Cleanup implements caddy.CleanerUpper. It is safe to call twice.
func (h *Handler) Cleanup() error {
	e := h.engine
	if e == nil {
		return nil
	}
	h.engine = nil
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	return e.Close(ctx)
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
