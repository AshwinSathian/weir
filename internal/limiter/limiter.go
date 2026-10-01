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
	// ErrQueueFull means the queue already held MaxQueue waiters.
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

// New returns a limiter with the given bounds.
func New(cfg Config) *Limiter {
	return &Limiter{cfg: cfg, byPart: make(map[uint64]int)}
}

// limit is the global cap. Every read goes through it so an adaptive policy
// can replace the static value (D13, OQ-3).
func (l *Limiter) limit() int { return l.cfg.Max }

// capFor is the cap for one partition; M8 adds missrate throttles here.
//
// Whatever changes limit or capFor later (an adaptive policy, a throttle
// expiring) must run Release's grant walk when a cap rises, or queued
// waiters stay parked while the fast path in Acquire admits newcomers.
func (l *Limiter) capFor(uint64) int { return l.cfg.PerPartition }

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
	// Release grants every runnable waiter before it unlocks, so no queued
	// waiter is runnable here and canRun alone keeps FIFO order among them.
	if l.canRun(c, part) {
		p := l.take(part)
		l.mu.Unlock()
		return p, nil
	}
	if c == Background {
		l.mu.Unlock()
		return nil, ErrShed
	}
	if len(l.queue) >= l.cfg.MaxQueue {
		l.mu.Unlock()
		return nil, ErrQueueFull
	}
	w := &waiter{part: part, class: c, ready: make(chan struct{})}
	l.queue = append(l.queue, w)
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
	if w.granted { // Release won the race: the slot is ours, keep it.
		return &Permit{l: l, part: part}, nil
	}
	if i := slices.Index(l.queue, w); i >= 0 {
		l.queue = slices.Delete(l.queue, i, i+1)
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
	// ponytail: O(queue) walk per release, at most MaxQueue (1024) waiters;
	// per-partition queues if BenchmarkLimiterAcquireRelease says it matters.
	kept := l.queue[:0]
	for _, w := range l.queue {
		if l.canRun(w.class, w.part) {
			l.inflight++
			l.byPart[w.part]++
			w.granted = true
			close(w.ready)
			continue
		}
		kept = append(kept, w)
	}
	clear(l.queue[len(kept):])
	l.queue = kept
}
