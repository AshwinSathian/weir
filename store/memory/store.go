package memory

import (
	"context"
	"errors"
	"hash/maphash"
	"math/bits"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// Config sizes a Store (05 §5.1).
type Config struct {
	MaxBytes     int64         // 0: 256 MiB
	Shards       int           // 0: 16; a power of two, at most MaxShards
	MaxRetention time.Duration // 0: 24h; caps every record's lifetime (E-11)
	// MaxHardEpochs caps how many distinct tags may hold a hard epoch (0:
	// 100000, E-6; 10000 before v1.1). A tag holds its place for
	// MaxRetention, so this is the number of URLs and groups that can be
	// hard-purged within that time; each costs about 100 bytes. It is a
	// tuning knob: its meaning and scale may change in a minor release.
	MaxHardEpochs int
	// EpochSlots is the size of the table that holds soft and invalidation
	// epochs (0: 1 << 19; a power of two, at most MaxEpochSlots, E-7). It is
	// a tuning knob: its meaning and scale may change in a minor release.
	EpochSlots int
	// MaxBytesPerOwner caps the bytes one Entry.Owner may hold in each shard
	// (FR-FAIR-2), so an owner may hold Shards times this in all. A cap below
	// one object's size declines every Set for that owner. 0 disables it. An over-quota Set first evicts that owner's
	// own entries and is declined if that is not enough; entries with the
	// zero Owner are never limited.
	MaxBytesPerOwner int64
	// SnapshotPath, when set, makes Close write a snapshot there (FR-SNP-1).
	// A snapshot holds entries stored under the forwarding rules of the
	// engine that wrote it. Put weir.Config.KeyGeneration() in the file name
	// so that an engine with other rules does not load them (FR-SNP-4).
	SnapshotPath string
	// SnapshotKeepFreshness makes a restart keep the cache warm (FR-SNP-5).
	// Without it every loaded entry is invalid as of the load and is
	// revalidated before any use, because a snapshot does not hold soft
	// purges or the invalidations of unsafe requests. With it, Close leaves
	// out the entries one of those covers and marks the file, and a load
	// that finds the mark, lost no record and sees a clock not behind the
	// file's keeps every entry as fresh as it was.
	//
	// The cost is T-33: a purge sent while the process was down, which it
	// could not receive, is not applied to what the snapshot brings back.
	// Purge again after a restart if one may have been missed, or leave
	// this off. A version before v1.2 that reads a marked file starts cold.
	SnapshotKeepFreshness bool
	// SnapshotTimeout bounds the snapshot written by Close; CloseContext uses
	// its own context instead. 0: 5s.
	SnapshotTimeout time.Duration
	// OnEvict, when set, receives per-Set eviction counts by queue. The queue
	// names are a stable vocabulary: "small" (entries evicted from the
	// small admission queue), "main" (entries evicted from the main queue)
	// and "expired" (entries dropped because they had expired). It runs
	// after the shard lock is released.
	OnEvict func(queue string, n int)
}

// shardTarget is the shard size Shards aims for. One Set on a full shard
// whose records are all hot walks its main queue up to four times under the
// write lock (05 §5.3): about 0.5 ms for 16 MiB of small records, 2.6 ms for
// 64 MiB and 10 ms for 256 MiB (BenchmarkEvictionPauseLargeShard, M4 Pro).
const shardTarget = 16 << 20

// Shards returns a shard count for a store of maxBytes that will hold objects
// of up to maxObject bytes: the power of two that makes shards of about
// 16 MiB, at least 16 and at most 1024, then halved while a shard's small
// queue (a tenth of the shard) could not hold one such object, down to 1. A
// larger store thereby gets more shards instead of larger ones, which keeps
// the longest eviction pause, and the readers it blocks, small (05 §5.1).
// New's own default stays 16, because a shard's size is also the store's
// MaxObjectBytes and changing it would refuse objects it used to take.
func Shards(maxBytes, maxObject int64) int {
	n := 16
	for n < 1024 && maxBytes/int64(n*2) >= shardTarget {
		n *= 2
	}
	for n > 1 && maxBytes/int64(n)/10 < maxObject {
		n /= 2
	}
	return n
}

// defaultMaxHardEpochs is Config.MaxHardEpochs when unset: at 24 h retention,
// one hard purge of a new URL or group a second, around the clock (E-6).
const defaultMaxHardEpochs = 100_000

// MaxShards bounds Config.Shards, so a mistyped count fails New instead of
// exhausting memory (NFR-2).
const MaxShards = 1 << 16

// Store is the in-process store. It implements store.Store, store.Sizer,
// store.SharedTagEpochs, store.VarySetter and store.FetchTracker.
type Store struct {
	shards  []shard
	mask    uint64
	seed    maphash.Seed // shard choice (T-22)
	fpSeed  maphash.Seed // ghost fingerprints, independent of shard choice
	onEvict func(string, int)
	ep      *epochs
	closed  atomic.Bool

	closeMu     sync.Mutex // serializes CloseContext
	snapDone    bool       // the snapshot was written; guarded by closeMu
	snapPath    string
	snapTimeout time.Duration
	snapKeep    bool          // Config.SnapshotKeepFreshness
	snapLoad    snapLoadStats // set once by New, then read-only
}

var (
	_ store.Store           = (*Store)(nil)
	_ store.Sizer           = (*Store)(nil)
	_ store.SharedTagEpochs = (*Store)(nil)
	_ store.FetchTracker    = (*Store)(nil)
)

// FetchBegin counts one fetch in progress that may store under k
// (store.FetchTracker). The count has its own lock in k's shard, so it never
// waits on, or delays, the shard's readers. Only keys with a count above 0
// are held, so its size is bounded by the fetches in progress (P5).
func (s *Store) FetchBegin(k store.Key) {
	sh := &s.shards[s.shardIndex(k)]
	sh.busyMu.Lock()
	if sh.busy == nil {
		sh.busy = map[store.Key]int{}
	}
	sh.busy[k]++
	sh.busyMu.Unlock()
}

// FetchEnd ends one FetchBegin(k).
func (s *Store) FetchEnd(k store.Key) {
	sh := &s.shards[s.shardIndex(k)]
	sh.busyMu.Lock()
	if sh.busy[k]--; sh.busy[k] <= 0 {
		delete(sh.busy, k)
	}
	sh.busyMu.Unlock()
}

// FetchInProgress reports whether a fetch that may store under k is counted.
func (s *Store) FetchInProgress(k store.Key) bool {
	sh := &s.shards[s.shardIndex(k)]
	sh.busyMu.Lock()
	defer sh.busyMu.Unlock()
	return sh.busy[k] > 0
}

// New returns an empty Store sized by cfg, or an error for a negative
// setting or a shard or slot count that is not a power of two within its bound.
func New(cfg Config) (*Store, error) {
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 256 << 20
	}
	if cfg.Shards == 0 {
		cfg.Shards = 16
	}
	if cfg.MaxRetention == 0 {
		cfg.MaxRetention = 24 * time.Hour
	}
	if cfg.MaxHardEpochs == 0 {
		cfg.MaxHardEpochs = defaultMaxHardEpochs
	}
	if cfg.EpochSlots == 0 {
		cfg.EpochSlots = 1 << 19
	}
	if cfg.SnapshotTimeout == 0 {
		cfg.SnapshotTimeout = defaultSnapshotTimeout
	}
	if cfg.MaxBytes < 0 || cfg.MaxRetention < 0 || cfg.MaxHardEpochs < 0 || cfg.SnapshotTimeout < 0 || cfg.MaxBytesPerOwner < 0 {
		return nil, errors.New("store: memory: negative MaxBytes, MaxRetention, MaxHardEpochs, SnapshotTimeout or MaxBytesPerOwner")
	}
	if !powerOfTwo(cfg.Shards, MaxShards) {
		return nil, errors.New("store: memory: Shards is not a power of two up to MaxShards")
	}
	if !powerOfTwo(cfg.EpochSlots, MaxEpochSlots) {
		return nil, errors.New("store: memory: EpochSlots is not a power of two up to MaxEpochSlots")
	}
	s := &Store{
		shards:      make([]shard, cfg.Shards),
		mask:        uint64(cfg.Shards - 1), //nolint:gosec // Shards is a positive power of two
		seed:        maphash.MakeSeed(),
		fpSeed:      maphash.MakeSeed(),
		onEvict:     cfg.OnEvict,
		snapPath:    cfg.SnapshotPath,
		snapTimeout: cfg.SnapshotTimeout,
		snapKeep:    cfg.SnapshotKeepFreshness,
		ep:          newEpochs(cfg.EpochSlots, cfg.MaxHardEpochs, cfg.MaxRetention),
	}
	for i := range s.shards {
		s.shards[i] = newShard(cfg.MaxBytes/int64(cfg.Shards), cfg.MaxBytesPerOwner)
	}
	if s.snapPath != "" {
		s.loadSnapshot()
	}
	return s, nil
}

func powerOfTwo(n, limit int) bool {
	return n > 0 && n <= limit && bits.OnesCount(uint(n)) == 1
}

// shardIndex hashes k with a per-process seed, so request inputs cannot be
// ground offline to crowd one shard (T-22, ADR-3).
func (s *Store) shardIndex(k store.Key) uint64 { return maphash.Comparable(s.seed, k) & s.mask }

// Get returns the live record at k or store.ErrNotFound.
func (s *Store) Get(_ context.Context, k store.Key) (*store.Entry, error) {
	if s.closed.Load() {
		return nil, store.ErrUnavailable
	}
	if e, ok := s.shards[s.shardIndex(k)].get(k); ok {
		return e, nil
	}
	return nil, store.ErrNotFound
}

// Set stores e at k until e.Expires, clamped to MaxRetention after its
// request time (E-11). It declines, returning nil, a record past its Expires
// (a no-op, 05 §2.3) or larger than MaxObjectBytes (which also removes any
// record at k) (S-4).
func (s *Store) Set(_ context.Context, k store.Key, e *store.Entry) error {
	if s.closed.Load() {
		return store.ErrUnavailable
	}
	s.put(k, e, false, nil)
	return nil
}

// varyPrev is the precondition of put for SetVarySpec.
type varyPrev struct{ prev *store.Entry }

// SetVarySpec stores next at k if the live record there is still prev (nil:
// none), in one shard lock hold (store.VarySetter). A record the store
// declines counts as swapped, as for Set (S-4).
func (s *Store) SetVarySpec(_ context.Context, k store.Key, prev, next *store.Entry) (bool, error) {
	if s.closed.Load() {
		return false, store.ErrUnavailable
	}
	return s.put(k, next, false, &varyPrev{prev}) != putStale, nil
}

type putResult int

const (
	putStored putResult = iota
	putExpired
	putDeclined // too large, or (when loading) no room left
	putStale    // SetVarySpec: the record at the key is no longer prev
)

// put is Set's body. With loading set, a record that does not fit the
// shard's byte budget is declined instead of evicting an earlier record, so
// a snapshot loads in file order up to MaxBytes (FR-SNP-3).
func (s *Store) put(k store.Key, e *store.Entry, loading bool, cas *varyPrev) putResult {
	sh := &s.shards[s.shardIndex(k)]
	size := e.Size()
	now := time.Now()
	// From RequestTime, not StoredAt: a hard epoch at P is pruned at
	// P + MaxRetention and applies to records requested at or before P, so
	// those must be gone by then (E-6). Records without RequestTime are
	// checked against every epoch (since is zero), so StoredAt is enough for
	// them. Records without either start now.
	from := e.RequestTime
	if from.IsZero() {
		from = e.StoredAt
	}
	if from.IsZero() || from.After(now) {
		from = now
	}
	expires := e.Expires
	if limit := from.Add(s.ep.retention); limit.Before(expires) {
		expires = limit
	}
	if !now.Before(expires) {
		return putExpired
	}
	if cas != nil {
		if size > sh.smallCap {
			return putDeclined // a spec that large is declined; it does not evict the record it was compared with
		}
		ev, swapped, ok := sh.setIf(k, cas.prev, e, size, expires, maphash.Comparable(s.fpSeed, k))
		if !swapped {
			return putStale
		}
		s.reportEvictions(ev)
		if !ok {
			return putDeclined
		}
		return putStored
	}
	if size > sh.smallCap {
		// Declined. Drop the record it would have replaced, so Set leaves
		// the new record or nothing, never an older one.
		sh.delete(k)
		return putDeclined
	}
	ev, ok := sh.set(k, e, size, expires, maphash.Comparable(s.fpSeed, k), loading)
	if !ok {
		return putDeclined
	}
	s.reportEvictions(ev)
	return putStored
}

func (s *Store) reportEvictions(ev evictions) {
	if s.onEvict != nil {
		for _, c := range []struct {
			q string
			n int
		}{{"small", ev.small}, {"main", ev.main}, {"expired", ev.expired}} {
			if c.n > 0 {
				s.onEvict(c.q, c.n)
			}
		}
	}
}

// Delete removes the record at k, if any.
func (s *Store) Delete(_ context.Context, k store.Key) error {
	if s.closed.Load() {
		return store.ErrUnavailable
	}
	s.shards[s.shardIndex(k)].delete(k)
	return nil
}

// SetEpoch records ep for t (E-2). A new hard tag beyond MaxHardEpochs
// returns an error wrapping store.ErrUnavailable (E-6).
func (s *Store) SetEpoch(_ context.Context, t store.Tag, ep store.Epoch) error {
	if s.closed.Load() {
		return store.ErrUnavailable
	}
	return s.ep.set(t, ep)
}

// NewestEpoch returns the most severe, then latest, epoch among tags at or
// after since (E-3). Soft and invalid times may be rounded up to the next
// whole second after the store was built (E-7).
func (s *Store) NewestEpoch(_ context.Context, tags []store.Tag, since time.Time) (store.Epoch, bool, error) {
	if s.closed.Load() {
		return store.Epoch{}, false, store.ErrUnavailable
	}
	ep, ok := s.ep.newestOf(tags, nil, since)
	return ep, ok, nil
}

// NewestEpochShared is NewestEpoch over tags and shared, reading shared tags
// in the soft and hard planes only (05 E-12).
func (s *Store) NewestEpochShared(_ context.Context, tags, shared []store.Tag, since time.Time) (store.Epoch, bool, error) {
	if s.closed.Load() {
		return store.Epoch{}, false, store.ErrUnavailable
	}
	ep, ok := s.ep.newestOf(tags, shared, since)
	return ep, ok, nil
}

// Info names the store; it is not remote.
func (s *Store) Info() store.Info { return store.Info{Name: "memory"} }

// Bytes returns the bytes currently accounted across all shards.
func (s *Store) Bytes() int64 {
	var n int64
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.RLock()
		n += sh.bytes
		sh.mu.RUnlock()
	}
	return n
}

// MaxObjectBytes returns the largest record the store admits: the small
// queue's share of one shard, 10% (05 §5.1).
func (s *Store) MaxObjectBytes() int64 { return s.shards[0].smallCap }
