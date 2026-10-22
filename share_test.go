package weir_test

import (
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/synctest"

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
