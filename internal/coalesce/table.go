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
// ready to use. Its size is bounded by the limiter: at most
// MaxConcurrent + MaxQueue flights exist at once (P5).
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
	done        chan struct{}
	res         any // set before done is closed; read only after <-done
	started     time.Time
	stream      atomic.Uint32
	creatorGone atomic.Bool
	published   atomic.Bool // with creatorGone, a store-then-load pair on each side (04 §6.4)

	sh  *shard // back-pointer so Publish removes only itself (03 §3.3)
	key store.Key
}

// Join returns the joinable flight for k, or creates one and reports
// created. A flight at least maxAge old is aged (FR-COA-3): it keeps
// running but is replaced in the table and no longer joined.
func (t *Table) Join(k store.Key, now time.Time, maxAge time.Duration) (f *Flight, created bool) {
	sh := &t.shards[k[0]&63]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if f := sh.m[k]; f != nil && now.Sub(f.started) < maxAge {
		return f, false
	}
	if sh.m == nil {
		sh.m = make(map[store.Key]*Flight)
	}
	f = &Flight{done: make(chan struct{}), started: now, sh: sh, key: k}
	sh.m[k] = f
	return f, true
}

// Done is closed when the flight publishes its result.
func (f *Flight) Done() <-chan struct{} { return f.done }

// Result returns the published result. Call it only after Done is closed.
func (f *Flight) Result() any { return f.res }

// Publish stores res, removes the flight from the table if it is still the
// current entry for its key, and closes Done. The flight's owner calls it
// exactly once.
func (f *Flight) Publish(res any) {
	f.res = res
	f.published.Store(true)
	f.sh.mu.Lock()
	if f.sh.m[f.key] == f {
		delete(f.sh.m, f.key)
	}
	f.sh.mu.Unlock()
	close(f.done)
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
	return f.published.Load()
}

// CreatorIsGone reports whether CreatorGone was called.
func (f *Flight) CreatorIsGone() bool { return f.creatorGone.Load() }
