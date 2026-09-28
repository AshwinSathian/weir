package weirhttp_test

import (
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
