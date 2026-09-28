package weirhttp

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
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

// HandlerOrigin calls an in-process http.Handler, buffering nothing: the
// handler runs on its own goroutine and writes into a pipe that becomes the
// response body. The Caddy adapter reuses this shape for next (08).
type HandlerOrigin struct{ Handler http.Handler }

// Fetch returns once the handler writes headers or returns. Cancelling ctx,
// or closing the returned body, cancels the handler's context and fails its
// writes and any body read in progress (03 §5). A handler that ignores its
// context and never writes keeps its goroutine until it returns, past
// Engine.Close too.
func (o *HandlerOrigin) Fetch(ctx context.Context, req *weir.Request) (*weir.Response, error) {
	target := req.Path
	if req.RawQuery != "" {
		target += "?" + req.RawQuery
	}
	// ParseRequestURI reads a leading "//" as path, as net/http's server does.
	u, err := url.ParseRequestURI(target)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", weir.ErrInvalidRequest, err)
	}
	h := req.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	hctx, cancel := context.WithCancel(ctx)
	hr := (&http.Request{
		Method: req.Method, URL: u, RequestURI: target, Host: req.Host, Header: h,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Body: http.NoBody,
	}).WithContext(hctx)
	if req.Scheme == "https" {
		// The scheme is keyed; handlers read it from r.TLS.
		hr.TLS = &tls.ConnectionState{}
	}
	if req.Body != nil {
		hr.Body = req.Body
		hr.ContentLength = -1 // unknown, as net/http's server reports it
		if n, err := strconv.ParseInt(h.Get("Content-Length"), 10, 64); err == nil && n >= 0 {
			hr.ContentLength = n
		}
	}
	pr, pw := io.Pipe()
	context.AfterFunc(hctx, func() { pw.CloseWithError(context.Cause(hctx)) })
	w := &pipeWriter{header: http.Header{}, pw: pw, ready: make(chan struct{}), head: req.Method == http.MethodHead}
	go func() {
		returned := false
		defer func() {
			_ = hr.Body.Close() // as net/http's server and RoundTrip do
			if v := recover(); v != nil || !returned {
				// !returned without a panic is runtime.Goexit.
				if v == nil {
					v = "runtime.Goexit"
				}
				err := fmt.Errorf("%w: handler panic: %v", weir.ErrOrigin, v)
				if !w.wrote {
					w.err = err
					w.wrote = true
					close(w.ready)
				}
				pw.CloseWithError(err)
				return
			}
			w.WriteHeader(http.StatusOK)
			if w.remain > 0 {
				// A clean EOF would get a truncated body stored (fetch.go).
				pw.CloseWithError(io.ErrUnexpectedEOF)
				return
			}
			_ = pw.Close()
		}()
		o.Handler.ServeHTTP(w, hr)
		returned = true
	}()
	select {
	case <-w.ready:
	case <-hctx.Done():
		cancel()
		return nil, context.Cause(hctx)
	}
	if w.err != nil {
		cancel()
		return nil, w.err
	}
	return &weir.Response{StatusCode: w.status, Header: w.sent, Body: &pipeBody{pr, cancel}}, nil
}

// pipeWriter is the http.ResponseWriter a HandlerOrigin handler writes to.
// Only the handler goroutine calls its methods; status, sent and err are
// read by Fetch after ready closes.
type pipeWriter struct {
	header http.Header
	pw     *io.PipeWriter
	ready  chan struct{}
	wrote  bool
	status int
	sent   http.Header // snapshot at WriteHeader: later handler edits race with the reader
	err    error
	head   bool  // HEAD: the body is discarded, as net/http's server does
	noBody bool  // 204 or 304: body writes fail with http.ErrBodyNotAllowed
	remain int64 // bytes still owed to a declared Content-Length; -1 if none
}

func (w *pipeWriter) Header() http.Header { return w.header }

// WriteHeader ignores 1xx codes: an Origin returns one final response. A
// code above 999 becomes 500, since WriteResponse would panic on it.
func (w *pipeWriter) WriteHeader(code int) {
	if w.wrote || code < 200 {
		return
	}
	if code > 999 {
		code = http.StatusInternalServerError
	}
	w.wrote, w.status, w.sent = true, code, w.header.Clone()
	w.remain = -1
	w.noBody = code == http.StatusNoContent || code == http.StatusNotModified // RFC 9110 §15.3.5, §15.4.5
	if cl, ok := w.sent["Content-Length"]; ok {
		n, err := int64(-1), error(nil)
		if len(cl) == 1 {
			n, err = strconv.ParseInt(cl[0], 10, 64)
		}
		switch {
		case err != nil || n < 0:
			delete(w.sent, "Content-Length") // as net/http's server does
		case !w.head && !w.noBody:
			w.remain = n
		}
	}
	close(w.ready)
}

// Write enforces a declared Content-Length the way net/http's server does.
func (w *pipeWriter) Write(p []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	switch {
	case w.head:
		return len(p), nil
	case w.noBody:
		return 0, http.ErrBodyNotAllowed
	case len(p) == 0:
		return 0, nil // a pipe write blocks until the next read, even for nothing
	case w.remain < 0:
		return w.pw.Write(p)
	}
	over := int64(len(p)) > w.remain
	if over {
		if w.remain == 0 {
			return 0, http.ErrContentLength
		}
		p = p[:w.remain]
	}
	n, err := w.pw.Write(p)
	w.remain -= int64(n)
	if err == nil && over {
		err = http.ErrContentLength
	}
	return n, err
}

// Flush sends headers; the pipe holds no body bytes to flush.
func (w *pipeWriter) Flush() { w.WriteHeader(http.StatusOK) }

// pipeBody is the response body; Close also cancels the handler.
type pipeBody struct {
	*io.PipeReader
	cancel context.CancelFunc
}

func (b *pipeBody) Close() error {
	b.cancel()
	return b.PipeReader.Close()
}
