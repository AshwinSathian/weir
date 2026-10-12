package weirhttp

import (
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

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
		// RFC 9110 §4.2.3: an empty http(s) path is "/". The key and the
		// forwarded request must agree on it (INV-1).
		if req.Path == "" {
			req.Path = "/"
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
	// FR-TMO-2, T-18: a body still streaming from the origin holds the origin
	// connection, so a client that stops reading must not hold the write
	// forever. Each write of it gets the stream's own idle bound. A stored
	// body holds nothing, and a slow client may take as long as the server's
	// own timeouts allow.
	var idle time.Duration
	if s, ok := resp.Body.(interface{ StreamIdle() time.Duration }); ok {
		idle = s.StreamIdle()
	}
	flush := streamsToClient(h)
	if !flush && idle <= 0 {
		_, err := io.Copy(w, resp.Body)
		return err
	}
	// An event stream, or a body of unknown length, must reach the client as
	// it arrives: net/http holds back writes until its buffer fills, so one
	// event sent and then silence would not be seen. Same rule as
	// httputil.ReverseProxy.
	rc := http.NewResponseController(w)
	if idle > 0 {
		// The connection may serve another request after this one.
		defer func() { _ = rc.SetWriteDeadline(time.Time{}) }()
	}
	buf := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if idle > 0 {
				_ = rc.SetWriteDeadline(time.Now().Add(idle)) // ErrNotSupported: the writer has no deadline
			}
			if _, err := w.Write(buf[:n]); err != nil {
				return err
			}
			if flush {
				_ = rc.Flush() // ErrNotSupported: nothing to flush
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return nil
			}
			return rerr
		}
	}
}

// streamsToClient reports whether a response body should be flushed after each
// read: an event stream, or no Content-Length.
func streamsToClient(h http.Header) bool {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(h.Get("Content-Type"))), "text/event-stream") {
		return true
	}
	return h.Get("Content-Length") == ""
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
