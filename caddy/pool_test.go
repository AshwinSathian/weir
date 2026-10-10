package weircaddy

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"

	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

func newCtx(t *testing.T) caddy.Context {
	t.Helper()
	ctx, cancel := caddy.NewContext(caddy.Context{Context: context.Background()})
	t.Cleanup(cancel)
	return ctx
}

// loadIn provisions raw inside ctx, so two calls with one ctx are one
// config load and two ctxs are two (08 §3).
func loadIn(t *testing.T, ctx caddy.Context, raw string) (*Handler, error) {
	t.Helper()
	v, err := ctx.LoadModuleByID("http.handlers.weir", []byte(raw))
	if err != nil {
		return nil, err
	}
	h := v.(*Handler)
	t.Cleanup(func() { _ = h.Cleanup() })
	return h, nil
}

func mustLoad(t *testing.T, ctx caddy.Context, raw string) *Handler {
	t.Helper()
	h, err := loadIn(t, ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func refs(t *testing.T, h *Handler) int {
	t.Helper()
	n, _ := stores.pool.References(h.pool.spec)
	return n
}

func globalEpoch(t *testing.T, st store.Store) (store.Epoch, bool) {
	t.Helper()
	ep, ok, err := st.NewestEpoch(context.Background(), []store.Tag{store.TagGlobal()}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return ep, ok
}

// FR-SNP-1, 08 §3: Cleanup deletes exactly once; an extra Delete would take another
// instance's reference and close the store under a live engine.
func TestCleanupDeletesOnce(t *testing.T) {
	ctx1, ctx2 := newCtx(t), newCtx(t)
	h1 := mustLoad(t, ctx1, `{"name":"once","max_bytes":"200MiB"}`)
	h2 := mustLoad(t, ctx2, `{"name":"once","max_bytes":"200MiB"}`)
	if refs(t, h1) != 2 {
		t.Fatalf("refs = %d, want 2", refs(t, h1))
	}
	spec := h1.pool.spec
	st := h1.pool.store
	for range 3 {
		if err := h1.Cleanup(); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := stores.pool.References(spec); n != 1 {
		t.Fatalf("refs after repeated Cleanup = %d, want 1", n)
	}
	// A live store answers ErrNotFound, never a closed-store error.
	if _, err := st.Get(context.Background(), store.Key{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("store closed under a live handler: %v", err)
	}
	if err := h2.Cleanup(); err != nil {
		t.Fatal(err)
	}
}

// 08 §3: the store, not the engine, survives a reload.
func TestPoolSharesStoreAcrossReload(t *testing.T) {
	h1 := mustLoad(t, newCtx(t), `{"name":"share","max_bytes":"200MiB"}`)
	h2 := mustLoad(t, newCtx(t), `{"name":"share","max_bytes":"200MiB","limiter":{"max_concurrent":9}}`)
	if h1.pool != h2.pool || h1.pool.store != h2.pool.store {
		t.Fatal("reload built a second store")
	}
	if h1.engine == h2.engine {
		t.Fatal("reload reused the engine")
	}
}

// 08 §3: one load may not disagree about a name; across loads it may.
func TestPoolSettingsMismatchFailsProvision(t *testing.T) {
	for name, second := range map[string]string{
		"max_bytes":      `{"name":"mis","max_bytes":"400MiB"}`,
		"snapshot_dir":   `{"name":"mis","max_bytes":"200MiB","snapshot_dir":"` + t.TempDir() + `"}`,
		"multi_host":     `{"name":"mis","max_bytes":"200MiB","multi_host":true}`,
		"key generation": `{"name":"mis","max_bytes":"200MiB","forward":{"allow":["x-a"]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := newCtx(t)
			mustLoad(t, ctx, `{"name":"mis","max_bytes":"200MiB"}`)
			if _, err := loadIn(t, ctx, second); err == nil {
				t.Fatal("conflicting site in one load accepted")
			}
		})
	}

	t.Run("identical sites in one load share a store", func(t *testing.T) {
		ctx := newCtx(t)
		a := mustLoad(t, ctx, `{"name":"same","max_bytes":"200MiB"}`)
		b := mustLoad(t, ctx, `{"name":"same","max_bytes":"200MiB","limiter":{"max_concurrent":9}}`)
		if a.pool != b.pool {
			t.Fatal("not shared")
		}
	})

	t.Run("a failed provision leaves no claim behind", func(t *testing.T) {
		ctx := newCtx(t)
		mustLoad(t, ctx, `{"name":"claim","max_bytes":"200MiB"}`)
		if _, err := loadIn(t, ctx, `{"name":"claim","max_bytes":"201MiB"}`); err == nil {
			t.Fatal("conflict accepted")
		}
		if n := stores.loadClaims(ctx.GetMetricsRegistry()); n != 1 {
			t.Fatalf("claims = %d, want 1", n)
		}
	})
}

// 08 §3: a resize reload has two settings for one name alive at once.
func TestPoolResizeReloadAllowed(t *testing.T) {
	old := mustLoad(t, newCtx(t), `{"name":"resize","max_bytes":"200MiB"}`)
	neu := mustLoad(t, newCtx(t), `{"name":"resize","max_bytes":"400MiB"}`)
	if old.pool == neu.pool {
		t.Fatal("resize shared the store")
	}
	if !old.pool.superseded.Load() || neu.pool.superseded.Load() {
		t.Fatal("old store not marked superseded, or new one is")
	}
}

// FR-SNP-1: when a reload rolls back and the newer store goes away, the older
// store is the writer again.
func TestRolledBackReloadRestoresSnapshotWriter(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "back.weir")
	old := mustLoad(t, newCtx(t), `{"name":"back","max_bytes":"200MiB","snapshot_dir":"`+dir+`"}`)
	neu := mustLoad(t, newCtx(t), `{"name":"back","max_bytes":"400MiB","snapshot_dir":"`+dir+`"}`)
	if err := neu.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if old.pool.superseded.Load() {
		t.Fatal("old store still flagged superseded")
	}
	if err := old.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snap); err != nil {
		t.Fatalf("no snapshot after rollback: %v", err)
	}
}

// FR-SNP-1, 08 §3: the pooled value closes with the last release.
func TestPoolDestructsOnLastRelease(t *testing.T) {
	h1 := mustLoad(t, newCtx(t), `{"name":"last","max_bytes":"200MiB"}`)
	h2 := mustLoad(t, newCtx(t), `{"name":"last","max_bytes":"200MiB"}`)
	spec, st := h1.pool.spec, h1.pool.store
	_ = h1.Cleanup()
	if _, err := st.Get(context.Background(), store.Key{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("store closed while one handler remains: %v", err)
	}
	_ = h2.Cleanup()
	if n, ok := stores.pool.References(spec); ok {
		t.Fatalf("pool still holds the store (%d refs)", n)
	}
	if _, err := st.Get(context.Background(), store.Key{}); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Fatalf("store open after the last release: %v", err)
	}
}

// FR-SNP-1, 08 §3: only the live store writes the snapshot at shutdown.
func TestSupersededStoreSkipsSnapshot(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "snap.weir")
	old := mustLoad(t, newCtx(t), `{"name":"snap","max_bytes":"200MiB","snapshot_dir":"`+dir+`"}`)
	neu := mustLoad(t, newCtx(t), `{"name":"snap","max_bytes":"400MiB","snapshot_dir":"`+dir+`"}`)
	if err := old.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snap); err == nil {
		t.Fatal("superseded store wrote a snapshot")
	}
	if err := neu.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snap); err != nil {
		t.Fatalf("live store wrote no snapshot: %v", err)
	}
}

// 08 §3, R-3: a changed key-generation hash is a hard epoch; an unchanged
// one keeps the cache.
func TestKeyGenHashChangeWritesHardEpoch(t *testing.T) {
	t.Run("unchanged hash writes nothing", func(t *testing.T) {
		a := mustLoad(t, newCtx(t), `{"name":"kg-same","max_bytes":"200MiB","key":{"query_sort":true}}`)
		mustLoad(t, newCtx(t), `{"name":"kg-same","max_bytes":"200MiB","key":{"query_sort":false},"multi_host":false}`)
		if _, ok := globalEpoch(t, a.pool.store); ok {
			t.Fatal("epoch written without a key-generation change")
		}
	})
	t.Run("changed hash writes one hard epoch", func(t *testing.T) {
		a := mustLoad(t, newCtx(t), `{"name":"kg-chg","max_bytes":"200MiB"}`)
		if _, ok := globalEpoch(t, a.pool.store); ok {
			t.Fatal("fresh store has an epoch")
		}
		mustLoad(t, newCtx(t), `{"name":"kg-chg","max_bytes":"200MiB","forward":{"allow":["x-a"]}}`)
		ep, ok := globalEpoch(t, a.pool.store)
		if !ok || ep.Mode != store.EpochHard {
			t.Fatalf("epoch = %+v, %v; want hard", ep, ok)
		}
	})
}

// FR-FAIR-3: the caps are on at 25% for a multi-host site and off otherwise.
func TestMultiHostEnablesFairnessCaps(t *testing.T) {
	multi := &Handler{Name: "m", MultiHost: true}
	single := &Handler{Name: "s"}
	if got := multi.weirConfig().Limiter.MaxPerHost; got != 16 {
		t.Fatalf("MaxPerHost = %d, want 16 (25%% of the default 64)", got)
	}
	multi.Limiter.MaxConcurrent = 128
	if got := multi.weirConfig().Limiter.MaxPerHost; got != 32 {
		t.Fatalf("MaxPerHost = %d, want 32", got)
	}
	multi.Limiter.MaxConcurrent = 1
	if got := multi.weirConfig().Limiter.MaxPerHost; got != 1 {
		t.Fatalf("MaxPerHost = %d, want at least 1", got)
	}
	if single.weirConfig().Limiter.MaxPerHost != 0 {
		t.Fatal("single-host site has a per-host cap")
	}

	// 256 MiB default, 16 shards, a quarter of a shard.
	if got := (&Handler{MultiHost: true}).memoryConfig().MaxBytesPerOwner; got != 4<<20 {
		t.Fatalf("MaxBytesPerOwner = %d, want 4 MiB", got)
	}
	if got := (&Handler{MultiHost: true, MaxBytes: 64 << 20}).memoryConfig().MaxBytesPerOwner; got != 1<<20 {
		t.Fatalf("MaxBytesPerOwner = %d, want 1 MiB", got)
	}
	if single.memoryConfig().MaxBytesPerOwner != 0 {
		t.Fatal("single-host store has an owner cap")
	}
}

// 08 §3: going from one host to two starts one new store, so the cap is never
// silently missing.
func TestSingleToMultiHostStartsNewStore(t *testing.T) {
	one := mustLoad(t, newCtx(t), `{"name":"grow","max_bytes":"200MiB"}`)
	two := mustLoad(t, newCtx(t), `{"name":"grow","max_bytes":"200MiB","multi_host":true}`)
	three := mustLoad(t, newCtx(t), `{"name":"grow","max_bytes":"200MiB","multi_host":true}`)
	if one.pool == two.pool {
		t.Fatal("multi_host reused the uncapped store")
	}
	if two.pool != three.pool {
		t.Fatal("a further reload with multi_host built another store")
	}
}

// snapshot_dir is parsed literally: braces would be placeholders Caddy does
// not expand here (08 §2).
func TestSnapshotDirValidation(t *testing.T) {
	for _, dir := range []string{"/var/{env.X}", "a}b", "a\x00b"} {
		if err := (&Handler{Name: "a", SnapshotDir: dir}).Validate(); err == nil {
			t.Errorf("snapshot_dir %q accepted", dir)
		}
	}
	if err := (&Handler{Name: "a", SnapshotDir: "/var/lib/weir"}).Validate(); err != nil {
		t.Error(err)
	}
}

// R-3: the hash is recorded only after the purge, so a Provision that failed
// after seeing the change does not let a retry of the same config skip it.
func TestKeyGenHashRetryAfterFailedProvisionStillPurges(t *testing.T) {
	a := mustLoad(t, newCtx(t), `{"name":"kg-retry","max_bytes":"200MiB"}`)
	if _, err := loadIn(t, newCtx(t), `{"name":"kg-retry","max_bytes":"200MiB","forward":{"allow":["x-a"]},"limiter":{"max_concurrent":-1}}`); err == nil {
		t.Fatal("invalid engine config accepted")
	}
	if _, ok := globalEpoch(t, a.pool.store); ok {
		t.Fatal("epoch written by a Provision that failed")
	}
	mustLoad(t, newCtx(t), `{"name":"kg-retry","max_bytes":"200MiB","forward":{"allow":["x-a"]}}`)
	if ep, ok := globalEpoch(t, a.pool.store); !ok || ep.Mode != store.EpochHard {
		t.Fatalf("retry wrote no hard epoch: %+v, %v", ep, ok)
	}
}

// R-3: a snapshot written under another hash, or with none recorded, is
// deleted before the store can load it; a matching one is kept.
func TestSnapshotKeyGenReconcile(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "r.weir")
	x, y := sha256.Sum256([]byte("x")), sha256.Sum256([]byte("y"))
	write := func() {
		if err := os.WriteFile(snap, []byte("snapshot"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if dropped, err := reconcileKeyGen(dir, "r", x); err != nil || !dropped {
		t.Fatalf("no record: dropped=%v err=%v", dropped, err)
	}
	if _, err := os.Stat(snap); err == nil {
		t.Fatal("snapshot with no hash record survived")
	}
	write()
	if dropped, err := reconcileKeyGen(dir, "r", x); err != nil || dropped {
		t.Fatalf("same hash: dropped=%v err=%v", dropped, err)
	}
	if _, err := os.Stat(snap); err != nil {
		t.Fatal("snapshot with matching hash deleted")
	}
	if dropped, err := reconcileKeyGen(dir, "r", y); err != nil || !dropped {
		t.Fatalf("changed hash: dropped=%v err=%v", dropped, err)
	}
	if _, err := os.Stat(snap); err == nil {
		t.Fatal("snapshot under an old hash survived")
	}
	if fi, err := os.Stat(keyGenPath(dir, "r")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("record: %v %v", fi, err)
	}
}

// R-3, 08 §3: a reload that changes the hash and the store spec together
// builds a new store; the record beside the snapshot follows the live hash.
func TestKeyGenRecordFollowsLoad(t *testing.T) {
	dir := t.TempDir()
	h := mustLoad(t, newCtx(t), `{"name":"rec","max_bytes":"200MiB","snapshot_dir":"`+dir+`"}`)
	want := keyGenHash(h.weirConfig())
	got, err := os.ReadFile(keyGenPath(dir, "rec"))
	if err != nil || [sha256.Size]byte(got) != want {
		t.Fatalf("record after first load: %x %v", got, err)
	}
	h2 := mustLoad(t, newCtx(t), `{"name":"rec","max_bytes":"400MiB","snapshot_dir":"`+dir+`","forward":{"allow":["x-a"]}}`)
	want2 := keyGenHash(h2.weirConfig())
	got, err = os.ReadFile(keyGenPath(dir, "rec"))
	if err != nil || [sha256.Size]byte(got) != want2 {
		t.Fatalf("record after spec and hash change: %x %v", got, err)
	}
}

// Rejecting early beats a late engine error about a field the operator
// cannot set (the largest object is 10% of a shard).
func TestMaxBytesBelowMinimumRejectedEarly(t *testing.T) {
	if err := (&Handler{Name: "a", MaxBytes: minStoreBytes - 1}).Validate(); err == nil {
		t.Fatal("max_bytes below the minimum accepted")
	}
	for _, n := range []ByteSize{0, minStoreBytes} {
		if err := (&Handler{Name: "a", MaxBytes: n}).Validate(); err != nil {
			t.Errorf("max_bytes %d rejected: %v", n, err)
		}
	}
	if _, err := load(t, `{"name":"tiny","max_bytes":"8MiB"}`); err == nil {
		t.Fatal("8MiB provisioned")
	}
}

// T-33-style planted-snapshot guard: a directory others can write is refused,
// and a relative one is not accepted at all.
func TestSnapshotDirSafety(t *testing.T) {
	if err := (&Handler{Name: "a", SnapshotDir: "relative/dir"}).Validate(); err == nil {
		t.Fatal("relative snapshot_dir accepted")
	}
	open := t.TempDir()
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := load(t, `{"name":"open","max_bytes":"200MiB","snapshot_dir":"`+open+`"}`); err == nil {
		t.Fatal("world-writable snapshot_dir accepted")
	}
	// "/dir/" and "/dir" are one store identity.
	dir := t.TempDir()
	a := mustLoad(t, newCtx(t), `{"name":"slash","max_bytes":"200MiB","snapshot_dir":"`+dir+`"}`)
	b := mustLoad(t, newCtx(t), `{"name":"slash","max_bytes":"200MiB","snapshot_dir":"`+dir+`/"}`)
	if a.pool != b.pool {
		t.Fatal("trailing slash built a second store")
	}
}

// 08 §3: concurrent acquire and release leave no reference, claim or live
// entry behind. Run under -race.
func TestRegistryConcurrentAcquireRelease(t *testing.T) {
	r := newStoreRegistry()
	spec := storeSpec{name: "conc", maxBytes: 0}
	var kg [sha256.Size]byte
	build := func() (*memory.Store, error) { return memory.New(memory.Config{}) }
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			load := new(int)
			for range 25 {
				p, _, err := r.acquire(load, spec, kg, build)
				if err != nil {
					t.Error(err)
					return
				}
				if err := r.release(load, p); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if n, ok := r.pool.References(spec); ok {
		t.Fatalf("pool still holds %d references", n)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.live) != 0 || len(r.loads) != 0 {
		t.Fatalf("leaked state: live=%d loads=%d", len(r.live), len(r.loads))
	}
}
