package weir_test

import (
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// FR-SRV-3, RFC 9111 §4.3.4: a 304 selects the stored response by its
// validator. One that names another entity-tag, weak or strong, selects
// nothing: it is discarded and the request repeated without conditionals.
// Before v1.1 only a strong tag was compared, so a 304 with W/"b" put new
// headers on the body of W/"a".
func TestNotModifiedWeakTagMismatchRefetches(t *testing.T) {
	for _, tc := range []struct {
		name, stored, in304 string
		refetch             bool
	}{
		{"another weak tag", `W/"a"`, `W/"b"`, true},
		{"another strong tag", `"a"`, `"b"`, true},
		{"weak form of the stored strong tag", `"a"`, `W/"a"`, false},
		{"strong form of the stored weak tag", `W/"a"`, `"a"`, false},
		{"the same tag", `W/"a"`, `W/"a"`, false},
		{"no tag", `W/"a"`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.New()
				o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=1"}, "Etag": {tc.stored}}, Body: []byte("v1")})
				e := newEngine(t, cacheCfg)
				defer closeEngine(t, e)
				serve(t, e, getReq("/a"), o)
				time.Sleep(2 * time.Second)
				o.Reset()
				o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
					if r.Header.Get("If-None-Match") != "" {
						h := http.Header{"Cache-Control": {"max-age=60"}}
						if tc.in304 != "" {
							h["Etag"] = []string{tc.in304}
						}
						return respond(http.StatusNotModified, h, ""), nil
					}
					return respond(http.StatusOK, http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"b"`}}, "v2"), nil
				}})

				_, body := serve(t, e, getReq("/a"), o)
				wantBody, wantCalls := "v1", 1
				if tc.refetch {
					wantBody, wantCalls = "v2", 2
				}
				if body != wantBody || o.TotalCalls() != wantCalls {
					t.Fatalf("body %q after %d origin calls, want %q after %d", body, o.TotalCalls(), wantBody, wantCalls)
				}
			})
		})
	}
}

// FR-SRV-8, RFC 9111 §5.2.1: with Client.HonorRevalidation, a request's
// max-age and min-fresh send a fresh entry to validation when it is older, or
// has less freshness left, than the client accepts. Without it they change
// nothing (D5).
func TestRequestMaxAgeAndMinFresh(t *testing.T) {
	for _, tc := range []struct {
		cc       string
		validate bool // with HonorRevalidation; never without
	}{
		{"max-age=5", true},   // the entry is 10 s old
		{"max-age=10", false}, // exactly as old as accepted
		{"max-age=30", false},
		{"min-fresh=55", true}, // 50 s of freshness left
		{"min-fresh=50", false},
		{"min-fresh=10", false},
		{"max-age=x", false}, // not delta-seconds: asks for nothing
		{"max-stale=100", false},
	} {
		for _, honor := range []bool{true, false} {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.New()
				o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"a"`}}, Body: []byte("v")})
				cfg := cacheCfg
				cfg.Client.HonorRevalidation = honor
				e := newEngine(t, cfg)
				defer closeEngine(t, e)
				serve(t, e, getReq("/a"), o)
				time.Sleep(10 * time.Second)

				resp, _ := serve(t, e, withHeader(getReq("/a"), "Cache-Control", tc.cc), o)
				if want := tc.validate && honor; resp.Cache.Hit == want {
					t.Errorf("%s, HonorRevalidation %v: hit = %v, want validation = %v", tc.cc, honor, resp.Cache.Hit, want)
				}
			})
		}
	}
}

// FR-STO-14: an encoded response is stored under the coding that was asked
// for, even when the origin sent no Vary. Before v1.1 a gzip body without
// Vary was stored under the URL and served to a client that had asked for
// identity.
func TestEncodedResponseVariesOnAcceptEncoding(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		o := testorigin.New()
		// An origin that compresses only when asked, and says nothing in Vary.
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			h := http.Header{"Cache-Control": {"max-age=60"}}
			if r.Header.Get("Accept-Encoding") == "gzip" {
				h["Content-Encoding"] = []string{"gzip"}
				return respond(http.StatusOK, h, "zipped"), nil
			}
			return respond(http.StatusOK, h, "plain"), nil
		}})
		gz := func() *weir.Request { return withHeader(getReq("/a"), "Accept-Encoding", "gzip") }

		if _, body := serve(t, e, gz(), o); body != "zipped" {
			t.Fatalf("gzip client got %q", body)
		}
		if resp, body := serve(t, e, getReq("/a"), o); body != "plain" || resp.Cache.Hit {
			t.Fatalf("a client that asked for identity got %q (hit %v), want the origin's plain body", body, resp.Cache.Hit)
		}
		// The plain response carries no Vary and no coding, so it is stored
		// for every coding and replaces the gzip variant (01 FR-KEY-7: a
		// response without Vary answers every request). A gzip client then
		// gets the plain body, which it can read; the reverse never happens.
		if resp, body := serve(t, e, getReq("/a"), o); body != "plain" || !resp.Cache.Hit {
			t.Fatalf("identity client's second request: %q (hit %v), want a hit with plain", body, resp.Cache.Hit)
		}
		if _, body := serve(t, e, gz(), o); body != "plain" && body != "zipped" {
			t.Fatalf("gzip client got %q", body)
		}

		// A plain response is stored once for every coding, as before.
		o.Route("/plain", cacheable("text"))
		serve(t, e, withHeader(getReq("/plain"), "Accept-Encoding", "gzip"), o)
		if resp, _ := serve(t, e, getReq("/plain"), o); !resp.Cache.Hit {
			t.Fatalf("an unencoded response is no longer shared across codings: %+v", resp.Cache)
		}
	})
}

// FR-NEG-1: a 503 the origin marks no-store or private is for the client
// that got it, and writes no negative entry.
func TestNegativeHonorsNoStore(t *testing.T) {
	for _, tc := range []struct {
		cc       string
		negative bool
	}{{"", true}, {"no-store", false}, {"private", false}, {"max-age=5", true}} {
		synctest.Test(t, func(t *testing.T) {
			o := testorigin.New()
			h := http.Header{}
			if tc.cc != "" {
				h["Cache-Control"] = []string{tc.cc}
			}
			o.Default(testorigin.Behavior{Status: http.StatusServiceUnavailable, Header: h})
			e := newEngine(t, cacheCfg)
			defer closeEngine(t, e)

			serve(t, e, getReq("/a"), o)
			resp, _ := serve(t, e, getReq("/a"), o)
			if got := resp.Cache.Detail == "negative"; got != tc.negative {
				t.Errorf("Cache-Control %q: negative entry served = %v, want %v", tc.cc, got, tc.negative)
			}
		})
	}
}

// FR-LIM-5: the Retry-After of a shed request is the queue wait plus a whole
// number of seconds drawn from Config.Rand, so clients shed together do not
// return together. At the defaults that is 2 or 3 seconds, half each: a
// fraction of a second would round every draw up to one value.
func TestShedRetryAfterJitter(t *testing.T) {
	for _, tc := range []struct {
		rand float64
		want time.Duration
	}{{0, 2 * time.Second}, {0.4, 2 * time.Second}, {0.6, 3 * time.Second}, {0.999, 3 * time.Second}} { // whole seconds: both values occur
		synctest.Test(t, func(t *testing.T) {
			gate := make(chan struct{})
			defer close(gate)
			o := testorigin.New()
			o.Default(testorigin.Behavior{Gate: gate})
			cfg := cacheCfg
			cfg.Limiter.MaxConcurrent = 1
			cfg.Rand = func() float64 { return tc.rand }
			e := newEngine(t, cfg)
			defer closeEngine(t, e)

			serveTimed(t, e, getReq("/held"), o)
			synctest.Wait()
			_, err := e.Serve(t.Context(), getReq("/shed"), o)
			after, ok := weir.RetryAfter(err)
			if !errors.Is(err, weir.ErrShed) || !ok || after != tc.want {
				t.Errorf("Rand %v: %v with Retry-After %v, want ErrShed with %v", tc.rand, err, after, tc.want)
			}
		})
	}
}
