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
	"github.com/prometheus/client_golang/prometheus"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/valkey"
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
	Name     string   `json:"name,omitempty"`
	MaxBytes ByteSize `json:"max_bytes,omitempty"`
	// MaxObjectBytes is weir.StorableConfig.MaxObjectBytes: the largest
	// response, headers included, that is stored (FR-STO-9). 0: 1 MiB. A
	// larger response is passed through to every client that asks. Up to
	// max_concurrent fetches buffer a body of this size at once, so the
	// process needs that much memory beside the store.
	MaxObjectBytes ByteSize `json:"max_object_bytes,omitempty"`
	SnapshotDir    string   `json:"snapshot_dir,omitempty"`
	// SnapshotKeepFresh keeps the cache warm across a restart: entries
	// loaded from the snapshot keep their freshness, where without it each
	// is revalidated on first use (memory.Config.SnapshotKeepFreshness,
	// FR-SNP-5). A purge sent while Caddy was down is then not applied to
	// what the snapshot brings back (T-33). It needs snapshot_dir.
	SnapshotKeepFresh bool          `json:"snapshot_keep_fresh,omitempty"`
	Key               KeyConfig     `json:"key,omitempty"`
	Forward           ForwardConfig `json:"forward,omitempty"`
	Bypass            BypassConfig  `json:"bypass,omitempty"`
	Limiter           LimiterConfig `json:"limiter,omitempty"`
	Stale             StaleConfig   `json:"stale,omitempty"`
	// Experiments is the A/B block (docs/10 §3). It changes which key a
	// request maps to, so it is not part of the key-generation hash (08 §3).
	Experiments *ExperimentsConfig `json:"experiments,omitempty"`
	// MultiHost says the site serves more than one host or uses on-demand
	// TLS. It turns on the per-host fairness caps at 25% (FR-FAIR-3) and is
	// part of the store identity (08 §3, §4b).
	MultiHost bool `json:"multi_host,omitempty"`
	// ClientPlaceholders gives the handlers behind weir the replacer of the
	// client whose request triggered a fetch, as before v1.1. Off, the
	// default, each forwarded request has a replacer bound to itself, so
	// {http.request.header.X}, {http.request.cookie.X} and {http.request.uri}
	// read what the origin is sent and nothing a client sent outside the key.
	// Placeholders that a matcher or handler in front of weir defined on the
	// client's replacer ({http.regexp.*}, {http.auth.*}, map outputs) are
	// then empty behind it; turn this on only if the site needs them there
	// and nothing behind weir puts client data into a cacheable fetch (08 §6).
	ClientPlaceholders bool `json:"client_placeholders,omitempty"`
	// Store, when set, replaces the memory store with a shared one (08 §2).
	// max_bytes and snapshot_dir belong to the memory store and are refused
	// with it.
	Store *StoreConfig `json:"store,omitempty"`

	engine *weir.Engine
	// pool and release are set by a successful Provision and not changed
	// afterwards. release gives back the one store reference; closed makes
	// Cleanup run it at most once.
	pool    *pooledStore
	release func() error
	// keyGenRetry is the background key-generation retry after a Provision
	// whose check was skipped or whose record write failed (08 §3); nil otherwise. Cleanup stops it first.
	keyGenRetry *keyGenRetry
	closed      *atomic.Bool // set by Provision; a pointer so Handler stays copyable
	// admin is the registry entry for the admin API (08 §7); set last by a
	// successful Provision, removed first by Cleanup.
	admin *adminEntry
	// metrics is this handler's share of the load's collector set (08 §8).
	metrics    *metricSet
	metricsReg *prometheus.Registry

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
	reg := ctx.GetMetricsRegistry()
	ms, err := metricSets.acquire(reg, h.Name)
	if err != nil {
		return fmt.Errorf("weir: register metrics: %w", err)
	}
	defer func() {
		if err != nil {
			metricSets.release(reg, h.Name, ms)
		}
	}()
	cfg.Observer = fanout{tap, ms.obs}
	load := any(reg)
	spec := h.storeSpec()
	fwdHash := keyGenHash(cfg)
	keyGen := keyGenHashFor(cfg, spec.valkey)
	var built bool // the pool called the constructor: a new store, not a reuse
	p, keyChanged, err := stores.acquire(load, spec, keyGen,
		func(sink *evictSink) (store.Store, int64, error) {
			built = true
			if h.Store != nil {
				// The client connects on first use, so an unreachable server
				// opens the store breaker instead of failing the load
				// (FR-STF-2).
				vc, err := h.Store.valkeyConfig(h.Name)
				if err != nil {
					return nil, 0, err
				}
				st, err := valkey.New(vc)
				if err != nil {
					return nil, 0, err
				}
				return st, 0, nil
			}
			size := int64(h.MaxBytes)
			if size == 0 {
				size = stores.autoSize(debug.SetMemoryLimit(-1), cfg.Logger, h.Name)
			}
			st, err := h.buildStore(keyGen, size, sink)
			return st, size, err
		})
	if err != nil {
		return err
	}
	if built && h.Store == nil {
		stores.checkOvercommit(cfg.Logger, h.Name)
	}
	for _, w := range h.storeWarnings() {
		cfg.Logger.Warn(w, "name", h.Name)
	}
	if built && h.Store != nil {
		// A new pool entry on a shared server: compare the forwarding rules
		// with the engine it replaces (R-3, 08 §3).
		if prev, ok := stores.liveFwd(h.Name, p); ok && prev != fwdHash {
			keyChanged = true
		}
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
	// The server's record covers what the in-process hashes cannot: a restart
	// after forward was tightened (R-3, 08 §3). Failing Provision is the safe
	// side if a purge that is needed cannot be written.
	purge := func(ctx context.Context) error {
		if perr := e.Purge(ctx, weir.Purge{All: true, Mode: weir.PurgeHard}); perr != nil {
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
			defer cancel()
			_ = e.Close(cctx)
			return fmt.Errorf("weir: key-generation change: %w", perr)
		}
		return nil
	}
	retry, err := reconcileServerKeyGen(ctx, p.store, fwdHash, keyChanged, purge, cfg.Logger, h.Name, false)
	if err != nil {
		return err
	}
	// Record the hash only now: a Provision that failed above must not
	// make a retry of the same config skip the purge.
	p.commitKeyGen(h, keyGen, fwdHash)
	h.engine, h.pool, h.release, h.closed = e, p, release, new(atomic.Bool)
	h.log, h.chainOnce = cfg.Logger, new(sync.Once)
	h.firstHost, h.warnedHosts = new(atomic.Pointer[string]), new(atomic.Bool)
	h.httpApp = func() (any, error) { return ctx.AppIfConfigured("http") }
	h.admin = &adminEntry{engine: e, tap: tap}
	h.metrics, h.metricsReg = ms, reg
	ms.addEngine(e)
	p.sink.attach(ms)
	engines.add(h.Name, h.admin)
	if retry {
		// Started last: nothing after this can fail, so Cleanup always
		// reaches stop. The retry's purge leaves the engine open (a failed
		// retry is tried again), unlike the one Provision passes above.
		h.keyGenRetry = startKeyGenRetry(p.store, fwdHash, func(ctx context.Context) error {
			return e.Purge(ctx, weir.Purge{All: true, Mode: weir.PurgeHard})
		}, cfg.Logger, h.Name)
	}
	return nil
}

// Validate implements caddy.Validator. Caddy runs it after Provision, so it
// re-checks only the adapter's own fields (08 §2).
func (h *Handler) Validate() error {
	if err := validateName(h.Name); err != nil {
		return err
	}
	if h.Store != nil {
		if h.MaxBytes != 0 || h.SnapshotDir != "" || h.SnapshotKeepFresh {
			return errors.New("weir: max_bytes, snapshot_dir and snapshot_keep_fresh size and persist the memory store; they cannot be combined with a store block")
		}
		if err := h.Store.validate(h.Name); err != nil {
			return err
		}
	}
	if err := h.Experiments.validate(); err != nil {
		return err
	}
	if h.MaxBytes != 0 && h.MaxBytes < minStoreBytes {
		return fmt.Errorf("weir: max_bytes must be at least %d MiB (the largest cacheable object is 10%% of a shard)", minStoreBytes>>20)
	}
	if h.MaxObjectBytes < 0 {
		return errors.New("weir: max_object_bytes must not be negative")
	}
	if h.MaxBytes != 0 && h.MaxObjectBytes*10 > h.MaxBytes {
		return errors.New("weir: max_object_bytes must be at most a tenth of max_bytes (an object has to fit a shard's small queue)")
	}
	if h.SnapshotKeepFresh && h.SnapshotDir == "" {
		return errors.New("weir: snapshot_keep_fresh needs snapshot_dir")
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
	if h.keyGenRetry != nil {
		h.keyGenRetry.stop() // before Close: its purge needs an open engine
	}
	engines.remove(h.Name, h.admin) // before Close: no new admin call reaches a closing engine
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	h.metrics.removeEngine(h.engine) // before Close: a scrape never polls a closing engine
	err := h.engine.Close(ctx)
	if metricSets.release(h.metricsReg, h.Name, h.metrics) {
		h.pool.sink.detach(h.metrics) // the set's last user: stop feeding its collectors
	}
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
	resp, err := h.engine.Serve(r.Context(), requestFor(r), newNextOrigin(next, r, h.log, h.ClientPlaceholders))
	if err != nil {
		return serveError(w, err)
	}
	if _, ok := resp.Header[nextStatusField]; ok {
		_ = resp.Body.Close()
		// Fields next set with its error (Allow on a 405, WWW-Authenticate,
		// Retry-After) survive the handle_errors route, as headers on w do
		// (08 §11). The representation fields describe the empty body, not
		// the page handle_errors writes, so they stay behind.
		for k, v := range resp.Header {
			switch k {
			case nextStatusField, "Content-Length", "Content-Type", "Content-Encoding", "Etag", "Last-Modified", "Cache-Control":
			default:
				w.Header()[k] = v
			}
		}
		return caddyhttp.Error(resp.StatusCode, errNextStatus)
	}
	// A client that leaves must not hold the origin stream open until the idle
	// timeout: closing the body ends the blocked read.
	stop := context.AfterFunc(r.Context(), func() { _ = resp.Body.Close() })
	defer stop()
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
	// FR-FAIR-4: Caddy's client_ip is the address trusted_proxies resolves,
	// so a site behind a load balancer still tells its clients apart.
	if ip, ok := caddyhttp.GetVar(r.Context(), caddyhttp.ClientIPVarKey).(string); ok && ip != "" {
		req.Client = ip
	}
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
	if h.MultiHost || h.warnedHosts.Load() { // nothing left to decide: every request passes here
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
