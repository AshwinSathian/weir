package memory

import (
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/storetest"
)

func newStore(t *testing.T, cfg Config) *Store {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func numKey(n uint64) store.Key {
	var k store.Key
	binary.BigEndian.PutUint64(k[24:], n)
	return k
}

func entry(bodyLen int) *store.Entry {
	return &store.Entry{Kind: store.KindResponse, Status: 200, Body: make([]byte, bodyLen), Expires: time.Now().Add(time.Hour)}
}

// 05 §2, §4, §5 (S-1..S-5, E-1..E-10).
func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store { return newStore(t, Config{MaxHardEpochs: 8}) },
		storetest.HardEpochCap(8), storetest.Synctest())
}

// FR-LCY-1, 05 §5.1: New rejects invalid sizes and applies defaults.
func TestNewConfig(t *testing.T) {
	for _, cfg := range []Config{{MaxBytes: -1}, {Shards: -1}, {Shards: 3}, {Shards: 1 << 40}} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) = nil error", cfg)
		}
	}
	s := newStore(t, Config{})
	if len(s.shards) != 16 || s.shards[0].cap != 16<<20 {
		t.Errorf("defaults: %d shards of %d bytes, want 16 of 16 MiB", len(s.shards), s.shards[0].cap)
	}
	if got := s.MaxObjectBytes(); got != (16<<20)/10 {
		t.Errorf("MaxObjectBytes = %d, want 10%% of a shard", got)
	}
}

// NFR-3, 05 §5.3: a record larger than the small queue is declined, not an error.
func TestSetDeclinesOversize(t *testing.T) {
	s := newStore(t, Config{MaxBytes: 1 << 20, Shards: 1})
	e := entry(int(s.MaxObjectBytes()))
	if err := s.Set(t.Context(), numKey(1), e); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := s.Get(t.Context(), numKey(1)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("oversize record stored: err = %v", err)
	}
	if s.Bytes() != 0 {
		t.Fatalf("Bytes = %d after declined Set", s.Bytes())
	}
	// A declined replacement removes the older record: Set leaves either the
	// new record or nothing, never the one it was asked to replace.
	if err := s.Set(t.Context(), numKey(2), entry(10)); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(t.Context(), numKey(2), e); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(t.Context(), numKey(2)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("declined replacement kept the old record: %v, %v", got, err)
	}
}

// NFR-3, 05 §5.3: a hit takes only the shard's read lock. With the read
// lock held here, a write-locking Get deadlocks, so a regression shows up as
// a test timeout rather than a failure.
func TestGetHitTakesReadLock(t *testing.T) {
	s := newStore(t, Config{Shards: 1})
	k := numKey(1)
	if err := s.Set(t.Context(), k, entry(10)); err != nil {
		t.Fatal(err)
	}
	sh := &s.shards[0]
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	for range 5 {
		if _, err := s.Get(t.Context(), k); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	if f := sh.m[k].freq.Load(); f != 3 {
		t.Fatalf("freq = %d after 5 hits, want capped at 3", f)
	}
}

// T-11, ADR-6, T6.11: 1 000 hot keys accessed repeatedly, then 100 000
// one-hit keys; at least 90% of the hot keys remain.
func TestS3FIFOScanResistance(t *testing.T) {
	s := newStore(t, Config{MaxBytes: 4 << 20})
	ctx := t.Context()
	const hot, flood = 1000, 100_000
	for i := range uint64(hot) {
		if err := s.Set(ctx, numKey(i), entry(64)); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		for i := range uint64(hot) {
			if _, err := s.Get(ctx, numKey(i)); err != nil {
				t.Fatalf("hot key %d missing before flood: %v", i, err)
			}
		}
	}
	for i := range uint64(flood) {
		if err := s.Set(ctx, numKey(hot+i), entry(64)); err != nil {
			t.Fatal(err)
		}
	}
	kept := 0
	for i := range uint64(hot) {
		if _, err := s.Get(ctx, numKey(i)); err == nil {
			kept++
		}
	}
	if kept < hot*9/10 {
		t.Fatalf("%d of %d hot keys kept after the flood, want >= 90%%", kept, hot)
	}
}

// NFR-3, INV-5, T6.11: after every Set, Bytes() <= MaxBytes, across inserts,
// in-place replacements with other sizes, promotions and deletes.
func TestByteAccountingBound(t *testing.T) {
	const maxBytes = 256 << 10
	s := newStore(t, Config{MaxBytes: maxBytes, Shards: 4})
	ctx := t.Context()
	r := rand.New(rand.NewPCG(1, 2))     //nolint:gosec // reproducible workload, not security
	last := map[store.Key]*store.Entry{} // S-4: a hit returns the latest Set, never an older one
	for i := range 50_000 {
		k := numKey(r.Uint64N(2000))
		switch r.IntN(10) {
		case 0:
			if err := s.Delete(ctx, k); err != nil {
				t.Fatal(err)
			}
			delete(last, k)
		case 1, 2, 3:
			if e, err := s.Get(ctx, k); err == nil && e != last[k] {
				t.Fatalf("op %d: Get returned a record other than the latest Set", i)
			}
		default:
			// Up to 10% over the limit, so some replacements are declined.
			e := entry(r.IntN(int(s.MaxObjectBytes()) * 11 / 10))
			if err := s.Set(ctx, k, e); err != nil {
				t.Fatal(err)
			}
			last[k] = e
		}
		if b := s.Bytes(); b > maxBytes || b < 0 {
			t.Fatalf("op %d: Bytes = %d, want in [0, %d]", i, b, maxBytes)
		}
	}
	for i := range s.shards {
		sh := &s.shards[i]
		var sum int64
		for _, n := range sh.m {
			sum += n.size
		}
		if sum != sh.bytes || sh.small.bytes+sh.main.bytes != sh.bytes || sh.small.len+sh.main.len != len(sh.m) {
			t.Fatalf("shard %d: nodes %d bytes, shard %d, small %d + main %d; lens %d + %d vs map %d",
				i, sum, sh.bytes, sh.small.bytes, sh.main.bytes, sh.small.len, sh.main.len, len(sh.m))
		}
		if max(1024, sh.main.len) < len(sh.ghost.ring)-sh.ghost.head {
			t.Fatalf("shard %d: ghost holds %d fingerprints", i, len(sh.ghost.ring)-sh.ghost.head)
		}
	}
}

// T-22, ADR-3: 100 000 keys sharing their first 8 bytes spread across
// shards within 20% of uniform.
func TestShardDistributionAdversarial(t *testing.T) {
	s := newStore(t, Config{})
	counts := make([]int, len(s.shards))
	const n = 100_000
	for i := range uint64(n) {
		var k store.Key
		copy(k[:8], "grinding")
		binary.BigEndian.PutUint64(k[8:], i)
		counts[s.shardIndex(k)]++
	}
	want := n / len(counts)
	for i, c := range counts {
		if c < want*8/10 || c > want*12/10 {
			t.Errorf("shard %d holds %d keys, want %d +/- 20%%", i, c, want)
		}
	}
}

// S-4, 05 §5.3: an expired record found by Get is unlinked and its bytes
// released.
func TestExpiredGetUnlinks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newStore(t, Config{Shards: 1})
		e := entry(10)
		e.Expires = time.Now().Add(time.Second)
		if err := s.Set(t.Context(), numKey(1), e); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		if _, err := s.Get(t.Context(), numKey(1)); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("Get at Expires = %v", err)
		}
		if s.Bytes() != 0 || len(s.shards[0].m) != 0 {
			t.Fatalf("expired record still accounted: %d bytes, %d nodes", s.Bytes(), len(s.shards[0].m))
		}
	})
}

// ADR-6, T-11: a key evicted from small into the ghost goes straight to
// main when it is set again.
func TestGhostHitInsertsIntoMain(t *testing.T) {
	s := newStore(t, Config{MaxBytes: 64 << 10, Shards: 1})
	sh := &s.shards[0]
	ctx := t.Context()
	k := numKey(0)
	if err := s.Set(ctx, k, entry(64)); err != nil {
		t.Fatal(err)
	}
	for i := uint64(1); sh.m[k] != nil; i++ {
		if err := s.Set(ctx, numKey(i), entry(64)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Set(ctx, k, entry(64)); err != nil {
		t.Fatal(err)
	}
	if n := sh.m[k]; n == nil || n.queue != queueMain {
		t.Fatalf("re-set key after ghost: node %+v, want in main", n)
	}
	if _, ok := sh.ghost.set[sh.m[k].fp]; ok {
		t.Fatal("fingerprint still in ghost after the hit")
	}
}

// NFR-3, 05 §5.3: the ghost holds at most max(1024, entries in main) after
// main shrinks, not only at its last add.
func TestGhostBoundFollowsMain(t *testing.T) {
	s := newStore(t, Config{MaxBytes: 2 << 20, Shards: 1})
	sh := &s.shards[0]
	ctx := t.Context()
	// Twice-read keys reach main once the flood pushes them out of small.
	const hot = 5000
	for i := range uint64(hot) {
		if err := s.Set(ctx, numKey(i), entry(64)); err != nil {
			t.Fatal(err)
		}
		_, _ = s.Get(ctx, numKey(i))
		_, _ = s.Get(ctx, numKey(i))
	}
	for i := range uint64(20_000) {
		if err := s.Set(ctx, numKey(hot+i), entry(64)); err != nil {
			t.Fatal(err)
		}
	}
	if sh.main.len <= ghostFloor {
		t.Fatalf("setup: main holds %d entries", sh.main.len)
	}
	for i := range uint64(hot) {
		if err := s.Delete(ctx, numKey(i)); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(sh.ghost.ring) - sh.ghost.head; n > max(ghostFloor, sh.main.len) {
		t.Fatalf("ghost holds %d fingerprints with %d entries in main", n, sh.main.len)
	}
}

// S-1, NFR-3: concurrent Set, Get and Delete under constant eviction keep
// every shard's bookkeeping consistent and within budget.
func TestConcurrentEvictionInvariants(t *testing.T) {
	const maxBytes = 128 << 10
	s := newStore(t, Config{MaxBytes: maxBytes, Shards: 2})
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Go(func() {
			r := rand.New(rand.NewPCG(uint64(g), 7)) //nolint:gosec // reproducible workload, not security
			for range 5000 {
				k := numKey(r.Uint64N(3000))
				switch r.IntN(8) {
				case 0:
					_ = s.Delete(t.Context(), k)
				case 1, 2, 3:
					_, _ = s.Get(t.Context(), k)
				default:
					_ = s.Set(t.Context(), k, entry(r.IntN(2000)))
				}
				if b := s.Bytes(); b > maxBytes {
					t.Errorf("Bytes = %d > %d", b, maxBytes)
					return
				}
			}
		})
	}
	wg.Wait()
	for i := range s.shards {
		sh := &s.shards[i]
		var sum int64
		n := 0
		for _, q := range []*fifo{&sh.small, &sh.main} {
			for x := q.head; x != nil; x = x.next {
				if sh.m[x.key] != x || sh.queue(x) != q {
					t.Fatalf("shard %d: node %x linked in the wrong queue or not mapped", i, x.key[24:])
				}
				sum += x.size
				n++
			}
		}
		if n != len(sh.m) || sum != sh.bytes || sh.small.bytes+sh.main.bytes != sh.bytes {
			t.Fatalf("shard %d: %d linked nodes, %d mapped; %d linked bytes, %d accounted", i, n, len(sh.m), sum, sh.bytes)
		}
	}
}
