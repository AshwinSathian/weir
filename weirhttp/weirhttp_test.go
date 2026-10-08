package weirhttp_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/weirhttp"
)

// seen records what the origin received on the wire.
type seen struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (s *seen) handler(h http.Header, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reqs = append(s.reqs, r)
		s.mu.Unlock()
		for k, v := range h {
			w.Header()[k] = v
		}
		_, _ = io.WriteString(w, body)
	})
}

func (s *seen) all() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reqs
}

// proxy starts an origin server and a weirhttp server in front of it. The
// returned client talks to the proxy.
func proxy(t *testing.T, s *seen, h http.Header, body string) (*weir.Engine, *http.Client) {
	t.Helper()
	originSrv := httptest.NewTestServer(t, s.handler(h, body))
	tr := originSrv.Client().Transport.(*http.Transport).Clone()
	tr.DisableCompression = true
	target, _ := url.Parse(originSrv.URL)

	e, err := weir.New(weir.Config{Freshness: weir.FreshnessConfig{NoJitter: true}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := e.Close(context.Background()); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	proxySrv := httptest.NewTestServer(t, weirhttp.Handler(e, &weirhttp.TransportOrigin{Target: target, Transport: tr}))
	client := proxySrv.Client()
	ctr := client.Transport.(*http.Transport).Clone()
	ctr.DisableCompression = true // the client sends no Accept-Encoding, so none may reach the origin
	client.Transport = ctr
	return e, client
}

// get sends a GET for u and returns the status, header and body.
func get(t *testing.T, c *http.Client, u *url.URL) (int, http.Header, string) {
	return do(t, c, http.MethodGet, u)
}

// do sends a body-less request and returns the status, header and body.
func do(t *testing.T, c *http.Client, method string, u *url.URL) (int, http.Header, string) {
	t.Helper()
	req := (&http.Request{Method: method, URL: u, Header: http.Header{"User-Agent": {""}}, Host: u.Host}).WithContext(t.Context())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, u.RequestURI(), err)
	}
	b, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, resp.Header, string(b)
}

// FR-FWD-1, INV-1, T-4: the origin receives the request target the client
// sent, byte for byte (no decoding or re-encoding by net/url), and no
// header the engine did not forward (Go's default User-Agent, transparent
// gzip).
func TestPathForwardedByteExact(t *testing.T) {
	tests := []struct {
		name   string
		method string
		opaque string // client request target, sent via URL.Opaque
		want   string // RequestURI seen by the origin
		wantAE string // Accept-Encoding seen by the origin: keyed "identity" on GET
	}{
		{"encoded slash stays encoded", "GET", "/a%2Fb", "/a%2Fb", "identity"},
		{"lowercase escape keeps its case", "GET", "/%7e/b", "/%7e/b", "identity"},
		{"raw query bytes pass through", "GET", "/q?b=%20&a=1+2", "/q?b=%20&a=1+2", "identity"},
		{"semicolon in path", "GET", "/a;v=1/b", "/a;v=1/b", "identity"},
		{"bytes net/url would escape stay raw", "GET", "/a|b{c}", "/a|b{c}", "identity"},
		{"double slash is not read as an authority", "GET", "//example.com//evil.example/x", "http://example.com//evil.example/x", "identity"},
		{"pass-through gets no transport gzip", "POST", "/submit", "/submit", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := &seen{}
				_, c := proxy(t, s, http.Header{"Cache-Control": {"no-store"}}, "ok")
				u := &url.URL{Scheme: "http", Host: "example.com", Opaque: tc.opaque}
				if code, _, _ := do(t, c, tc.method, u); code != http.StatusOK {
					t.Fatalf("status %d", code)
				}
				reqs := s.all()
				if len(reqs) != 1 {
					t.Fatalf("origin calls = %d, want 1", len(reqs))
				}
				r := reqs[0]
				if r.RequestURI != tc.want {
					t.Errorf("origin RequestURI = %q, want %q", r.RequestURI, tc.want)
				}
				if ae := strings.Join(r.Header["Accept-Encoding"], ","); ae != tc.wantAE {
					t.Errorf("origin Accept-Encoding = %q, want %q", ae, tc.wantAE)
				}
				if v, ok := r.Header["User-Agent"]; ok {
					t.Errorf("origin got User-Agent %q the engine did not forward", v)
				}
				if r.Host != "example.com" {
					t.Errorf("origin Host = %q, want example.com", r.Host)
				}
			})
		})
	}
}

// FR-UPG-1, T-44: weirhttp hands CONNECT and protocol upgrades to the next
// handler before the engine; Handler, which has no next, answers 501.
func TestAdaptersRouteUpgradesAround(t *testing.T) {
	upgrade := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/ws", nil)
	upgrade.Header.Set("Connection", "keep-alive, Upgrade")
	upgrade.Header.Set("Upgrade", "websocket")
	connect := httptest.NewRequestWithContext(t.Context(), http.MethodConnect, "http://example.com:443", nil)
	connect.RequestURI = "example.com:443"

	for _, r := range []*http.Request{upgrade, connect} {
		t.Run(r.Method+" "+r.RequestURI, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e, err := weir.New(weir.Config{})
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				defer func() { _ = e.Close(context.Background()) }()
				origin := weir.OriginFunc(func(context.Context, *weir.Request) (*weir.Response, error) {
					t.Error("origin called for an upgrade")
					return nil, errors.New("unreachable")
				})

				nextCalled := false
				next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					nextCalled = true
					w.WriteHeader(http.StatusSwitchingProtocols)
				})
				w := httptest.NewRecorder()
				weirhttp.Middleware(e, origin)(next).ServeHTTP(w, r)
				if !nextCalled || w.Code != http.StatusSwitchingProtocols {
					t.Errorf("Middleware: next called = %v, status %d", nextCalled, w.Code)
				}

				w = httptest.NewRecorder()
				weirhttp.Handler(e, origin).ServeHTTP(w, r)
				if w.Code != http.StatusNotImplemented {
					t.Errorf("Handler: status %d, want 501", w.Code)
				}
			})
		})
	}
}

// FR-SRV-9, 03 §5: a cacheable response goes through the adapter to the
// origin once, and the repeated request is a hit with Cache-Status.
func TestWeirhttpEndToEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &seen{}
		_, c := proxy(t, s, http.Header{"Cache-Control": {"max-age=60"}, "Content-Type": {"text/plain"}}, "hello")
		u := &url.URL{Scheme: "http", Host: "example.com", Path: "/page"}

		code, h, body := get(t, c, u)
		if code != http.StatusOK || body != "hello" {
			t.Fatalf("miss: status %d body %q", code, body)
		}
		if cs := h.Get("Cache-Status"); !strings.Contains(cs, "fwd=uri-miss") {
			t.Errorf("miss Cache-Status = %q", cs)
		}

		time.Sleep(time.Second)
		code, h, body = get(t, c, u)
		if code != http.StatusOK || body != "hello" {
			t.Fatalf("hit: status %d body %q", code, body)
		}
		if cs := h.Get("Cache-Status"); !strings.Contains(cs, "hit") {
			t.Errorf("hit Cache-Status = %q", cs)
		}
		if n := len(s.all()); n != 1 {
			t.Errorf("origin calls = %d, want 1", n)
		}
	})
}

// 01 §4, 03 §5 item 5: errors map through StatusCode and RetryAfter; a
// client that went away gets nothing written.
func TestWriteError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		code       int
		retryAfter string
	}{
		{"shed is 503 with Retry-After", &weir.RetryError{Err: weir.ErrShed, After: 1500 * time.Millisecond}, 503, "2"},
		{"origin timeout is 504", weir.ErrOriginTimeout, 504, ""},
		{"unknown error is 502", errors.New("boom"), 502, ""},
		{"client gone writes nothing", context.Canceled, 200, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			weirhttp.WriteError(w, tc.err)
			if w.Code != tc.code || w.Header().Get("Retry-After") != tc.retryAfter {
				t.Errorf("got %d Retry-After %q, want %d %q", w.Code, w.Header().Get("Retry-After"), tc.code, tc.retryAfter)
			}
			if tc.code == 200 && w.Body.Len() != 0 {
				t.Errorf("wrote body %q for a gone client", w.Body)
			}
		})
	}
}

// INV-1, FR-FWD-1: the default transport never adds Accept-Encoding: gzip
// (http.Transport does unless DisableCompression is set).
func TestDefaultTransportDisablesCompression(t *testing.T) {
	if tr, ok := weirhttp.DefaultTransport().(*http.Transport); !ok || !tr.DisableCompression {
		t.Fatalf("default transport %T does not disable compression", weirhttp.DefaultTransport())
	}
}

// RFC 9110 §7.6.1, FR-STO-11: hop-by-hop fields from the origin never reach
// the client, on misses and pass-through as well as hits.
func TestWriteResponseDropsHopByHop(t *testing.T) {
	w := httptest.NewRecorder()
	resp := &weir.Response{StatusCode: 200, Header: http.Header{
		"Connection":  {"close, X-Conn-Only"},
		"Keep-Alive":  {"timeout=5"},
		"X-Conn-Only": {"1"},
		"Upgrade":     {"h2c"},
		"X-Keep":      {"1"},
	}, Body: io.NopCloser(strings.NewReader("ok"))}
	if err := weirhttp.WriteResponse(w, resp); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}
	for _, name := range []string{"Connection", "Keep-Alive", "X-Conn-Only", "Upgrade"} {
		if v, ok := w.Header()[name]; ok {
			t.Errorf("client got hop-by-hop %s %q", name, v)
		}
	}
	if w.Header().Get("X-Keep") != "1" || w.Body.String() != "ok" {
		t.Errorf("end-to-end field or body lost: %v %q", w.Header(), w.Body)
	}
}

// INV-1, FR-FWD-1: the default transport ignores HTTP_PROXY. Through a
// forward proxy, net/http sends a URL.Opaque path as an origin-form target
// the proxy cannot route.
func TestDefaultTransportIgnoresProxyEnv(t *testing.T) {
	if tr, ok := weirhttp.DefaultTransport().(*http.Transport); !ok || tr.Proxy != nil {
		t.Fatalf("default transport %T uses a proxy function", weirhttp.DefaultTransport())
	}
}

// FR-UPG-1, INV-1: an Upgrade whose only token is h2c (curl --http2 on an
// http URL) is served through the engine and keyed like a plain request;
// the origin never sees Upgrade or HTTP2-Settings. h2c next to websocket
// still routes around the engine (T-44).
func TestH2CUpgradeServedNormally(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &seen{}
		_, c := proxy(t, s, http.Header{"Cache-Control": {"max-age=60"}}, "hello")
		u := &url.URL{Scheme: "http", Host: "example.com", Path: "/page"}
		if code, _, body := get(t, c, u); code != http.StatusOK || body != "hello" {
			t.Fatalf("plain: status %d body %q", code, body)
		}
		send := func(upgrade string) (int, http.Header) {
			req := (&http.Request{Method: http.MethodGet, URL: u, Host: u.Host, Header: http.Header{
				"User-Agent":     {""},
				"Connection":     {"Upgrade, HTTP2-Settings"},
				"Upgrade":        {upgrade},
				"Http2-Settings": {"AAMAAABkAAQAAP__"},
			}}).WithContext(t.Context())
			resp, err := c.Do(req)
			if err != nil {
				t.Fatalf("Upgrade %q: %v", upgrade, err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			return resp.StatusCode, resp.Header
		}
		code, h := send("h2c")
		if code != http.StatusOK || !strings.Contains(h.Get("Cache-Status"), "hit") {
			t.Errorf("h2c: status %d Cache-Status %q, want a hit on the plain entry", code, h.Get("Cache-Status"))
		}
		if code, _ := send("h2c, websocket"); code != http.StatusNotImplemented {
			t.Errorf("h2c, websocket: status %d, want 501 from Handler", code)
		}
		reqs := s.all()
		if len(reqs) != 1 {
			t.Fatalf("origin got %d requests, want 1", len(reqs))
		}
	})
}

// FR-UPG-1, INV-1: a miss with a lone h2c Upgrade reaches the origin
// without Upgrade or HTTP2-Settings.
func TestH2CUpgradeNotForwarded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &seen{}
		_, c := proxy(t, s, http.Header{"Cache-Control": {"max-age=60"}}, "hello")
		u := &url.URL{Scheme: "http", Host: "example.com", Path: "/page"}
		req := (&http.Request{Method: http.MethodGet, URL: u, Host: u.Host, Header: http.Header{
			"User-Agent":     {""},
			"Connection":     {"Upgrade, HTTP2-Settings"},
			"Upgrade":        {"h2c"},
			"Http2-Settings": {"AAMAAABkAAQAAP__"},
		}}).WithContext(t.Context())
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		reqs := s.all()
		if resp.StatusCode != http.StatusOK || len(reqs) != 1 {
			t.Fatalf("status %d, origin requests %d", resp.StatusCode, len(reqs))
		}
		for _, name := range []string{"Upgrade", "Http2-Settings", "Connection"} {
			if v, ok := reqs[0].Header[name]; ok {
				t.Errorf("origin got %s %q", name, v)
			}
		}
	})
}

// FR-VAL-1, INV-1, T-6: an absolute-form target keeps the raw path and
// query bytes too. net/http leaves '#' in URL.Path and EscapedPath turns it
// into "%23", which would serve "/a#x" as the literal "/a%23x" instead of
// rejecting it like the origin-form target.
// FR-VAL-1, RFC 9110 §4.2.3: an empty absolute-form path is "/".
func TestAbsoluteFormEmptyPath(t *testing.T) {
	for _, target := range []string{"http://example.com", "http://example.com?x=1", "https://example.com"} {
		t.Run(target, func(t *testing.T) {
			r, err := http.ReadRequest(bufio.NewReader(strings.NewReader("GET " + target + " HTTP/1.1\r\nHost: example.com\r\n\r\n")))
			if err != nil {
				t.Fatalf("ReadRequest: %v", err)
			}
			if req := weirhttp.RequestFrom(r); req.Path != "/" {
				t.Fatalf("RequestFrom(%q).Path = %q, want /", target, req.Path)
			}
		})
	}
}

func TestRequestFromAbsoluteFormRaw(t *testing.T) {
	tests := []struct{ target, path, query string }{
		{"http://example.com/a#x", "/a#x", ""},
		{"http://example.com/a?q=1#f", "/a", "q=1#f"},
		{"http://example.com/a%7e/%2F?b=%41", "/a%7e/%2F", "b=%41"},
		{"http://example.com?q=1", "/", "q=1"},
		{"http://example.com", "/", ""},
		{"https://example.com:8443/p", "/p", ""},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			r, err := http.ReadRequest(bufio.NewReader(strings.NewReader("GET " + tt.target + " HTTP/1.1\r\nHost: example.com\r\n\r\n")))
			if err != nil {
				t.Fatalf("ReadRequest: %v", err)
			}
			req := weirhttp.RequestFrom(r)
			if req.Path != tt.path || req.RawQuery != tt.query {
				t.Fatalf("RequestFrom(%q) = path %q query %q, want %q %q", tt.target, req.Path, req.RawQuery, tt.path, tt.query)
			}
		})
	}
}

// FR-VAL-1, T-6: a fragment in an absolute-form target is rejected like
// one in origin-form, over the wire, and never reaches the origin.
func TestAbsoluteFormFragmentRejected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, err := weir.New(weir.Config{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer func() { _ = e.Close(context.Background()) }()
		origin := weir.OriginFunc(func(context.Context, *weir.Request) (*weir.Response, error) {
			t.Error("origin called for a fragment target")
			return nil, errors.New("unreachable")
		})
		r, err := http.ReadRequest(bufio.NewReader(strings.NewReader("GET http://example.com/a#x HTTP/1.1\r\nHost: example.com\r\n\r\n")))
		if err != nil {
			t.Fatalf("ReadRequest: %v", err)
		}
		w := httptest.NewRecorder()
		weirhttp.Handler(e, origin).ServeHTTP(w, r.WithContext(t.Context()))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400", w.Code)
		}
	})
}

// FR-VAL-1, INV-1, NFR-2: for every target net/http accepts, RequestFrom
// does not panic, and an absolute-form target keeps its raw bytes after the
// authority, so no '#' or escape is added or hidden before Validate.
func FuzzRequestFrom(f *testing.F) {
	f.Add("http://example.com/a#x")
	f.Add("http://example.com/a?q=1#f")
	f.Add("/a/b?c=%41")
	f.Fuzz(func(t *testing.T, target string) {
		r, err := http.ReadRequest(bufio.NewReader(strings.NewReader("GET " + target + " HTTP/1.1\r\nHost: example.com\r\n\r\n")))
		if err != nil {
			return
		}
		req := weirhttp.RequestFrom(r)
		_, rest, ok := strings.Cut(r.RequestURI, "://")
		i := strings.IndexAny(rest, "/?#")
		if !ok || strings.HasPrefix(r.RequestURI, "/") || i < 0 {
			return
		}
		if want := strings.Replace(rest[i:], "?", "", 1); req.Path+req.RawQuery != want {
			t.Fatalf("target %q: path %q query %q, want the raw bytes %q", r.RequestURI, req.Path, req.RawQuery, rest[i:])
		}
	})
}
