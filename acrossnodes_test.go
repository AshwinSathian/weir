package weir_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

// lockStore is a store two engines share, as two nodes share a Valkey
// server, with the store.Locker capability. Only the store.Store methods of
// the memory store show through, so the engines see a remote store.
type lockStore struct {
	store.Store
	mu    sync.Mutex
	locks map[store.Key]time.Time // key -> lock expiry
	fail  bool
}

func newLockStore(t *testing.T) *lockStore {
	t.Helper()
	m, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return &lockStore{Store: m, locks: map[store.Key]time.Time{}}
}

func (s *lockStore) Info() store.Info { return store.Info{Name: "shared", Remote: true} }

func (s *lockStore) Lock(_ context.Context, k store.Key, ttl time.Duration) (func(), bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return nil, false, store.ErrUnavailable
	}
	if exp, ok := s.locks[k]; ok && time.Now().Before(exp) {
		return nil, false, nil
	}
	exp := time.Now().Add(ttl)
	s.locks[k] = exp
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.locks[k].Equal(exp) { // only the holder's own lock
			delete(s.locks, k)
		}
	}, true, nil
}

func (s *lockStore) Held(_ context.Context, k store.Key) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return false, store.ErrUnavailable
	}
	exp, ok := s.locks[k]
	return ok && time.Now().Before(exp), nil
}

// twoNodes runs one request on each of two engines that share st, while
// the origin answers after delay with h, and returns what each got and how
// many times the origin was called.
func twoNodes(t *testing.T, st store.Store, h http.Header, delay time.Duration) (calls int32, got [2]*weir.Response) {
	t.Helper()
	var n atomic.Int32
	o := weir.OriginFunc(func(context.Context, *weir.Request) (*weir.Response, error) {
		n.Add(1)
		time.Sleep(delay)
		hdr := http.Header{"Cache-Control": {"max-age=60"}}
		for k, v := range h {
			hdr[k] = v
		}
		return &weir.Response{StatusCode: http.StatusOK, Header: hdr, Body: io.NopCloser(strings.NewReader("hello"))}, nil
	})
	cfg := cacheCfg
	cfg.Store = st
	a, b := newEngine(t, cfg), newEngine(t, cfg)
	defer closeEngine(t, a)
	defer closeEngine(t, b)
	var wg sync.WaitGroup
	for i, e := range []*weir.Engine{a, b} {
		wg.Go(func() {
			resp, err := e.Serve(t.Context(), getReq("/x"), o)
			if err != nil {
				t.Errorf("node %d: %v", i, err)
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != "hello" {
				t.Errorf("node %d body %q", i, body)
			}
			got[i] = resp
		})
		time.Sleep(time.Millisecond) // node a asks first
	}
	wg.Wait()
	return n.Load(), got
}

// FR-COA-11: two engines that share a store with locks fetch a missing key
// once between them; the other serves what the first stored, as a hit.
func TestAcrossNodesFetchOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls, got := twoNodes(t, newLockStore(t), nil, 300*time.Millisecond)
		if calls != 1 {
			t.Fatalf("origin calls = %d, want 1", calls)
		}
		if got[1] == nil || !got[1].Cache.Hit {
			t.Fatalf("second node: %+v, want a hit on what the first stored", got[1])
		}
	})
}

// FR-COA-11: without the capability, or when the store cannot answer the
// lock, each engine fetches for itself, as before.
func TestAcrossNodesFallsBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st := newLockStore(t)
		st.fail = true
		if calls, _ := twoNodes(t, st, nil, 300*time.Millisecond); calls != 2 {
			t.Fatalf("lock unavailable: origin calls = %d, want 2", calls)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		m, err := memory.New(memory.Config{})
		if err != nil {
			t.Fatal(err)
		}
		if calls, _ := twoNodes(t, struct{ store.Store }{m}, nil, 300*time.Millisecond); calls != 2 {
			t.Fatalf("no capability: origin calls = %d, want 2", calls)
		}
	})
}

// FR-COA-11, FR-STO-1: a response the first node could not store leaves the
// other nothing to serve, so it fetches for itself once the lock is gone.
func TestAcrossNodesUnstorableFetchesAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls, got := twoNodes(t, newLockStore(t), http.Header{"Cache-Control": {"private"}}, 300*time.Millisecond)
		if calls != 2 {
			t.Fatalf("origin calls = %d, want 2", calls)
		}
		if got[1] == nil || got[1].Cache.Hit {
			t.Fatalf("second node: %+v, want its own fetch", got[1])
		}
	})
}

// FR-COA-11, FR-COA-4, T-19: a lock holder that never answers (a hanging
// origin, or a node that died holding it) ends the waiting flight as a
// timeout at the flight's own bound, so its waiters do not each fetch past
// it; once the dead holder's lock expires, the next request fetches.
func TestAcrossNodesLockHolderBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st := &deadHolder{lockStore: newLockStore(t)}
		cfg := cacheCfg
		cfg.Store = st
		cfg.Timeouts.Origin = 2 * time.Second
		cfg.Limiter.MaxQueueWait = time.Second
		cfg.Negative.Disable = true
		e := newEngine(t, cfg)
		defer closeEngine(t, e)
		var calls atomic.Int32
		o := weir.OriginFunc(func(context.Context, *weir.Request) (*weir.Response, error) {
			calls.Add(1)
			return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: io.NopCloser(strings.NewReader("hi"))}, nil
		})
		bound := cfg.Timeouts.Origin + 2*cfg.Limiter.MaxQueueWait
		start := time.Now()
		var wg sync.WaitGroup
		for range 5 {
			wg.Go(func() {
				if _, err := e.Serve(t.Context(), getReq("/x"), o); !errors.Is(err, weir.ErrOriginTimeout) {
					t.Errorf("err = %v, want ErrOriginTimeout", err)
				}
			})
		}
		wg.Wait()
		if d := time.Since(start); d > bound {
			t.Fatalf("waited %v on a dead holder, bound %v", d, bound)
		}
		if calls.Load() != 0 {
			t.Fatalf("origin calls = %d while another node held the lock, want 0", calls.Load())
		}
		time.Sleep(2 * time.Second) // the dead holder's lock (bound + 1 s) expires
		resp, err := e.Serve(t.Context(), getReq("/x"), o)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if calls.Load() != 1 {
			t.Fatalf("origin calls = %d after the lock expired, want 1", calls.Load())
		}
	})
}

// deadHolder is a lockStore whose first lock request finds the lock already
// taken by a node that will never release it; the lock still expires.
type deadHolder struct {
	*lockStore
	once sync.Once
}

func (d *deadHolder) Lock(ctx context.Context, k store.Key, ttl time.Duration) (func(), bool, error) {
	d.once.Do(func() { _, _, _ = d.lockStore.Lock(ctx, k, ttl) }) // the dead node took it
	return d.lockStore.Lock(ctx, k, ttl)
}

// optimistic is a lock that always grants without asking anything, as the
// Valkey store's does when CoalesceAcrossNodes is off.
type optimistic struct{ store.Store }

func (optimistic) Lock(context.Context, store.Key, time.Duration) (func(), bool, error) {
	return func() {}, true, nil
}
func (optimistic) Held(context.Context, store.Key) (bool, error) { return false, nil }

// down is a store whose every call fails, as a server that is down, but
// whose lock answers without asking it.
type down struct{ optimistic }

func (down) Get(context.Context, store.Key) (*store.Entry, error) { return nil, store.ErrUnavailable }
func (down) Set(context.Context, store.Key, *store.Entry) error   { return store.ErrUnavailable }
func (down) Delete(context.Context, store.Key) error              { return store.ErrUnavailable }
func (down) SetEpoch(context.Context, store.Tag, store.Epoch) error {
	return store.ErrUnavailable
}
func (down) NewestEpoch(context.Context, []store.Tag, time.Time) (store.Epoch, bool, error) {
	return store.Epoch{}, false, store.ErrUnavailable
}
func (down) Info() store.Info { return store.Info{Name: "down", Remote: true} }

// FR-STF-2, FR-COA-11: a lock answered without the server says nothing about
// the server's health, so it never closes the store breaker that failures
// opened.
func TestLockSuccessDoesNotResetStoreBreaker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, err := memory.New(memory.Config{})
		if err != nil {
			t.Fatal(err)
		}
		var errs atomic.Int32
		cfg := cacheCfg
		cfg.Store = down{optimistic{m}}
		cfg.Observer = storeErrCount{&errs}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)
		o := weir.OriginFunc(func(context.Context, *weir.Request) (*weir.Response, error) {
			return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: io.NopCloser(strings.NewReader("x"))}, nil
		})
		for range 20 {
			resp, err := e.Serve(t.Context(), getReq("/x"), o)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
		}
		if n := errs.Load(); n > 10 {
			t.Fatalf("store errors = %d over 20 requests; the breaker never opened", n)
		}
	})
}

type storeErrCount struct{ n *atomic.Int32 }

func (c storeErrCount) Observe(ev weir.Event) {
	if ev.Kind == weir.EvStoreError {
		c.n.Add(1)
	}
}

// node is one engine's view of a shared lockStore, so a test can change what
// a single node's store calls answer.
type node struct {
	*lockStore
	epochCalls atomic.Int32
	epochs     func(n int32) (store.Epoch, bool, error) // nil: the shared store's
}

func (n *node) NewestEpoch(ctx context.Context, tags []store.Tag, since time.Time) (store.Epoch, bool, error) {
	c := n.epochCalls.Add(1)
	if n.epochs != nil {
		return n.epochs(c)
	}
	return n.lockStore.NewestEpoch(ctx, tags, since)
}

// FR-COA-11, FR-PRG-7, NFR-2: a purge that the waiting node's last check
// sees, after its lookup did not (a failed epoch read is "no epoch", T-9),
// makes it fetch for itself; it never serves the purged entry and never
// panics on the dropped one.
func TestAcrossNodesPurgedWhileWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		shared := newLockStore(t)
		a := &node{lockStore: shared}
		b := &node{lockStore: shared, epochs: func(n int32) (store.Epoch, bool, error) {
			if n == 1 {
				return store.Epoch{}, false, store.ErrUnavailable // the lookup's read fails
			}
			return store.Epoch{Mode: store.EpochSoft, At: time.Now()}, true, nil // purged since
		}}
		var calls atomic.Int32
		o := weir.OriginFunc(func(context.Context, *weir.Request) (*weir.Response, error) {
			calls.Add(1)
			time.Sleep(300 * time.Millisecond)
			return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: io.NopCloser(strings.NewReader("hello"))}, nil
		})
		cfg := cacheCfg
		cfg.Store = a
		ea := newEngine(t, cfg)
		defer closeEngine(t, ea)
		cfg.Store = b
		eb := newEngine(t, cfg)
		defer closeEngine(t, eb)
		var wg sync.WaitGroup
		for _, e := range []*weir.Engine{ea, eb} {
			wg.Go(func() {
				resp, err := e.Serve(t.Context(), getReq("/x"), o)
				if err != nil {
					t.Error(err)
					return
				}
				resp.Body.Close()
			})
			time.Sleep(time.Millisecond)
		}
		wg.Wait()
		if calls.Load() != 2 {
			t.Fatalf("origin calls = %d, want 2: the purged node fetched for itself", calls.Load())
		}
	})
}

// FR-COA-11, T-19: when the origin hangs, the followers on the waiting node
// do not each fetch past the flight's bound; they get the other node's
// recorded failure (FR-NEG-1) or the timeout.
func TestAcrossNodesHangingOriginNoHerd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		shared := newLockStore(t)
		var calls atomic.Int32
		o := weir.OriginFunc(func(ctx context.Context, _ *weir.Request) (*weir.Response, error) {
			calls.Add(1)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		cfg := cacheCfg
		cfg.Store = shared
		cfg.Timeouts.Origin = 2 * time.Second
		cfg.Limiter.MaxQueueWait = time.Second
		ea, eb := newEngine(t, cfg), newEngine(t, cfg)
		defer closeEngine(t, ea)
		defer closeEngine(t, eb)
		var wg sync.WaitGroup
		wg.Go(func() { _, _ = ea.Serve(t.Context(), getReq("/x"), o) })
		time.Sleep(time.Millisecond)
		for range 5 {
			wg.Go(func() {
				resp, err := eb.Serve(t.Context(), getReq("/x"), o)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode < 500 {
						t.Errorf("status %d from a hanging origin", resp.StatusCode)
					}
				}
			})
		}
		wg.Wait()
		if n := calls.Load(); n > 2 {
			t.Fatalf("origin calls = %d, want at most 2 (one per node)", n)
		}
	})
}

// FR-COA-11, FR-KEY-7: a response with Vary is shared across nodes too,
// under the variant the waiting node's own request selects.
func TestAcrossNodesVary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls, got := twoNodes(t, newLockStore(t), http.Header{"Vary": {"Accept-Encoding"}}, 300*time.Millisecond)
		if calls != 1 || got[1] == nil || !got[1].Cache.Hit {
			t.Fatalf("calls %d, second node %+v; want 1 call and a hit", calls, got[1])
		}
	})
}
