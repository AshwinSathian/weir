package weircaddy

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/caddyserver/caddy/v2"

	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

// Memory-store defaults (memory.Config: 0 means 256 MiB and 16 shards).
// defaultStoreBytes is also FR-MEM-1's size when no memory limit is set.
const (
	// minStoreBytes is the smallest max_bytes whose largest object (10% of a
	// shard, 05 §5.1) fits the default 1 MiB Storable.MaxObjectBytes, which
	// the adapter has no key for. Rejected early instead of clamped, so the
	// operator sees why (16 shards x 10 x 1 MiB).
	minStoreBytes     = defaultShards * 10 << 20
	defaultStoreBytes = 256 << 20
	defaultShards     = 16
)

// stores is the process-wide pool of memory stores (08 §3). The package-level
// state is the point: it is what survives a config reload.
var stores = newStoreRegistry()

// storeSpec is the pool key (08 §3): everything fixed when a store is built.
// valkey is the digest of the store block (zero for the memory store), so any
// changed store setting, the password included, builds a new store and the key
// never holds a secret.
// The shard count is not a key yet: no setting changes it, so every store
// uses the memory default (16). ownerCap is FR-FAIR-3, "more than one host or
// on-demand TLS". snapshotDir is part of the key so a changed directory
// builds a store that loads from it.
type storeSpec struct {
	name        string
	maxBytes    int64
	ownerCap    bool
	snapshotDir string
	valkey      [sha256.Size]byte
}

// pooledStore is the value in the UsagePool. It implements caddy.Destructor.
type pooledStore struct {
	spec  storeSpec
	store store.Store
	reg   *storeRegistry

	// superseded is set when a newer store with the same name exists, so
	// the final snapshot writer is the live store (08 §3).
	superseded atomic.Bool

	// size is the byte budget the store was built with (08 §7), fixed for
	// its life.
	size atomic.Int64

	// sink receives the store's eviction counts and is repointed at each
	// Provision's metric set (08 §8).
	sink *evictSink

	mu      sync.Mutex
	keyGen  [sha256.Size]byte // hash of the newest engine that used the store
	holders []holder          // engines that committed a hash, oldest first
}

// holder is one provisioned handler and the hash it committed.
type holder struct {
	who  *Handler
	hash [sha256.Size]byte
}

// keyGenChanged reports whether h differs from the hash of the engine that
// last used the store. It does not record h: that happens in commitKeyGen
// once the purge succeeded, so a failed Provision followed by a retry of the
// same config still purges (R-3).
func (p *pooledStore) keyGenChanged(h [sha256.Size]byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.keyGen != h
}

// commitKeyGen records h as the store's hash, held by who, after the engine
// that carries it is built and the hard epoch is written.
func (p *pooledStore) commitKeyGen(who *Handler, h [sha256.Size]byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.holders = append(p.holders, holder{who, h})
	p.keyGen = h
}

// dropHolder removes who. If who held the newest hash, the store goes back to
// the hash of the newest remaining holder: a reload that was rolled back must
// not leave its hash behind, or re-applying it would skip the purge while the
// older engine has been storing under the looser rules (R-3).
func (p *pooledStore) dropHolder(who *Handler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := slices.IndexFunc(p.holders, func(h holder) bool { return h.who == who })
	if i < 0 {
		return
	}
	last := i == len(p.holders)-1
	p.holders = slices.Delete(p.holders, i, i+1)
	if last && len(p.holders) > 0 {
		p.keyGen = p.holders[len(p.holders)-1].hash
	}
}

func (p *pooledStore) currentKeyGen() [sha256.Size]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.keyGen
}

// Destruct implements caddy.Destructor. Caddy calls it outside the pool lock
// with no context, so the snapshot is bounded by closeTimeout (08 §3).
func (p *pooledStore) Destruct() error {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	return p.destruct(ctx)
}

// destruct closes the store, writing the snapshot only when no newer store of
// the name exists. The hash record is rewritten first, so it describes the
// snapshot that follows even when a rolled-back reload changed it (R-3). If
// the record cannot be written the snapshot is skipped.
func (p *pooledStore) destruct(ctx context.Context) error {
	superseded := p.reg.retire(p)
	var recErr error
	if !superseded && p.spec.snapshotDir != "" {
		recErr = writeKeyGenFile(p.spec.snapshotDir, p.spec.name, p.currentKeyGen())
	}
	if superseded || recErr != nil {
		// A cancelled context makes CloseContext discard the snapshot; the
		// store is closed either way (FR-SNP-1).
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if err := p.closeStore(cctx); err != nil && !errors.Is(err, context.Canceled) {
			return errors.Join(recErr, err)
		}
		return recErr
	}
	return p.closeStore(ctx)
}

// closeStore closes the store under ctx when it takes one (the memory store
// writes its snapshot under it) and plainly otherwise (the Valkey store).
func (p *pooledStore) closeStore(ctx context.Context) error {
	if c, ok := p.store.(interface{ CloseContext(context.Context) error }); ok {
		return c.CloseContext(ctx)
	}
	return p.store.Close()
}

// storeRegistry wraps the UsagePool with the two pieces of state the pool
// itself lacks. Both maps are bounded by the number of configured sites and
// loads, never by request input (NFR-3).
type storeRegistry struct {
	pool *caddy.UsagePool

	mu sync.Mutex
	// live holds the stores alive per name, most recently acquired last. The
	// last one is the live writer; the others are superseded. When it goes
	// away (a reload rolled back) the previous one becomes the writer again.
	live  map[string][]*pooledStore
	loads map[any]map[string]*claim

	// warnedNoLimit makes the "no memory limit" warning one per process.
	warnedNoLimit atomic.Bool
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
		pool:  caddy.NewUsagePool(),
		live:  map[string][]*pooledStore{},
		loads: map[any]map[string]*claim{},
	}
}

// acquire takes a reference to the store for spec, building it with build
// when no live handler has it. keyChanged is true when a store that other
// handlers already used was last used with a different key-generation hash.
func (r *storeRegistry) acquire(load any, spec storeSpec, keyGen [sha256.Size]byte,
	build func(*evictSink) (store.Store, int64, error)) (p *pooledStore, keyChanged bool, err error) {
	if err := r.claimName(load, spec, keyGen); err != nil {
		return nil, false, err
	}
	v, loaded, err := r.pool.LoadOrNew(spec, func() (caddy.Destructor, error) {
		sink := new(evictSink)
		st, size, err := build(sink)
		if err != nil {
			return nil, err
		}
		ps := &pooledStore{spec: spec, store: st, reg: r, keyGen: keyGen, sink: sink}
		ps.size.Store(size)
		return ps, nil
	})
	if err != nil {
		r.unclaimName(load, spec.name)
		return nil, false, err
	}
	p = v.(*pooledStore)

	r.mu.Lock()
	r.live[spec.name] = append(slices.DeleteFunc(r.live[spec.name], func(q *pooledStore) bool { return q == p }), p)
	r.markWriter(spec.name)
	r.mu.Unlock()

	// A store built just now carries keyGen already (buildStore reconciled it
	// with the snapshot); only a reused store can be at a different hash.
	return p, loaded && p.keyGenChanged(keyGen), nil
}

// release gives back one reference. Callers call it exactly once per
// successful acquire: an extra Delete takes another instance's reference.
func (r *storeRegistry) release(load any, p *pooledStore) error {
	r.unclaimName(load, p.spec.name)
	_, err := r.pool.Delete(p.spec)
	return err
}

// retire removes p from the live set and reports, in the same critical
// section, whether a newer store had superseded it, so the writer decision
// and the removal cannot straddle another acquire.
//
// ponytail: a store of the same spec built after UsagePool.Delete removed
// the key but before this Destruct finishes would load the old snapshot.
// Caddy serializes config loads, and Cleanup of the old config runs inside
// the load that replaces it, so no caller reaches that window today. The
// upgrade is a per-name closing gate in acquire.
func (r *storeRegistry) retire(p *pooledStore) (superseded bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	superseded = p.superseded.Load()
	name := p.spec.name
	r.live[name] = slices.DeleteFunc(r.live[name], func(q *pooledStore) bool { return q == p })
	if len(r.live[name]) == 0 {
		delete(r.live, name)
		return superseded
	}
	r.markWriter(name)
	return superseded
}

// markWriter flags every live store of name except the newest as superseded.
// Caller holds r.mu.
func (r *storeRegistry) markWriter(name string) {
	l := r.live[name]
	for i, q := range l {
		q.superseded.Store(i != len(l)-1)
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
	if h.Store != nil {
		// Memory-only settings are refused by Validate, so the key is the name
		// and the digest (08 §3).
		return storeSpec{name: h.Name, valkey: h.Store.digest()}
	}
	return storeSpec{
		name: h.Name, maxBytes: int64(h.MaxBytes), ownerCap: h.MultiHost, snapshotDir: h.snapshotDir(),
	}
}

// memoryConfig maps the handler onto the store settings. A multi-host site
// gets MaxBytesPerOwner at 25% of a shard (FR-FAIR-3).
func (h *Handler) memoryConfig(size int64, sink *evictSink) memory.Config {
	cfg := memory.Config{
		MaxBytes:        size,
		OnEvict:         sink.emit,
		SnapshotTimeout: closeTimeout,
	}
	if dir := h.snapshotDir(); dir != "" {
		cfg.SnapshotPath = filepath.Join(dir, h.Name+".weir")
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
// here, not in Validate. Caddy's own validate command runs Provision, so it
// reaches this code and does touch the directory (runbook 7.8). A
// snapshot written under a different key-generation hash is deleted before
// the store can load it (R-3), and the current hash is recorded beside it.
func (h *Handler) buildStore(keyGen [sha256.Size]byte, size int64, sink *evictSink) (*memory.Store, error) {
	if dir := h.snapshotDir(); dir != "" {
		if err := prepareSnapshotDir(dir); err != nil {
			return nil, err
		}
		if _, err := reconcileKeyGen(dir, h.Name, keyGen); err != nil {
			return nil, err
		}
	}
	st, err := memory.New(h.memoryConfig(size, sink))
	if err != nil {
		return nil, fmt.Errorf("weir: %w", err)
	}
	return st, nil
}

// snapshotDir is the cleaned directory, so "/data" and "/data/" are one
// store identity.
func (h *Handler) snapshotDir() string {
	if h.SnapshotDir == "" {
		return ""
	}
	return filepath.Clean(h.SnapshotDir)
}

// prepareSnapshotDir creates dir (0700) and refuses one another user could
// write into: a planted snapshot would be decoded as trusted cache content.
func prepareSnapshotDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("weir: snapshot_dir: %w", err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("weir: snapshot_dir: %w", err)
	}
	if !fi.IsDir() {
		return errors.New("weir: snapshot_dir is not a directory")
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return errors.New("weir: snapshot_dir must not be writable by group or others")
	}
	if !ownedByUs(fi) {
		return errors.New("weir: snapshot_dir must be owned by the user Caddy runs as")
	}
	return nil
}

func keyGenPath(dir, name string) string { return filepath.Join(dir, name+".weir.keygen") }

// reconcileKeyGen compares the hash recorded beside the snapshot with want.
// On a mismatch, or when none is recorded, it deletes the snapshot and records
// want. It reports whether the snapshot was dropped.
func reconcileKeyGen(dir, name string, want [sha256.Size]byte) (dropped bool, err error) {
	got, err := os.ReadFile(keyGenPath(dir, name))
	switch {
	case err == nil && len(got) == sha256.Size && [sha256.Size]byte(got) == want:
		return false, nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return false, fmt.Errorf("weir: snapshot key-generation record: %w", err)
	}
	if err := os.Remove(filepath.Join(dir, name+".weir")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("weir: dropping stale snapshot: %w", err)
	}
	return true, writeKeyGenFile(dir, name, want)
}

// writeKeyGenFile replaces the record atomically with mode 0600.
func writeKeyGenFile(dir, name string, h [sha256.Size]byte) error {
	f, err := os.CreateTemp(dir, name+".weir.keygen.*")
	if err != nil {
		return fmt.Errorf("weir: snapshot key-generation record: %w", err)
	}
	_, werr := f.Write(h[:])
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("weir: snapshot key-generation record: %w", err)
	}
	if err := os.Rename(f.Name(), keyGenPath(dir, name)); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("weir: snapshot key-generation record: %w", err)
	}
	return nil
}

// validateSnapshotDir rejects what Caddy would not expand here: braces are
// placeholders (08 §2), and NUL cannot be in a path.
func validateSnapshotDir(dir string) error {
	if dir != "" && !filepath.IsAbs(dir) {
		return errors.New("weir: snapshot_dir must be an absolute path")
	}
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
