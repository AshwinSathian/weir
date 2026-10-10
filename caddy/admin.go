package weircaddy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/store"
)

func init() { caddy.RegisterModule(adminRouter{}) }

// Admin API bounds (rule 5, NFR-3, 08 §7).
const (
	maxAdminBody   = 1 << 20
	maxPurgeURLs   = 1000
	maxPurgeGroups = 100
	// normalModeTTL satisfies SetMode's ttl check when the caller switches
	// back to normal and sends none: normal has no expiry.
	normalModeTTL = time.Minute
)

// adminRouter is the admin.api.weir module (08 §7). It is rebuilt on every
// config load and holds nothing: engines come from the registry.
type adminRouter struct{}

// CaddyModule implements caddy.Module.
func (adminRouter) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "admin.api.weir",
		New: func() caddy.Module { return new(adminRouter) },
	}
}

// Routes implements caddy.AdminRouter. One prefix route; the path is split by
// hand because Caddy's mux has no wildcards for the name segment. Purge is
// reachable only here, never on a site listener (T-26).
func (adminRouter) Routes() []caddy.AdminRoute {
	return []caddy.AdminRoute{{Pattern: "/weir/", Handler: caddy.AdminHandlerFunc(serveAdmin)}}
}

func apiError(status int, format string, args ...any) caddy.APIError {
	return caddy.APIError{HTTPStatus: status, Err: fmt.Errorf(format, args...)}
}

// serveAdmin routes /weir/<name>/<action>. A name that fails the charset check
// cannot name a handler, so it is a 404 like any unknown name.
func serveAdmin(w http.ResponseWriter, r *http.Request) error {
	rest, ok := strings.CutPrefix(r.URL.Path, "/weir/")
	if !ok {
		return apiError(http.StatusNotFound, "not found")
	}
	name, action, ok := strings.Cut(rest, "/")
	if !ok || validateName(name) != nil {
		return apiError(http.StatusNotFound, "not found")
	}
	var method string
	var handle func(http.ResponseWriter, *http.Request, []*adminEntry) error
	switch action {
	case "purge":
		method, handle = http.MethodPost, adminPurge
	case "mode":
		method, handle = http.MethodPost, adminMode
	case "stats":
		method, handle = http.MethodGet, adminStats
	default:
		return apiError(http.StatusNotFound, "not found")
	}
	live := engineList(name)
	if len(live) == 0 {
		return apiError(http.StatusNotFound, "no weir handler named %q", name)
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		return apiError(http.StatusMethodNotAllowed, "method not allowed")
	}
	return handle(w, r, live)
}

// decodeBody reads one JSON object of at most maxAdminBody bytes, rejecting
// unknown fields and trailing data. A body over the cap is a 413.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		if _, perr := dec.Token(); !errors.Is(perr, io.EOF) {
			err = perr
			if err == nil {
				err = errors.New("trailing data after the JSON object")
			}
		}
	}
	if err == nil {
		return nil
	}
	if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
		return apiError(http.StatusRequestEntityTooLarge, "request body over %d bytes", maxAdminBody)
	}
	return apiError(http.StatusBadRequest, "invalid JSON body: %v", err)
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

// purgeBody is the JSON form of weir.Purge (08 §7).
type purgeBody struct {
	Mode   string   `json:"mode,omitempty"` // "soft" (default) or "hard"
	All    bool     `json:"all,omitempty"`
	URLs   []string `json:"urls,omitempty"`
	Origin string   `json:"origin,omitempty"`
	Groups []string `json:"groups,omitempty"`
	Eager  bool     `json:"eager,omitempty"`
}

type purgeReply struct {
	Status   string `json:"status"`
	Scrubbed *int64 `json:"scrubbed,omitempty"` // records deleted; only for eager
	Warning  string `json:"warning,omitempty"`
}

// adminPurge writes the epochs through one live engine: epochs live in the
// shared store, so any engine does. An engine already closed is skipped.
func adminPurge(w http.ResponseWriter, r *http.Request, live []*adminEntry) error {
	var b purgeBody
	if err := decodeBody(w, r, &b); err != nil {
		return err
	}
	if len(b.URLs) > maxPurgeURLs || len(b.Groups) > maxPurgeGroups {
		return apiError(http.StatusBadRequest, "a purge takes at most %d urls and %d groups", maxPurgeURLs, maxPurgeGroups)
	}
	if !b.All && len(b.URLs) == 0 && len(b.Groups) == 0 {
		return apiError(http.StatusBadRequest, "name what to purge: all, urls or groups")
	}
	p := weir.Purge{All: b.All, URLs: b.URLs, Origin: b.Origin, Groups: b.Groups, Eager: b.Eager}
	switch b.Mode {
	case "", "soft":
		p.Mode = weir.PurgeSoft
	case "hard":
		p.Mode = weir.PurgeHard
	default:
		return apiError(http.StatusBadRequest, "mode must be soft or hard")
	}
	err := weir.ErrClosed
	var n int64
	for _, e := range live {
		n, err = e.tap.purge(r.Context(), e.engine, p)
		if !errors.Is(err, weir.ErrClosed) {
			break
		}
	}
	rep := purgeReply{Status: "accepted"}
	switch {
	case err == nil:
	case errors.Is(err, weir.ErrEagerUnsupported):
		rep.Warning = "the store cannot delete records; the epochs are written and the entries are unreachable"
		err = nil
	}
	if err != nil {
		return purgeError(err)
	}
	if p.Eager && rep.Warning == "" {
		rep.Scrubbed = &n
	}
	return writeJSON(w, http.StatusAccepted, rep)
}

// purge runs one purge and returns the number of records an eager one deleted.
// Calls on one engine are serialized so the count is this call's own.
func (t *purgeTap) purge(ctx context.Context, e *weir.Engine, p weir.Purge) (int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	before := t.scrubbed.Load()
	err := e.Purge(ctx, p)
	return t.scrubbed.Load() - before, err
}

// purgeError maps an engine error to an admin status. Details of store
// failures stay in the message because the admin API is operator-only.
func purgeError(err error) error {
	switch {
	case errors.Is(err, weir.ErrInvalidRequest):
		return apiError(http.StatusBadRequest, "%v", err)
	case errors.Is(err, weir.ErrClosed), errors.Is(err, store.ErrUnavailable):
		return apiError(http.StatusServiceUnavailable, "%v", err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return apiError(http.StatusServiceUnavailable, "%v", err)
	}
	return apiError(http.StatusInternalServerError, "%v", err)
}

var modesByName = map[string]weir.Mode{
	"normal":         weir.ModeNormal,
	"stale-on-error": weir.ModeStaleOnError,
	"bypass":         weir.ModeBypass,
}

type modeBody struct {
	Mode string `json:"mode"`
	TTL  string `json:"ttl,omitempty"`
}

type modeReply struct {
	Mode    string `json:"mode"`
	TTL     string `json:"ttl,omitempty"`
	Engines int    `json:"engines"`
}

// adminMode applies the mode to every live engine of the name, so the old and
// new engine of a reload overlap agree (D33). SetMode validates before it
// changes anything, and all engines see the same arguments, so a bad request
// fails on the first engine with nothing applied.
func adminMode(w http.ResponseWriter, r *http.Request, live []*adminEntry) error {
	var b modeBody
	if err := decodeBody(w, r, &b); err != nil {
		return err
	}
	m, ok := modesByName[b.Mode]
	if !ok {
		return apiError(http.StatusBadRequest, "mode must be normal, stale-on-error or bypass")
	}
	ttl := time.Duration(0)
	if b.TTL != "" {
		var err error
		if ttl, err = time.ParseDuration(b.TTL); err != nil {
			return apiError(http.StatusBadRequest, "invalid ttl: %v", err)
		}
	} else if m == weir.ModeNormal {
		ttl = normalModeTTL
	}
	for _, e := range live {
		if err := e.engine.SetMode(m, ttl); err != nil {
			return apiError(http.StatusBadRequest, "%v", err)
		}
	}
	rep := modeReply{Mode: b.Mode, Engines: len(live)}
	if m != weir.ModeNormal {
		rep.TTL = ttl.String()
	}
	return writeJSON(w, http.StatusOK, rep)
}

type statsEntry struct {
	Inflight     int    `json:"inflight"`
	Queued       int    `json:"queued"`
	BreakerState string `json:"breaker_state"`
	StoreBytes   int64  `json:"store_bytes"`
}

var breakerNames = [...]string{weir.BreakerClosed: "closed", weir.BreakerHalfOpen: "half-open", weir.BreakerOpen: "open"}

// adminStats returns one entry per live engine (08 §7). Stats only reads, so
// it is safe on an engine that is closing.
func adminStats(w http.ResponseWriter, _ *http.Request, live []*adminEntry) error {
	out := make([]statsEntry, 0, len(live))
	for _, e := range live {
		s := e.engine.Stats()
		bs := "unknown"
		if int(s.BreakerState) < len(breakerNames) {
			bs = breakerNames[s.BreakerState]
		}
		out = append(out, statsEntry{Inflight: s.Inflight, Queued: s.Queued, BreakerState: bs, StoreBytes: s.StoreBytes})
	}
	return writeJSON(w, http.StatusOK, out)
}

// Interface guards.
var (
	_ caddy.Module      = adminRouter{}
	_ caddy.AdminRouter = adminRouter{}
)
