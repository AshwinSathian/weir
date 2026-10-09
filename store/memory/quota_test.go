package memory

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/AshwinSathian/weir/store"
)

func ownedEntry(owner byte, bodyLen int) *store.Entry {
	e := entry(bodyLen)
	e.Owner = store.Tag{owner}
	return e
}

func present(ctx context.Context, s *Store, k store.Key) bool {
	_, err := s.Get(ctx, k)
	return err == nil
}

// ownerBytes reads the shard-0 account of one owner.
func ownerBytes(s *Store, owner byte) int64 {
	sh := &s.shards[0]
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	return sh.owners[store.Tag{owner}]
}

// FR-FAIR-2, T-32: a tenant flooding unique pages turns over its own quota
// and never evicts another tenant's entries.
func TestOwnerQuotaIsolatesTenants(t *testing.T) {
	size := ownedEntry(1, 100).Size()
	cfg := Config{MaxBytes: 1 << 20, Shards: 1, MaxBytesPerOwner: 10 * size}

	t.Run("a flooding tenant evicts only its own entries", func(t *testing.T) {
		s := newStore(t, cfg)
		for i := uint64(1); i <= 5; i++ {
			_ = s.Set(t.Context(), numKey(i), ownedEntry(2, 100)) // tenant B
		}
		for i := uint64(100); i < 300; i++ {
			_ = s.Set(t.Context(), numKey(i), ownedEntry(1, 100)) // tenant A floods
			_, _ = s.Get(t.Context(), numKey(i))
			_, _ = s.Get(t.Context(), numKey(i)) // hot enough to reach main
		}
		for i := uint64(1); i <= 5; i++ {
			if !present(t.Context(), s, numKey(i)) {
				t.Fatalf("tenant B entry %d was evicted by tenant A", i)
			}
		}
		if got := ownerBytes(s, 1); got > cfg.MaxBytesPerOwner {
			t.Fatalf("tenant A holds %d bytes, quota %d", got, cfg.MaxBytesPerOwner)
		}
		if !present(t.Context(), s, numKey(299)) || present(t.Context(), s, numKey(100)) {
			t.Fatal("tenant A should keep its newest page and lose its oldest")
		}
	})

	t.Run("an entry larger than the quota is declined and drops the record it replaces", func(t *testing.T) {
		s := newStore(t, Config{MaxBytes: 1 << 20, Shards: 1, MaxBytesPerOwner: 2 * size})
		_ = s.Set(t.Context(), numKey(1), ownedEntry(1, 100))
		_ = s.Set(t.Context(), numKey(1), ownedEntry(1, 1000))
		if present(t.Context(), s, numKey(1)) {
			t.Fatal("over-quota replacement left the older record")
		}
		if got := ownerBytes(s, 1); got != 0 {
			t.Fatalf("owner account = %d after the drop, want 0", got)
		}
	})

	t.Run("the victim scan is bounded per queue, so a set with no own entry in reach is declined", func(t *testing.T) {
		s := newStore(t, Config{MaxBytes: 1 << 20, Shards: 1, MaxBytesPerOwner: 2 * size})
		for i := uint64(1); i <= ownerScan+6; i++ {
			_ = s.Set(t.Context(), numKey(i), ownedEntry(0, 100)) // oldest: unlimited entries of no owner
		}
		_ = s.Set(t.Context(), numKey(1000), ownedEntry(1, 100))
		_ = s.Set(t.Context(), numKey(1001), ownedEntry(1, 100))
		_ = s.Set(t.Context(), numKey(1002), ownedEntry(1, 100)) // own entries sit beyond the scan
		if present(t.Context(), s, numKey(1002)) {
			t.Fatal("set stored although no own victim was in reach")
		}
		if !present(t.Context(), s, numKey(1000)) || !present(t.Context(), s, numKey(1001)) {
			t.Fatal("a declined set evicted entries")
		}
		for i := uint64(1); i <= ownerScan+6; i++ {
			if !present(t.Context(), s, numKey(i)) {
				t.Fatalf("unowned entry %d evicted", i)
			}
		}
	})

	t.Run("own entries in main are reachable past a full window of foreign small nodes", func(t *testing.T) {
		s := newStore(t, Config{MaxBytes: 1 << 20, Shards: 1, MaxBytesPerOwner: 3 * size})
		sh := &s.shards[0]
		// Three entries of tenant A, promoted to main by hand the way
		// evictSmall would, then more foreign nodes than one window in small.
		for i := uint64(1); i <= 3; i++ {
			_ = s.Set(t.Context(), numKey(i), ownedEntry(1, 100))
		}
		sh.mu.Lock()
		for n := sh.small.tail; n != nil; {
			prev := n.prev
			sh.small.remove(n)
			n.queue = queueMain
			sh.main.push(n)
			n = prev
		}
		sh.mu.Unlock()
		for i := uint64(100); i < 100+ownerScan+6; i++ {
			_ = s.Set(t.Context(), numKey(i), ownedEntry(0, 100))
		}
		if err := s.Set(t.Context(), numKey(500), ownedEntry(1, 100)); err != nil || !present(t.Context(), s, numKey(500)) {
			t.Fatal("tenant A could not turn over its quota held in main")
		}
		if got := ownerBytes(s, 1); got != 3*size {
			t.Fatalf("owner account = %d, want %d", got, 3*size)
		}
		if sh.main.len != 2 || present(t.Context(), s, numKey(1)) {
			t.Fatalf("main holds %d nodes, want 2 with A's oldest evicted", sh.main.len)
		}
	})

	t.Run("replacing a key does not double count and delete frees the quota", func(t *testing.T) {
		s := newStore(t, cfg)
		for range 5 {
			_ = s.Set(t.Context(), numKey(1), ownedEntry(1, 100))
		}
		if got := ownerBytes(s, 1); got != size {
			t.Fatalf("owner account = %d, want %d", got, size)
		}
		_ = s.Delete(t.Context(), numKey(1))
		if got := len(s.shards[0].owners); got != 0 {
			t.Fatalf("owner map holds %d tenants after delete, want 0", got)
		}
	})

	t.Run("entries without an owner are not limited", func(t *testing.T) {
		s := newStore(t, cfg)
		for i := uint64(1); i <= 30; i++ {
			_ = s.Set(context.Background(), numKey(i), entry(100))
		}
		if !present(t.Context(), s, numKey(1)) || len(s.shards[0].owners) != 0 {
			t.Fatal("ownerless entries were counted or evicted")
		}
	})

	t.Run("quota off by default keeps no account", func(t *testing.T) {
		s := newStore(t, Config{MaxBytes: 1 << 20, Shards: 1})
		_ = s.Set(context.Background(), numKey(1), ownedEntry(1, 100))
		if len(s.shards[0].owners) != 0 {
			t.Fatal("owner map filled with the quota off")
		}
	})

	t.Run("a negative quota is rejected", func(t *testing.T) {
		if _, err := New(Config{MaxBytesPerOwner: -1}); err == nil {
			t.Fatal("New accepted a negative MaxBytesPerOwner")
		}
	})

	t.Run("a quota of one entry keeps only the newest", func(t *testing.T) {
		s := newStore(t, Config{MaxBytes: 1 << 20, Shards: 1, MaxBytesPerOwner: size})
		short := ownedEntry(1, 100)
		short.Expires = time.Now().Add(time.Minute)
		_ = s.Set(t.Context(), numKey(1), short)
		_ = s.Set(t.Context(), numKey(2), ownedEntry(1, 100))
		if present(t.Context(), s, numKey(1)) || !present(t.Context(), s, numKey(2)) {
			t.Fatal("quota turnover should drop the older own entry")
		}
	})
}

// FR-FAIR-2, FR-SNP-3, T-32: the snapshot loader admits records through the
// same quota, declining what no longer fits instead of evicting.
func TestSnapshotLoadHonorsOwnerQuota(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weir.snap")
	f := newSnapFile()
	mk := func(owner byte) *store.Entry {
		e := liveEntry("0123456789")
		e.Owner = store.Tag{owner}
		return e
	}
	size := mk(1).Size()
	for i := uint64(1); i <= 6; i++ {
		f.entry(t, numKey(i), mk(1))
	}
	f.entry(t, numKey(100), mk(2))
	f.write(t, path)

	s := loadFrom(t, path, Config{MaxBytes: 1 << 20, Shards: 1, MaxBytesPerOwner: 3 * size})
	t.Cleanup(func() { _ = s.Close() })
	if got := ownerBytes(s, 1); got != 3*size {
		t.Errorf("owner 1 loaded %d bytes, want %d", got, 3*size)
	}
	if !present(t.Context(), s, numKey(1)) || present(t.Context(), s, numKey(6)) {
		t.Error("loader should keep file order and decline the records past the quota")
	}
	if !present(t.Context(), s, numKey(100)) {
		t.Error("another owner's record was dropped by owner 1's quota")
	}
}

// FR-FAIR-2, T-32: edge cases of the quota account.
func TestOwnerQuotaAccounting(t *testing.T) {
	size := ownedEntry(1, 100).Size()

	t.Run("a replacement that changes owner moves the account", func(t *testing.T) {
		s := newStore(t, Config{MaxBytes: 1 << 20, Shards: 1, MaxBytesPerOwner: 4 * size})
		_ = s.Set(t.Context(), numKey(1), ownedEntry(1, 100))
		_ = s.Set(t.Context(), numKey(1), ownedEntry(2, 100))
		if ownerBytes(s, 1) != 0 || ownerBytes(s, 2) != size {
			t.Fatalf("accounts = %d and %d, want 0 and %d", ownerBytes(s, 1), ownerBytes(s, 2), size)
		}
	})

	t.Run("a replacement into a full owner evicts that owner's other entry", func(t *testing.T) {
		s := newStore(t, Config{MaxBytes: 1 << 20, Shards: 1, MaxBytesPerOwner: size})
		_ = s.Set(t.Context(), numKey(1), ownedEntry(1, 100))
		_ = s.Set(t.Context(), numKey(2), ownedEntry(2, 100))
		_ = s.Set(t.Context(), numKey(2), ownedEntry(1, 100)) // key 2 changes hands into a full owner
		if present(t.Context(), s, numKey(1)) || !present(t.Context(), s, numKey(2)) {
			t.Fatal("owner 1 should keep only the entry it just received")
		}
		if ownerBytes(s, 1) != size || ownerBytes(s, 2) != 0 {
			t.Fatalf("accounts = %d and %d", ownerBytes(s, 1), ownerBytes(s, 2))
		}
	})

	t.Run("one entry as large as the quota evicts every older own entry", func(t *testing.T) {
		small := ownedEntry(1, 100).Size()
		big := ownedEntry(1, 100+int(2*small)).Size() // about three small entries
		s := newStore(t, Config{MaxBytes: 1 << 20, Shards: 1, MaxBytesPerOwner: big})
		for i := uint64(1); i <= 3; i++ {
			_ = s.Set(t.Context(), numKey(i), ownedEntry(1, 100))
		}
		_ = s.Set(t.Context(), numKey(9), ownedEntry(1, 100+int(2*small)))
		if !present(t.Context(), s, numKey(9)) || present(t.Context(), s, numKey(1)) || ownerBytes(s, 1) > big {
			t.Fatalf("size == quota: stored %v, account %d, quota %d", present(t.Context(), s, numKey(9)), ownerBytes(s, 1), big)
		}
	})

	t.Run("an expired own entry is a victim and counts as expired", func(t *testing.T) {
		var expired, small, main int
		s := newStore(t, Config{MaxBytes: 1 << 20, Shards: 1, MaxBytesPerOwner: size,
			OnEvict: func(q string, n int) {
				switch q {
				case "expired":
					expired += n
				case "small":
					small += n
				case "main":
					main += n
				}
			}})
		_ = s.Set(t.Context(), numKey(1), ownedEntry(1, 100))
		sh := &s.shards[0]
		sh.mu.Lock()
		sh.m[numKey(1)].expires = time.Now().Add(-time.Second)
		sh.mu.Unlock()
		_ = s.Set(t.Context(), numKey(2), ownedEntry(1, 100))
		if expired != 1 || small != 0 || main != 0 {
			t.Fatalf("OnEvict counts expired %d small %d main %d, want 1 0 0", expired, small, main)
		}
		_ = s.Set(t.Context(), numKey(3), ownedEntry(1, 100))
		if small != 1 {
			t.Fatalf("a live small victim counted %d, want 1", small)
		}
	})
}

// FR-PRG-8: Scrub deletes response records by exact tag, in every shard,
// and leaves other records and kinds alone. 05 S-2: a closed store and a
// cancelled context are ErrUnavailable.
func TestScrub(t *testing.T) {
	a, b := store.Tag{1}, store.Tag{2}
	tagged := func(kind store.Kind, tags ...store.Tag) *store.Entry {
		e := entry(10)
		e.Kind, e.Tags = kind, tags
		return e
	}
	s := newStore(t, Config{MaxBytes: 1 << 20, Shards: 4})
	for i := uint64(1); i <= 20; i++ {
		_ = s.Set(t.Context(), numKey(i), tagged(store.KindResponse, store.TagGlobal(), a))
	}
	_ = s.Set(t.Context(), numKey(100), tagged(store.KindResponse, store.TagGlobal(), b))
	_ = s.Set(t.Context(), numKey(101), tagged(store.KindVarySpec, a))
	_ = s.Set(t.Context(), numKey(102), tagged(store.KindResponse))

	if n, err := s.Scrub(t.Context(), nil); n != 0 || err != nil {
		t.Fatalf("Scrub(nil) = %d, %v; want 0, nil", n, err)
	}
	if n, err := s.Scrub(t.Context(), []store.Tag{a}); n != 20 || err != nil {
		t.Fatalf("Scrub(a) = %d, %v; want 20, nil", n, err)
	}
	for i := uint64(1); i <= 20; i++ {
		if present(t.Context(), s, numKey(i)) {
			t.Fatalf("record %d with tag a survived", i)
		}
	}
	for _, k := range []uint64{100, 101, 102} {
		if !present(t.Context(), s, numKey(k)) {
			t.Fatalf("record %d was scrubbed but does not match", k)
		}
	}
	if n, _ := s.Scrub(t.Context(), []store.Tag{store.TagGlobal()}); n != 1 {
		t.Fatalf("Scrub(global) = %d, want the one remaining response with it", n)
	}
	if got := s.Bytes(); got != tagged(store.KindVarySpec, a).Size()+tagged(store.KindResponse).Size() {
		t.Fatalf("Bytes = %d after scrub: byte account not released", got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Scrub(ctx, []store.Tag{a}); !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("Scrub with cancelled ctx = %v, want ErrUnavailable", err)
	}
	_ = s.Close()
	if _, err := s.Scrub(t.Context(), []store.Tag{a}); !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("Scrub after Close = %v, want ErrUnavailable", err)
	}
}
