package weirhttp

import (
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/AshwinSathian/weir"
)

// RequestFrom builds the engine request from r. Path and RawQuery come from
// the raw request target, so the engine keys and forwards the bytes the
// client sent (FR-FWD-1, INV-1). Absolute-form targets fall back to the
// parsed URL.
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
	} else {
		req.Path, req.RawQuery = r.URL.EscapedPath(), r.URL.RawQuery
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		req.Body = r.Body
	}
	return req
}

// WriteResponse writes resp to w and closes its body.
func WriteResponse(w http.ResponseWriter, resp *weir.Response) error {
	defer resp.Body.Close()
	h := w.Header()
	for k, v := range resp.Header {
		h[k] = slices.Clone(v) // value slices are shared with the stored entry (P4)
	}
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
