package memory

import (
	"sync"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// evictions counts records a Set evicted, per OnEvict queue name.
type evictions struct{ small, main, expired int }

// shard is one independently locked S3-FIFO cache (05 §5.3).
type shard struct {
	mu       sync.RWMutex
	m        map[store.Key]*node
	small    fifo // holds at most smallCap bytes before eviction
	main     fifo
	ghost    ghost
	bytes    int64
	cap      int64
	smallCap int64
}

func newShard(capBytes int64) shard {
	return shard{
		m:        map[store.Key]*node{},
		ghost:    ghost{set: map[uint64]uint32{}},
		cap:      capBytes,
		smallCap: capBytes / 10,
	}
}

// get returns k's live entry. A hit takes only the read lock, so a hot key
// does not serialize its readers.
func (sh *shard) get(k store.Key) (*store.Entry, bool) {
	now := time.Now()
	sh.mu.RLock()
	n := sh.m[k]
	if n == nil {
		sh.mu.RUnlock()
		return nil, false
	}
	if !n.expired(now) {
		e := n.e
		for {
			f := n.freq.Load()
			if f >= 3 || n.freq.CompareAndSwap(f, f+1) {
				break
			}
		}
		sh.mu.RUnlock()
		return e, true
	}
	sh.mu.RUnlock()
	sh.mu.Lock()
	// The key may have been replaced between the two locks.
	if cur := sh.m[k]; cur == n && n.expired(now) {
		sh.unlink(n)
	}
	sh.mu.Unlock()
	return nil, false
}

// set stores e at k (size already checked against smallCap) and evicts
// until the shard is within its byte budget. With noEvict, a record that
// would exceed the budget is refused (ok false) and nothing changes.
func (sh *shard) set(k store.Key, e *store.Entry, size int64, expires time.Time, fp uint64, noEvict bool) (ev evictions, ok bool) {
	now := time.Now()
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if noEvict {
		grow := size
		if n := sh.m[k]; n != nil {
			grow -= n.size
		}
		if sh.bytes+grow > sh.cap {
			return ev, false
		}
	}
	if n := sh.m[k]; n != nil {
		// Replace in place, keeping queue and freq (05 §5.3).
		q := sh.queue(n)
		q.bytes += size - n.size
		sh.bytes += size - n.size
		n.e, n.size, n.expires = e, size, expires
	} else {
		n = &node{key: k, e: e, size: size, expires: expires, fp: fp}
		if sh.ghost.take(fp) {
			n.queue = queueMain
		}
		sh.m[k] = n
		sh.queue(n).push(n)
		sh.bytes += size
	}
	for sh.bytes > sh.cap {
		if sh.small.bytes > sh.smallCap || sh.main.len == 0 {
			sh.evictSmall(now, &ev)
		} else {
			sh.evictMain(now, &ev)
		}
	}
	return ev, true
}

// evictSmall handles the small queue's tail: expired records drop, records
// hit twice or more move to main, the rest drop into the ghost.
func (sh *shard) evictSmall(now time.Time, ev *evictions) {
	n := sh.small.tail
	switch {
	case n.expired(now):
		sh.unlink(n)
		ev.expired++
	case n.freq.Load() >= 2:
		sh.small.remove(n)
		n.freq.Store(0)
		n.queue = queueMain
		sh.main.push(n)
	default:
		sh.unlink(n)
		sh.ghost.add(n.fp, sh.main.len)
		ev.small++
	}
}

// evictMain handles the main queue's tail: expired records drop, records hit
// since their last pass are reinserted with one less frequency, the rest drop.
// Frequencies are at most 3, so the eviction loop ends within
// 4*len(main)+len(small) steps.
func (sh *shard) evictMain(now time.Time, ev *evictions) {
	n := sh.main.tail
	switch f := n.freq.Load(); {
	case n.expired(now):
		sh.unlink(n)
		ev.expired++
	case f >= 1:
		sh.main.remove(n)
		n.freq.Store(f - 1)
		sh.main.push(n)
	default:
		sh.unlink(n)
		ev.main++
	}
}

func (sh *shard) delete(k store.Key) {
	sh.mu.Lock()
	if n := sh.m[k]; n != nil {
		sh.unlink(n)
	}
	sh.mu.Unlock()
}

func (sh *shard) unlink(n *node) {
	sh.queue(n).remove(n)
	delete(sh.m, n.key)
	sh.bytes -= n.size
	if n.queue == queueMain {
		sh.ghost.trim(sh.main.len)
	}
}

func (sh *shard) queue(n *node) *fifo {
	if n.queue == queueMain {
		return &sh.main
	}
	return &sh.small
}
