package weircaddy

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/http"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/weirhttp"
)

// nextOrigin is the weir.Origin that sends forwarded requests down the
// Caddy handler chain (08 §4). It reuses weirhttp.HandlerOrigin for the pipe
// and the Content-Length rules. Its Fetch only delegates to that origin; the
// engine remains the sole caller of Origin.Fetch (hard rule 2).
type nextOrigin struct {
	next caddyhttp.Handler
	base *http.Request // the triggering request: values only, never its writer
	// vars is a private copy of the request's Caddy variable table, taken on
	// the client's goroutine. The table is an unlocked map, so a background
	// fetch must not share it with the request still being logged.
	vars map[string]any
	log  *slog.Logger
}

// errNextFailed is what the engine and handle_errors see when next fails.
// next's own error stays out of the message: it can carry upstream addresses
// and Caddy internals that {http.error.message} would show to clients.
var errNextFailed = errors.New("next handler failed")

func newNextOrigin(next caddyhttp.Handler, base *http.Request, log *slog.Logger) nextOrigin {
	o := nextOrigin{next: next, base: base, log: log}
	if v, ok := base.Context().Value(caddyhttp.VarsCtxKey).(map[string]any); ok {
		o.vars = maps.Clone(v)
	}
	return o
}

// Fetch runs next on a clone of base carrying the forwarded request. It may
// run after the triggering request finished (SWR, early refresh), so it
// never touches that request's ResponseWriter: next writes into the pipe.
// ctx keeps the creating request's values, Caddy's replacer and vars among
// them, but none of its cancellation (FR-COA-9).
func (o nextOrigin) Fetch(ctx context.Context, fwd *weir.Request) (*weir.Response, error) {
	h := weirhttp.HandlerOrigin{Handler: http.HandlerFunc(o.serve)}
	return h.Fetch(ctx, fwd)
}

// serve receives the request HandlerOrigin built from the forwarded one and
// merges it onto a clone of base, so Caddy-owned fields (RemoteAddr, context
// values) stay while everything the origin can see comes from fwd (INV-1).
func (o nextOrigin) serve(w http.ResponseWriter, hr *http.Request) {
	ctx := hr.Context()
	if o.vars != nil {
		ctx = context.WithValue(ctx, caddyhttp.VarsCtxKey, o.vars)
	}
	// Clone also keeps RemoteAddr (08 §6 warns about X-Forwarded-For) and
	// unexported path-value state, which is harmless under Caddy.
	r := o.base.Clone(ctx)
	r.Method, r.URL, r.RequestURI, r.Host = hr.Method, hr.URL, hr.RequestURI, hr.Host
	r.Header, r.Body, r.ContentLength, r.TLS = hr.Header, hr.Body, hr.ContentLength, hr.TLS
	r.Proto, r.ProtoMajor, r.ProtoMinor = hr.Proto, hr.ProtoMajor, hr.ProtoMinor
	// Parsed state of the client's request does not describe the forwarded one.
	r.Form, r.PostForm, r.MultipartForm, r.Trailer, r.TransferEncoding = nil, nil, nil, nil, nil
	// The client's body rewinder and cancel hook must not outlive the swap (INV-1).
	r.GetBody, r.Close, r.Pattern, r.Response = nil, false, "", nil
	err := o.next.ServeHTTP(w, r)
	if err == nil {
		return
	}
	// A 4xx from next (file_server's 404) is a response, not an origin
	// failure: as a 502 it would trip the breaker for every missing path.
	var he caddyhttp.HandlerError
	if errors.As(err, &he) && he.StatusCode >= 400 && he.StatusCode < 500 {
		w.WriteHeader(he.StatusCode)
		return
	}
	if o.log != nil {
		o.log.Debug("weir: next handler failed", "error", err)
	}
	// ponytail: HandlerOrigin has no error channel, so the failure leaves as a
	// panic it recovers into weir.ErrOrigin (502). Upgrade path: an Origin
	// that returns the error directly instead of reusing HandlerOrigin.
	panic(errNextFailed)
}
