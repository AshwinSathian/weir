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

	// Per-owner quota (FR-FAIR-2). owners is nil when maxOwner is 0; else it
	// holds only owners with bytes in this shard, so len <= len(m).
	maxOwner int64
	owners   map[store.Tag]int64
}

// ownerScan bounds the nodes one over-quota Set walks looking for its own
// owner's entries (FR-FAIR-2, 05 §5.3), so a flood cannot make Set O(shard).
const ownerScan = 64

func newShard(capBytes, maxOwner int64) shard {
	var owners map[store.Tag]int64
	if maxOwner > 0 {
		owners = map[store.Tag]int64{}
	}
	return shard{
		m:        map[store.Key]*node{},
		ghost:    ghost{set: map[uint64]uint32{}},
		cap:      capBytes,
		smallCap: capBytes / 10,
		maxOwner: maxOwner,
		owners:   owners,
	}
}

// addOwner moves owner's byte account by delta. The zero Tag is "no owner"
// and is never limited. Caller holds sh.mu.
func (sh *shard) addOwner(owner store.Tag, delta int64) {
	if sh.owners == nil || owner == (store.Tag{}) {
		return
	}
	if v := sh.owners[owner] + delta; v > 0 {
		sh.owners[owner] = v
	} else {
		delete(sh.owners, owner)
	}
}

// fitOwner makes room under the owner's quota for a record of size that
// replaces old (nil for a new key). It evicts only entries of the same
// owner, found within ownerScan nodes of the queue tails, small first, and
// does nothing unless that frees enough (T-32). With noEvict, as when a
// snapshot loads, it never evicts. Caller holds sh.mu.
func (sh *shard) fitOwner(old *node, owner store.Tag, size int64, noEvict bool, ev *evictions) bool {
	if sh.owners == nil || owner == (store.Tag{}) {
		return true
	}
	held := sh.owners[owner]
	if old != nil && old.owner == owner {
		held -= old.size
	}
	over := held + size - sh.maxOwner
	if over <= 0 {
		return true
	}
	if noEvict || size > sh.maxOwner {
		return false
	}
	var victims [ownerScan]*node
	nv, scanned, freed := 0, 0, int64(0)
	for _, q := range [...]*fifo{&sh.small, &sh.main} {
		for n := q.tail; n != nil && scanned < ownerScan && freed < over; n = n.prev {
			scanned++
			if n != old && n.owner == owner {
				victims[nv] = n
				nv++
				freed += n.size
			}
		}
	}
	if freed < over {
		return false
	}
	now := time.Now()
	for _, n := range victims[:nv] {
		switch {
		case n.expired(now):
			ev.expired++
		case n.queue == queueMain:
			ev.main++
		default:
			ev.small++
		}
		sh.unlink(n)
	}
	return true
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
	if !sh.fitOwner(sh.m[k], e.Owner, size, noEvict, &ev) {
		if !noEvict {
			// Like an oversize record: leave the new record or nothing,
			// never the older one (S-4).
			if n := sh.m[k]; n != nil {
				sh.unlink(n)
			}
		}
		return ev, false
	}
	if n := sh.m[k]; n != nil {
		// Replace in place, keeping queue and freq (05 §5.3).
		q := sh.queue(n)
		q.bytes += size - n.size
		sh.bytes += size - n.size
		sh.addOwner(n.owner, -n.size)
		sh.addOwner(e.Owner, size)
		n.e, n.size, n.expires, n.owner = e, size, expires, e.Owner
	} else {
		n = &node{key: k, e: e, size: size, expires: expires, fp: fp, owner: e.Owner}
		sh.addOwner(e.Owner, size)
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
	sh.addOwner(n.owner, -n.size)
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
