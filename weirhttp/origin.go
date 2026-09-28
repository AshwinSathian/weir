package weirhttp

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/AshwinSathian/weir"
)

// TransportOrigin sends forwarded requests to Target with Transport.
type TransportOrigin struct {
	// Target holds the scheme and host of the origin. Its path is ignored.
	Target *url.URL
	// Transport sends the requests. Nil means a clone of
	// http.DefaultTransport with DisableCompression set and no Proxy. A
	// custom *http.Transport must do the same: compression adds an unkeyed
	// Accept-Encoding: gzip to requests that carry none (INV-1), and through
	// a forward proxy the URL.Opaque path goes out as an origin-form target.
	Transport http.RoundTripper
	// Rewrite, when set, edits each outgoing request (Via, origin auth).
	// Headers it adds to cacheable requests are unkeyed input (T-4).
	Rewrite func(*http.Request)
}

var defaultTransport = sync.OnceValue(func() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableCompression = true
	t.Proxy = nil
	return t
})

// Fetch sends req to Target exactly as given: the path goes out through
// URL.Opaque so net/url does not re-encode it (FR-FWD-1, INV-1).
func (o *TransportOrigin) Fetch(ctx context.Context, req *weir.Request) (*weir.Response, error) {
	u := &url.URL{Scheme: o.Target.Scheme, Host: o.Target.Host, Opaque: req.Path, RawQuery: req.RawQuery}
	if strings.HasPrefix(req.Path, "//") {
		// URL.RequestURI would send "scheme://rest", naming a different
		// authority; absolute form keeps the path intact (RFC 9112 §3.2.2).
		u.Opaque = "//" + req.Host + req.Path
	}
	h := req.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	if _, ok := h["User-Agent"]; !ok {
		h["User-Agent"] = []string{""} // an empty value stops net/http adding its own
	}
	hr := (&http.Request{
		Method: req.Method,
		URL:    u,
		Header: h,
		Host:   req.Host,
		Body:   req.Body,
	}).WithContext(ctx)
	if req.Body != nil {
		// net/http sends Content-Length from this field, never from Header;
		// zero with a body means unknown length.
		hr.ContentLength, _ = strconv.ParseInt(h.Get("Content-Length"), 10, 64)
	}
	if o.Rewrite != nil {
		o.Rewrite(hr)
	}
	rt := o.Transport
	if rt == nil {
		rt = defaultTransport()
	}
	resp, err := rt.RoundTrip(hr) //nolint:bodyclose // the body moves to the returned Response
	if err != nil {
		return nil, err
	}
	return &weir.Response{StatusCode: resp.StatusCode, Header: resp.Header, Body: resp.Body}, nil
}
