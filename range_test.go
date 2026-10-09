package weir_test

import (
	"io"
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

// FR-RNG-1: an origin that said Accept-Ranges: none is not contradicted.
func TestRangeIgnoredWhenAcceptRangesNone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := rangeOrigin(t, http.Header{"Accept-Ranges": {"none"}}, "0123456789")
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		serve(t, e, getReq("/a"), o)
		resp, body := serve(t, e, withHeader(getReq("/a"), "Range", "bytes=1-2"), o)
		if resp.StatusCode != http.StatusOK || body != "0123456789" || resp.Header.Get("Content-Range") != "" || !resp.Cache.Hit {
			t.Fatalf("got %d %q Content-Range=%q hit=%v, want the full 200 hit", resp.StatusCode, body, resp.Header.Get("Content-Range"), resp.Cache.Hit)
		}
	})
}

// rangeFillOrigin answers a Range request with a 206 of the first two bytes
// and anything else with the full 200. hdr overrides the default headers;
// total is the size the Content-Range declares.
func rangeFillOrigin(t *testing.T, hdr http.Header, total string) *testorigin.Origin {
	t.Helper()
	return rangeFillGated(t, hdr, total, nil)
}

// rangeFillGated is rangeFillOrigin whose full-object answers wait for gate.
func rangeFillGated(t *testing.T, hdr http.Header, total string, gate <-chan struct{}) *testorigin.Origin {
	t.Helper()
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
		h := http.Header{"Cache-Control": {"max-age=60"}, "Content-Type": {"text/plain"}}
		for k, v := range hdr {
			h[k] = v
		}
		status, body := http.StatusOK, "0123456789"
		if r.Header.Get("Range") != "" {
			status, body = http.StatusPartialContent, "01"
			h.Set("Content-Range", "bytes 0-1/"+total)
		} else if gate != nil {
			<-gate
		}
		return &weir.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
	}})
	return o
}

// FR-RNG-4, T-37
func TestRangeMissBackgroundFillBounded(t *testing.T) {
	t.Run("a 206 with a known total fills the key once, then ranges hit", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := rangeFillOrigin(t, nil, "10")
			e := newEngine(t, cacheCfg)
			defer closeEngine(t, e)

			resp, body := serve(t, e, withHeader(getReq("/a"), "Range", "bytes=0-1"), o)
			if resp.StatusCode != http.StatusPartialContent || body != "01" || resp.Cache.Hit {
				t.Fatalf("got %d %q hit=%v, want the origin's 206", resp.StatusCode, body, resp.Cache.Hit)
			}
			synctest.Wait()
			if n := o.Calls("/a"); n != 2 {
				t.Fatalf("origin calls = %d, want the range plus one fill", n)
			}
			reqs := o.Requests()
			if reqs[0].Header.Get("Range") == "" || reqs[1].Header.Get("Range") != "" {
				t.Fatalf("Range on the calls = %q, %q; the fill must not carry it",
					reqs[0].Header.Get("Range"), reqs[1].Header.Get("Range"))
			}
			resp, body = serve(t, e, withHeader(getReq("/a"), "Range", "bytes=2-4"), o)
			if resp.StatusCode != http.StatusPartialContent || body != "234" || !resp.Cache.Hit {
				t.Fatalf("got %d %q hit=%v, want a 206 hit from the filled entry", resp.StatusCode, body, resp.Cache.Hit)
			}
			if n := o.Calls("/a"); n != 2 {
				t.Fatalf("origin calls = %d after the hit, want 2", n)
			}
		})
	})

	t.Run("concurrent range misses start one fill", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			gate := make(chan struct{})
			o := rangeFillGated(t, nil, "10", gate)
			e := newEngine(t, cacheCfg)
			defer closeEngine(t, e)
			defer close(gate)
			for range 5 {
				go func() { serve(t, e, withHeader(getReq("/a"), "Range", "bytes=0-1"), o) }()
			}
			synctest.Wait()
			full := 0
			for _, r := range o.Requests() {
				if r.Header.Get("Range") == "" {
					full++
				}
			}
			if full != 1 {
				t.Fatalf("full fetches = %d, want 1", full)
			}
		})
	})

	tests := []struct {
		name  string
		hdr   http.Header
		total string
		cfg   weir.Config
		req   func() *weir.Request
	}{
		{"total over MaxObjectBytes", nil, "11", weir.Config{Freshness: cacheCfg.Freshness, Storable: weir.StorableConfig{MaxObjectBytes: 10}}, nil},
		{"unknown total", nil, "*", cacheCfg, nil},
		{"unparsable total", nil, "9999999999999999999999", cacheCfg, nil},
		{"206 that is not storable", http.Header{"Cache-Control": {"no-store"}}, "10", cacheCfg, nil},
		{"206 with Set-Cookie", http.Header{"Set-Cookie": {"a=b"}}, "10", cacheCfg, nil},
		{"206 with no freshness", http.Header{"Cache-Control": {"no-cache"}}, "10", cacheCfg, nil},
		{"request with credentials", nil, "10", cacheCfg, func() *weir.Request {
			return withHeader(withHeader(getReq("/a"), "Authorization", "Bearer x"), "Range", "bytes=0-1")
		}},
		{"no-store request", nil, "10", cacheCfg, func() *weir.Request {
			return withHeader(withHeader(getReq("/a"), "Cache-Control", "no-store"), "Range", "bytes=0-1")
		}},
	}
	for _, tc := range tests {
		t.Run("no fill: "+tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := rangeFillOrigin(t, tc.hdr, tc.total)
				e := newEngine(t, tc.cfg)
				defer closeEngine(t, e)
				req := withHeader(getReq("/a"), "Range", "bytes=0-1")
				if tc.req != nil {
					req = tc.req()
				}
				resp, _ := serve(t, e, req, o)
				if resp.StatusCode != http.StatusPartialContent {
					t.Fatalf("status = %d, want 206", resp.StatusCode)
				}
				synctest.Wait()
				if n := o.Calls("/a"); n != 1 {
					t.Fatalf("origin calls = %d, want only the range request", n)
				}
			})
		})
	}

	t.Run("a 200 answer to a range request is not a fill trigger", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := rangeOrigin(t, nil, "0123456789")
			e := newEngine(t, cacheCfg)
			defer closeEngine(t, e)
			serve(t, e, withHeader(getReq("/a"), "Range", "bytes=0-1"), o)
			synctest.Wait()
			if n := o.Calls("/a"); n != 1 {
				t.Fatalf("origin calls = %d, want 1", n)
			}
		})
	})
}

// FR-RNG-4, T-37: with a stale entry under the key the fill revalidates it
// (no Range, If-None-Match), and the freshened entry then answers ranges.
func TestRangeMissFillRevalidatesStaleEntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			h := http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"v1"`}}
			status, body := http.StatusOK, "0123456789"
			switch {
			case r.Header.Get("Range") != "":
				status, body = http.StatusPartialContent, "01"
				h.Set("Content-Range", "bytes 0-1/10")
			case r.Header.Get("If-None-Match") != "":
				status, body = http.StatusNotModified, ""
			}
			return &weir.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
		}})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		serve(t, e, getReq("/a"), o)
		time.Sleep(61 * time.Second)

		resp, body := serve(t, e, withHeader(getReq("/a"), "Range", "bytes=0-1"), o)
		if resp.StatusCode != http.StatusPartialContent || body != "01" || resp.Cache.Hit {
			t.Fatalf("got %d %q hit=%v, want the origin's 206", resp.StatusCode, body, resp.Cache.Hit)
		}
		synctest.Wait()
		reqs := o.Requests() // initial fetch, the range, the fill
		if len(reqs) != 3 || reqs[2].Header.Get("Range") != "" || reqs[2].Header.Get("If-None-Match") != `"v1"` {
			t.Fatalf("calls = %d; fill must be a conditional request without Range: %v", len(reqs), reqs[len(reqs)-1].Header)
		}
		resp, body = serve(t, e, withHeader(getReq("/a"), "Range", "bytes=2-4"), o)
		if resp.StatusCode != http.StatusPartialContent || body != "234" || !resp.Cache.Hit || o.Calls("/a") != 3 {
			t.Fatalf("got %d %q hit=%v calls=%d, want a 206 hit from the revalidated entry", resp.StatusCode, body, resp.Cache.Hit, o.Calls("/a"))
		}
	})
}
