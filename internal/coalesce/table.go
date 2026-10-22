package coalesce

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// Stream hand-off states (04 §6.4). Both ClaimStream and AbandonStream move
// out of streamUnclaimed, so exactly one of them wins.
const (
	streamUnclaimed uint32 = iota
	streamClaimed
	streamAbandoned
)

// Table maps coalescing keys to in-progress flights. The zero value is
// ready to use. Its size is bounded by the limiter and the breaker's
// waiters: at most MaxConcurrent + MaxQueue + MaxQueue/4 flights exist at
// once (P5, 04 §8.3).
type Table struct {
	shards [64]shard // index: first key byte & 63; keys are SHA-256 output
}

type shard struct {
	mu sync.Mutex
	m  map[store.Key]*Flight
}

// Flight is one shared origin fetch. Waiters block on Done and read Result
// after it closes.
type Flight struct {
	done chan struct{}
	// mu orders Join, Leave and Publish, so the waiter count a Publish
	// hands its result is exact: every request it counts later either
	// takes the result or calls Leave and gets it back (FR-COA-10).
	mu          sync.Mutex
	res         any // set under mu before done is closed; read only after <-done or from Leave
	waiters     int // requests that will take the result: the creator, then each Join
	sealed      bool
	started     time.Time
	stream      atomic.Uint32
	creatorGone atomic.Bool
	published   atomic.Bool // with creatorGone, a store-then-load pair on each side (04 §6.4)

	sh  *shard // back-pointer so Publish removes only itself (03 §3.3)
	key store.Key
}

// Join returns the joinable flight for k, or creates one and reports
// created. Every Join counts a request that takes the result: it must read
// Result after Done, or call Leave (CreatorGone for a creator). A flight at least maxAge old is aged (FR-COA-3): it keeps
// running but is replaced in the table and no longer joined.
func (t *Table) Join(k store.Key, now time.Time, maxAge time.Duration) (f *Flight, created bool) {
	sh := &t.shards[k[0]&63]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if f := sh.m[k]; f != nil && now.Sub(f.started) < maxAge {
		f.mu.Lock()
		f.waiters++
		f.mu.Unlock()
		return f, false
	}
	if sh.m == nil {
		sh.m = make(map[store.Key]*Flight)
	}
	f = &Flight{done: make(chan struct{}), started: now, sh: sh, key: k, waiters: 1}
	sh.m[k] = f
	return f, true
}

// NewFlight returns a flight outside any table, for a fetch no one may
// join that still needs runFlight's publishing and recovery.
func NewFlight() *Flight { return &Flight{done: make(chan struct{}), waiters: 1} }

// Len returns the number of joinable flights in the table.
func (t *Table) Len() int {
	n := 0
	for i := range t.shards {
		sh := &t.shards[i]
		sh.mu.Lock()
		n += len(sh.m)
		sh.mu.Unlock()
	}
	return n
}

// Done is closed when the flight publishes its result.
func (f *Flight) Done() <-chan struct{} { return f.done }

// Result returns the published result. Call it only after Done is closed.
func (f *Flight) Result() any { return f.res }

// Publish stores res, removes the flight from the table if it is still the
// current entry for its key, and closes Done. The flight's owner calls it
// exactly once.
func (f *Flight) Publish(res any) { f.PublishCounted(res, nil) }

// PublishCounted is Publish that first calls counted, when not nil, with the
// number of requests that will take res: each either reads Result after
// Done or gets res back from Leave. No request joins after it is called.
func (f *Flight) PublishCounted(res any, counted func(waiters int)) {
	if f.sh != nil {
		f.sh.mu.Lock()
		if f.sh.m[f.key] == f {
			delete(f.sh.m, f.key)
		}
		f.sh.mu.Unlock()
	}
	f.mu.Lock()
	if counted != nil {
		counted(f.waiters)
	}
	f.res, f.sealed = res, true
	f.mu.Unlock()
	f.published.Store(true)
	close(f.done)
}

// Leave takes a request that has stopped waiting out of the count. When the
// flight has already published, the request was counted and gets the result
// back, to release what it holds (a shared stream).
func (f *Flight) Leave() (res any, published bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sealed {
		return f.res, true
	}
	f.waiters--
	return nil, false
}

// ClaimStream reports whether the creator won an oversized response stream.
// It and AbandonStream succeed at most once between them.
func (f *Flight) ClaimStream() bool {
	return f.stream.CompareAndSwap(streamUnclaimed, streamClaimed)
}

// AbandonStream reports whether the flight goroutine won the stream and
// must close it because no requester will read it.
func (f *Flight) AbandonStream() bool {
	return f.stream.CompareAndSwap(streamUnclaimed, streamAbandoned)
}

// CreatorGone records that the creating request stopped waiting (timeout or
// cancellation), so no one will claim a stream. It reports whether the
// flight had already published: runFlight may then have checked
// CreatorIsGone too early, so the creator must abandon the stream itself.
// Both sides store their own flag before loading the other's, so at least
// one of them sees both set.
func (f *Flight) CreatorGone() (published bool) {
	f.creatorGone.Store(true)
	_, published = f.Leave()
	return published
}

// CreatorIsGone reports whether CreatorGone was called.
func (f *Flight) CreatorIsGone() bool { return f.creatorGone.Load() }
