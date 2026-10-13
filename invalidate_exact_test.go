package weir_test

import (
	"context"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

// epochCounter counts SetEpoch calls. remote makes the engine treat the store
// as one other nodes share.
type epochCounter struct {
	*memory.Store // embedded whole, so the store's optional capabilities stay visible
	remote        bool
	n             atomic.Int64
}

func (s *epochCounter) SetEpoch(ctx context.Context, t store.Tag, ep store.Epoch) error {
	s.n.Add(1)
	return s.Store.SetEpoch(ctx, t, ep)
}

func (s *epochCounter) Info() store.Info { return store.Info{Name: "counted", Remote: s.remote} }

func countedEngine(t *testing.T, cfg weir.Config, remote bool) (*weir.Engine, *epochCounter) {
	t.Helper()
	m, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	st := &epochCounter{Store: m, remote: remote}
	cfg.Store = st
	return newEngine(t, cfg), st
}

// FR-INV-1, T-29: an unsafe request to a URI with no record and no fetch in
// progress writes no epoch, and one to a URI with a record does.
func TestInvalidationSkipsAbsentURI(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(unsafeAnswer(http.StatusOK, nil, "v"))
		e, st := countedEngine(t, cacheCfg, false)
		defer closeEngine(t, e)

		serve(t, e, postReq("/absent"), o)
		if n := st.n.Load(); n != 0 {
			t.Fatalf("%d epochs written for a URI with no record, want 0", n)
		}
		serve(t, e, getReq("/a"), o)
		time.Sleep(time.Second)
		serve(t, e, postReq("/a"), o)
		if n := st.n.Load(); n != 1 {
			t.Fatalf("%d epochs written for a stored URI, want 1", n)
		}
		time.Sleep(time.Second)
		if resp, _ := serve(t, e, getReq("/a"), o); resp.Cache.Hit {
			t.Fatal("hit after an unsafe request to the same URI")
		}
	})
}

// FR-INV-3, T-29: the same rule for the URIs a response names in Location
// and Content-Location.
func TestInvalidationSkipsAbsentLocation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(unsafeAnswer(http.StatusCreated, http.Header{"Location": {"/item"}, "Content-Location": {"/nowhere"}}, "v"))
		e, st := countedEngine(t, cacheCfg, false)
		defer closeEngine(t, e)

		serve(t, e, getReq("/item"), o)
		time.Sleep(time.Second)
		serve(t, e, postReq("/create"), o)
		if n := st.n.Load(); n != 1 {
			t.Fatalf("%d epochs written, want 1: /item is stored, /create and /nowhere are not", n)
		}
		time.Sleep(time.Second)
		if resp, _ := serve(t, e, getReq("/item"), o); resp.Cache.Hit {
			t.Fatal("hit on the Location target after the unsafe request")
		}
	})
}

// FR-INV-1, FR-PRG-7: a GET sent before an unsafe request and stored after it
// is invalid at its next lookup. The fetch is counted from before it is sent,
// so the unsafe request finds it although the store holds nothing yet.
func TestInvalidationCoversFetchInProgress(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  func() *weir.Request
		cc   string
	}{
		{"on a flight", func() *weir.Request { return getReq("/a") }, "max-age=60"},
		// A request with Authorization never leads a flight (FR-COA-8).
		{"fetched directly", func() *weir.Request { return withHeader(getReq("/a"), "Authorization", "Bearer x") }, "public, max-age=60"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				gate := make(chan struct{})
				o := testorigin.New()
				o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
					if r.Method == http.MethodGet {
						<-gate
					}
					return &weir.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {tc.cc}}, Body: http.NoBody}, nil
				}})
				e, st := countedEngine(t, cacheCfg, false)
				defer closeEngine(t, e)

				get := serveAsync(t.Context(), e, tc.req(), o)
				synctest.Wait()
				time.Sleep(time.Second)
				serve(t, e, postReq("/a"), o)
				if n := st.n.Load(); n != 1 {
					t.Fatalf("%d epochs written while a fetch for the URI was in progress, want 1", n)
				}
				close(gate)
				if s := <-get; s.err != nil {
					t.Fatal(s.err)
				}
				time.Sleep(2 * time.Second)
				before := o.TotalCalls()
				if resp, _ := serve(t, e, getReq("/a"), o); resp.Cache.Hit || o.TotalCalls() != before+1 {
					t.Fatalf("the response fetched before the unsafe request was served as a hit: %+v", resp.Cache)
				}
			})
		})
	}
}

// FR-INV-1: with a keyed header one URI has many keys, so the engine cannot
// tell that none holds a record, and writes the epoch.
func TestInvalidationUnconditionalWithKeyedInputs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(unsafeAnswer(http.StatusOK, nil, "v"))
		cfg := cacheCfg
		cfg.Key.Headers = []string{"X-Tenant"}
		e, st := countedEngine(t, cfg, false)
		defer closeEngine(t, e)

		serve(t, e, postReq("/absent"), o)
		if n := st.n.Load(); n != 1 {
			t.Fatalf("%d epochs written, want 1", n)
		}
	})
}

// FR-INV-1: a remote store is shared, and another node may be fetching the
// URI, so the epoch is always written.
func TestInvalidationUnconditionalOnRemoteStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(unsafeAnswer(http.StatusOK, nil, "v"))
		e, st := countedEngine(t, cacheCfg, true)
		defer closeEngine(t, e)

		serve(t, e, postReq("/absent"), o)
		if n := st.n.Load(); n != 1 {
			t.Fatalf("%d epochs written, want 1", n)
		}
	})
}

// T-29, 05 E-8: unsafe requests to URIs nobody cached leave the cache alone.
// Before v1.1, 500 000 of them sent about half of all untouched entries back
// to the origin, and 60 000 sent 4%.
func TestInvalidationFloodLeavesCacheIntact(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const entries, flood = 2000, 200_000
		o := testorigin.New()
		o.Default(unsafeAnswer(http.StatusOK, nil, "v"))
		cfg := cacheCfg
		cfg.Limiter.MaxConcurrent = 256
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for i := range entries {
			serve(t, e, getReq("/a/"+strconv.Itoa(i)), o)
		}
		time.Sleep(time.Second)
		for i := range flood {
			r := postReq("/x")
			r.RawQuery = "r=" + strconv.Itoa(i)
			serve(t, e, r, o)
		}
		time.Sleep(2 * time.Second)
		o.Reset()
		for i := range entries {
			serve(t, e, getReq("/a/"+strconv.Itoa(i)), o)
		}
		if n := o.TotalCalls(); n != 0 {
			t.Fatalf("%d of %d untouched entries went back to the origin, want 0", n, entries)
		}
	})
}
