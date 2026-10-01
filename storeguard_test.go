package weir_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

// faultyStore wraps a memory store. fail makes every call return
// ErrUnavailable, failEpoch only NewestEpoch, block makes every call wait for
// its context. It records the longest call and counts calls.
type faultyStore struct {
	store.Store
	remote    bool
	fail      atomic.Bool
	failEpoch atomic.Bool
	block     atomic.Bool
	calls     atomic.Int64
	deadlines atomic.Int64 // calls whose context had a deadline

	mu      sync.Mutex
	longest time.Duration
}

func newFaultyStore(t *testing.T, remote bool) *faultyStore {
	t.Helper()
	m, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return &faultyStore{Store: m, remote: remote}
}

func (s *faultyStore) Info() store.Info { return store.Info{Name: "faulty", Remote: s.remote} }

// enter counts a call and blocks or fails it as configured. A blocked call
// ends with ErrUnavailable when its context does, as 05 S-2 requires.
func (s *faultyStore) enter(ctx context.Context) error {
	s.calls.Add(1)
	if _, ok := ctx.Deadline(); ok {
		s.deadlines.Add(1)
	}
	if s.block.Load() {
		start := time.Now()
		<-ctx.Done()
		s.mu.Lock()
		s.longest = max(s.longest, time.Since(start))
		s.mu.Unlock()
		return fmt.Errorf("faulty: %w: %w", store.ErrUnavailable, ctx.Err())
	}
	if s.fail.Load() {
		return store.ErrUnavailable
	}
	return nil
}

func (s *faultyStore) Get(ctx context.Context, k store.Key) (*store.Entry, error) {
	if err := s.enter(ctx); err != nil {
		return nil, err
	}
	return s.Store.Get(ctx, k)
}

func (s *faultyStore) Set(ctx context.Context, k store.Key, e *store.Entry) error {
	if err := s.enter(ctx); err != nil {
		return err
	}
	return s.Store.Set(ctx, k, e)
}

func (s *faultyStore) SetEpoch(ctx context.Context, t store.Tag, ep store.Epoch) error {
	if err := s.enter(ctx); err != nil {
		return err
	}
	return s.Store.SetEpoch(ctx, t, ep)
}

func (s *faultyStore) NewestEpoch(ctx context.Context, tags []store.Tag, since time.Time) (store.Epoch, bool, error) {
	if err := s.enter(ctx); err != nil {
		return store.Epoch{}, false, err
	}
	if s.failEpoch.Load() {
		return store.Epoch{}, false, store.ErrUnavailable
	}
	return s.Store.NewestEpoch(ctx, tags, since)
}

func (s *faultyStore) longestCall() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.longest
}

// FR-STF-2, FR-STF-3, FR-COA-7, T6.5: with every store call failing, 1 000
// requests over 10 keys still coalesce into one fetch per key and stay
// under MaxConcurrent, and the store breaker opens.
func TestStoreOutageStillCoalescedAndLimited(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const slots = 8
		o := testorigin.NewChecked(t, slots, 16)
		b := cacheable("v")
		b.Delay = 50 * time.Millisecond
		o.Default(b)
		s := newFaultyStore(t, false)
		s.fail.Store(true)
		obs := &eventCounter{}
		cfg := cacheCfg
		cfg.Store = s
		cfg.Observer = obs
		cfg.Limiter = weir.LimiterConfig{MaxConcurrent: slots}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		chs := make([]<-chan served, 1000)
		for i := range chs {
			chs[i] = serveAsync(t.Context(), e, getReq(fmt.Sprintf("/k%d", i%10)), o)
		}
		for i, ch := range chs {
			if r := <-ch; r.err != nil || r.body != "v" {
				t.Fatalf("request %d: %v, %q", i, r.err, r.body)
			}
		}
		if n := o.TotalCalls(); n > 10 {
			t.Fatalf("origin calls = %d, want at most 10 (one per key)", n)
		}
		if n := obs.count("store-breaker/open"); n != 1 {
			t.Fatalf("EvStoreBreaker open = %d, want 1", n)
		}
		if n := obs.count("store-error/get"); n < 5 {
			t.Fatalf("EvStoreError get = %d, want at least 5", n)
		}
		// FR-STF-1: an in-process store gets the caller's context, no deadline.
		if n := s.deadlines.Load(); n != 0 {
			t.Fatalf("calls to an in-process store with a deadline = %d, want 0", n)
		}
	})
}

// FR-STF-1, FR-STF-2, T6.5: a remote store that never answers costs each
// call at most Timeouts.Store; after 5 timeouts the breaker opens and
// requests skip the store entirely; after the open period calls reach the
// store again, a failure reopens it for twice as long and a success closes
// it.
func TestStoreSlowRemote(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const storeTimeout = 50 * time.Millisecond
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(cacheable("v"))
		s := newFaultyStore(t, true)
		s.block.Store(true)
		obs := &eventCounter{}
		cfg := cacheCfg
		cfg.Store = s
		cfg.Observer = obs
		cfg.Timeouts.Store = storeTimeout
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		// A miss makes four store calls (lookup Get, the newest-wins Get,
		// Set, the purge check's NewestEpoch), each cut at Timeouts.Store;
		// the second request's lookup is the fifth failure.
		for i, want := range []time.Duration{4 * storeTimeout, storeTimeout} {
			r := <-serveTimed(t, e, getReq(fmt.Sprintf("/a%d", i)), o)
			if r.err != nil || r.elapsed != want {
				t.Fatalf("request %d: %v after %v, want %v", i, r.err, r.elapsed, want)
			}
		}
		if d := s.longestCall(); d != storeTimeout {
			t.Fatalf("longest store call = %v, want %v", d, storeTimeout)
		}
		if n := obs.count("store-breaker/open"); n != 1 {
			t.Fatalf("EvStoreBreaker open = %d, want 1", n)
		}
		before := s.calls.Load()
		for i := range 10 {
			r := <-serveTimed(t, e, getReq(fmt.Sprintf("/b%d", i)), o)
			if r.err != nil || r.elapsed != 0 {
				t.Fatalf("request %d with the breaker open: %v after %v, want no store time", i, r.err, r.elapsed)
			}
		}
		if n := s.calls.Load() - before; n != 0 {
			t.Fatalf("store calls with the breaker open = %d, want 0", n)
		}

		// After 1 s a failing call reopens it for 2 s; then a success closes it.
		time.Sleep(time.Second)
		<-serveTimed(t, e, getReq("/c"), o)
		if n := obs.count("store-breaker/open"); n != 2 {
			t.Fatalf("EvStoreBreaker open after a failed call = %d, want 2", n)
		}
		time.Sleep(time.Second)
		before = s.calls.Load()
		<-serveTimed(t, e, getReq("/d"), o)
		if n := s.calls.Load() - before; n != 0 {
			t.Fatalf("store calls 1 s into a 2 s open period = %d, want 0", n)
		}
		time.Sleep(time.Second + time.Millisecond) // past the end of the 2 s period
		s.block.Store(false)
		if r := <-serveTimed(t, e, getReq("/e"), o); r.err != nil || r.elapsed != 0 {
			t.Fatalf("request after recovery: %v after %v", r.err, r.elapsed)
		}
		if n := obs.count("store-breaker/closed"); n != 1 {
			t.Fatalf("EvStoreBreaker closed = %d, want 1", n)
		}
	})
}

// T-9, FR-STF-1, 04 §6.3: a remote store failing NewestEpoch still serves
// the entry (fail open) and emits EvStoreError epoch.
func TestEpochLookupErrorEmitsEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(cacheable("v"))
		s := newFaultyStore(t, true)
		obs := &eventCounter{}
		cfg := cacheCfg
		cfg.Store = s
		cfg.Observer = obs
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/x"), o)
		s.failEpoch.Store(true)
		resp, body := serve(t, e, getReq("/x"), o)
		if !resp.Cache.Hit || body != "v" {
			t.Fatalf("second request hit=%v %q, want a hit", resp.Cache.Hit, body)
		}
		if n := o.TotalCalls(); n != 1 {
			t.Fatalf("origin calls = %d, want 1", n)
		}
		if n := obs.count("store-error/epoch"); n != 1 {
			t.Fatalf("EvStoreError epoch = %d, want 1", n)
		}
	})
}
