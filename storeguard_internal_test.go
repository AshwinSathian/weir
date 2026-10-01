package weir

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// guardStore answers Get with err, or blocks until the context ends and
// returns ErrUnavailable as 05 S-2 requires.
type guardStore struct {
	store.Store
	err   error
	block bool
}

func (s *guardStore) Info() store.Info { return store.Info{Remote: true} }

func (s *guardStore) Get(ctx context.Context, _ store.Key) (*store.Entry, error) {
	if s.block {
		<-ctx.Done()
		return nil, fmt.Errorf("guard: %w: %w", store.ErrUnavailable, ctx.Err())
	}
	return nil, s.err
}

// openDurations records the Duration of every EvStoreBreaker open event.
type openDurations struct {
	mu sync.Mutex
	d  []time.Duration
}

func (o *openDurations) Observe(ev Event) {
	if ev.Kind == EvStoreBreaker && ev.Reason == "open" {
		o.mu.Lock()
		o.d = append(o.d, ev.Duration)
		o.mu.Unlock()
	}
}

// FR-STF-2, 05 S-2: callers whose context ends while the store blocks do not
// count against it, so clients that disconnect cannot open the breaker.
func TestStoreGuardCallerCancelNotCounted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs := &openDurations{}
		g := newStoreGuard(&guardStore{block: true}, 50*time.Millisecond, obs)
		for range 10 {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
			if _, err := g.get(ctx, store.Key{}); !errors.Is(err, store.ErrUnavailable) {
				t.Fatalf("get = %v, want ErrUnavailable from the store", err)
			}
			cancel()
		}
		if n := g.fails.Load(); n != 0 || len(obs.d) != 0 {
			t.Fatalf("fails = %d, opens = %v; want none", n, obs.d)
		}
	})
}

// FR-STF-2, FR-STF-4, 05 S-3: only 5 consecutive failures open the breaker;
// any error other than ErrNotFound is a failure; the open period doubles
// from 1 s and stops at 30 s.
func TestStoreGuardBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs := &openDurations{}
		s := &guardStore{}
		g := newStoreGuard(s, 50*time.Millisecond, obs)
		call := func(err error) {
			s.err = err
			_, _ = g.get(t.Context(), store.Key{})
		}
		other := errors.New("connection reset") // not wrapped: still a failure (S-3)
		for range 4 {
			call(other)
		}
		call(store.ErrNotFound) // a miss is a success and resets the count
		for range 4 {
			call(other)
		}
		if len(obs.d) != 0 {
			t.Fatalf("opened after 4+4 failures split by a success: %v", obs.d)
		}
		call(other)
		for range 7 {
			time.Sleep(30*time.Second + time.Millisecond)
			call(other)
		}
		// A call that started before the breaker opened succeeds late: the
		// breaker closes, so the next 5 failures open it again, from 1 s.
		_ = g.exit(t.Context(), "get", nil)
		for range 5 {
			call(other)
		}
		for range 100 {
			call(other)
		}
		if n := g.fails.Load(); n != storeBreakerFails {
			t.Fatalf("fails after 100 failures while open = %d, want it held at %d", n, storeBreakerFails)
		}
		want := []time.Duration{1, 2, 4, 8, 16, 30, 30, 30, 1}
		for i := range want {
			want[i] *= time.Second
		}
		if fmt.Sprint(obs.d) != fmt.Sprint(want) {
			t.Fatalf("open durations = %v, want %v", obs.d, want)
		}
	})
}
