package weircaddy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/headers"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"

	"github.com/AshwinSathian/weir"
)

var serveSeq atomic.Int32

// loadServe provisions a handler under a name no other test uses (the store
// pool is process-wide) and swaps in a logger that writes to the buffer.
func loadServe(t *testing.T, extra string) (*Handler, *bytes.Buffer) {
	t.Helper()
	raw := fmt.Sprintf(`{"name":"serve-%d"%s}`, serveSeq.Add(1), extra)
	h, err := load(t, raw)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	h.log = slog.New(slog.NewTextHandler(&buf, nil))
	return h, &buf
}

// respond is a next handler that writes a cacheable body and counts calls.
func respond(calls *atomic.Int32, body string) caddyhttp.Handler {
	return caddyhttp.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) error {
		calls.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		_, err := io.WriteString(w, body)
		return err
	})
}

// FR-UPG-1, T-44: CONNECT and upgrades reach next with the caller's own
// ResponseWriter and request, never through the engine.
func TestUpgradeAndConnectBypassEngine(t *testing.T) {
	cases := map[string]func() *http.Request{
		"CONNECT": func() *http.Request {
			return httptest.NewRequestWithContext(context.Background(), http.MethodConnect, "http://example.com:443", nil)
		},
		"websocket upgrade": func() *http.Request {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/ws", nil)
			r.Header.Set("Connection", "Upgrade")
			r.Header.Set("Upgrade", "websocket")
			return r
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			h, _ := loadServe(t, "")
			r, w := mk(), httptest.NewRecorder()
			var gotW http.ResponseWriter
			var gotR *http.Request
			next := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
				gotW, gotR = w, r
				return nil
			})
			if err := h.ServeHTTP(w, r, next); err != nil {
				t.Fatal(err)
			}
			if gotW != http.ResponseWriter(w) || gotR != r {
				t.Fatal("next did not receive the original writer and request")
			}
		})
	}
}

// FR-COA-9, 08 §4: Fetch keeps the values of the context it is given but
// not the base request's cancellation, and next never sees the original
// ResponseWriter.
func TestNextOriginUsesDetachedContext(t *testing.T) {
	type key struct{}
	baseCtx, cancelBase := context.WithCancel(context.Background())
	base := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/a", nil).WithContext(baseCtx)
	base.Header.Set("X-K", "client")
	cancelBase() // the triggering request has finished

	fetchCtx := context.WithValue(context.WithoutCancel(baseCtx), key{}, "replacer")
	var seen any
	var live error
	var sawWriter http.ResponseWriter
	next := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		seen, live, sawWriter = r.Context().Value(key{}), r.Context().Err(), w
		w.Header().Set("X-Seen-Path", r.URL.Path)
		r.Header.Set("X-K", "mutated")
		_, err := io.WriteString(w, "ok")
		return err
	})
	fwd := &weir.Request{Method: http.MethodGet, Scheme: "http", Host: "example.com", Path: "/b", RawQuery: "q=1", Header: http.Header{"X-K": {"v"}}}
	resp, err := nextOrigin{next: next, base: base}.Fetch(fetchCtx, fwd)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "ok" || resp.Header.Get("X-Seen-Path") != "/b" {
		t.Fatalf("body %q, path header %q", body, resp.Header.Get("X-Seen-Path"))
	}
	if seen != "replacer" {
		t.Fatalf("context value = %v, want the fetch context's", seen)
	}
	if live != nil {
		t.Fatalf("next saw a cancelled context: %v", live)
	}
	if _, isRec := sawWriter.(*httptest.ResponseRecorder); isRec || sawWriter == nil {
		t.Fatalf("next got writer %T", sawWriter)
	}
	// The clone must not alias the base's mutable state.
	if base.URL.Path != "/a" || base.Header.Get("X-K") != "client" {
		t.Fatalf("base request changed: %v %v", base.URL, base.Header)
	}
}

// 08 §4: the forwarded request, not the client's, is what next sees.
func TestNextOriginForwardsKeyedRequest(t *testing.T) {
	base := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/a?x=1", nil)
	base.Header.Set("Cookie", "session=abc")
	base.GetBody = func() (io.ReadCloser, error) { return http.NoBody, nil }
	var got *http.Request
	next := caddyhttp.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) error {
		got = r
		return nil
	})
	fwd := &weir.Request{Method: http.MethodGet, Scheme: "https", Host: "example.com", Path: "/b", RawQuery: "y=2", Header: http.Header{}}
	resp, err := nextOrigin{next: next, base: base}.Fetch(context.Background(), fwd)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got.URL.Path != "/b" || got.URL.RawQuery != "y=2" || got.RequestURI != "/b?y=2" {
		t.Fatalf("target = %v / %q", got.URL, got.RequestURI)
	}
	if got.Header.Get("Cookie") != "" {
		t.Fatal("client cookie reached next")
	}
	if got.TLS == nil {
		t.Fatal("scheme https lost")
	}
	if got.GetBody != nil || got.Pattern != "" {
		t.Fatal("client request state survived the clone")
	}
}

// FR-LCY-2, 08 §4: an error from next is an origin failure (502), not a response.
func TestNextOriginErrorIsOriginError(t *testing.T) {
	base := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/a", nil)
	boom := errors.New("dial failed")
	next := caddyhttp.HandlerFunc(func(http.ResponseWriter, *http.Request) error { return boom })
	fwd := &weir.Request{Method: http.MethodGet, Scheme: "http", Host: "example.com", Path: "/a", Header: http.Header{}}
	_, err := nextOrigin{next: next, base: base}.Fetch(context.Background(), fwd)
	if !errors.Is(err, weir.ErrOrigin) || weir.StatusCode(err) != http.StatusBadGateway {
		t.Fatalf("err = %v, status %d", err, weir.StatusCode(err))
	}
}

// D34, 08 §4 step 4: engine errors become caddyhttp errors so handle_errors
// runs, with Retry-After set on the writer first.
func TestErrorsReturnHandlerError(t *testing.T) {
	t.Run("shed carries status and Retry-After", func(t *testing.T) {
		w := httptest.NewRecorder()
		err := serveError(w, &weir.RetryError{Err: weir.ErrShed, After: 2500 * time.Millisecond})
		var he caddyhttp.HandlerError
		if !errors.As(err, &he) || he.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("err = %#v", err)
		}
		if got := w.Header().Get("Retry-After"); got != "3" {
			t.Fatalf("Retry-After = %q, want 3", got)
		}
	})
	t.Run("invalid request is 400 without Retry-After", func(t *testing.T) {
		w := httptest.NewRecorder()
		err := serveError(w, fmt.Errorf("%w: bad", weir.ErrInvalidRequest))
		var he caddyhttp.HandlerError
		if !errors.As(err, &he) || he.StatusCode != http.StatusBadRequest || w.Header().Get("Retry-After") != "" {
			t.Fatalf("err = %#v, header %v", err, w.Header())
		}
	})
	t.Run("closed engine answers 503", func(t *testing.T) {
		// The handler keeps h.engine after Cleanup, so requests still running
		// on the old config get ErrClosed (08 §3).
		h, _ := loadServe(t, "")
		if err := h.Cleanup(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/a", nil)
		err := h.ServeHTTP(httptest.NewRecorder(), r, respond(new(atomic.Int32), "x"))
		var he caddyhttp.HandlerError
		if !errors.As(err, &he) || he.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("err = %v", err)
		}
	})
}

// FR-LCY-2, FR-COA-9: end to end through the engine: a miss reaches next once, the second
// request is a hit.
func TestServeCachesThroughNext(t *testing.T) {
	h, _ := loadServe(t, "")
	var calls atomic.Int32
	var orig http.ResponseWriter
	inner := respond(&calls, "hello")
	next := caddyhttp.HandlerFunc(func(nw http.ResponseWriter, r *http.Request) error {
		if nw == orig {
			t.Error("next received the client's ResponseWriter")
		}
		return inner.ServeHTTP(nw, r)
	})
	for range 2 {
		w := httptest.NewRecorder()
		orig = w
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/cached", nil)
		if err := h.ServeHTTP(w, r, next); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || w.Body.String() != "hello" {
			t.Fatalf("status %d body %q", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("next called %d times, want 1", calls.Load())
	}
}

func proxyWith(ops *headers.HeaderOps) *reverseproxy.Handler {
	rp := &reverseproxy.Handler{}
	if ops != nil {
		rp.Headers = &headers.Handler{Request: ops}
	}
	return rp
}

// T-4, 08 §6: reverse_proxy after weir adds the client address unless the
// operator strips X-Forwarded-For.
func TestForwardedForWarning(t *testing.T) {
	self := &Handler{Name: "x"}
	cases := []struct {
		name  string
		after []caddyhttp.MiddlewareHandler
		want  bool
	}{
		{"plain reverse_proxy warns", []caddyhttp.MiddlewareHandler{proxyWith(nil)}, true},
		{"header_up -X-Forwarded-For is quiet", []caddyhttp.MiddlewareHandler{proxyWith(&headers.HeaderOps{Delete: []string{"x-forwarded-for"}})}, false},
		{"no reverse_proxy is quiet", []caddyhttp.MiddlewareHandler{&headers.Handler{}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := chainWarnings(append([]caddyhttp.MiddlewareHandler{self}, c.after...), self)
			has := false
			for _, w := range got {
				has = has || strings.Contains(w, "X-Forwarded-For")
			}
			if has != c.want {
				t.Fatalf("warnings = %q, want X-Forwarded-For warning: %v", got, c.want)
			}
		})
	}
	t.Run("handlers before weir are ignored", func(t *testing.T) {
		got := chainWarnings([]caddyhttp.MiddlewareHandler{proxyWith(nil), self}, self)
		if len(got) != 0 {
			t.Fatalf("warnings = %q", got)
		}
	})
}

// T-45, 08 §6: a placeholder behind weir that still depends on the first
// client is warned about. With a replacer per forwarded request (the default)
// that is the peer address and Caddy variables; with client_placeholders it
// is everything the client's replacer reads.
func TestPlaceholderHeaderWarning(t *testing.T) {
	self := &Handler{Name: "x"}
	legacy := &Handler{Name: "x", ClientPlaceholders: true}
	strip := []string{"X-Forwarded-For"}
	cases := []struct {
		name         string
		ops          *headers.HeaderOps
		want, legacy bool
	}{
		{"remote_host in header_up", &headers.HeaderOps{Delete: strip, Set: http.Header{"X-Real-Ip": {"{http.request.remote.host}"}}}, true, true},
		{"client cookie in header_up", &headers.HeaderOps{Delete: strip, Set: http.Header{"X-Foo": {"{http.request.header.Cookie}"}}}, false, true},
		{"shortcut placeholder in Add", &headers.HeaderOps{Delete: strip, Add: http.Header{"X-Ip": {"{remote_host}"}}}, true, true},
		{"caddy variable", &headers.HeaderOps{Delete: strip, Set: http.Header{"X-Geo": {"{http.vars.country}"}}}, true, true},
		{"static value is quiet", &headers.HeaderOps{Delete: strip, Set: http.Header{"X-Env": {"prod"}}}, false, false},
		{"request uuid", &headers.HeaderOps{Delete: strip, Set: http.Header{"X-Request-Id": {"{http.request.uuid}"}}}, false, true},
		{"tls server name", &headers.HeaderOps{Delete: strip, Set: http.Header{"X-Sni": {"{http.request.tls.server_name}"}}}, false, true},
		{"host placeholder is keyed and quiet", &headers.HeaderOps{Delete: strip, Set: http.Header{"X-Host": {"{http.request.host}"}}}, false, false},
	}
	for _, c := range cases {
		for _, m := range []struct {
			name string
			h    *Handler
			want bool
		}{{"own replacer", self, c.want}, {"client_placeholders", legacy, c.legacy}} {
			t.Run(c.name+"/"+m.name, func(t *testing.T) {
				got := chainWarnings([]caddyhttp.MiddlewareHandler{m.h, proxyWith(c.ops)}, m.h)
				has := false
				for _, w := range got {
					has = has || strings.Contains(w, "placeholder")
				}
				if has != m.want {
					t.Fatalf("warnings = %q, want placeholder warning: %v", got, m.want)
				}
			})
		}
	}
	t.Run("a header handler after weir is checked too", func(t *testing.T) {
		hh := &headers.Handler{Request: &headers.HeaderOps{Set: http.Header{"X-Ip": {"{http.request.remote.host}"}}}}
		got := chainWarnings([]caddyhttp.MiddlewareHandler{self, hh}, self)
		if len(got) != 1 || !strings.Contains(got[0], "placeholder") {
			t.Fatalf("warnings = %q", got)
		}
	})
}

// T-4, T-45: the route scan finds weir inside subroutes and reports each problem once
// per process lifetime of the handler.
func TestRouteScanFindsHandler(t *testing.T) {
	self := &Handler{Name: "x"}
	inner := caddyhttp.Route{Handlers: []caddyhttp.MiddlewareHandler{self, proxyWith(nil)}}
	sub := &caddyhttp.Subroute{Routes: caddyhttp.RouteList{inner}}
	app := &caddyhttp.App{Servers: map[string]*caddyhttp.Server{
		"srv0": {Routes: caddyhttp.RouteList{
			{Handlers: []caddyhttp.MiddlewareHandler{&headers.Handler{}}},
			{Handlers: []caddyhttp.MiddlewareHandler{sub}},
		}},
	}}
	got := appWarnings(app, self)
	if len(got) != 1 || !strings.Contains(got[0], "X-Forwarded-For") {
		t.Fatalf("warnings = %q", got)
	}
	if other := appWarnings(app, &Handler{Name: "y"}); len(other) != 0 {
		t.Fatalf("unrelated handler got warnings %q", other)
	}
}

// 08 §2, FR-FAIR-3: a second distinct host while multi_host is off is the
// operator's likely mistake. One warning, naming the key; the engine keeps
// one remembered host, not a set keyed by request input (NFR-3).
func TestSecondHostWithoutMultiHostWarnsOnce(t *testing.T) {
	serve := func(h *Handler, host string) {
		t.Helper()
		var calls atomic.Int32
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+host+"/p", nil)
		if err := h.ServeHTTP(httptest.NewRecorder(), r, respond(&calls, "x")); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("warns once on the second host", func(t *testing.T) {
		h, buf := loadServe(t, "")
		serve(h, "a.example")
		serve(h, "a.example:443")
		if strings.Contains(buf.String(), "multi_host") {
			t.Fatalf("warned for one host: %s", buf)
		}
		serve(h, "b.example")
		serve(h, "c.example")
		serve(h, "b.example")
		if n := strings.Count(buf.String(), "multi_host"); n != 1 {
			t.Fatalf("multi_host warnings = %d, want 1: %s", n, buf)
		}
	})
	t.Run("multi_host on stays quiet", func(t *testing.T) {
		h, buf := loadServe(t, `,"multi_host":true`)
		serve(h, "a.example")
		serve(h, "b.example")
		if strings.Contains(buf.String(), "multi_host") {
			t.Fatalf("warned with multi_host on: %s", buf)
		}
	})
}

// T-47-style abuse check (INV-1, breaker): a 4xx that next chooses is a
// response. Missing paths must not open the breaker or hide good paths, and
// the 4xx goes back to Caddy as an error so handle_errors runs (D34).
func TestNextErrors4xxIsResponse(t *testing.T) {
	h, _ := loadServe(t, "")
	next := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		if strings.HasPrefix(r.URL.Path, "/missing") {
			return caddyhttp.Error(http.StatusNotFound, errors.New("file does not exist"))
		}
		w.Header().Set("Cache-Control", "max-age=60")
		_, err := io.WriteString(w, "good")
		return err
	})
	for i := range 200 {
		w := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("http://example.com/missing%d", i), nil)
		err := h.ServeHTTP(w, r, next)
		var he caddyhttp.HandlerError
		if !errors.As(err, &he) || he.StatusCode != http.StatusNotFound || w.Body.Len() != 0 {
			t.Fatalf("missing%d: err %v, body %q, want a 404 handler error and nothing written", i, err, w.Body.String())
		}
		if strings.Contains(err.Error(), "file does not exist") {
			t.Fatalf("next's message leaked: %v", err)
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/exists", nil)
	if err := h.ServeHTTP(w, r, next); err != nil || w.Code != 200 || w.Body.String() != "good" {
		t.Fatalf("exists: err %v, status %d body %q", err, w.Code, w.Body.String())
	}
}

// A 5xx or plain error from next stays an origin failure, and its text never
// reaches the error the operator's handle_errors route can template.
func TestNextErrorTextIsHidden(t *testing.T) {
	base := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/a", nil)
	for name, boom := range map[string]error{
		"plain": errors.New("dial tcp 10.0.0.7:8080: refused"),
		"5xx":   caddyhttp.Error(http.StatusBadGateway, errors.New("dial tcp 10.0.0.7:8080: refused")),
		"abort": http.ErrAbortHandler,
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			next := caddyhttp.HandlerFunc(func(http.ResponseWriter, *http.Request) error { return boom })
			fwd := &weir.Request{Method: http.MethodGet, Scheme: "http", Host: "example.com", Path: "/a", Header: http.Header{}}
			_, err := newNextOrigin(next, base, log, false).Fetch(context.Background(), fwd)
			if !errors.Is(err, weir.ErrOrigin) || strings.Contains(err.Error(), "10.0.0.7") {
				t.Fatalf("err = %v", err)
			}
			if name != "abort" && !strings.Contains(buf.String(), "10.0.0.7") {
				t.Fatalf("cause not logged: %s", buf.String())
			}
		})
	}
}

// The fetch goroutine gets its own copy of Caddy's unlocked variable table.
func TestNextOriginCopiesVars(t *testing.T) {
	vars := map[string]any{"k": "client"}
	ctx := context.WithValue(context.Background(), caddyhttp.VarsCtxKey, vars)
	base := httptest.NewRequestWithContext(ctx, http.MethodGet, "http://example.com/a", nil)
	next := caddyhttp.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) error {
		caddyhttp.SetVar(r.Context(), "k", "fetch")
		return nil
	})
	o := newNextOrigin(next, base, nil, false)
	fwd := &weir.Request{Method: http.MethodGet, Scheme: "http", Host: "example.com", Path: "/a", Header: http.Header{}}
	resp, err := o.Fetch(ctx, fwd)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if vars["k"] != "client" {
		t.Fatalf("client's vars were written: %v", vars)
	}
}

// 08 §4: a rewrite before weir (handle_path, rewrite) changes only r.URL; the
// engine must key and forward the rewritten target.
func TestRewriteBeforeWeirIsHonored(t *testing.T) {
	mk := func(rewrite bool) *http.Request {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/api/x?a=1", nil)
		orig := *r
		u := *r.URL
		orig.URL = &u
		r = r.WithContext(context.WithValue(r.Context(), caddyhttp.OriginalRequestCtxKey, orig))
		if rewrite {
			r.URL.Path, r.URL.RawQuery = "/x", "b=2"
		}
		return r
	}
	if req := requestFor(mk(true)); req.Path != "/x" || req.RawQuery != "b=2" {
		t.Fatalf("rewritten: %q ? %q", req.Path, req.RawQuery)
	}
	if req := requestFor(mk(false)); req.Path != "/api/x" || req.RawQuery != "a=1" {
		t.Fatalf("untouched: %q ? %q", req.Path, req.RawQuery)
	}
	h, _ := loadServe(t, "")
	var got string
	next := caddyhttp.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) error { got = r.RequestURI; return nil })
	if err := h.ServeHTTP(httptest.NewRecorder(), mk(true), next); err != nil {
		t.Fatal(err)
	}
	if got != "/x?b=2" {
		t.Fatalf("next saw %q", got)
	}
}

// NFR-2 / 08 §4: a gone client is not an error route.
func TestServeErrorClientGone(t *testing.T) {
	if err := serveError(httptest.NewRecorder(), context.Canceled); err != nil {
		t.Fatalf("err = %v", err)
	}
}

// T-4: the route warnings are logged once, however many requests follow.
func TestChainWarningsLoggedOnce(t *testing.T) {
	h, buf := loadServe(t, "")
	app := &caddyhttp.App{Servers: map[string]*caddyhttp.Server{
		"srv0": {Routes: caddyhttp.RouteList{{Handlers: []caddyhttp.MiddlewareHandler{h, proxyWith(nil)}}}},
	}}
	h.httpApp = func() (any, error) { return app, nil }
	var calls atomic.Int32
	for range 3 {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/p", nil)
		if err := h.ServeHTTP(httptest.NewRecorder(), r, respond(&calls, "x")); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(buf.String(), "level=WARN"); n != 1 {
		t.Fatalf("X-Forwarded-For warnings = %d, want 1: %s", n, buf)
	}
}

// FR-COA-9, 08 §4: a stale-while-revalidate refresh runs after the triggering
// request finished. next must get a live context and never the client's writer.
func TestBackgroundRefreshNeverSeesClientWriter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, _ := loadServe(t, `,"stale":{"while_revalidate":"1m"}`)
		var calls atomic.Int32
		var orig atomic.Pointer[httptest.ResponseRecorder]
		var sawClientWriter atomic.Bool
		var ctxErr atomic.Value
		next := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			n := calls.Add(1)
			if rec := orig.Load(); rec != nil && http.ResponseWriter(rec) == w {
				sawClientWriter.Store(true)
			}
			if n > 1 {
				ctxErr.Store(fmt.Sprint(r.Context().Err()))
			}
			w.Header().Set("Cache-Control", "max-age=1")
			_, err := io.WriteString(w, "body")
			return err
		})
		serve := func(ctx context.Context) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			orig.Store(rec)
			r := httptest.NewRequestWithContext(ctx, http.MethodGet, "http://example.com/swr", nil)
			if err := h.ServeHTTP(rec, r, next); err != nil {
				t.Error(err)
			}
			return rec
		}
		serve(context.Background())
		time.Sleep(5 * time.Second) // stale now, inside the 1m window
		ctx, cancel := context.WithCancel(context.Background())
		rec := serve(ctx)
		cancel() // the triggering request is over before the refresh finishes
		synctest.Wait()
		if rec.Body.String() != "body" {
			t.Fatalf("stale body = %q", rec.Body.String())
		}
		if calls.Load() != 2 {
			t.Fatalf("next called %d times, want 2 (miss + refresh)", calls.Load())
		}
		if sawClientWriter.Load() {
			t.Fatal("next received the client's ResponseWriter")
		}
		if v := ctxErr.Load(); v != "<nil>" {
			t.Fatalf("refresh context error = %v", v)
		}
		if err := h.Cleanup(); err != nil {
			t.Fatal(err)
		}
	})
}

// FR-FAIR-4: the engine's client is Caddy's client_ip, the address
// trusted_proxies resolves, and the peer address when Caddy set none.
func TestRequestForClient(t *testing.T) {
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/a", nil)
	r.RemoteAddr = "10.0.0.5:4711"
	if got := requestFor(r).Client; got != "10.0.0.5" {
		t.Fatalf("Client without client_ip = %q, want the peer address", got)
	}
	vars := map[string]any{caddyhttp.ClientIPVarKey: "203.0.113.9"}
	r = r.WithContext(context.WithValue(r.Context(), caddyhttp.VarsCtxKey, vars))
	if got := requestFor(r).Client; got != "203.0.113.9" {
		t.Fatalf("Client = %q, want Caddy's client_ip", got)
	}
}

// placeholderOrigin runs one fetch of fwd for the client request base and
// returns what the handler behind weir read through its replacer.
func placeholderOrigin(t *testing.T, base *http.Request, fwd *weir.Request, clientPlaceholders bool, tmpl string) string {
	t.Helper()
	var got string
	next := caddyhttp.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) error {
		repl := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)
		got = repl.ReplaceAll(tmpl, "")
		return nil
	})
	resp, err := newNextOrigin(next, base, nil, clientPlaceholders).Fetch(base.Context(), fwd)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return got
}

// clientRequest is a client request as Caddy hands it to weir: a replacer
// bound to it, its variable table, and Caddy's copy of the original request.
func clientRequest() *http.Request {
	vars := map[string]any{"tenant": "acme"}
	ctx := context.WithValue(context.Background(), caddyhttp.VarsCtxKey, vars)
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "http://example.com/a?id=1&utm_source=mail", nil)
	r.Header.Set("Cookie", "session=secret")
	r.Header.Set("X-Unkeyed", "client-chosen")
	r.Header.Set("Accept-Encoding", "gzip, br")
	orig := *r
	u := *r.URL
	orig.URL = &u
	r = r.WithContext(context.WithValue(r.Context(), caddyhttp.OriginalRequestCtxKey, orig))
	caddyhttp.NewTestReplacer(r)
	return r
}

// T-4, T-45, INV-1: behind weir a placeholder reads the forwarded request.
// Before v1.1 it read the request of the client that triggered the fetch, so
// an unkeyed header, a stripped cookie or a dropped query parameter could be
// written into a fetch whose response everyone shares.
func TestPlaceholdersReadForwardedRequest(t *testing.T) {
	fwd := &weir.Request{Method: http.MethodGet, Scheme: "http", Host: "example.com", Path: "/a", RawQuery: "id=1",
		Header: http.Header{"Accept-Encoding": {"gzip"}}}
	const tmpl = "{http.request.header.X-Unkeyed}|{http.request.cookie.session}|{http.request.uri}|" +
		"{http.request.orig_uri}|{http.request.uri.query.utm_source}|{http.request.header.Accept-Encoding}|{http.vars.tenant}"

	got := placeholderOrigin(t, clientRequest(), fwd, false, tmpl)
	if want := "|" + "|/a?id=1|/a?id=1||gzip|acme"; got != want {
		t.Fatalf("behind weir the placeholders read %q, want %q", got, want)
	}
	// client_placeholders is the old behavior, for sites that need it.
	got = placeholderOrigin(t, clientRequest(), fwd, true, tmpl)
	if want := "client-chosen|secret|/a?id=1&utm_source=mail|http://example.com/a?id=1&utm_source=mail|mail|gzip, br|acme"; got != want {
		t.Fatalf("with client_placeholders the placeholders read %q, want %q", got, want)
	}
}

// T-45: the client's own replacer and variable table are not written by the
// fetch, which may run after the client's request has ended.
func TestForwardedReplacerLeavesClientAlone(t *testing.T) {
	base := clientRequest()
	clientRepl := base.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)
	vars := base.Context().Value(caddyhttp.VarsCtxKey).(map[string]any)
	started := vars["start_time"]
	fwd := &weir.Request{Method: http.MethodGet, Scheme: "http", Host: "example.com", Path: "/a", Header: http.Header{}}
	placeholderOrigin(t, base, fwd, false, "{http.request.uri}")
	if got := clientRepl.ReplaceAll("{http.request.header.X-Unkeyed}", ""); got != "client-chosen" {
		t.Fatalf("the client's replacer now reads %q", got)
	}
	if vars["start_time"] != started || len(vars) != 2 {
		t.Fatalf("the client's variable table was written: %v", vars)
	}
}
