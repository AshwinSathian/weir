package limiter

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// Shed errors. ErrQueueFull and ErrQueueTimeout wrap ErrShed so the engine
// can test errors.Is(err, ErrShed) and still name the EvShed detail
// (04 §11). The engine maps all three to its own ErrShed.
var (
	// ErrShed means a background fetch found no slot outside the reserve.
	ErrShed = errors.New("weir: limiter shed")
	// ErrQueueFull means the queue already held MaxQueue waiters, or
	// queueCap waiters for the same partition.
	ErrQueueFull = fmt.Errorf("%w: queue full", ErrShed)
	// ErrQueueTimeout means a foreground waiter waited MaxWait.
	ErrQueueTimeout = fmt.Errorf("%w: queue timeout", ErrShed)
)

// Class says how an Acquire may wait (FR-LIM-2, FR-LIM-4, FR-WRM-*).
type Class uint8

const (
	// Foreground fetches use every slot and queue for at most MaxWait.
	Foreground Class = iota
	// Background fetches stay out of the reserve and never queue.
	Background
	// Warm fetches stay out of the reserve and queue until their context ends.
	Warm
	// Refresh fetches are the refreshes stale serving owes (FR-STL-1). They
	// stay out of the reserve and wait until their context ends, in a
	// backlog of their own that is granted only after every Foreground and
	// Warm waiter that can run (FR-LIM-4).
	Refresh
)

// Config holds the limiter's bounds. The engine fills defaults (04 §5).
type Config struct {
	Max          int // slots in flight at once (FR-LIM-1)
	MaxQueue     int // waiters at once (FR-LIM-2)
	PerPartition int // slots per partition (FR-LIM-3)
	// Headroom lets a fetch taken with AcquireOver run although its
	// partition is at PerPartition, while more than Reserve + Headroom slots
	// are free (FR-LIM-3). Zero makes PerPartition a strict cap for every
	// fetch.
	Headroom int
	PerHost  int           // slots per host; 0 disables (FR-FAIR-1)
	Reserve  int           // slots only Foreground may take (FR-LIM-4)
	MaxWait  time.Duration // Foreground queue wait (FR-LIM-2)
}

// Limiter bounds concurrent origin fetches globally and per partition,
// with a FIFO queue that skips waiters whose partition is full (ADR-8), and
// a second one of the same bound for Refresh fetches. Its memory is
// O(Max + MaxQueue) whatever the number of partitions (FR-LIM-6).
type Limiter struct {
	mu       sync.Mutex
	cfg      Config
	inflight int
	// borrowed counts the slots held past a partition's cap (FR-LIM-3).
	// inflight includes them and byPart does not, so the fetches a caller
	// vouched for never use up the cap the partition's other fetches share.
	borrowed int
	byPart   map[uint64]int // only partitions with a slot under their cap; len <= Max
	byHost   map[uint64]int // only hosts with inflight > 0, and only when PerHost > 0; len <= Max
	queue    []*waiter      // FIFO, len <= MaxQueue
	queuedBy map[uint64]int // waiters per partition, each <= queueCap(); only partitions with queued > 0; len <= MaxQueue
	// The Refresh backlog: FIFO, len <= MaxQueue, with the same share per
	// partition. Apart from queue so that stale entries waiting for a
	// refresh never take a place a client is waiting in (FR-LIM-4).
	later   []*waiter
	laterBy map[uint64]int

	// The newest miss-rate report (FR-MR-3): cap overrides per partition,
	// at most missrate's TopK of them, in force until thrUntil. thrEnd is
	// the end of the window the report closed; only a later one replaces it.
	throttled map[uint64]int
	thrEnd    time.Time
	thrUntil  time.Time
}

type waiter struct {
	part    uint64
	host    uint64
	class   Class
	over    bool          // taken with AcquireOver
	ready   chan struct{} // closed when granted
	granted bool
	borrow  bool // granted past the partition's cap
}

// Permit is one held slot. Release returns it.
type Permit struct {
	l        *Limiter
	part     uint64
	host     uint64
	borrow   bool // the slot is counted in borrowed, not byPart
	released bool // guarded by l.mu
}

// Counts returns the slots held and the waiters queued, for gauges.
func (l *Limiter) Counts() (inflight, queued int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inflight, len(l.queue)
}

// New returns a limiter with the given bounds.
func New(cfg Config) *Limiter {
	return &Limiter{cfg: cfg, byPart: make(map[uint64]int), byHost: make(map[uint64]int), queuedBy: make(map[uint64]int), laterBy: make(map[uint64]int)}
}

// limit is the global cap. Every read goes through it so an adaptive policy
// can replace the static value (D13, OQ-3).
func (l *Limiter) limit() int { return l.cfg.Max }

// capFor is the cap for one partition: PerPartition, or a lower override
// from the miss-rate report in force (FR-MR-3). Caller holds l.mu.
//
// Whatever changes limit or capFor (an adaptive policy, a throttle ending)
// must run the grant walk when a cap rises, or queued waiters stay parked
// while the fast path in Acquire admits newcomers.
func (l *Limiter) capFor(part uint64) int {
	if c, ok := l.throttled[part]; ok {
		return min(c, l.cfg.PerPartition)
	}
	return l.cfg.PerPartition
}

// Throttle replaces the cap overrides with those of the miss-rate window
// that closed at end; they hold until until (FR-MR-3). It reports whether
// caps from this report are now in force. A cap below 1 would park its
// partition's waiters with no Release to wake them, so it counts as 1.
//
// Reports can arrive late or out of order (04 §8.4), so one that is not
// newer than the report held is ignored, and one already past until only
// clears the map: a flood, an idle hour and one request must not throttle
// on the hour-old window.
func (l *Limiter) Throttle(end, until time.Time, caps map[uint64]int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !end.After(l.thrEnd) {
		return false
	}
	l.thrEnd, l.thrUntil = end, until
	l.throttled = nil
	if len(caps) > 0 && time.Now().Before(until) {
		l.throttled = make(map[uint64]int, len(caps))
		for part, c := range caps {
			l.throttled[part] = max(c, 1)
		}
	}
	l.grant() // a partition the new report left out has its cap back
	return l.throttled != nil
}

// expire drops the throttle report at its deadline and reports whether it
// did. Caller holds l.mu.
//
// ponytail: no timer. The next Acquire, Release or waiter timeout after the
// deadline drops the report, so a waiter parked behind a throttle waits for
// that call. One always comes: the waiter is parked because a fetch of its
// partition is in flight, whose Release runs the grant walk, and a
// Foreground waiter's own MaxWait timer ends the wait before that if the
// fetch is slow. A time.AfterFunc here would be a goroutine for Close to
// own (hard rule 10).
func (l *Limiter) expire() bool {
	if l.throttled == nil || time.Now().Before(l.thrUntil) {
		return false
	}
	l.throttled = nil
	return true
}

// queueCap is how many waiters one partition may queue (FR-LIM-3, T-11):
// a quarter of the queue, so one flooded path leaves three quarters to the
// rest, but never fewer than the partition's slots. A cap as low as
// PerPartition sheds most of a legitimate cold start on one path with many
// query strings.
func (l *Limiter) queueCap() int { return max(l.cfg.PerPartition, l.cfg.MaxQueue/4) }

// canRun reports whether a fetch of class c for part and host may take a
// slot now, and whether that slot is one past the partition's cap. Caller
// holds l.mu.
func (l *Limiter) canRun(c Class, over bool, part, host uint64) (ok, borrow bool) {
	limit := l.limit()
	if c != Foreground {
		limit -= l.cfg.Reserve
	}
	if l.cfg.PerHost > 0 && l.byHost[host] >= l.cfg.PerHost {
		return false, false // FR-FAIR-1: one host cannot take every slot
	}
	if l.inflight >= limit {
		return false, false
	}
	if l.byPart[part] < l.capFor(part) {
		return true, false
	}
	// FR-LIM-3: past its cap a partition runs only the fetches the caller
	// asks it for (a client's, with AcquireOver) and the refreshes stale
	// serving owes, and only while Headroom slots stay free below the
	// reserve, so a foreground or a background fetch for any other partition
	// still starts at once. A partition the miss-rate report throttles has
	// no such allowance (FR-MR-3); the slots it already holds past the cap
	// run out and are not replaced.
	if !over && c != Refresh || l.cfg.Headroom <= 0 {
		return false, false
	}
	if _, thr := l.throttled[part]; thr {
		return false, false
	}
	return l.inflight < l.limit()-l.cfg.Reserve-l.cfg.Headroom, true
}

// take records a granted slot. Caller holds l.mu.
func (l *Limiter) take(part, host uint64, borrow bool) *Permit {
	l.hold(part, host, borrow)
	return &Permit{l: l, part: part, host: host, borrow: borrow}
}

// hold counts one more slot in flight. Caller holds l.mu.
func (l *Limiter) hold(part, host uint64, borrow bool) {
	l.inflight++
	if borrow {
		l.borrowed++
	} else {
		l.byPart[part]++
	}
	if l.cfg.PerHost > 0 {
		l.byHost[host]++
	}
}

// Acquire takes a slot for part and host (both hashes), waiting as its class
// allows; host matters only when Config.PerHost is set. It returns
// an error wrapping ErrShed when no slot comes free in time, or ctx.Err()
// when ctx ends first.
func (l *Limiter) Acquire(ctx context.Context, c Class, part, host uint64) (*Permit, error) {
	return l.acquire(ctx, c, false, part, host)
}

// AcquireOver is Acquire for a Foreground fetch that may run past its
// partition's cap while Config.Headroom slots stay free (FR-LIM-3). The
// engine uses it for every client's fetch (v1.2). The cap cannot tell a
// cache-busting flood from a search page with many honest queries, and
// holding either to it while the origin's budget sits idle refuses clients
// for nothing (CMP-F5). What the flood must not do is take slots another
// path needs, and Headroom is what guarantees that (T-11).
func (l *Limiter) AcquireOver(ctx context.Context, part, host uint64) (*Permit, error) {
	return l.acquire(ctx, Foreground, true, part, host)
}

func (l *Limiter) acquire(ctx context.Context, c Class, over bool, part, host uint64) (*Permit, error) {
	l.mu.Lock()
	if l.expire() {
		l.grant() // parked waiters of the freed partitions go first
	}
	// Release, Throttle and the lines above grant every runnable waiter
	// before they unlock, so no queued waiter is runnable here and canRun
	// alone keeps FIFO order among them.
	if ok, borrow := l.canRun(c, over, part, host); ok {
		p := l.take(part, host, borrow)
		l.mu.Unlock()
		return p, nil
	}
	if c == Background {
		l.mu.Unlock()
		return nil, ErrShed
	}
	// ponytail: waiters held back by PerHost share MaxQueue; a flood of
	// distinct paths on one host can fill it. Ceiling: foreground requests for
	// other hosts then shed. Upgrade: a per-host queued count like queuedBy.
	// T-11: a flood on one partition, whose waiters cannot run anyway,
	// must not fill the queue every other partition shares (FR-LIM-3).
	q, by := &l.queue, l.queuedBy
	if c == Refresh {
		q, by = &l.later, l.laterBy
	}
	if len(*q) >= l.cfg.MaxQueue || by[part] >= l.queueCap() {
		l.mu.Unlock()
		return nil, ErrQueueFull
	}
	w := &waiter{part: part, host: host, class: c, over: over, ready: make(chan struct{})}
	*q = append(*q, w)
	by[part]++
	l.mu.Unlock()

	var expired <-chan time.Time
	if c == Foreground {
		t := time.NewTimer(l.cfg.MaxWait)
		defer t.Stop()
		expired = t.C
	}
	var err error
	select {
	case <-w.ready:
		return &Permit{l: l, part: part, host: host, borrow: w.borrow}, nil // written before ready closed
	case <-expired:
		err = ErrQueueTimeout
	case <-ctx.Done():
		err = ctx.Err()
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	// On a quiet limiter this timeout is the first call past a throttle's
	// deadline; without it the waiter is shed with room in its partition.
	if l.expire() {
		l.grant()
	}
	if w.granted { // a grant won the race: the slot is ours, keep it.
		return &Permit{l: l, part: part, host: host, borrow: w.borrow}, nil
	}
	if i := slices.Index(*q, w); i >= 0 {
		*q = slices.Delete(*q, i, i+1)
		unqueue(by, part)
	}
	return nil, err
}

// Release returns the slot and grants it, in FIFO order, to every waiter
// that can now run. A second call is a no-op.
func (p *Permit) Release() {
	l := p.l
	l.mu.Lock()
	defer l.mu.Unlock()
	if p.released {
		return
	}
	p.released = true
	l.inflight--
	if p.borrow {
		l.borrowed--
	} else if l.byPart[p.part]--; l.byPart[p.part] <= 0 {
		delete(l.byPart, p.part)
	}
	if l.cfg.PerHost > 0 {
		if l.byHost[p.host]--; l.byHost[p.host] <= 0 {
			delete(l.byHost, p.host)
		}
	}
	l.expire()
	l.grant()
}

// grant gives a slot, in FIFO order, to every waiter that can now run:
// first the clients' queue, then the refresh backlog with what is left.
// Caller holds l.mu.
func (l *Limiter) grant() {
	l.queue = l.grantFrom(l.queue, l.queuedBy)
	l.later = l.grantFrom(l.later, l.laterBy)
}

// grantFrom is grant for one queue; it returns the waiters still waiting.
func (l *Limiter) grantFrom(q []*waiter, by map[uint64]int) []*waiter {
	// ponytail: the walk passes every waiter whose partition is full before
	// it finds one that can run, at most MaxQueue (8192 by default) of them,
	// about 8 ns each: four flooded paths at their queue cap cost a release
	// some 65 µs under the lock, and a full refresh backlog whose slots
	// outside the reserve are all taken costs the same again. Per-partition
	// queues if that ever shows.
	kept := q[:0]
	for i, w := range q {
		if l.inflight >= l.limit() {
			// No slot is left for any class, so nobody behind w can run
			// either. The usual Release frees one slot and ends here after
			// the first waiter that takes it, not after the whole queue.
			kept = append(kept, q[i:]...)
			break
		}
		if ok, borrow := l.canRun(w.class, w.over, w.part, w.host); ok {
			l.hold(w.part, w.host, borrow)
			w.granted, w.borrow = true, borrow
			close(w.ready)
			unqueue(by, w.part)
			continue
		}
		kept = append(kept, w)
	}
	clear(q[len(kept):])
	return kept
}

// unqueue drops one waiter of part from a queue's per-partition count.
// Caller holds l.mu.
func unqueue(by map[uint64]int, part uint64) {
	if by[part]--; by[part] <= 0 {
		delete(by, part)
	}
}
