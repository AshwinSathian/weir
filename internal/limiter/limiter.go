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
)

// Config holds the limiter's bounds. The engine fills defaults (04 §5).
type Config struct {
	Max          int           // slots in flight at once (FR-LIM-1)
	MaxQueue     int           // waiters at once (FR-LIM-2)
	PerPartition int           // slots per partition (FR-LIM-3)
	Reserve      int           // slots only Foreground may take (FR-LIM-4)
	MaxWait      time.Duration // Foreground queue wait (FR-LIM-2)
}

// Limiter bounds concurrent origin fetches globally and per partition,
// with a FIFO queue that skips waiters whose partition is full (ADR-8).
// Its memory is O(Max + MaxQueue) whatever the number of partitions
// (FR-LIM-6).
type Limiter struct {
	mu       sync.Mutex
	cfg      Config
	inflight int
	byPart   map[uint64]int // only partitions with inflight > 0; len <= Max
	queue    []*waiter      // FIFO, len <= MaxQueue
	queuedBy map[uint64]int // waiters per partition, each <= queueCap(); only partitions with queued > 0; len <= MaxQueue

	// The newest miss-rate report (FR-MR-3): cap overrides per partition,
	// at most missrate's TopK of them, in force until thrUntil. thrEnd is
	// the end of the window the report closed; only a later one replaces it.
	throttled map[uint64]int
	thrEnd    time.Time
	thrUntil  time.Time
}

type waiter struct {
	part    uint64
	class   Class
	ready   chan struct{} // closed when granted
	granted bool
}

// Permit is one held slot. Release returns it.
type Permit struct {
	l        *Limiter
	part     uint64
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
	return &Limiter{cfg: cfg, byPart: make(map[uint64]int), queuedBy: make(map[uint64]int)}
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

// canRun reports whether a fetch of class c for part may take a slot now.
// Caller holds l.mu.
func (l *Limiter) canRun(c Class, part uint64) bool {
	limit := l.limit()
	if c != Foreground {
		limit -= l.cfg.Reserve
	}
	return l.inflight < limit && l.byPart[part] < l.capFor(part)
}

// take records a granted slot. Caller holds l.mu.
func (l *Limiter) take(part uint64) *Permit {
	l.inflight++
	l.byPart[part]++
	return &Permit{l: l, part: part}
}

// Acquire takes a slot for part, waiting as its class allows. It returns
// an error wrapping ErrShed when no slot comes free in time, or ctx.Err()
// when ctx ends first.
func (l *Limiter) Acquire(ctx context.Context, c Class, part uint64) (*Permit, error) {
	l.mu.Lock()
	if l.expire() {
		l.grant() // parked waiters of the freed partitions go first
	}
	// Release, Throttle and the lines above grant every runnable waiter
	// before they unlock, so no queued waiter is runnable here and canRun
	// alone keeps FIFO order among them.
	if l.canRun(c, part) {
		p := l.take(part)
		l.mu.Unlock()
		return p, nil
	}
	if c == Background {
		l.mu.Unlock()
		return nil, ErrShed
	}
	// T-11: a flood on one partition, whose waiters cannot run anyway,
	// must not fill the queue every other partition shares (FR-LIM-3).
	if len(l.queue) >= l.cfg.MaxQueue || l.queuedBy[part] >= l.queueCap() {
		l.mu.Unlock()
		return nil, ErrQueueFull
	}
	w := &waiter{part: part, class: c, ready: make(chan struct{})}
	l.queue = append(l.queue, w)
	l.queuedBy[part]++
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
		return &Permit{l: l, part: part}, nil
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
		return &Permit{l: l, part: part}, nil
	}
	if i := slices.Index(l.queue, w); i >= 0 {
		l.queue = slices.Delete(l.queue, i, i+1)
		l.unqueue(part)
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
	if l.byPart[p.part]--; l.byPart[p.part] <= 0 {
		delete(l.byPart, p.part)
	}
	l.expire()
	l.grant()
}

// grant gives a slot, in FIFO order, to every waiter that can now run.
// Caller holds l.mu.
func (l *Limiter) grant() {
	// ponytail: O(queue) walk per release, at most MaxQueue (1024) waiters;
	// per-partition queues if BenchmarkLimiterAcquireRelease says it matters.
	kept := l.queue[:0]
	for _, w := range l.queue {
		if l.canRun(w.class, w.part) {
			l.inflight++
			l.byPart[w.part]++
			w.granted = true
			close(w.ready)
			l.unqueue(w.part)
			continue
		}
		kept = append(kept, w)
	}
	clear(l.queue[len(kept):])
	l.queue = kept
}

// unqueue drops one waiter of part from queuedBy. Caller holds l.mu.
func (l *Limiter) unqueue(part uint64) {
	if l.queuedBy[part]--; l.queuedBy[part] <= 0 {
		delete(l.queuedBy, part)
	}
}
