package weir_test

import (
	"bytes"
	"context"
	"net/http"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

// specDropper remembers where vary specs are stored and can delete them, as
// eviction does.
type specDropper struct {
	*memory.Store
	mu    sync.Mutex
	specs []store.Key
}

func (s *specDropper) Set(ctx context.Context, k store.Key, e *store.Entry) error {
	if e.Kind == store.KindVarySpec {
		s.mu.Lock()
		s.specs = append(s.specs, k)
		s.mu.Unlock()
	}
	return s.Store.Set(ctx, k, e)
}

func (s *specDropper) SetVarySpec(ctx context.Context, k store.Key, prev, next *store.Entry) (bool, error) {
	s.mu.Lock()
	s.specs = append(s.specs, k)
	s.mu.Unlock()
	return s.Store.SetVarySpec(ctx, k, prev, next)
}

func (s *specDropper) dropSpecs(t *testing.T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.specs) == 0 {
		t.Fatal("no vary spec was stored")
	}
	for _, k := range s.specs {
		_ = s.Delete(t.Context(), k)
	}
	s.specs = nil
}

// FR-INV-1, FR-KEY-7: a variant whose vary spec is gone is unreachable, so an
// invalidation that found nothing under the primary key missed nothing. In
// review, the variant stayed servable: the spec was evicted, an unsafe
// request wrote no epoch because the primary key held no record, another
// variant's fetch wrote a new spec, and the old variant came back as a hit
// with the body from before the unsafe request.
func TestInvalidationReachesOrphanVariant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, err := memory.New(memory.Config{})
		if err != nil {
			t.Fatal(err)
		}
		defer m.Close()
		st := &specDropper{Store: m}
		cfg := cacheCfg
		cfg.Store = st
		cfg.Forward.Allow = []string{"Accept-Language"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)
		version := "v1"
		o := testorigin.New()
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			if r.Method != http.MethodGet {
				version = "v2"
				return respond(http.StatusOK, http.Header{}, ""), nil
			}
			return respond(http.StatusOK, http.Header{"Cache-Control": {"max-age=600"}, "Vary": {"Accept-Language"}},
				version+"-"+r.Header.Get("Accept-Language")), nil
		}})
		lang := func(l string) *weir.Request { return withHeader(getReq("/a"), "Accept-Language", l) }

		serve(t, e, lang("en"), o)
		serve(t, e, lang("fr"), o)
		time.Sleep(time.Second)
		st.dropSpecs(t)
		serve(t, e, postReq("/a"), o)
		time.Sleep(2 * time.Second)
		if _, body := serve(t, e, lang("en"), o); body != "v2-en" {
			t.Fatalf("en after the unsafe request: %q, want v2-en", body)
		}
		if resp, body := serve(t, e, lang("fr"), o); body != "v2-fr" {
			t.Fatalf("fr after the unsafe request: %q (hit %v), want v2-fr: the variant from before it must not come back", body, resp.Cache.Hit)
		}
	})
}

// FR-INV-1: the count of fetches in progress is the store's, so an unsafe
// request served by one engine sees a fetch another engine runs on the same
// store. In review the count was per engine, and the response a second
// engine fetched before the unsafe request was served as a hit after it. The
// Caddy adapter shares one memory store between the engines of a name.
func TestInvalidationSeesOtherEnginesFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, err := memory.New(memory.Config{})
		if err != nil {
			t.Fatal(err)
		}
		defer m.Close()
		cfg := cacheCfg
		cfg.Store = m
		a, b := newEngine(t, cfg), newEngine(t, cfg)
		defer closeEngine(t, a)
		defer closeEngine(t, b)
		gate := make(chan struct{})
		o := testorigin.New()
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			if r.Method == http.MethodGet {
				<-gate
			}
			return respond(http.StatusOK, http.Header{"Cache-Control": {"max-age=60"}}, "old"), nil
		}})

		get := serveAsync(t.Context(), a, getReq("/a"), o)
		synctest.Wait()
		time.Sleep(time.Second)
		serve(t, b, postReq("/a"), o)
		close(gate)
		if s := <-get; s.err != nil {
			t.Fatal(s.err)
		}
		time.Sleep(2 * time.Second)
		for name, e := range map[string]*weir.Engine{"the engine that fetched": a, "the engine that took the unsafe request": b} {
			if resp, _ := serve(t, e, getReq("/a"), o); resp.Cache.Hit {
				t.Fatalf("%s served the response from before the unsafe request as a hit", name)
			}
			break // the first lookup revalidates and stores a new response for both
		}
	})
}

// FR-INV-1: the same for a fetch that Warm runs.
func TestInvalidationCoversWarm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		o := testorigin.New()
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			if r.Method == http.MethodGet {
				<-gate
			}
			return respond(http.StatusOK, http.Header{"Cache-Control": {"max-age=60"}}, "old"), nil
		}})
		e, st := countedEngine(t, cacheCfg, false)
		defer closeEngine(t, e)

		warmed := make(chan error, 1)
		go func() {
			_, err := e.Warm(t.Context(), slices.Values([]*weir.Request{getReq("/a")}), o)
			warmed <- err
		}()
		synctest.Wait()
		time.Sleep(time.Second)
		serve(t, e, postReq("/a"), o)
		if n := st.n.Load(); n != 1 {
			t.Fatalf("%d epochs written while Warm was fetching the URI, want 1", n)
		}
		close(gate)
		if err := <-warmed; err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		if resp, _ := serve(t, e, getReq("/a"), o); resp.Cache.Hit {
			t.Fatal("the response Warm fetched before the unsafe request was served as a hit")
		}
	})
}

// FR-STO-12, FR-STL-2: an error status never puts a marker in the place of
// the response the request found, whatever its size and whether or not the
// breaker counts it. In review an over-size 503, a 429 and a 507 each did:
// the next request carried no validator, and the stale copy that
// stale-if-error would have served was gone for the rest of the outage.
func TestErrorStatusLeavesResponseInPlace(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 2<<20)
	for _, tc := range []struct {
		name   string
		status int
		body   []byte
	}{
		{"over-size 503", http.StatusServiceUnavailable, big},
		{"429", http.StatusTooManyRequests, nil},
		{"507", http.StatusInsufficientStorage, nil},
		{"over-size 403", http.StatusForbidden, big},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.New()
				o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=1"}, "Etag": {`"a"`}}, Body: []byte("a")})
				cfg := cacheCfg
				cfg.Negative.Disable = true // every request below reaches the origin
				e := newEngine(t, cfg)
				defer closeEngine(t, e)
				serve(t, e, getReq("/a"), o)
				time.Sleep(2 * time.Second)
				o.Reset()
				o.Default(testorigin.Behavior{Status: tc.status, Body: tc.body})

				for i := range 2 {
					resp, _ := serve(t, e, getReq("/a"), o)
					if resp.Cache.Detail == "hit-for-miss" {
						t.Fatalf("request %d found a marker where the stored response was", i)
					}
				}
				for i, r := range o.Requests() {
					if r.Header.Get("If-None-Match") != `"a"` {
						t.Fatalf("fetch %d carried no validator: the stored response was replaced", i)
					}
				}
			})
		})
	}
}

// FR-SRV-8, FR-COA-1: the look a new flight takes at the store judges the
// entry against the client's own directives. In review a request with
// no-cache whose first lookup missed was answered from an entry stored in
// between, with no origin call.
func TestStoredMeanwhileHonorsRevalidation(t *testing.T) {
	for _, tc := range []struct {
		cc        string
		wantCalls int
	}{{"", 1}, {"no-cache", 2}, {"max-age=0", 2}, {"min-fresh=3600", 2}, {"max-age=600", 1}} {
		synctest.Test(t, func(t *testing.T) {
			m, err := memory.New(memory.Config{})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			st := &missOnce{Store: m}
			cfg := cacheCfg
			cfg.Store = st
			cfg.Client.HonorRevalidation = true
			e := newEngine(t, cfg)
			defer closeEngine(t, e)
			o := testorigin.New()
			o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"a"`}}, Body: []byte("v")})

			serve(t, e, getReq("/a"), o)
			st.armed.Store(true)
			req := getReq("/a")
			if tc.cc != "" {
				req.Header.Set("Cache-Control", tc.cc)
			}
			serve(t, e, req, o)
			if n := o.TotalCalls(); n != tc.wantCalls {
				t.Errorf("Cache-Control %q: %d origin calls, want %d", tc.cc, n, tc.wantCalls)
			}
		})
	}
}

// FR-TMO-2, FR-FWD-8: the body of a response Serve streams from the origin
// says so, which is how weirhttp.WriteResponse knows to bound its writes and
// where to find trailers; a stored body does not.
func TestStreamBodyExposesIdleAndTrailers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(cacheable("v"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		type stream interface {
			StreamIdle() time.Duration
			Trailer() http.Header
		}

		resp, err := e.Serve(t.Context(), postReq("/a"), o)
		if err != nil {
			t.Fatal(err)
		}
		s, ok := resp.Body.(stream)
		if !ok || s.StreamIdle() != 60*time.Second {
			t.Fatalf("pass-through body %T: no idle bound of Timeouts.StreamIdle", resp.Body)
		}
		resp.Body.Close()

		serve(t, e, getReq("/a"), o)
		hit, err := e.Serve(t.Context(), getReq("/a"), o)
		if err != nil {
			t.Fatal(err)
		}
		defer hit.Body.Close()
		if _, ok := hit.Body.(stream); ok {
			t.Fatalf("stored body %T claims to be a live stream", hit.Body)
		}
	})
}
