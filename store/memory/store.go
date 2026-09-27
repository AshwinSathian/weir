package memory

import (
	"context"
	"errors"
	"hash/maphash"
	"math/bits"
	"sync/atomic"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// Config sizes a Store (05 §5.1).
type Config struct {
	MaxBytes int64 // 0: 256 MiB
	Shards   int   // 0: 16; a power of two, at most MaxShards
	// OnEvict, when set, receives per-Set eviction counts by queue: "small",
	// "main" or "expired". It runs after the shard lock is released.
	OnEvict func(queue string, n int)
}

// MaxShards bounds Config.Shards, so a mistyped count fails New instead of
// exhausting memory (NFR-2).
const MaxShards = 1 << 16

// Store is the in-process store. It implements store.Store and store.Sizer.
type Store struct {
	shards  []shard
	mask    uint64
	seed    maphash.Seed // shard choice (T-22)
	fpSeed  maphash.Seed // ghost fingerprints, independent of shard choice
	onEvict func(string, int)
	closed  atomic.Bool
}

var (
	_ store.Store = (*Store)(nil)
	_ store.Sizer = (*Store)(nil)
)

// New returns an empty Store sized by cfg, or an error for a negative size or
// a shard count that is not a power of two up to MaxShards.
func New(cfg Config) (*Store, error) {
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 256 << 20
	}
	if cfg.Shards == 0 {
		cfg.Shards = 16
	}
	if cfg.MaxBytes < 0 {
		return nil, errors.New("store: memory: negative MaxBytes")
	}
	if cfg.Shards < 0 || cfg.Shards > MaxShards || bits.OnesCount(uint(cfg.Shards)) != 1 {
		return nil, errors.New("store: memory: Shards is not a power of two up to MaxShards")
	}
	s := &Store{
		shards:  make([]shard, cfg.Shards),
		mask:    uint64(cfg.Shards - 1), //nolint:gosec // Shards is a positive power of two
		seed:    maphash.MakeSeed(),
		fpSeed:  maphash.MakeSeed(),
		onEvict: cfg.OnEvict,
	}
	for i := range s.shards {
		s.shards[i] = newShard(cfg.MaxBytes / int64(cfg.Shards))
	}
	return s, nil
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

// Set stores e at k. It declines, returning nil, a record past its Expires
// (a no-op, 05 §2.3) or larger than MaxObjectBytes (which also removes any
// record at k) (S-4).
func (s *Store) Set(_ context.Context, k store.Key, e *store.Entry) error {
	if s.closed.Load() {
		return store.ErrUnavailable
	}
	sh := &s.shards[s.shardIndex(k)]
	size := e.Size()
	if !time.Now().Before(e.Expires) {
		return nil
	}
	if size > sh.smallCap {
		// Declined. Drop the record it would have replaced, so Set leaves
		// the new record or nothing, never an older one.
		sh.delete(k)
		return nil
	}
	ev := sh.set(k, e, size, maphash.Comparable(s.fpSeed, k))
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
	return nil
}

// Delete removes the record at k, if any.
func (s *Store) Delete(_ context.Context, k store.Key) error {
	if s.closed.Load() {
		return store.ErrUnavailable
	}
	s.shards[s.shardIndex(k)].delete(k)
	return nil
}

// SetEpoch records nothing yet.
//
// ponytail: epochs are a no-op until M1-10 adds the sketch and hard table.
func (s *Store) SetEpoch(context.Context, store.Tag, store.Epoch) error {
	if s.closed.Load() {
		return store.ErrUnavailable
	}
	return nil
}

// NewestEpoch reports no epoch until M1-10.
func (s *Store) NewestEpoch(context.Context, []store.Tag, time.Time) (store.Epoch, bool, error) {
	if s.closed.Load() {
		return store.Epoch{}, false, store.ErrUnavailable
	}
	return store.Epoch{}, false, nil
}

// Info names the store; it is not remote.
func (s *Store) Info() store.Info { return store.Info{Name: "memory"} }

// Close marks the store closed. It is idempotent.
func (s *Store) Close() error {
	s.closed.Store(true)
	return nil
}

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
