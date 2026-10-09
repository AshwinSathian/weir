package weir_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

func rangeOrigin(t *testing.T, h http.Header, body string) *testorigin.Origin {
	t.Helper()
	o := testorigin.NewChecked(t, 64, 16)
	hdr := http.Header{"Cache-Control": {"max-age=60"}, "Content-Type": {"text/plain"}}
	for k, v := range h {
		hdr[k] = v
	}
	o.Default(testorigin.Behavior{Header: hdr, Body: []byte(body)})
	return o
}

// FR-RNG-1, FR-RNG-2, FR-RNG-5
func TestRangeSingleFromCache(t *testing.T) {
	tests := []struct {
		name, rng, body, contentRange string
	}{
		{"closed range", "bytes=2-5", "2345", "bytes 2-5/10"},
		{"open end", "bytes=7-", "789", "bytes 7-9/10"},
		{"suffix", "bytes=-4", "6789", "bytes 6-9/10"},
		{"end past the body is clamped", "bytes=8-99", "89", "bytes 8-9/10"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := rangeOrigin(t, nil, "0123456789")
				e := newEngine(t, cacheCfg)
				defer closeEngine(t, e)
				serve(t, e, getReq("/a"), o)

				resp, body := serve(t, e, withHeader(getReq("/a"), "Range", tc.rng), o)
				if resp.StatusCode != http.StatusPartialContent || body != tc.body {
					t.Fatalf("got %d %q, want 206 %q", resp.StatusCode, body, tc.body)
				}
				if got := resp.Header.Get("Content-Range"); got != tc.contentRange {
					t.Fatalf("Content-Range = %q, want %q", got, tc.contentRange)
				}
				if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(len(tc.body)) {
					t.Fatalf("Content-Length = %q, want %d", got, len(tc.body))
				}
				if resp.Header.Get("Content-Type") != "text/plain" || resp.Header.Get("Age") == "" {
					t.Fatalf("lost stored fields: %v", resp.Header)
				}
				// FR-RNG-5
				if !resp.Cache.Hit || !strings.Contains(strings.Join(resp.Header["Cache-Status"], ","), "hit") {
					t.Fatalf("not a hit: %+v %v", resp.Cache, resp.Header["Cache-Status"])
				}
				if n := o.Calls("/a"); n != 1 {
					t.Fatalf("origin calls = %d, want 1", n)
				}
				// The stored entry is untouched (P4): a full request still gets all of it.
				if _, full := serve(t, e, getReq("/a"), o); full != "0123456789" {
					t.Fatalf("stored body changed to %q", full)
				}
			})
		})
	}
}

// FR-RNG-3
func TestRangeUnsatisfiable416(t *testing.T) {
	for _, rng := range []string{"bytes=10-", "bytes=50-60", "bytes=-0"} {
		t.Run(rng, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := rangeOrigin(t, http.Header{"Etag": {`"v1"`}}, "0123456789")
				e := newEngine(t, cacheCfg)
				defer closeEngine(t, e)
				serve(t, e, getReq("/a"), o)

				resp, body := serve(t, e, withHeader(getReq("/a"), "Range", rng), o)
				if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable || body != "" {
					t.Fatalf("got %d %q, want 416 with no body", resp.StatusCode, body)
				}
				if got := resp.Header.Get("Content-Range"); got != "bytes */10" {
					t.Fatalf("Content-Range = %q", got)
				}
				if !resp.Cache.Hit || resp.Header.Get("Content-Length") != "" {
					t.Fatalf("416: hit=%v Content-Length=%q", resp.Cache.Hit, resp.Header.Get("Content-Length"))
				}
			})
		})
	}
}

// FR-RNG-3: RFC 9110 §14.2 lets a server ignore Range it cannot honor.
func TestRangeMultiOrInvalidGets200(t *testing.T) {
	for _, rng := range []string{"bytes=0-1,4-5", "bytes=5-2", "items=0-1", "bytes=a-b", "bytes", "bytes=0-1, "} {
		t.Run(rng, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := rangeOrigin(t, nil, "0123456789")
				e := newEngine(t, cacheCfg)
				defer closeEngine(t, e)
				serve(t, e, getReq("/a"), o)

				resp, body := serve(t, e, withHeader(getReq("/a"), "Range", rng), o)
				if resp.StatusCode != http.StatusOK || body != "0123456789" || resp.Header.Get("Content-Range") != "" || !resp.Cache.Hit {
					t.Fatalf("got %d %q Content-Range=%q hit=%v, want the full 200 hit", resp.StatusCode, body, resp.Header.Get("Content-Range"), resp.Cache.Hit)
				}
			})
		})
	}
	t.Run("repeated Range lines", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := rangeOrigin(t, nil, "0123456789")
			e := newEngine(t, cacheCfg)
			defer closeEngine(t, e)
			serve(t, e, getReq("/a"), o)
			req := getReq("/a")
			req.Header["Range"] = []string{"bytes=0-1", "bytes=3-4"}
			if resp, body := serve(t, e, req, o); resp.StatusCode != http.StatusOK || body != "0123456789" {
				t.Fatalf("got %d %q", resp.StatusCode, body)
			}
		})
	})
	t.Run("HEAD ignores Range", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := rangeOrigin(t, nil, "0123456789")
			e := newEngine(t, cacheCfg)
			defer closeEngine(t, e)
			serve(t, e, getReq("/a"), o)
			resp, body := serve(t, e, withHeader(headReq("/a"), "Range", "bytes=0-1"), o)
			if resp.StatusCode != http.StatusOK || body != "" || resp.Header.Get("Content-Range") != "" {
				t.Fatalf("got %d %q Content-Range=%q", resp.StatusCode, body, resp.Header.Get("Content-Range"))
			}
		})
	})
}

// FR-RNG-1: only a strong validator lets the range apply.
func TestIfRangeStrongOnly(t *testing.T) {
	lm := "Fri, 01 Jan 1999 00:00:00 GMT" // older than the response Date (synctest starts in 2000)
	tests := []struct {
		name     string
		stored   http.Header
		ifRange  []string
		wantCode int
	}{
		{"matching strong etag", http.Header{"Etag": {`"v1"`}}, []string{`"v1"`}, 206},
		{"other etag", http.Header{"Etag": {`"v1"`}}, []string{`"v2"`}, 200},
		{"weak etag in If-Range", http.Header{"Etag": {`W/"v1"`}}, []string{`W/"v1"`}, 200},
		{"weak stored etag", http.Header{"Etag": {`W/"v1"`}}, []string{`"v1"`}, 200},
		{"strong last-modified date", http.Header{"Last-Modified": {lm}}, []string{lm}, 206},
		{"other date", http.Header{"Last-Modified": {lm}}, []string{"Sat, 02 Jan 1999 00:00:00 GMT"}, 200},
		{"last-modified too close to Date is weak", http.Header{"Last-Modified": {"Mon, 01 Jan 2035 00:00:00 GMT"}}, []string{"Mon, 01 Jan 2035 00:00:00 GMT"}, 200},
		{"repeated If-Range", http.Header{"Etag": {`"v1"`}}, []string{`"v1"`, `"v1"`}, 200},
		{"empty If-Range", http.Header{"Etag": {`"v1"`}}, []string{""}, 200},
		{"validator the entry lacks", nil, []string{`"v1"`}, 200},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := rangeOrigin(t, tc.stored, "0123456789")
				e := newEngine(t, cacheCfg)
				defer closeEngine(t, e)
				serve(t, e, getReq("/a"), o)
				req := withHeader(getReq("/a"), "Range", "bytes=0-1")
				req.Header["If-Range"] = tc.ifRange
				resp, body := serve(t, e, req, o)
				want := "0123456789"
				if tc.wantCode == 206 {
					want = "01"
				}
				if resp.StatusCode != tc.wantCode || body != want {
					t.Fatalf("got %d %q, want %d %q", resp.StatusCode, body, tc.wantCode, want)
				}
			})
		})
	}
}

// FR-RNG-1: an entry servable under SWR is sliced too, while it revalidates in the background.
func TestRangeOnSWREntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := rangeOrigin(t, http.Header{"Cache-Control": {"max-age=10, stale-while-revalidate=30"}}, "0123456789")
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		serve(t, e, getReq("/a"), o)
		time.Sleep(20 * time.Second)
		resp, body := serve(t, e, withHeader(getReq("/a"), "Range", "bytes=1-2"), o)
		if resp.StatusCode != http.StatusPartialContent || body != "12" || resp.Cache.Stale != weir.StaleWhileRevalidate {
			t.Fatalf("got %d %q stale=%v, want 206 \"12\" under SWR", resp.StatusCode, body, resp.Cache.Stale)
		}
	})
}

// FR-SRV-2, RFC 9110 §13.2.2: a matching If-None-Match is judged before Range.
func TestRangeAfterClientConditional(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := rangeOrigin(t, http.Header{"Etag": {`"v1"`}}, "0123456789")
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		serve(t, e, getReq("/a"), o)
		resp, _ := serve(t, e, withHeader(withHeader(getReq("/a"), "Range", "bytes=1-2"), "If-None-Match", `"v1"`), o)
		if resp.StatusCode != http.StatusNotModified {
			t.Fatalf("got %d, want 304", resp.StatusCode)
		}
	})
}

// FR-RNG-1: only a stored 200 is sliced.
func TestRangeOnNonOKEntryNotSliced(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Status: http.StatusMovedPermanently, Header: http.Header{"Cache-Control": {"max-age=60"}, "Location": {"/b"}}, Body: []byte("0123456789")})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		serve(t, e, getReq("/a"), o)
		resp, body := serve(t, e, withHeader(getReq("/a"), "Range", "bytes=1-2"), o)
		if resp.StatusCode != http.StatusMovedPermanently || body != "0123456789" || resp.Header.Get("Content-Range") != "" {
			t.Fatalf("got %d %q Content-Range=%q", resp.StatusCode, body, resp.Header.Get("Content-Range"))
		}
	})
}
