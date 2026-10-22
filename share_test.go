package weir_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// overHerd sends n concurrent requests (made by req) for an over-size object
// while the origin is held, then releases it, and returns what each got.
func overHerd(t *testing.T, h http.Header, n int, req func(i int) *weir.Request) (calls int, bodies []int64, collapsed int) {
	t.Helper()
	gate := make(chan struct{})
	var count atomic.Int32
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(testorigin.Behavior{Func: func(*weir.Request) (*weir.Response, error) {
		count.Add(1)
		<-gate
		hdr := http.Header{"Cache-Control": {"max-age=60"}}
		for k, v := range h {
			hdr[k] = v
		}
		return &weir.Response{StatusCode: http.StatusOK, Header: hdr, Body: io.NopCloser(io.LimitReader(zeros{}, 3<<20))}, nil
	}})
	e := newEngine(t, cacheCfg)
	defer closeEngine(t, e)
	type res struct {
		n         int64
		collapsed bool
	}
	chs := make([]chan res, n)
	for i := range chs {
		chs[i] = make(chan res, 1)
		go func() {
			resp, err := e.Serve(t.Context(), req(i), o)
			if err != nil {
				t.Errorf("request %d: %v", i, err)
				chs[i] <- res{}
				return
			}
			m, _ := io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			chs[i] <- res{m, resp.Cache.Collapsed}
		}()
	}
	synctest.Wait()
	close(gate)
	for _, ch := range chs {
		r := <-ch
		bodies = append(bodies, r.n)
		if r.collapsed {
			collapsed++
		}
	}
	return int(count.Load()), bodies, collapsed
}

// FR-COA-10, FR-STO-6, T-8: an over-size response the store would refuse
// for any reason but its size is never shared. Each waiter fetches for
// itself, as the store's rules say.
func TestSharedStreamOnlyWhenStorable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		h      http.Header
		shared bool
	}{
		{"storable but for size is shared", nil, true},
		{"private is not shared", http.Header{"Cache-Control": {"private, max-age=60"}}, false},
		{"Set-Cookie is not shared", http.Header{"Set-Cookie": {"s=1"}}, false},
		{"Vary: * is not shared", http.Header{"Vary": {"*"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls, bodies, collapsed := overHerd(t, tc.h, 4, func(int) *weir.Request { return getReq("/big") })
				for i, b := range bodies {
					if b != 3<<20 {
						t.Fatalf("request %d read %d bytes, want the whole body", i, b)
					}
				}
				switch {
				case tc.shared && (calls != 1 || collapsed != 3):
					t.Fatalf("calls %d, collapsed %d; want 1 and 3", calls, collapsed)
				case !tc.shared && (calls != 4 || collapsed != 0):
					t.Fatalf("calls %d, collapsed %d; want 4 and 0", calls, collapsed)
				}
			})
		})
	}
}

// FR-COA-10, FR-KEY-7: a follower shares the stream only when it asked for
// the same variant; one that asked for another fetches its own.
func TestSharedStreamSameVariantOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Accept-Encoding reaches the origin as one coding (FR-STO-14), so
		// the identity request selects another variant than the gzip ones.
		calls, bodies, collapsed := overHerd(t, http.Header{"Vary": {"Accept-Encoding"}}, 4, func(i int) *weir.Request {
			ae := "gzip"
			if i == 3 {
				ae = "identity"
			}
			return withHeader(getReq("/big"), "Accept-Encoding", ae)
		})
		for i, b := range bodies {
			if b != 3<<20 {
				t.Fatalf("request %d read %d bytes", i, b)
			}
		}
		// The identity request may lead or follow; either way it never
		// shares a gzip stream, and the gzip requests never share its.
		if calls < 2 || collapsed > 2 {
			t.Fatalf("calls %d, collapsed %d; the identity request must not share a gzip stream", calls, collapsed)
		}
	})
}

// FR-COA-10: a HEAD follower gets the headers and takes no place on the
// stream, so the others still read it all.
func TestSharedStreamHeadFollower(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls, bodies, _ := overHerd(t, nil, 3, func(i int) *weir.Request {
			r := getReq("/big")
			if i == 2 {
				r.Method = http.MethodHead
			}
			return r
		})
		if calls != 1 {
			t.Fatalf("calls %d, want 1", calls)
		}
		if bodies[0] != 3<<20 || bodies[1] != 3<<20 || bodies[2] != 0 {
			t.Fatalf("bodies %v", bodies)
		}
	})
}

// FR-COA-10, FR-STO-6, T-8: with StripSetCookie the creator keeps its own
// Set-Cookie, and a follower reading the shared stream never gets it.
func TestSharedStreamStripsSetCookieForFollowers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Func: func(*weir.Request) (*weir.Response, error) {
			<-gate
			return &weir.Response{StatusCode: http.StatusOK,
				Header: http.Header{"Cache-Control": {"max-age=60"}, "Set-Cookie": {"session=creator"}},
				Body:   io.NopCloser(io.LimitReader(zeros{}, 3<<20))}, nil
		}})
		cfg := cacheCfg
		cfg.Storable.StripSetCookie = true
		e := newEngine(t, cfg)
		defer closeEngine(t, e)
		type res struct {
			cookie    string
			collapsed bool
		}
		chs := make([]chan res, 3)
		for i := range chs {
			chs[i] = make(chan res, 1)
			go func() {
				resp, err := e.Serve(t.Context(), getReq("/big"), o)
				if err != nil {
					t.Error(err)
					chs[i] <- res{}
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				chs[i] <- res{resp.Header.Get("Set-Cookie"), resp.Cache.Collapsed}
			}()
		}
		synctest.Wait()
		close(gate)
		for _, ch := range chs {
			r := <-ch
			if r.collapsed && r.cookie != "" {
				t.Fatalf("a follower got Set-Cookie %q", r.cookie)
			}
			if !r.collapsed && r.cookie != "session=creator" {
				t.Fatalf("the creator lost its Set-Cookie: %q", r.cookie)
			}
		}
	})
}

// closeFlagBody is an origin body that records whether it was closed.
type closeFlagBody struct {
	io.Reader
	closed *atomic.Bool
}

func (b closeFlagBody) Close() error { b.closed.Store(true); return nil }

// FR-COA-10: a warm request that joins the flight takes itself out of the
// count, so when the only client closes its body unread the origin body is
// closed too, not held for a reader that will never come.
func TestSharedStreamWarmJoinerDoesNotHoldBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		var closed atomic.Bool
		o := weir.OriginFunc(func(context.Context, *weir.Request) (*weir.Response, error) {
			<-gate
			return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{"Cache-Control": {"max-age=60"}},
				Body: closeFlagBody{io.LimitReader(zeros{}, 3<<20), &closed}}, nil
		})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		got := make(chan *weir.Response, 1)
		go func() {
			resp, err := e.Serve(t.Context(), getReq("/big"), o)
			if err != nil {
				t.Error(err)
			}
			got <- resp
		}()
		synctest.Wait()
		warmed := make(chan struct{})
		go func() {
			defer close(warmed)
			_, _ = e.Warm(t.Context(), slices.Values([]*weir.Request{getReq("/big")}), o)
		}()
		synctest.Wait()
		close(gate)
		resp := <-got
		<-warmed
		resp.Body.Close()
		synctest.Wait()
		if !closed.Load() {
			t.Fatal("origin body still open after the only reader closed")
		}
	})
}

// FR-COA-10: a slow client moved off a shared stream continues on a range
// request of its own when the origin offers byte ranges and a strong
// entity-tag, and gets the whole body; the fast client is not held.
func TestSharedStreamSlowClientResumesWithRange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const size = 4 << 20
		body := make([]byte, size)
		for i := range body {
			body[i] = byte(i % 251)
		}
		gate := make(chan struct{})
		var full, ranged atomic.Int32
		o := weir.OriginFunc(func(_ context.Context, r *weir.Request) (*weir.Response, error) {
			h := http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"v1"`}, "Accept-Ranges": {"bytes"}}
			if rg := r.Header.Get("Range"); rg != "" {
				ranged.Add(1)
				if r.Header.Get("If-Range") != `"v1"` {
					t.Errorf("resume without If-Range: %v", r.Header)
				}
				var off int
				if _, err := fmt.Sscanf(rg, "bytes=%d-", &off); err != nil {
					t.Errorf("Range %q: %v", rg, err)
				}
				h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, size-1, size))
				return &weir.Response{StatusCode: http.StatusPartialContent, Header: h, Body: io.NopCloser(bytes.NewReader(body[off:]))}, nil
			}
			full.Add(1)
			<-gate
			return &weir.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(bytes.NewReader(body))}, nil
		})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		resps := make(chan *weir.Response, 2)
		for range 2 {
			go func() {
				resp, err := e.Serve(t.Context(), getReq("/big"), o)
				if err != nil {
					t.Error(err)
				}
				resps <- resp
			}()
		}
		synctest.Wait()
		close(gate)
		a, b := <-resps, <-resps
		var slowGot bytes.Buffer
		p := make([]byte, 4096)
		n, _ := b.Body.Read(p) // the slow client reads a little, then stalls
		slowGot.Write(p[:n])
		start := time.Now()
		if m, err := io.Copy(io.Discard, a.Body); err != nil || m != size {
			t.Fatalf("fast client: %d bytes, %v", m, err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("fast client held %v by the slow one", d)
		}
		if _, err := io.Copy(&slowGot, b.Body); err != nil {
			t.Fatalf("slow client: %v", err)
		}
		if !bytes.Equal(slowGot.Bytes(), body) {
			t.Fatalf("slow client got %d bytes, not the body", slowGot.Len())
		}
		a.Body.Close()
		b.Body.Close()
		if full.Load() != 1 || ranged.Load() != 1 {
			t.Fatalf("origin: %d full, %d ranged; want 1 and 1", full.Load(), ranged.Load())
		}
	})
}
