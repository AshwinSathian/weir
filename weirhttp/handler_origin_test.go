package weirhttp_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/weirhttp"
)

// FR-FWD-1, INV-1, FR-STR-1: HandlerOrigin returns as soon as the handler
// writes headers, streams the body as the handler writes it, and hands the
// handler the forwarded request unchanged.
func TestHandlerOriginStreams(t *testing.T) {
	t.Run("headers return before the handler finishes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			release := make(chan struct{})
			done := make(chan struct{})
			var got *http.Request
			o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(done)
				got = r
				w.Header().Set("X-A", "1")
				w.WriteHeader(http.StatusNonAuthoritativeInfo)
				w.Header().Set("X-A", "changed after WriteHeader")
				_, _ = io.WriteString(w, "a")
				w.(http.Flusher).Flush()
				<-release
				_, _ = io.WriteString(w, "b")
			})}
			req := &weir.Request{
				Method: "GET", Scheme: "http", Host: "example.com",
				Path: "//x/a%2Fb", RawQuery: "q=%20",
				Header: http.Header{"Accept": {"text/plain"}},
			}
			resp, err := o.Fetch(t.Context(), req)
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusNonAuthoritativeInfo || resp.Header.Get("X-A") != "1" {
				t.Errorf("got %d X-A=%q, want 203 X-A=1", resp.StatusCode, resp.Header.Get("X-A"))
			}
			if got.RequestURI != "//x/a%2Fb?q=%20" || got.URL.EscapedPath() != "//x/a%2Fb" || got.URL.RawQuery != "q=%20" {
				t.Errorf("handler saw RequestURI %q path %q query %q", got.RequestURI, got.URL.EscapedPath(), got.URL.RawQuery)
			}
			if got.Host != "example.com" || got.Method != http.MethodGet || got.Header.Get("Accept") != "text/plain" {
				t.Errorf("handler saw %s Host %q header %v", got.Method, got.Host, got.Header)
			}
			buf := make([]byte, 1)
			if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != "a" {
				t.Fatalf("first byte %q, %v; want a", buf, err)
			}
			close(release)
			rest, err := io.ReadAll(resp.Body)
			if err != nil || string(rest) != "b" {
				t.Errorf("rest %q, %v; want b", rest, err)
			}
			<-done
		})
	})
	t.Run("handler that writes nothing is 200 with an empty body", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
			resp, err := o.Fetch(t.Context(), &weir.Request{Method: "GET", Path: "/", Header: http.Header{}})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK || len(body) != 0 || err != nil {
				t.Errorf("got %d %q %v, want 200 empty", resp.StatusCode, body, err)
			}
		})
	})
	t.Run("handler panic before headers is an error, not a crash", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })}
			if _, err := o.Fetch(t.Context(), &weir.Request{Method: "GET", Path: "/"}); !errors.Is(err, weir.ErrOrigin) {
				t.Errorf("Fetch err = %v, want weir.ErrOrigin", err)
			}
		})
	})
	t.Run("handler panic after headers fails the body read", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "a")
				panic("boom")
			})}
			resp, err := o.Fetch(t.Context(), &weir.Request{Method: "GET", Path: "/"})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			// A clean EOF here would let a truncated body be stored as complete.
			if _, err := io.ReadAll(resp.Body); !errors.Is(err, weir.ErrOrigin) {
				t.Errorf("ReadAll err = %v, want weir.ErrOrigin", err)
			}
		})
	})
	t.Run("body shorter than Content-Length fails the read", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			// A clean EOF would let the engine store 5 bytes under
			// Content-Length: 10, and every hit would hang the client.
			o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "10")
				_, _ = io.WriteString(w, "hello")
			})}
			resp, err := o.Fetch(t.Context(), &weir.Request{Method: "GET", Path: "/"})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if _, err := io.ReadAll(resp.Body); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("ReadAll err = %v, want io.ErrUnexpectedEOF", err)
			}
		})
	})
	t.Run("write past Content-Length fails", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var writeErr error
			o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "2")
				_, writeErr = io.WriteString(w, "abc")
			})}
			resp, err := o.Fetch(t.Context(), &weir.Request{Method: "GET", Path: "/"})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if !errors.Is(writeErr, http.ErrContentLength) || len(body) > 2 {
				t.Errorf("write err %v, body %q; want http.ErrContentLength and at most 2 bytes", writeErr, body)
			}
		})
	})
	t.Run("HEAD discards the body the handler writes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "5")
				_, _ = io.WriteString(w, "hello")
			})}
			resp, err := o.Fetch(t.Context(), &weir.Request{Method: "HEAD", Path: "/"})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || len(body) != 0 || resp.Header.Get("Content-Length") != "5" {
				t.Errorf("HEAD got %q, %v, Content-Length %q; want empty body, 5", body, err, resp.Header.Get("Content-Length"))
			}
		})
	})
	t.Run("304 with Content-Length ends cleanly", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "5") // the selected representation's length (RFC 9110 §8.6)
				w.WriteHeader(http.StatusNotModified)
			})}
			resp, err := o.Fetch(t.Context(), &weir.Request{Method: "GET", Path: "/"})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			_, err = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Errorf("304 body read err = %v, want clean EOF", err)
			}
		})
	})
	t.Run("runtime.Goexit after headers fails the body read", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "a")
				runtime.Goexit() // what t.FailNow in a handler does
			})}
			resp, err := o.Fetch(t.Context(), &weir.Request{Method: "GET", Path: "/"})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if _, err := io.ReadAll(resp.Body); !errors.Is(err, weir.ErrOrigin) {
				t.Errorf("ReadAll err = %v, want weir.ErrOrigin", err)
			}
		})
	})
	for _, code := range []int{http.StatusNoContent, http.StatusNotModified} {
		t.Run(fmt.Sprintf("%d body writes are refused", code), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// Stored, a 204 with a body would be written on every hit.
				var writeErr error
				o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Cache-Control", "max-age=60")
					w.WriteHeader(code)
					_, writeErr = io.WriteString(w, "body")
				})}
				resp, err := o.Fetch(t.Context(), &weir.Request{Method: "GET", Path: "/"})
				if err != nil {
					t.Fatalf("Fetch: %v", err)
				}
				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil || len(body) != 0 || !errors.Is(writeErr, http.ErrBodyNotAllowed) {
					t.Errorf("got body %q, %v, write err %v; want empty, http.ErrBodyNotAllowed", body, err, writeErr)
				}
			})
		})
	}
	for _, cl := range [][]string{{"abc"}, {"-1"}, {"1", "2"}, {}} {
		t.Run(fmt.Sprintf("invalid Content-Length %q is dropped", cl), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header()["Content-Length"] = cl
					_, _ = io.WriteString(w, "x")
				})}
				resp, err := o.Fetch(t.Context(), &weir.Request{Method: "GET", Path: "/"})
				if err != nil {
					t.Fatalf("Fetch: %v", err)
				}
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if v, ok := resp.Header["Content-Length"]; ok || string(body) != "x" {
					t.Errorf("Content-Length %q kept, body %q", v, body)
				}
			})
		})
	}
	t.Run("request body and length reach the handler and are closed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			for _, tc := range []struct {
				cl     string
				wantCL int64
			}{{"5", 5}, {"", -1}} {
				var got string
				var gotCL int64
				rb := &closeTracker{Reader: strings.NewReader("hello")}
				o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					b, _ := io.ReadAll(r.Body)
					got, gotCL = string(b), r.ContentLength
				})}
				h := http.Header{}
				if tc.cl != "" {
					h.Set("Content-Length", tc.cl)
				}
				resp, err := o.Fetch(t.Context(), &weir.Request{Method: "POST", Path: "/", Header: h, Body: rb})
				if err != nil {
					t.Fatalf("Fetch: %v", err)
				}
				_, _ = io.ReadAll(resp.Body) // EOF comes after the handler returns
				_ = resp.Body.Close()
				if got != "hello" || gotCL != tc.wantCL || !rb.closed {
					t.Errorf("CL %q: handler got %q length %d, closed %v; want hello %d true", tc.cl, got, gotCL, rb.closed, tc.wantCL)
				}
			}
		})
	})
	t.Run("malformed escape in the path is an invalid request", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			called := false
			o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })}
			if _, err := o.Fetch(t.Context(), &weir.Request{Method: "GET", Path: "/a%zz"}); !errors.Is(err, weir.ErrInvalidRequest) || called {
				t.Errorf("err = %v, handler called %v; want weir.ErrInvalidRequest, not called", err, called)
			}
		})
	})
	t.Run("https scheme reaches the handler as r.TLS", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var tlsSet bool
			o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { tlsSet = r.TLS != nil })}
			resp, err := o.Fetch(t.Context(), &weir.Request{Method: "GET", Scheme: "https", Path: "/"})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			_, _ = io.ReadAll(resp.Body) // EOF comes after the handler returns
			_ = resp.Body.Close()
			if !tlsSet {
				t.Error("handler saw r.TLS == nil for an https request")
			}
		})
	})
}

// FR-COA-9, 03 §5 duty 2: cancelling ctx (Close cancels the fetch context)
// stops the handler (its context ends and its writes fail) and any body
// read in progress.
func TestHandlerOriginCancel(t *testing.T) {
	t.Run("cancel before headers", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			done := make(chan struct{})
			o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				defer close(done)
				<-r.Context().Done()
			})}
			ctx, cancel := context.WithCancel(t.Context())
			errc := make(chan error, 1)
			go func() {
				_, err := o.Fetch(ctx, &weir.Request{Method: "GET", Path: "/"})
				errc <- err
			}()
			synctest.Wait()
			cancel()
			if err := <-errc; !errors.Is(err, context.Canceled) {
				t.Errorf("Fetch err = %v, want context.Canceled", err)
			}
			<-done
		})
	})
	t.Run("cancel stops a blocked body read", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			release := make(chan struct{})
			o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				<-release // stalls, ignoring its context
			})}
			ctx, cancel := context.WithCancel(t.Context())
			resp, err := o.Fetch(ctx, &weir.Request{Method: "GET", Path: "/"})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			errc := make(chan error, 1)
			go func() {
				_, err := resp.Body.Read(make([]byte, 1))
				errc <- err
			}()
			synctest.Wait()
			cancel()
			if err := <-errc; !errors.Is(err, context.Canceled) {
				t.Errorf("body read err = %v, want context.Canceled", err)
			}
			close(release)
		})
	})
	t.Run("closing the body cancels the handler", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			done := make(chan struct{})
			o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(done)
				w.WriteHeader(http.StatusOK)
				<-r.Context().Done()
			})}
			resp, err := o.Fetch(t.Context(), &weir.Request{Method: "GET", Path: "/"})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			_ = resp.Body.Close()
			<-done
		})
	})
	t.Run("cancel fails a blocked handler write", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			done := make(chan struct{})
			var writeErr error
			o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				defer close(done)
				w.WriteHeader(http.StatusOK)
				// A handler that ignores its context still stops: its next
				// write fails once ctx is cancelled.
				_, writeErr = io.WriteString(w, "x")
			})}
			ctx, cancel := context.WithCancel(t.Context())
			resp, err := o.Fetch(ctx, &weir.Request{Method: "GET", Path: "/"})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			cancel()
			synctest.Wait()
			<-done
			if writeErr == nil {
				t.Error("handler write succeeded after cancel")
			}
			if _, err := resp.Body.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
				t.Errorf("body read err = %v, want context.Canceled", err)
			}
		})
	})
}

// NFR-2, RFC 9111 §3.3 (01 §9 table): through the engine, a HandlerOrigin response is stored and
// served as a hit, and a body cut short of its Content-Length is never stored.
func TestHandlerOriginThroughEngine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		short := atomic.Bool{}
		o := &weirhttp.HandlerOrigin{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.Header().Set("Cache-Control", "max-age=60")
			w.Header().Set("Content-Length", "5")
			if short.Load() {
				_, _ = io.WriteString(w, "he")
				return
			}
			_, _ = io.WriteString(w, "hello")
		})}
		// Negative caching off: each truncated fetch must reach the origin.
		e, err := weir.New(weir.Config{Freshness: weir.FreshnessConfig{NoJitter: true}, Negative: weir.NegativeConfig{Disable: true}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer func() { _ = e.Close(context.Background()) }()
		h := weirhttp.Handler(e, o)
		get := func(path string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com"+path, nil))
			return w
		}
		for range 2 {
			if w := get("/ok"); w.Code != http.StatusOK || w.Body.String() != "hello" {
				t.Fatalf("/ok got %d %q", w.Code, w.Body)
			}
		}
		if n := calls.Load(); n != 1 {
			t.Errorf("origin calls for /ok = %d, want 1 (second is a hit)", n)
		}
		short.Store(true)
		for range 2 {
			if w := get("/short"); w.Code == http.StatusOK && w.Body.String() == "he" {
				t.Errorf("/short served a truncated body as complete")
			}
		}
		if n := calls.Load(); n != 3 {
			t.Errorf("origin calls = %d, want 3 (truncated body not stored)", n)
		}
	})
}

// closeTracker records whether the request body was closed.
type closeTracker struct {
	io.Reader
	closed bool
}

func (c *closeTracker) Close() error {
	c.closed = true
	return nil
}
