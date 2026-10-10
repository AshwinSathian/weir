package valkey

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/store"
)

type fakeClient struct {
	pol    map[string]string
	polErr error
	closed atomic.Int32
	kv     fakeKV   // the entry commands (entries_test.go)
	ep     fakeEp   // the epoch scripts (epochs_test.go)
	vary   fakeVary // hooks for the vary compare-and-set (vary_test.go)
}

func (f *fakeClient) policies(context.Context) (map[string]string, error) { return f.pol, f.polErr }
func (f *fakeClient) close()                                              { f.closed.Add(1) }

// fakeDialer counts dials and returns what the test sets. If block is
// non-nil the dial waits for it or for the context.
type fakeDialer struct {
	mu    sync.Mutex
	calls int
	times []time.Time
	err   error
	cl    *fakeClient
	block chan struct{}
}

func (d *fakeDialer) dial(ctx context.Context, _ Config) (client, error) {
	d.mu.Lock()
	d.calls++
	d.times = append(d.times, time.Now())
	err, cl, block := d.err, d.cl, d.block
	d.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return cl, nil
}

func (d *fakeDialer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func newFake(t *testing.T, mutate func(*Config), d *fakeDialer) *Store {
	t.Helper()
	cfg := validConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := newStore(cfg, d.dial)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func okClient() *fakeClient {
	return &fakeClient{pol: map[string]string{"127.0.0.1:6379": "volatile-lfu"}}
}

// FR-STF-2, 05 §7: a bad config fails New; nothing connects.
// FR-STF-2.
func TestNewBadConfigFails(t *testing.T) {
	_, err := New(Config{})
	if !errors.Is(err, weir.ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

// FR-STF-2: an unreachable server does not fail New; calls return
// ErrUnavailable. The real client is used against a closed local port.
func TestNewUnreachableServerSucceeds(t *testing.T) {
	cfg := Config{Addrs: []string{"127.0.0.1:1"}, CallTimeout: time.Second}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(t.Context(), store.Key{}); !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("Get err = %v, want ErrUnavailable", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// FR-STF-2, FR-LCY-2: failing dials leave no goroutine behind (synctest
// fails the test if one outlives the bubble).
func TestUnreachableServerNoGoroutineLeak(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDialer{err: errors.New("connection refused")}
		s := newFake(t, nil, d)
		for range 3 {
			if err := s.Delete(t.Context(), store.Key{}); !errors.Is(err, store.ErrUnavailable) {
				t.Fatalf("err = %v", err)
			}
			time.Sleep(2 * time.Second)
		}
		s.Close()
	})
}

// FR-STF-2, 05 §7: at most one dial per second; calls in between fail fast
// with the last error.
func TestReconnectRateLimited(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDialer{err: errors.New("connection refused")}
		s := newFake(t, nil, d)
		defer s.Close()
		for range 10 {
			if _, err := s.Get(t.Context(), store.Key{}); !errors.Is(err, store.ErrUnavailable) {
				t.Fatalf("err = %v", err)
			}
		}
		if d.count() != 1 {
			t.Fatalf("dials = %d, want 1", d.count())
		}
		time.Sleep(time.Second)
		_, _ = s.Get(t.Context(), store.Key{})
		_, _ = s.Get(t.Context(), store.Key{})
		if d.count() != 2 {
			t.Fatalf("dials = %d, want 2 after one second", d.count())
		}
		if gap := d.times[1].Sub(d.times[0]); gap < time.Second {
			t.Fatalf("gap = %v, want >= 1s", gap)
		}
	})
}

// FR-STF-2: a successful connect is kept: later calls do not dial again.
func TestConnectOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDialer{cl: okClient()}
		s := newFake(t, nil, d)
		for range 5 {
			_, _ = s.Get(t.Context(), store.Key{})
		}
		if d.count() != 1 {
			t.Fatalf("dials = %d, want 1", d.count())
		}
		s.Close()
		if d.cl.closed.Load() != 1 {
			t.Fatalf("client closed %d times, want 1", d.cl.closed.Load())
		}
	})
}

// FR-STF-2, P8: concurrent callers share one dial (single flight).
func TestConcurrentCallersShareDial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDialer{cl: okClient(), block: make(chan struct{})}
		s := newFake(t, nil, d)
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() { _, _ = s.Get(t.Context(), store.Key{}) })
		}
		synctest.Wait()
		close(d.block)
		wg.Wait()
		if d.count() != 1 {
			t.Fatalf("dials = %d, want 1", d.count())
		}
		s.Close()
	})
}

// FR-STF-2, S-2: a remote store gets the store breaker and timeout.
func TestInfo(t *testing.T) {
	s := newFake(t, nil, &fakeDialer{})
	defer s.Close()
	if got := s.Info(); got != (store.Info{Name: "valkey", Remote: true}) {
		t.Fatalf("Info = %+v", got)
	}
}

// FR-LCY-2.
func TestCloseTwice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDialer{cl: okClient()}
		s := newFake(t, nil, d)
		_, _ = s.Get(t.Context(), store.Key{})
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if d.cl.closed.Load() != 1 {
			t.Fatalf("client closed %d times, want 1", d.cl.closed.Load())
		}
	})
}

// 05 §7: a Close during a connect ends it; the client just built is closed
// and the waiting caller sees ErrUnavailable.
func TestCloseDuringConnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cl := okClient()
		d := &fakeDialer{cl: cl, block: make(chan struct{})}
		s := newFake(t, nil, d)
		errc := make(chan error, 1)
		go func() {
			_, err := s.Get(t.Context(), store.Key{})
			errc <- err
		}()
		synctest.Wait() // the dial is blocked
		s.Close()       // cancels the dial; Close waits for the dial goroutine
		if err := <-errc; !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
		if _, err := s.Get(t.Context(), store.Key{}); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("after Close err = %v", err)
		}
	})
}

// 05 §7: no client is built after Close.
func TestNoDialAfterClose(t *testing.T) {
	d := &fakeDialer{cl: okClient()}
	s := newFake(t, nil, d)
	s.Close()
	ctx := t.Context()
	_, _ = s.Get(ctx, store.Key{})
	_ = s.Set(ctx, store.Key{}, &store.Entry{})
	_ = s.Delete(ctx, store.Key{})
	_ = s.SetEpoch(ctx, store.Tag{}, store.Epoch{})
	_, _, _ = s.NewestEpoch(ctx, nil, time.Time{})
	if d.count() != 0 {
		t.Fatalf("dials = %d after Close", d.count())
	}
}

// 05 §7: a context that is already done never dials.
func TestCanceledContextDoesNotDial(t *testing.T) {
	d := &fakeDialer{cl: okClient()}
	s := newFake(t, nil, d)
	defer s.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Get(ctx, store.Key{}); !errors.Is(err, store.ErrUnavailable) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want ErrUnavailable wrapping Canceled", err)
	}
	if d.count() != 0 {
		t.Fatalf("dials = %d, want 0", d.count())
	}
}

// S-2: a call without a deadline gets CallTimeout; a dial that hangs ends.
func TestCallTimeoutAppliesToDial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDialer{cl: okClient(), block: make(chan struct{})}
		s := newFake(t, func(c *Config) { c.CallTimeout = 2 * time.Second }, d)
		start := time.Now()
		_, err := s.Get(context.Background(), store.Key{})
		if !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("err = %v", err)
		}
		if el := time.Since(start); el != 2*time.Second {
			t.Fatalf("waited %v, want 2s", el)
		}
		s.Close()
	})
}

// T-29, 05 §7: allkeys-* can evict epoch keys; the store stays
// ErrUnavailable and names the policy. volatile-* and noeviction pass.
func TestPolicyCheck(t *testing.T) {
	const a, b = "10.0.0.1:6379", "10.0.0.2:6379"
	cases := []struct {
		name string
		pol  map[string]string
		skip bool
		fail bool
	}{
		{"standalone volatile-lfu passes", map[string]string{a: "volatile-lfu"}, false, false},
		{"standalone noeviction passes", map[string]string{a: "noeviction"}, false, false},
		{"unknown policy fails", map[string]string{a: "future-evict-all"}, false, true},
		{"standalone volatile-ttl passes", map[string]string{a: "volatile-ttl"}, false, false},
		{"standalone allkeys-lfu fails", map[string]string{a: "allkeys-lfu"}, false, true},
		{"cluster all nodes volatile passes", map[string]string{a: "volatile-lfu", b: "noeviction"}, false, false},
		{"cluster one allkeys node fails", map[string]string{a: "volatile-lfu", b: "allkeys-lru"}, false, true},
		{"no nodes reported fails", map[string]string{}, false, true},
		{"empty policy fails", map[string]string{a: ""}, false, true},
		{"SkipPolicyCheck accepts allkeys-lfu", map[string]string{a: "allkeys-lfu"}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cl := &fakeClient{pol: tc.pol}
				d := &fakeDialer{cl: cl}
				s := newFake(t, func(c *Config) { c.SkipPolicyCheck = tc.skip }, d)
				defer s.Close()
				_, err := s.acquire(t.Context())
				if tc.fail {
					if !errors.Is(err, store.ErrUnavailable) || !errors.Is(err, errPolicy) ||
						!strings.Contains(err.Error(), "maxmemory-policy") {
						t.Fatalf("err = %v, want ErrUnavailable naming the policy", err)
					}
					if cl.closed.Load() != 1 {
						t.Fatalf("rejected client closed %d times, want 1", cl.closed.Load())
					}
					// Every call stays unavailable, even after the retry gap.
					time.Sleep(2 * time.Second)
					if _, err := s.Get(t.Context(), store.Key{}); !errors.Is(err, store.ErrUnavailable) {
						t.Fatalf("later Get err = %v", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			})
		})
	}
}

// A policy lookup that fails (CONFIG disabled) is ErrUnavailable too.
func TestPolicyCheckLookupError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDialer{cl: &fakeClient{polErr: errors.New("unknown command CONFIG")}}
		s := newFake(t, nil, d)
		defer s.Close()
		if _, err := s.acquire(t.Context()); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("err = %v", err)
		}
	})
}

// 05 §7: client options. No retry, no client cache, standalone unless
// Cluster, three MOVED redirections.
func TestClientOption(t *testing.T) {
	cfg, _ := validConfig().Validate()
	o := clientOption(cfg)
	if !o.DisableRetry || !o.DisableCache || !o.ForceSingleClient || o.ReplicaOnly || o.SendToReplicas != nil ||
		o.ClusterOption.MaxMovedRedirections != 3 {
		t.Fatalf("option = %+v", o)
	}
	cfg.Cluster = true
	if clientOption(cfg).ForceSingleClient {
		t.Fatal("Cluster must not force a single client")
	}
}

// FR-LCY-2, 05 §7: a Close that lands while a dial is returning closes the
// client just built; the waiting caller sees ErrUnavailable.
func TestCloseClosesClientBuiltDuringConnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cl := okClient()
		s := newFake(t, nil, &fakeDialer{})
		release := make(chan struct{})
		s.dial = func(context.Context, Config) (client, error) {
			<-release // ignores ctx, like a dial that already finished
			return cl, nil
		}
		errc := make(chan error, 1)
		go func() {
			_, err := s.Get(t.Context(), store.Key{})
			errc <- err
		}()
		synctest.Wait()
		go func() { _ = s.Close() }()
		synctest.Wait() // Close has set closed and waits for the dial
		close(release)
		if err := <-errc; !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
		synctest.Wait()
		if cl.closed.Load() != 1 {
			t.Fatalf("client closed %d times, want 1", cl.closed.Load())
		}
	})
}

// FR-STF-2: the shared dial survives the first caller's short deadline.
func TestShortDeadlineCallerDoesNotCancelSharedDial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDialer{cl: okClient(), block: make(chan struct{})}
		s := newFake(t, nil, d)
		defer s.Close()
		short, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if _, err := s.Get(short, store.Key{}); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("err = %v", err)
		}
		close(d.block)
		synctest.Wait()
		if _, err := s.acquire(t.Context()); err != nil {
			t.Fatalf("shared dial was cancelled: %v", err)
		}
		if d.count() != 1 {
			t.Fatalf("dials = %d, want 1", d.count())
		}
	})
}

// FR-STF-2: callers that pile onto a failing dial share it, all get
// ErrUnavailable, none hangs, and the gap runs from the end of the attempt.
func TestConcurrentCallersShareFailingDial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDialer{err: errors.New("refused"), block: make(chan struct{})}
		s := newFake(t, nil, d)
		defer s.Close()
		var wg sync.WaitGroup
		var bad atomic.Int32
		for range 8 {
			wg.Go(func() {
				if _, err := s.Get(t.Context(), store.Key{}); !errors.Is(err, store.ErrUnavailable) {
					bad.Add(1)
				}
			})
		}
		synctest.Wait()
		time.Sleep(900 * time.Millisecond) // the dial is slow
		close(d.block)
		wg.Wait()
		if bad.Load() != 0 || d.count() != 1 {
			t.Fatalf("bad = %d, dials = %d, want 0 and 1", bad.Load(), d.count())
		}
		time.Sleep(900 * time.Millisecond) // under 1s from the end of the attempt
		_, _ = s.Get(t.Context(), store.Key{})
		if d.count() != 1 {
			t.Fatalf("dials = %d, want 1 inside the gap", d.count())
		}
		time.Sleep(200 * time.Millisecond)
		_, _ = s.Get(t.Context(), store.Key{})
		if d.count() != 2 {
			t.Fatalf("dials = %d, want 2 after the gap", d.count())
		}
	})
}

// 05 §7: a second Close does not return before the first has stopped
// everything.
func TestSecondCloseWaitsForDial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		s := newFake(t, nil, &fakeDialer{})
		s.dial = func(context.Context, Config) (client, error) {
			<-release
			return okClient(), nil
		}
		go func() { _, _ = s.Get(t.Context(), store.Key{}) }()
		synctest.Wait()
		go func() { _ = s.Close() }()
		synctest.Wait()
		var done atomic.Bool
		go func() { _ = s.Close(); done.Store(true) }()
		synctest.Wait()
		if done.Load() {
			t.Fatal("second Close returned while the dial was still running")
		}
		close(release)
		synctest.Wait()
		if !done.Load() {
			t.Fatal("second Close never returned")
		}
	})
}

// Standalone mode takes one address (valkey-go uses only the first).
func TestStandaloneRejectsSeveralAddrs(t *testing.T) {
	c := Config{Addrs: []string{"a:1", "b:1"}}
	if _, err := c.Validate(); !errors.Is(err, weir.ErrInvalidConfig) {
		t.Fatalf("standalone err = %v", err)
	}
	c.Cluster = true
	if _, err := c.Validate(); err != nil {
		t.Fatalf("cluster err = %v", err)
	}
}
