package valkey

import (
	"context"
	"encoding/binary"
	"hash/maphash"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

const (
	defaultLocalBytes    = 64 << 20
	minLocalBytes        = 16 << 20
	maxLocalBytes        = 4 << 30
	defaultLocalEpochTTL = time.Second
	maxLocalEpochTTL     = time.Minute
	// maxEpochAnswers bounds the cached purge-check answers (P5): one per
	// distinct (tags, since) a lookup asked about. Past it the cache starts
	// over, which costs one round trip per entry, never a wrong answer.
	maxEpochAnswers = 1 << 16
)

// localTier is the in-process copy in front of the server (Config.LocalTier,
// 05 §7). Records go through a memory store, so their bytes are bounded and
// evicted by S3-FIFO; purge-check answers are kept for epochTTL, so a hit
// costs no round trip. An epoch this node writes clears the answers at
// once; one another node writes is seen within epochTTL.
type localTier struct {
	m        *memory.Store
	epochTTL time.Duration
	seed     maphash.Seed

	mu      sync.Mutex
	answers map[uint64]epochAnswer
	// gen counts forgets: an answer read from the server before a forget
	// must not be remembered after it, or a lookup racing this node's own
	// purge would cache the answer from before the purge.
	gen uint64
	// writes counts this node's record writes and deletes, striped by the
	// key's first byte, so a Get that read the server before one of them
	// does not put back the older copy, and a write to one key rarely holds
	// back the copies of others.
	writes [256]atomic.Uint64
}

type epochAnswer struct {
	ep  store.Epoch
	ok  bool
	got time.Time
}

func newLocalTier(c *LocalTierConfig, maxObject int64, maxRetention time.Duration) (*localTier, error) {
	// MaxRetention as on the server: a copy must not outlive the hard epoch
	// that could purge it, which the server prunes after MaxRetention (E-11).
	m, err := memory.New(memory.Config{MaxBytes: c.MaxBytes, Shards: memory.Shards(c.MaxBytes, min(maxObject, 1<<20)), MaxRetention: maxRetention})
	if err != nil {
		return nil, err
	}
	return &localTier{m: m, epochTTL: c.EpochTTL, seed: maphash.MakeSeed(), answers: map[uint64]epochAnswer{}}, nil
}

// answerKey hashes a purge check's question. The two tag lists are kept
// apart, since a shared tag never matches in invalid mode (E-12).
func (l *localTier) answerKey(tags, shared []store.Tag, since time.Time) uint64 {
	var h maphash.Hash
	h.SetSeed(l.seed)
	for _, t := range tags {
		_, _ = h.Write(t[:]) // maphash.Hash.Write never fails
	}
	_ = h.WriteByte(0)
	for _, t := range shared {
		_, _ = h.Write(t[:])
	}
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(since.UnixNano())) //nolint:gosec // bits, not a quantity
	_, _ = h.Write(b[:])
	return h.Sum64()
}

// answer returns a live cached answer, or the generation a fresh one must be
// remembered under.
func (l *localTier) answer(k uint64, now time.Time) (epochAnswer, uint64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.answers[k]
	if !ok || now.Sub(a.got) >= l.epochTTL {
		return epochAnswer{}, l.gen, false
	}
	return a, l.gen, true
}

func (l *localTier) remember(k uint64, a epochAnswer, gen uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if gen != l.gen {
		return
	}
	if len(l.answers) >= maxEpochAnswers {
		clear(l.answers)
	}
	l.answers[k] = a
}

// forget drops every answer: this node wrote an epoch, which must apply to
// its own lookups at once.
func (l *localTier) forget() {
	l.mu.Lock()
	clear(l.answers)
	l.gen++
	l.mu.Unlock()
}

// localGet returns a live local copy of k.
func (l *localTier) get(ctx context.Context, k store.Key) (*store.Entry, bool) {
	e, err := l.m.Get(ctx, k)
	if err != nil || (!e.Expires.IsZero() && !time.Now().Before(e.Expires)) {
		return nil, false
	}
	return e, true
}
