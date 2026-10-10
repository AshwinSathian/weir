package weircaddy

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/caddyserver/caddy/v2"

	"github.com/AshwinSathian/weir/store/memory"
)

// Memory-store defaults the fairness cap is computed from (memory.Config:
// 0 means 256 MiB and 16 shards). P2-04 replaces the size with auto-sizing.
const (
	defaultStoreBytes = 256 << 20
	defaultShards     = 16
)

// stores is the process-wide pool of memory stores (08 §3). The package-level
// state is the point: it is what survives a config reload.
var stores = newStoreRegistry()

// storeSpec is the pool key (08 §3): everything fixed when a store is built.
// The shard count is not a key yet: no setting changes it, so every store
// uses the memory default (16). ownerCap is FR-FAIR-3 "more than one host or on-demand TLS". snapshotDir is part of the
// key so a changed directory builds a store that loads from it.
type storeSpec struct {
	name        string
	maxBytes    int64
	ownerCap    bool
	snapshotDir string
}

// pooledStore is the value in the UsagePool. It implements caddy.Destructor.
type pooledStore struct {
	spec  storeSpec
	store *memory.Store
	reg   *storeRegistry

	// superseded is set when a newer store with the same name exists, so
	// the final snapshot writer is the live store (08 §3).
	superseded atomic.Bool

	mu     sync.Mutex
	keyGen [sha256.Size]byte // hash of the newest engine that used the store
}

// swapKeyGen records the hash of the engine now using the store and reports
// whether it differs from the previous one.
func (p *pooledStore) swapKeyGen(h [sha256.Size]byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	changed := p.keyGen != h
	p.keyGen = h
	return changed
}

// Destruct implements caddy.Destructor. Caddy calls it outside the pool lock
// with no context, so the snapshot is bounded by closeTimeout (08 §3).
func (p *pooledStore) Destruct() error {
	p.reg.forget(p)
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	if p.superseded.Load() {
		// A cancelled context makes CloseContext discard the snapshot; the
		// store is closed either way (FR-SNP-1).
		cancel()
		if err := p.store.CloseContext(ctx); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	}
	return p.store.CloseContext(ctx)
}

// storeRegistry wraps the UsagePool with the two pieces of state the pool
// itself lacks. Both maps are bounded by the number of configured sites and
// loads, never by request input (NFR-3).
type storeRegistry struct {
	pool *caddy.UsagePool

	mu     sync.Mutex
	latest map[string]*pooledStore // newest store per name
	loads  map[any]map[string]*claim
}

// claim records what one config load has said about a name, so a second site
// in the same load that disagrees fails (08 §3).
type claim struct {
	spec   storeSpec
	keyGen [sha256.Size]byte
	refs   int
}

func newStoreRegistry() *storeRegistry {
	return &storeRegistry{
		pool:   caddy.NewUsagePool(),
		latest: map[string]*pooledStore{},
		loads:  map[any]map[string]*claim{},
	}
}

// acquire takes a reference to the store for spec, building it with build
// when no live handler has it. keyChanged is true when a store that other
// handlers already used was last used with a different key-generation hash.
func (r *storeRegistry) acquire(load any, spec storeSpec, keyGen [sha256.Size]byte,
	build func() (*memory.Store, error)) (p *pooledStore, keyChanged bool, err error) {
	if err := r.claimName(load, spec, keyGen); err != nil {
		return nil, false, err
	}
	v, loaded, err := r.pool.LoadOrNew(spec, func() (caddy.Destructor, error) {
		st, err := build()
		if err != nil {
			return nil, err
		}
		return &pooledStore{spec: spec, store: st, reg: r, keyGen: keyGen}, nil
	})
	if err != nil {
		r.unclaimName(load, spec.name)
		return nil, false, err
	}
	p = v.(*pooledStore)

	r.mu.Lock()
	if prev := r.latest[spec.name]; prev != nil && prev != p {
		prev.superseded.Store(true)
	}
	p.superseded.Store(false)
	r.latest[spec.name] = p
	r.mu.Unlock()

	if loaded {
		keyChanged = p.swapKeyGen(keyGen)
	}
	return p, keyChanged, nil
}

// release gives back one reference. Callers call it exactly once per
// successful acquire: an extra Delete takes another instance's reference.
func (r *storeRegistry) release(load any, p *pooledStore) error {
	r.unclaimName(load, p.spec.name)
	_, err := r.pool.Delete(p.spec)
	return err
}

func (r *storeRegistry) forget(p *pooledStore) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.latest[p.spec.name] == p {
		delete(r.latest, p.spec.name)
	}
}

func (r *storeRegistry) claimName(load any, spec storeSpec, keyGen [sha256.Size]byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := r.loads[load]
	if names == nil {
		names = map[string]*claim{}
		r.loads[load] = names
	}
	if c := names[spec.name]; c != nil {
		if c.spec != spec || c.keyGen != keyGen {
			return fmt.Errorf("weir: name %q is used by two sites in this config with different store or key-generation settings", spec.name)
		}
		c.refs++
		return nil
	}
	names[spec.name] = &claim{spec: spec, keyGen: keyGen, refs: 1}
	return nil
}

func (r *storeRegistry) unclaimName(load any, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := r.loads[load]
	c := names[name]
	if c == nil {
		return
	}
	if c.refs--; c.refs == 0 {
		delete(names, name)
	}
	if len(names) == 0 {
		delete(r.loads, load)
	}
}

// loadClaims reports how many site claims a load holds (tests).
func (r *storeRegistry) loadClaims(load any) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.loads[load] {
		n += c.refs
	}
	return n
}

// storeSpec derives the pool key from the handler.
func (h *Handler) storeSpec() storeSpec {
	return storeSpec{
		name: h.Name, maxBytes: int64(h.MaxBytes), ownerCap: h.MultiHost, snapshotDir: h.SnapshotDir,
	}
}

// memoryConfig maps the handler onto the store settings. A multi-host site
// gets MaxBytesPerOwner at 25% of a shard (FR-FAIR-3).
func (h *Handler) memoryConfig() memory.Config {
	cfg := memory.Config{
		MaxBytes:        int64(h.MaxBytes),
		SnapshotTimeout: closeTimeout,
	}
	if h.SnapshotDir != "" {
		cfg.SnapshotPath = filepath.Join(h.SnapshotDir, h.Name+".weir")
	}
	if h.MultiHost {
		size := cfg.MaxBytes
		if size == 0 {
			size = defaultStoreBytes
		}
		cfg.MaxBytesPerOwner = max(1, size/defaultShards/4)
	}
	return cfg
}

// buildStore creates the memory store for h. The snapshot directory is made
// here, not at Validate, so caddy validate has no side effects on disk.
func (h *Handler) buildStore() (*memory.Store, error) {
	if h.SnapshotDir != "" {
		if err := os.MkdirAll(h.SnapshotDir, 0o700); err != nil {
			return nil, fmt.Errorf("weir: snapshot_dir: %w", err)
		}
	}
	st, err := memory.New(h.memoryConfig())
	if err != nil {
		return nil, fmt.Errorf("weir: %w", err)
	}
	return st, nil
}

// validateSnapshotDir rejects what Caddy would not expand here: braces are
// placeholders (08 §2), and NUL cannot be in a path.
func validateSnapshotDir(dir string) error {
	for i := range len(dir) {
		switch dir[i] {
		case '{', '}':
			return errors.New("weir: snapshot_dir must not contain braces (placeholders are not expanded)")
		case 0:
			return errors.New("weir: snapshot_dir must not contain a NUL byte")
		}
	}
	return nil
}
