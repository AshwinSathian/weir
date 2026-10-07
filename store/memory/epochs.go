package memory

import (
	"fmt"
	"hash/maphash"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// MaxEpochSlots bounds Config.EpochSlots: 2^26 cells is 256 MiB per plane.
const MaxEpochSlots = 1 << 26

// pruneBatch caps how many expired hard epochs one SetEpoch removes (05 §5.4).
const pruneBatch = 64

// noEpoch marks an unset offset in the global and newest atomics.
const noEpoch = math.MinInt64

// epochs is the epoch table (05 §4, §5.4). Every time is kept as a
// monotonic offset from base, so wall-clock steps cannot hide a purge (§4.3).
type epochs struct {
	base      time.Time
	retention time.Duration
	maxHard   int
	globalTag store.Tag
	global    [3]atomic.Int64 // by mode-1, nanoseconds after base (E-5)
	newest    atomic.Int64    // any mode, nanoseconds after base (E-10)
	mu        sync.RWMutex
	hard      map[store.Tag]time.Time // E-6
	planes    [2][]atomic.Uint32      // soft, invalid; seconds after base, rounded up (E-7)
	mask      uint64
	seed      maphash.Seed // T-29: cells cannot be targeted offline
}

func newEpochs(slots, maxHard int, retention time.Duration) *epochs {
	ep := &epochs{
		base:      time.Now(),
		retention: retention,
		maxHard:   maxHard,
		globalTag: store.TagGlobal(),
		hard:      map[store.Tag]time.Time{},
		mask:      uint64(slots - 1), //nolint:gosec // slots is a positive power of two
		seed:      maphash.MakeSeed(),
	}
	for i := range ep.global {
		ep.global[i].Store(noEpoch)
	}
	ep.newest.Store(noEpoch)
	for i := range ep.planes {
		ep.planes[i] = make([]atomic.Uint32, slots)
	}
	return ep
}

// offset returns t as nanoseconds after base. Times about 292 years or more
// before base saturate; they are lifted off the noEpoch sentinel so such an
// epoch still applies to since values as old (E-8).
func (ep *epochs) offset(t time.Time) int64 {
	return max(int64(t.Sub(ep.base)), noEpoch+1)
}

// cells returns tag t's d = 2 sketch positions (E-7).
func (ep *epochs) cells(t store.Tag) (uint64, uint64) {
	h := maphash.Comparable(ep.seed, t)
	return h & ep.mask, (h >> 32) & ep.mask
}

func raise(a *atomic.Int64, v int64) {
	for {
		old := a.Load()
		if old >= v || a.CompareAndSwap(old, v) {
			return
		}
	}
}

func raise32(a *atomic.Uint32, v uint32) {
	for {
		old := a.Load()
		if old >= v || a.CompareAndSwap(old, v) {
			return
		}
	}
}

// set records e for t (E-2). Cells are written before newest, so a reader
// that passes the fast-path check also sees the cells.
func (ep *epochs) set(t store.Tag, e store.Epoch) error {
	off := ep.offset(e.At)
	switch {
	case e.Mode < store.EpochSoft || e.Mode > store.EpochHard:
		return fmt.Errorf("store: memory: epoch mode %d", e.Mode)
	case t == ep.globalTag:
		raise(&ep.global[e.Mode-1], off)
	case e.Mode == store.EpochHard:
		if err := ep.setHard(t, e.At); err != nil {
			return err
		}
	default:
		// Whole seconds rounded up, at least 1 so 0 stays "unset"; rounding
		// only over-invalidates (E-8). Divide then round, since adding
		// first overflows for a saturated offset. Clamping at MaxUint32
		// under-invalidates only for since values 136 years after New.
		sec := off / int64(time.Second)
		if off%int64(time.Second) > 0 {
			sec++
		}
		sec = max(1, sec)
		c := uint32(min(sec, math.MaxUint32)) //nolint:gosec // clamped to uint32 range
		i, j := ep.cells(t)
		p := ep.planes[e.Mode-1]
		raise32(&p[i], c)
		raise32(&p[j], c)
	}
	raise(&ep.newest, off)
	return nil
}

func (ep *epochs) setHard(t store.Tag, at time.Time) error {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	cur, ok := ep.hard[t]
	if !ok && len(ep.hard) >= ep.maxHard {
		ep.prune()
		if len(ep.hard) >= ep.maxHard {
			return fmt.Errorf("store: memory: %d hard epochs held: %w", len(ep.hard), store.ErrUnavailable)
		}
	}
	if !ok || at.After(cur) {
		ep.hard[t] = at
	}
	return nil
}

// prune drops up to pruneBatch hard epochs older than retention: every
// record they could apply to has expired (E-6, E-11). Caller holds mu.
//
// ponytail: walks the whole table when little is expired, at most
// MaxHardEpochs per operator Purge; add an At-ordered queue if purges get hot.
func (ep *epochs) prune() {
	cutoff := time.Now().Add(-ep.retention)
	n := 0
	for t, at := range ep.hard {
		if at.Before(cutoff) {
			delete(ep.hard, t)
			if n++; n == pruneBatch {
				return
			}
		}
	}
}

// newestOf returns the most severe, then latest, epoch among tags and shared
// at or after since (E-3). Tags in shared are not read in the invalid plane
// (E-12).
func (ep *epochs) newestOf(tags, shared []store.Tag, since time.Time) (store.Epoch, bool) {
	s := int64(since.Sub(ep.base))
	if ep.newest.Load() < s {
		return store.Epoch{}, false // E-10
	}
	for m := store.EpochHard; m >= store.EpochSoft; m-- {
		best := int64(noEpoch)
		for _, t := range tags {
			best = max(best, ep.tagEpoch(t, m, s))
		}
		if m != store.EpochInvalid { // E-12
			for _, t := range shared {
				best = max(best, ep.tagEpoch(t, m, s))
			}
		}
		if best != noEpoch {
			return store.Epoch{At: ep.base.Add(time.Duration(best)), Mode: m}, true
		}
	}
	return store.Epoch{}, false
}

// tagEpoch returns t's mode-m epoch offset if it is at or after s, else noEpoch.
func (ep *epochs) tagEpoch(t store.Tag, m store.EpochMode, s int64) int64 {
	if t != ep.globalTag {
		return ep.lookup(t, m, s)
	}
	if g := ep.global[m-1].Load(); g >= s {
		return g
	}
	return noEpoch
}

// lookup returns t's mode-m epoch offset if it is at or after s, else noEpoch.
func (ep *epochs) lookup(t store.Tag, m store.EpochMode, s int64) int64 {
	var off int64
	if m == store.EpochHard {
		ep.mu.RLock()
		at, ok := ep.hard[t]
		ep.mu.RUnlock()
		if !ok {
			return noEpoch
		}
		off = ep.offset(at)
	} else {
		i, j := ep.cells(t)
		p := ep.planes[m-1]
		c := min(p[i].Load(), p[j].Load())
		if c == 0 {
			return noEpoch
		}
		off = int64(c) * int64(time.Second)
	}
	if off < s {
		return noEpoch
	}
	return off
}
