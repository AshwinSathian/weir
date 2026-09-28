package weirhttp

import (
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/keys"
)

// RequestFrom builds the engine request from r. Path and RawQuery come from
// the raw request target, so the engine keys and forwards the bytes the
// client sent (FR-FWD-1, INV-1). Absolute-form targets are cut after the
// authority; other targets fall back to the parsed URL.
func RequestFrom(r *http.Request) *weir.Request {
	req := &weir.Request{
		Method: r.Method,
		Scheme: "http",
		Host:   r.Host,
		Header: r.Header,
	}
	if r.TLS != nil {
		req.Scheme = "https"
	}
	if t := r.RequestURI; t == "*" || strings.HasPrefix(t, "/") {
		req.Path, req.RawQuery, _ = strings.Cut(t, "?")
	} else if _, rest, ok := strings.Cut(t, "://"); ok {
		// T-6: net/http leaves '#' in URL.Path and EscapedPath re-encodes it
		// as "%23", hiding a fragment Validate must reject (FR-VAL-1). The
		// authority ends at the first '/', '?' or '#' (RFC 3986 §3.2).
		if i := strings.IndexAny(rest, "/?#"); i >= 0 {
			req.Path, req.RawQuery, _ = strings.Cut(rest[i:], "?")
		}
	} else {
		req.Path, req.RawQuery = r.URL.EscapedPath(), r.URL.RawQuery
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		req.Body = r.Body
	}
	return req
}

// WriteResponse writes resp to w, without hop-by-hop fields, and closes
// its body.
func WriteResponse(w http.ResponseWriter, resp *weir.Response) error {
	defer resp.Body.Close()
	// Clone: value slices are shared with the stored entry (P4). Misses and
	// pass-through carry the origin's hop-by-hop fields (RFC 9110 §7.6.1).
	h := resp.Header.Clone()
	keys.DropHopByHop(h, resp.Header["Connection"])
	maps.Copy(w.Header(), h)
	w.WriteHeader(resp.StatusCode)
	_, err := io.Copy(w, resp.Body)
	return err
}

// WriteError writes the status that weir.StatusCode maps err to, with
// Retry-After when weir.RetryAfter has a hint. It writes nothing when the
// client has gone away (status 499).
func WriteError(w http.ResponseWriter, err error) {
	code := weir.StatusCode(err)
	if code == 499 {
		return
	}
	if d, ok := weir.RetryAfter(err); ok {
		w.Header().Set("Retry-After", strconv.FormatInt(int64(d.Seconds()), 10))
	}
	http.Error(w, http.StatusText(code), code)
}
