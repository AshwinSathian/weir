package weircaddy

import (
	"context"
	"net/http"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/weirhttp"
)

// nextOrigin is the weir.Origin that sends forwarded requests down the
// Caddy handler chain (08 §4). It reuses weirhttp.HandlerOrigin for the pipe
// and the Content-Length rules.
type nextOrigin struct {
	next caddyhttp.Handler
	base *http.Request // the triggering request: values only, never its writer
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
	r := o.base.Clone(hr.Context())
	r.Method, r.URL, r.RequestURI, r.Host = hr.Method, hr.URL, hr.RequestURI, hr.Host
	r.Header, r.Body, r.ContentLength, r.TLS = hr.Header, hr.Body, hr.ContentLength, hr.TLS
	r.Proto, r.ProtoMajor, r.ProtoMinor = hr.Proto, hr.ProtoMajor, hr.ProtoMinor
	// Parsed state of the client's request does not describe the forwarded one.
	r.Form, r.PostForm, r.MultipartForm, r.Trailer, r.TransferEncoding = nil, nil, nil, nil, nil
	if err := o.next.ServeHTTP(w, r); err != nil {
		// ponytail: HandlerOrigin has no error channel, so the error leaves as a
		// panic it recovers into a weir.ErrOrigin (502). Statuses next picked
		// (file_server's 404) are lost; P2-03b decides whether to map them.
		panic(err)
	}
}
