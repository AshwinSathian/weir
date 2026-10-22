package weir_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

// wave sends n concurrent requests for path and returns them once all ended.
func wave(t *testing.T, e *weir.Engine, o weir.Origin, path string, n int) []timedResp {
	t.Helper()
	chs := make([]chan timedResp, n)
	for i := range chs {
		chs[i] = make(chan timedResp, 1)
		go func() {
			start := time.Now()
			resp, err := e.Serve(t.Context(), getReq(path), o)
			r := timedResp{err: err}
			if err == nil {
				r.n, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				r.cache = resp.Cache
			}
			r.elapsed = time.Since(start)
			chs[i] <- r
		}()
	}
	out := make([]timedResp, n)
	for i, ch := range chs {
		out[i] = <-ch
		if out[i].err != nil {
			t.Fatalf("request %d: %v", i, out[i].err)
		}
	}
	return out
}

type timedResp struct {
	err     error
	n       int64
	elapsed time.Duration
	cache   weir.CacheInfo
}

func slowest(rs []timedResp) time.Duration {
	var d time.Duration
	for _, r := range rs {
		d = max(d, r.elapsed)
	}
	return d
}

// FR-STO-12, FR-COA-10, T-37: an object over MaxObjectBytes leaves a marker,
// so later requests do not buffer it, and each wave shares one unbuffered
// fetch. Before v1.1 every wave waited for the first MaxObjectBytes and then
// fetched again; before v1.2 every request of a wave fetched.
func TestOversizeWritesMarker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		big := bytes.Repeat([]byte("x"), 2<<20)
		o := testorigin.New()
		o.Default(testorigin.Behavior{Delay: time.Second, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: big})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		wave(t, e, o, "/big", 10) // the first wave finds no marker yet
		for w := range 2 {
			before := o.TotalCalls()
			rs := wave(t, e, o, "/big", 10)
			if d := slowest(rs); d != time.Second {
				t.Fatalf("wave %d: slowest request took %v, want one origin delay: no wait on a flight", w, d)
			}
			if n := o.TotalCalls() - before; n != 1 {
				t.Fatalf("wave %d: %d origin calls, want 1", w, n)
			}
			for i, r := range rs {
				if r.cache.Detail != "hit-for-miss" || r.n != int64(len(big)) {
					t.Fatalf("wave %d request %d: detail %q, %d bytes; want hit-for-miss and the whole body", w, i, r.cache.Detail, r.n)
				}
			}
		}
	})
}

// gatedBody sends head, then blocks until gate closes or ctx ends.
type gatedBody struct {
	head *bytes.Reader
	gate <-chan struct{}
	ctx  context.Context
}

func (b *gatedBody) Read(p []byte) (int, error) {
	if b.head.Len() > 0 {
		return b.head.Read(p)
	}
	select {
	case <-b.gate:
		return 0, io.EOF
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	}
}

func (*gatedBody) Close() error { return nil }

// gatedOrigin answers 200 with max-age, an optional Content-Length, and a
// body of head bytes that does not end until gate closes.
func gatedOrigin(head int, contentLength int64, gate <-chan struct{}, calls *atomic.Int32) weir.Origin {
	return weir.OriginFunc(func(ctx context.Context, _ *weir.Request) (*weir.Response, error) {
		calls.Add(1)
		h := http.Header{"Cache-Control": {"max-age=60"}}
		if contentLength >= 0 {
			h["Content-Length"] = []string{strconv.FormatInt(contentLength, 10)}
		}
		return &weir.Response{StatusCode: 200, Header: h,
			Body: &gatedBody{head: bytes.NewReader(make([]byte, head)), gate: gate, ctx: ctx}}, nil
	})
}

// FR-STO-12, T-18: behind an over-size marker the response streams. Serve
// returns at the headers and holds none of the body.
func TestMarkerFetchIsUnbuffered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		done := make(chan struct{})
		close(done)
		var calls atomic.Int32
		// Leaves the marker: 2 MiB, no Content-Length, so it is read first.
		serve(t, e, getReq("/big"), gatedOrigin(2<<20, -1, done, &calls))

		gate := make(chan struct{})
		resp, err := e.Serve(t.Context(), getReq("/big"), gatedOrigin(1024, -1, gate, &calls))
		if err != nil {
			t.Fatalf("Serve behind the marker: %v", err) // buffered, it would wait for the gate
		}
		if resp.Cache.Detail != "hit-for-miss" {
			t.Fatalf("detail = %q, want hit-for-miss", resp.Cache.Detail)
		}
		close(gate)
		n, _ := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if n != 1024 {
			t.Fatalf("read %d bytes, want 1024", n)
		}
	})
}

// FR-STO-12, T-18: a Content-Length above MaxObjectBytes already says the
// object cannot be stored, so nothing of it is buffered, and it leaves the
// marker.
func TestContentLengthOverLimitSkipsBuffer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs := &eventCounter{}
		cfg := cacheCfg
		cfg.Observer = obs
		e := newEngine(t, cfg)
		defer closeEngine(t, e)
		gate := make(chan struct{})
		var calls atomic.Int32
		o := gatedOrigin(1024, 2<<20, gate, &calls)

		resp, err := e.Serve(t.Context(), getReq("/big"), o)
		if err != nil {
			t.Fatalf("Serve: %v", err) // a buffered read would wait for the gate
		}
		close(gate)
		resp.Body.Close()
		if n := obs.count("not-stored/too-large"); n != 1 {
			t.Fatalf("EvNotStored{too-large} = %d, want 1", n)
		}
		resp, _ = serve(t, e, getReq("/big"), o)
		if resp.Cache.Detail != "hit-for-miss" {
			t.Fatalf("second request detail = %q, want hit-for-miss", resp.Cache.Detail)
		}
	})
}

// FR-STO-12, T-31: an over-size response to a request with unkeyed input or
// credentials writes no marker, like any other unstorable response.
func TestOversizeNoMarkerFromAuthorized(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		big := bytes.Repeat([]byte("x"), 2<<20)
		o := testorigin.New()
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"public, max-age=60"}}, Body: big})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, withHeader(getReq("/big"), "Authorization", "Bearer x"), o)
		if resp, _ := serve(t, e, getReq("/big"), o); resp.Cache.Detail == "hit-for-miss" {
			t.Fatal("a request with Authorization left a marker")
		}
	})
}

// copyStore returns a fresh copy of each record, as a remote store does.
type copyStore struct{ store.Store }

func (s copyStore) Get(ctx context.Context, k store.Key) (*store.Entry, error) {
	e, err := s.Store.Get(ctx, k)
	if err != nil {
		return nil, err
	}
	c := *e
	return &c, nil
}

// FR-STO-12, RFC 9111 §4.3.3: a full response that is not storable replaces
// the stale response the request found with a marker. Before v1.1 the stale
// response stayed for its whole retention, and every request in that time
// waited for a flight and then fetched again. The entry is matched by its
// times, so a store that returns copies behaves the same.
func TestMarkerReplacesFoundEntry(t *testing.T) {
	for _, tc := range []struct {
		name string
		wrap func(store.Store) store.Store
	}{
		{"memory store", func(s store.Store) store.Store { return s }},
		{"store that returns copies", func(s store.Store) store.Store { return copyStore{s} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, err := memory.New(memory.Config{})
				if err != nil {
					t.Fatal(err)
				}
				defer m.Close()
				cfg := cacheCfg
				cfg.Store = tc.wrap(m)
				e := newEngine(t, cfg)
				defer closeEngine(t, e)
				o := testorigin.New()
				o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=1, stale-if-error=60"}, "Etag": {`"a"`}}, Body: []byte("a")})
				serve(t, e, getReq("/a"), o)
				time.Sleep(2 * time.Second)
				o.Default(testorigin.Behavior{Delay: time.Second, Header: http.Header{"Cache-Control": {"private"}}, Body: []byte("p")})

				wave(t, e, o, "/a", 10) // finds the stale entry; its flight writes the marker
				rs := wave(t, e, o, "/a", 10)
				if d := slowest(rs); d != time.Second {
					t.Fatalf("second wave: slowest request took %v, want one origin delay", d)
				}
				for i, r := range rs {
					if r.cache.Detail != "hit-for-miss" {
						t.Fatalf("request %d: %+v, want hit-for-miss", i, r.cache)
					}
				}
			})
		})
	}
}

// FR-STO-12, FR-SRV-3: a 304 only confirmed the stored body. When its headers
// make the merged response unstorable, the stored response stays: validating
// it costs the origin less than a full response each time.
func TestNotModifiedLeavesResponseInPlace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=1"}, "Etag": {`"a"`}}, Body: []byte("a")})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		serve(t, e, getReq("/a"), o)
		time.Sleep(2 * time.Second)
		o.Default(testorigin.Behavior{Status: http.StatusNotModified, Header: http.Header{"Cache-Control": {"private"}, "Etag": {`"a"`}}})

		serve(t, e, getReq("/a"), o)
		resp, body := serve(t, e, getReq("/a"), o)
		if resp.Cache.Detail == "hit-for-miss" || resp.Cache.FwdStatus != http.StatusNotModified || body != "a" {
			t.Fatalf("got %+v with body %q; want another validation of the stored response", resp.Cache, body)
		}
	})
}

// FR-STO-12, FR-NEG-1: a 5xx never puts a marker in a stored response's place.
func TestServerErrorLeavesResponseInPlace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=1, stale-if-error=60"}}, Body: []byte("a")})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		serve(t, e, getReq("/a"), o)
		time.Sleep(2 * time.Second)
		o.Default(testorigin.Behavior{Status: http.StatusBadGateway})

		for range 2 {
			resp, body := serve(t, e, getReq("/a"), o)
			if resp.Cache.Stale != weir.StaleIfError || body != "a" {
				t.Fatalf("got %+v with body %q; want the stale response", resp.Cache, body)
			}
		}
	})
}
