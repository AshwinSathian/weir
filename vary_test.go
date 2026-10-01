package weir_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// varyBody answers with Vary: field and the field's forwarded value as the
// body, so a test can tell which variant it got.
func varyBody(field string, delay time.Duration) testorigin.Behavior {
	return testorigin.Behavior{Delay: delay, Func: func(r *weir.Request) (*weir.Response, error) {
		return &weir.Response{StatusCode: http.StatusOK,
			Header: http.Header{"Cache-Control": {"max-age=60"}, "Vary": {field}},
			Body:   io.NopCloser(strings.NewReader(r.Header.Get(field)))}, nil
	}}
}

func varyReq(path, field, value string) *weir.Request {
	r := getReq(path)
	if value != "" {
		r.Header.Set(field, value)
	}
	return r
}

// FR-KEY-7, FR-KEY-9, T-15, T6.7: in VaryAuto a header named in Vary is
// folded into the variant key, read from the forwarded request. Two values
// give two variants; each is then a hit that returns its own body.
func TestVaryUnconfiguredHeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(varyBody("X-Custom", 0))
		cfg := cacheCfg
		cfg.Forward.Allow = []string{"X-Custom"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for _, v := range []string{"a", "b"} {
			if _, body := serve(t, e, varyReq("/v", "X-Custom", v), o); body != v {
				t.Fatalf("miss %s: body %q", v, body)
			}
		}
		for _, v := range []string{"a", "b"} {
			resp, body := serve(t, e, varyReq("/v", "X-Custom", v), o)
			if body != v || !resp.Cache.Hit {
				t.Fatalf("second %s: body %q hit=%v", v, body, resp.Cache.Hit)
			}
		}
		// FR-KEY-11: an absent field only matches absent.
		resp, body := serve(t, e, varyReq("/v", "X-Custom", ""), o)
		if body != "" || resp.Cache.Hit || resp.Cache.Fwd != weir.FwdVaryMiss {
			t.Fatalf("absent: body %q hit=%v fwd=%v", body, resp.Cache.Hit, resp.Cache.Fwd)
		}
		if n := o.Calls("/v"); n != 3 {
			t.Fatalf("origin calls = %d, want 3", n)
		}
	})
}

// FR-KEY-11: list values that differ only in whitespace around commas and
// at the ends select the same variant.
func TestVaryNormalizesValues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(varyBody("X-Custom", 0))
		cfg := cacheCfg
		cfg.Forward.Allow = []string{"X-Custom"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, varyReq("/v", "X-Custom", "a,b"), o)
		r := getReq("/v")
		r.Header["X-Custom"] = []string{" a ", "\tb "}
		if resp, _ := serve(t, e, r, o); !resp.Cache.Hit {
			t.Fatalf("combined lines missed: %+v", resp.Cache)
		}
	})
}

// FR-KEY-8, FR-STO-7: Vary: * prevents storage.
func TestVaryStar(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Vary": {"Accept-Language, *"}}})
		var ev eventCounter
		cfg := cacheCfg
		cfg.Observer = &ev
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/s"), o)
		if resp, _ := serve(t, e, getReq("/s"), o); resp.Cache.Hit {
			t.Fatal("Vary: * response was served from the cache")
		}
		if n := ev.count(weir.EvNotStored.String() + "/vary-star"); n == 0 {
			t.Fatalf("no EvNotStored{vary-star}: %v", ev.n)
		}
	})
}

// FR-KEY-10: a Vary listing more than MaxVaryHeaders names is not stored.
func TestVaryTooManyNames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Vary": {"A, B", "C"}}})
		var ev eventCounter
		cfg := cacheCfg
		cfg.Observer = &ev
		cfg.Key.MaxVaryHeaders = 2
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/m"), o)
		if n := ev.count(weir.EvNotStored.String() + "/vary-too-many"); n != 1 {
			t.Fatalf("EvNotStored{vary-too-many} = %d: %v", n, ev.n)
		}
	})
}

// FR-COA-5, FR-KEY-7: followers of a flight whose response varies on a
// field they differ in re-enter and coalesce again on their variant key:
// 300 requests over 3 languages cost 1 + 2 origin calls.
func TestVaryFollowersRecoalesce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(varyBody("Accept-Language", 100*time.Millisecond))
		cfg := cacheCfg
		cfg.Forward.Allow = []string{"Accept-Language"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		langs := []string{"en", "fr", "de"}
		chs := make([]<-chan served, 300)
		for i := range chs {
			chs[i] = serveAsync(t.Context(), e, varyReq("/l", "Accept-Language", langs[i%3]), o)
		}
		for i, ch := range chs {
			s := <-ch
			if s.err != nil || s.body != langs[i%3] {
				t.Fatalf("request %d (%s): body %q err %v", i, langs[i%3], s.body, s.err)
			}
		}
		if n := o.Calls("/l"); n != 3 {
			t.Fatalf("origin calls = %d, want 1 + 2", n)
		}
	})
}

// FR-WRM-2, FR-KEY-7: a warm request that joins a flight storing another
// variant fetches its own instead of counting as skipped.
func TestWarmFollowerOfOtherVariant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		gate := make(chan struct{})
		b := varyBody("Accept-Language", 0)
		b.Gate = gate
		o.Default(b)
		cfg := cacheCfg
		cfg.Forward.Allow = []string{"Accept-Language"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		fg := serveAsync(t.Context(), e, varyReq("/w", "Accept-Language", "en"), o)
		synctest.Wait()
		warmed := make(chan weir.WarmStats, 1)
		go func() {
			st, _ := e.Warm(t.Context(), func(yield func(*weir.Request) bool) {
				yield(varyReq("/w", "Accept-Language", "fr"))
			}, o)
			warmed <- st
		}()
		synctest.Wait()
		close(gate)
		<-fg
		if st := <-warmed; st != (weir.WarmStats{Fetched: 1}) || o.Calls("/w") != 2 {
			t.Fatalf("Warm = %+v with %d origin calls; want 1 fetched, 2 calls", st, o.Calls("/w"))
		}
		if resp, body := serve(t, e, varyReq("/w", "Accept-Language", "fr"), o); !resp.Cache.Hit || body != "fr" {
			t.Fatalf("fr after warm: hit=%v body %q", resp.Cache.Hit, body)
		}
	})
}

// FR-NEG-1, FR-KEY-7: a negative entry for one variant lands under its
// variant key, so the vary spec and the other variants stay reachable.
// Accept-Encoding forwards only its bucket, so the request stays keyed
// (FR-NEG-4).
func TestVaryNegativeStaysInVariant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		ok := varyBody("Accept-Encoding", 0)
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			if r.Header.Get("Accept-Encoding") == "identity" {
				return &weir.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: http.NoBody}, nil
			}
			return ok.Func(r)
		}})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, varyReq("/n", "Accept-Encoding", "gzip"), o)
		serve(t, e, varyReq("/n", "Accept-Encoding", ""), o)
		if resp, _ := serve(t, e, varyReq("/n", "Accept-Encoding", ""), o); resp.Cache.Detail != "negative" {
			t.Fatalf("identity: %+v, want a negative hit", resp.Cache)
		}
		if resp, body := serve(t, e, varyReq("/n", "Accept-Encoding", "gzip"), o); !resp.Cache.Hit || body != "gzip" {
			t.Fatalf("gzip: hit=%v body %q, want the stored variant", resp.Cache.Hit, body)
		}
		if n := o.Calls("/n"); n != 2 {
			t.Fatalf("origin calls = %d, want 2", n)
		}
	})
}
