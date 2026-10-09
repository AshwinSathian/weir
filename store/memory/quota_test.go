package memory

import (
	"context"
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

func present(s *Store, k store.Key) bool {
	_, err := s.Get(context.Background(), k)
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
		ctx := context.Background()
		for i := uint64(1); i <= 5; i++ {
			_ = s.Set(ctx, numKey(i), ownedEntry(2, 100)) // tenant B
		}
		for i := uint64(100); i < 300; i++ {
			_ = s.Set(ctx, numKey(i), ownedEntry(1, 100)) // tenant A floods
			_, _ = s.Get(ctx, numKey(i))
			_, _ = s.Get(ctx, numKey(i)) // hot enough to reach main
		}
		for i := uint64(1); i <= 5; i++ {
			if !present(s, numKey(i)) {
				t.Fatalf("tenant B entry %d was evicted by tenant A", i)
			}
		}
		if got := ownerBytes(s, 1); got > cfg.MaxBytesPerOwner {
			t.Fatalf("tenant A holds %d bytes, quota %d", got, cfg.MaxBytesPerOwner)
		}
		if !present(s, numKey(299)) || present(s, numKey(100)) {
			t.Fatal("tenant A should keep its newest page and lose its oldest")
		}
	})

	t.Run("an entry larger than the quota is declined and drops the record it replaces", func(t *testing.T) {
		s := newStore(t, Config{MaxBytes: 1 << 20, Shards: 1, MaxBytesPerOwner: 2 * size})
		ctx := context.Background()
		_ = s.Set(ctx, numKey(1), ownedEntry(1, 100))
		_ = s.Set(ctx, numKey(1), ownedEntry(1, 1000))
		if present(s, numKey(1)) {
			t.Fatal("over-quota replacement left the older record")
		}
		if got := ownerBytes(s, 1); got != 0 {
			t.Fatalf("owner account = %d after the drop, want 0", got)
		}
	})

	t.Run("the victim scan is bounded, so a set with no own entries in reach is declined", func(t *testing.T) {
		s := newStore(t, Config{MaxBytes: 1 << 20, Shards: 1, MaxBytesPerOwner: 2 * size})
		ctx := context.Background()
		for i := uint64(1); i <= ownerScan+6; i++ {
			_ = s.Set(ctx, numKey(i), ownedEntry(0, 100)) // oldest: unlimited entries of no owner
		}
		_ = s.Set(ctx, numKey(1000), ownedEntry(1, 100))
		_ = s.Set(ctx, numKey(1001), ownedEntry(1, 100))
		_ = s.Set(ctx, numKey(1002), ownedEntry(1, 100)) // own entries sit beyond the scan
		if present(s, numKey(1002)) {
			t.Fatal("set stored although no own victim was in reach")
		}
		if !present(s, numKey(1000)) || !present(s, numKey(1001)) {
			t.Fatal("a declined set evicted entries")
		}
		for i := uint64(1); i <= ownerScan+6; i++ {
			if !present(s, numKey(i)) {
				t.Fatalf("other tenant's entry %d evicted", i)
			}
		}
	})

	t.Run("replacing a key does not double count and delete frees the quota", func(t *testing.T) {
		s := newStore(t, cfg)
		ctx := context.Background()
		for range 5 {
			_ = s.Set(ctx, numKey(1), ownedEntry(1, 100))
		}
		if got := ownerBytes(s, 1); got != size {
			t.Fatalf("owner account = %d, want %d", got, size)
		}
		_ = s.Delete(ctx, numKey(1))
		if got := len(s.shards[0].owners); got != 0 {
			t.Fatalf("owner map holds %d tenants after delete, want 0", got)
		}
	})

	t.Run("entries without an owner are not limited", func(t *testing.T) {
		s := newStore(t, cfg)
		for i := uint64(1); i <= 30; i++ {
			_ = s.Set(context.Background(), numKey(i), entry(100))
		}
		if !present(s, numKey(1)) || len(s.shards[0].owners) != 0 {
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
		ctx := context.Background()
		short := ownedEntry(1, 100)
		short.Expires = time.Now().Add(time.Minute)
		_ = s.Set(ctx, numKey(1), short)
		_ = s.Set(ctx, numKey(2), ownedEntry(1, 100))
		if present(s, numKey(1)) || !present(s, numKey(2)) {
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
	if !present(s, numKey(1)) || present(s, numKey(6)) {
		t.Error("loader should keep file order and decline the records past the quota")
	}
	if !present(s, numKey(100)) {
		t.Error("another owner's record was dropped by owner 1's quota")
	}
}
