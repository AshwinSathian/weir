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
	MaxBytes      int64         // 0: 256 MiB
	Shards        int           // 0: 16; a power of two, at most MaxShards
	MaxRetention  time.Duration // 0: 24h; caps every record's lifetime (E-11)
	MaxHardEpochs int           // 0: 10000 (E-6)
	EpochSlots    int           // 0: 1 << 19; a power of two, at most MaxEpochSlots (E-7)
	// SnapshotPath, when set, makes Close write a snapshot there (FR-SNP-1).
	SnapshotPath string
	// SnapshotTimeout bounds the snapshot written by Close; CloseContext uses
	// its own context instead. 0: 5s.
	SnapshotTimeout time.Duration
	// OnEvict, when set, receives per-Set eviction counts by queue: "small",
	// "main" or "expired". It runs after the shard lock is released.
	OnEvict func(queue string, n int)
}

// MaxShards bounds Config.Shards, so a mistyped count fails New instead of
// exhausting memory (NFR-2).
const MaxShards = 1 << 16

// Store is the in-process store. It implements store.Store, store.Sizer and store.SharedTagEpochs.
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
	snapLoad    snapLoadStats // set once by New, then read-only
}

var (
	_ store.Store           = (*Store)(nil)
	_ store.Sizer           = (*Store)(nil)
	_ store.SharedTagEpochs = (*Store)(nil)
)

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
		cfg.MaxHardEpochs = 10000
	}
	if cfg.EpochSlots == 0 {
		cfg.EpochSlots = 1 << 19
	}
	if cfg.SnapshotTimeout == 0 {
		cfg.SnapshotTimeout = defaultSnapshotTimeout
	}
	if cfg.MaxBytes < 0 || cfg.MaxRetention < 0 || cfg.MaxHardEpochs < 0 || cfg.SnapshotTimeout < 0 {
		return nil, errors.New("store: memory: negative MaxBytes, MaxRetention, MaxHardEpochs or SnapshotTimeout")
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
		ep:          newEpochs(cfg.EpochSlots, cfg.MaxHardEpochs, cfg.MaxRetention),
	}
	for i := range s.shards {
		s.shards[i] = newShard(cfg.MaxBytes / int64(cfg.Shards))
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
	s.put(k, e, false)
	return nil
}

type putResult int

const (
	putStored putResult = iota
	putExpired
	putDeclined // too large, or (when loading) no room left
)

// put is Set's body. With loading set, a record that does not fit the
// shard's byte budget is declined instead of evicting an earlier record, so
// a snapshot loads in file order up to MaxBytes (FR-SNP-3).
func (s *Store) put(k store.Key, e *store.Entry, loading bool) putResult {
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
	return putStored
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
