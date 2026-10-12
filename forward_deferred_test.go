package weir_test

import (
	"context"
	"io"
	"net/http"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// INV-1, FR-FWD-1, NFR-5: the forwarded header is built on demand, and every
// path that reaches the origin demands it. Each fetch below must carry the
// Accept-Encoding bucket, which only the built forward has; a path that
// skipped the build would send a request with no header at all.
func TestCacheableFetchAlwaysHasForward(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var bad []string
		seen := map[string]int{}
		o := weir.OriginFunc(func(_ context.Context, r *weir.Request) (*weir.Response, error) {
			mu.Lock()
			seen[r.Path]++
			if r.Method == http.MethodGet && !slices.Equal(r.Header["Accept-Encoding"], []string{"gzip"}) {
				bad = append(bad, r.Path)
			}
			mu.Unlock()
			h := http.Header{"Cache-Control": {"max-age=2, stale-while-revalidate=60"}, "Etag": {`"a"`}}
			switch r.Path {
			case "/vary":
				h["Vary"] = []string{"Accept-Language"}
			case "/range":
				if r.Header.Get("Range") != "" {
					h["Content-Range"] = []string{"bytes 0-0/4"}
					return &weir.Response{StatusCode: 206, Header: h, Body: io.NopCloser(slicesReader("b"))}, nil
				}
			}
			return &weir.Response{StatusCode: 200, Header: h, Body: io.NopCloser(slicesReader("body"))}, nil
		})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		gz := func(path string) *weir.Request { return withHeader(getReq(path), "Accept-Encoding", "gzip, br") }

		serve(t, e, gz("/miss"), o)                                   // a flight
		serve(t, e, withHeader(gz("/auth"), "Authorization", "x"), o) // a direct fetch
		head := gz("/head")
		head.Method = http.MethodHead
		serve(t, e, head, o)                                           // HEAD, fetched as GET
		serve(t, e, gz("/vary"), o)                                    // stores a vary spec
		serve(t, e, gz("/vary"), o)                                    // a hit through the spec
		serve(t, e, withHeader(gz("/range"), "Range", "bytes=0-0"), o) // pass-through, then a background fill
		synctest.Wait()
		serve(t, e, gz("/swr"), o)
		time.Sleep(3 * time.Second)
		serve(t, e, gz("/swr"), o) // stale-while-revalidate: a background refresh
		synctest.Wait()
		if _, err := e.Warm(t.Context(), slices.Values([]*weir.Request{gz("/warm")}), o); err != nil {
			t.Fatal(err)
		}

		mu.Lock()
		defer mu.Unlock()
		if len(bad) != 0 {
			t.Fatalf("fetches without the built forward: %v", bad)
		}
		for path, want := range map[string]int{"/miss": 1, "/auth": 1, "/head": 1, "/vary": 1, "/range": 2, "/swr": 2, "/warm": 1} {
			if seen[path] != want {
				t.Errorf("%s fetched %d times, want %d: the path under test did not run", path, seen[path], want)
			}
		}
	})
}

type stringReader struct {
	s string
	i int
}

func (r *stringReader) Read(p []byte) (int, error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}

func slicesReader(s string) io.Reader { return &stringReader{s: s} }

// NFR-5: a fresh hit allocates at most 8 objects. It was 12 before the
// request's classification stayed on the stack and its forwarded header was
// left unbuilt; the release gate's bound is 16.
func TestServeHitAllocations(t *testing.T) {
	o := testorigin.New()
	o.Default(cacheable("body"))
	e, err := weir.New(cacheCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close(context.Background())
	req := withHeader(getReq("/a"), "Accept-Encoding", "gzip, br")
	hit := func() {
		resp, err := e.Serve(context.Background(), req, o)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	hit()
	if n := testing.AllocsPerRun(200, hit); n > 8 {
		t.Fatalf("a fresh hit allocates %v objects, want at most 8", n)
	}
}

// NFR-5, RFC 9110 §9.3.2: a stored response has a Content-Length even when
// the origin sent the body chunked, so a hit is written with its length and
// a HEAD reports it. The origin's own value is kept, and a 204 gets none.
func TestStoredResponseHasContentLength(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: []byte("12345")})
		o.Route("/declared", testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Content-Length": {"5"}}, Body: []byte("12345")})
		o.Route("/empty", testorigin.Behavior{Status: http.StatusNoContent, Header: http.Header{"Cache-Control": {"max-age=60"}}})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		for path, want := range map[string][]string{"/chunked": {"5"}, "/declared": {"5"}, "/empty": nil} {
			serve(t, e, getReq(path), o)
			resp, _ := serve(t, e, getReq(path), o)
			if !resp.Cache.Hit || !slices.Equal(resp.Header["Content-Length"], want) {
				t.Errorf("%s: hit %v with Content-Length %v, want a hit with %v", path, resp.Cache.Hit, resp.Header["Content-Length"], want)
			}
		}
		head := getReq("/chunked")
		head.Method = http.MethodHead
		if resp, body := serve(t, e, head, o); resp.Header.Get("Content-Length") != "5" || body != "" {
			t.Errorf("HEAD: Content-Length %q, body %q; want 5 and no body", resp.Header.Get("Content-Length"), body)
		}
	})
}
