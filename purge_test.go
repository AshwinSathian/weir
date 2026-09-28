package weir_test

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

func postReq(path string) *weir.Request {
	r := getReq(path)
	r.Method = http.MethodPost
	return r
}

// unsafeAnswer answers unsafe methods with status and header, and GET with
// a cacheable body.
func unsafeAnswer(status int, h http.Header, body string) testorigin.Behavior {
	return testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
		if r.Method != http.MethodGet {
			return &weir.Response{StatusCode: status, Header: h.Clone(), Body: http.NoBody}, nil
		}
		return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"` + body + `"`}},
			Body: io.NopCloser(strings.NewReader(body))}, nil
	}}
}

// FR-INV-1, FR-INV-3: a 2xx response to an unsafe method invalidates the
// target URI and same-origin Location and Content-Location URIs; a
// cross-origin one and a 4xx answer invalidate nothing. An invalidated
// entry with a validator is revalidated, not refetched in full.
func TestUnsafeMethodInvalidates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(cacheable("v"))
		o.Route("/a", unsafeAnswer(http.StatusCreated, http.Header{
			"Location":         {"b?q=1"},
			"Content-Location": {"https://EXAMPLE.com:443/c"},
		}, "a"))
		o.Route("/x", unsafeAnswer(http.StatusCreated, http.Header{"Location": {"https://other.example/y"}}, "x"))
		o.Route("/f", unsafeAnswer(http.StatusSeeOther, http.Header{"Location": {"/g"}}, "f"))
		o.Route("/d", unsafeAnswer(http.StatusNotFound, http.Header{"Location": {"/e"}}, "d"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		other := func(path string) *weir.Request { r := getReq(path); r.Host = "other.example"; return r }
		reqs := map[string]func() *weir.Request{
			"/a":   func() *weir.Request { return getReq("/a") },
			"/b?q": func() *weir.Request { r := getReq("/b"); r.RawQuery = "q=1"; return r },
			"/c":   func() *weir.Request { return getReq("/c") },
			"/d":   func() *weir.Request { return getReq("/d") },
			"/e":   func() *weir.Request { return getReq("/e") },
			"/x":   func() *weir.Request { return getReq("/x") },
			"/g":   func() *weir.Request { return getReq("/g") },
			"oy":   func() *weir.Request { return other("/y") },
		}
		for _, r := range reqs {
			serve(t, e, r(), o)
		}
		time.Sleep(time.Second)
		serve(t, e, postReq("/a"), o)
		serve(t, e, postReq("/x"), o)
		serve(t, e, postReq("/f"), o)
		serve(t, e, postReq("/d"), o)
		time.Sleep(2 * time.Second) // epochs round up to whole seconds (E-7)

		for name, want := range map[string]bool{"/a": false, "/b?q": false, "/c": false, "/x": false, "/g": false, "/d": true, "/e": true, "oy": true} {
			resp, _ := serve(t, e, reqs[name](), o)
			if resp.Cache.Hit != want {
				t.Errorf("%s: hit = %v, want %v (%s)", name, resp.Cache.Hit, want, resp.Header.Get("Cache-Status"))
			}
		}
		var last *weir.Request
		for _, r := range o.Requests() {
			if r.Path == "/a" && r.Method == http.MethodGet {
				last = r
			}
		}
		if got := last.Header.Get("If-None-Match"); got != `"a"` {
			t.Errorf("revalidation of /a sent If-None-Match %q, want %q", got, `"a"`)
		}
	})
}

// FR-PRG-7, T-10: a fetch sent before an invalidation and stored after it
// does not survive the invalidation.
func TestPurgeDuringInflightFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		var mu sync.Mutex
		gets := 0
		o := testorigin.New()
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			if r.Method == http.MethodPost {
				return &weir.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
			}
			mu.Lock()
			gets++
			first := gets == 1
			mu.Unlock()
			if first {
				<-gate
			}
			return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{"Cache-Control": {"max-age=60"}},
				Body: io.NopCloser(strings.NewReader("old"))}, nil
		}})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		done := make(chan struct{})
		go func() {
			defer close(done)
			serve(t, e, getReq("/a"), o)
		}()
		synctest.Wait() // the GET is at the origin
		time.Sleep(time.Second)
		serve(t, e, postReq("/a"), o)
		time.Sleep(time.Second)
		close(gate)
		<-done

		time.Sleep(2 * time.Second)
		resp, _ := serve(t, e, getReq("/a"), o)
		if resp.Cache.Hit {
			t.Fatalf("pre-invalidation fetch served as a hit: %s", resp.Header.Get("Cache-Status"))
		}
		mu.Lock()
		defer mu.Unlock()
		if gets != 2 {
			t.Fatalf("origin GETs = %d, want 2", gets)
		}
	})
}
