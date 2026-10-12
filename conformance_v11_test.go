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

// FR-STO-14: a response in a coding the fetch did not ask for is not stored,
// and an encoded response is stored under the coding that was asked for even
// when the origin sent no Vary. Before v1.1 a gzip body without Vary was
// stored under the URL and served to a client that had asked for identity.
func TestEncodingMismatchNotStored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs := &eventCounter{}
		cfg := cacheCfg
		cfg.Observer = obs
		e := newEngine(t, cfg)
		defer closeEngine(t, e)
		o := testorigin.New()
		// An origin that compresses whatever it is asked, and says nothing in Vary.
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Content-Encoding": {"gzip"}}, Body: []byte("zipped")})
		gz := func() *weir.Request { return withHeader(getReq("/a"), "Accept-Encoding", "gzip") }

		if resp, _ := serve(t, e, getReq("/a"), o); resp.Cache.Stored {
			t.Fatal("a gzip body was stored for a fetch that asked for identity")
		}
		if n := obs.count("not-stored/encoding"); n != 1 {
			t.Fatalf("EvNotStored{encoding} = %d, want 1", n)
		}
		if resp, _ := serve(t, e, gz(), o); !resp.Cache.Stored {
			t.Fatalf("a gzip body for a fetch that asked for gzip was not stored: %+v", resp.Cache)
		}
		if resp, _ := serve(t, e, gz(), o); !resp.Cache.Hit {
			t.Fatalf("second gzip request: %+v, want a hit", resp.Cache)
		}
		if resp, _ := serve(t, e, getReq("/a"), o); resp.Cache.Hit {
			t.Fatal("a client that asked for identity was served the stored gzip body")
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

// FR-LIM-5: the Retry-After of a shed request is one to one and a half queue
// waits, drawn from Config.Rand, so clients shed together do not return
// together.
func TestShedRetryAfterJitter(t *testing.T) {
	for _, tc := range []struct {
		rand float64
		want time.Duration
	}{{0, 2 * time.Second}, {0.5, 3 * time.Second}, {0.999, 3 * time.Second}} { // 2.5 s and 3 s round up to 3
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
