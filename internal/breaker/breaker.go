package breaker

import (
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen means the breaker is open, or half-open with every probe
// place taken. The engine maps it to its own ErrCircuitOpen.
var ErrCircuitOpen = errors.New("weir: circuit open")

// State is the breaker's state. The order matches weir.BreakerState.
type State uint8

// State values.
const (
	Closed State = iota
	HalfOpen
	Open
)

// String returns the state's event detail: closed, half-open or open.
func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case HalfOpen:
		return "half-open"
	default:
		return "open"
	}
}

// Outcome is the origin-health result of one fetch (ADR-7).
type Outcome uint8

// Outcome values.
const (
	// Success is any response that says the origin is up, 4xx included.
	Success Outcome = iota
	// Failure is a transport error, a timeout, 502, 503 or 504.
	Failure
	// Status500 counts as Failure only with Config.CountStatus500.
	Status500
)

// Config holds the breaker's settings. The engine fills defaults (04 §5).
type Config struct {
	Window         time.Duration // rolling window, split into 10 buckets
	MinRequests    int           // outcomes in the window before it may trip
	FailureRatio   float64       // failures / outcomes that trips it, (0, 1]
	OpenFor        time.Duration // first open period, before jitter
	MaxOpenFor     time.Duration // cap on the doubled open period
	HalfOpenProbes int           // concurrent probes while half-open
	// HalfOpenFor is how long a half-open period lasts before the probes
	// it admitted are given up and a new period starts (0: MaxOpenFor). It
	// must outlast the origin timeout, or a hanging origin gets a new probe
	// each period while the old ones still hang.
	HalfOpenFor time.Duration
	// RampTo makes closing gradual (FR-CB-4). Each probe that succeeds
	// doubles the probes admitted at a time, and the breaker closes when
	// that number reaches RampTo, or when a half-open period ends with a
	// success and no failure. At or below HalfOpenProbes the first success
	// closes it.
	RampTo         int
	CountStatus500 bool
	// MinPartitions is how many distinct partitions must have failed in the
	// window before it may trip, unless nothing in the window succeeded
	// (FR-CB-2). 1 or less turns the rule off.
	MinPartitions int
}

// bucketParts bounds the failing partitions one bucket remembers (P5). A
// bucket that saw more counts as "many": the rule is met.
const bucketParts = 8

// probeYield is how long a half-open period refuses a probe from a partition
// whose last probe failed, so another partition gets the first try (FR-CB-4).
// A site with one URL recovers this much later.
const probeYield = time.Second

type bucket struct {
	start    int64 // monotonic ns since the breaker was made
	ok, fail uint32
	parts    [bucketParts]uint64 // distinct partitions that failed, first nparts
	nparts   uint8
	many     bool // more than bucketParts partitions failed
}

// addPart records that partition part failed in this bucket.
func (bk *bucket) addPart(part uint64) {
	for _, p := range bk.parts[:bk.nparts] {
		if p == part {
			return
		}
	}
	if int(bk.nparts) == len(bk.parts) {
		bk.many = true
		return
	}
	bk.parts[bk.nparts] = part
	bk.nparts++
}

// Breaker is a failure-ratio circuit breaker over a rolling window
// (FR-CB-1 to FR-CB-4). A nil *Breaker allows everything and records
// nothing, so a disabled breaker needs no branches at call sites.
type Breaker struct {
	mu        sync.Mutex
	state     State
	buckets   [10]bucket
	base      time.Time     // bucket times are offsets from it, on the monotonic clock
	openUntil time.Time     // while Open: end of the period; while HalfOpen: when held probes are given up
	openFor   time.Duration // current, doubles on reopen
	probes    int           // in flight while HalfOpen
	allowed   int           // probes admitted at a time in this half-open period; doubles per success
	good      bool          // a probe of this half-open period succeeded
	gen       uint64        // counts half-open periods; a Probe names its period
	halfSince time.Time     // when the current half-open period began
	ended     chan struct{} // closed when a probe ends or the period does; nil until a caller waits
	// yield holds the partitions whose probes failed since the breaker last
	// closed, newest last, at most bucketParts (FR-CB-4).
	yield    [bucketParts]uint64
	nyield   int
	cfg      Config
	rnd      func() float64
	onChange func(from, to State)
}

// Probe is the ticket Allow returns. A zero Probe is an ordinary fetch;
// a non-zero one is a half-open probe of period gen.
type Probe struct{ gen uint64 }

// New returns a closed breaker. rnd returns values in [0, 1) for the open
// jitter and is called once per open period, under the lock; onChange, when not nil, is called after every transition, outside
// the lock (FR-CB-6). Concurrent transitions may reach onChange out of
// order, so a consumer that needs the current state reads State.
func New(cfg Config, rnd func() float64, onChange func(from, to State)) *Breaker {
	return &Breaker{cfg: cfg, rnd: rnd, onChange: onChange, openFor: cfg.OpenFor, base: time.Now()}
}

// State returns the current state, moving Open to HalfOpen when the open
// period has ended.
func (b *Breaker) State() State {
	if b == nil {
		return Closed
	}
	b.mu.Lock()
	from := b.state
	b.expire(time.Now())
	to := b.state
	b.mu.Unlock()
	b.notify(from, to)
	return to
}

// Peek returns the state State would, without moving the breaker or
// calling onChange. Gauges read it, so a poll never reports a transition.
func (b *Breaker) Peek() State {
	if b == nil {
		return Closed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == Open && !time.Now().Before(b.openUntil) {
		return HalfOpen
	}
	return b.state
}

// Allow is AllowFor without a partition.
func (b *Breaker) Allow() (Probe, error) { return b.AllowFor(0) }

// AllowFor admits a fetch for partition part (a hash) or returns
// ErrCircuitOpen (FR-CB-5). Every admitted fetch must end in Record,
// RecordFor or Cancel. While half-open, a partition whose last probe failed
// is not admitted as a probe during the first probeYield of the period: one
// failing route must not take every probe (FR-CB-4).
func (b *Breaker) AllowFor(part uint64) (Probe, error) {
	p, _, err := b.allow(part, false)
	return p, err
}

// AllowOrWait is AllowFor for a caller that can wait for a probe (FR-CB-4).
// When it refuses because the half-open period's probes are all in flight,
// it also returns a channel that is closed when one of them ends or the
// period does; the caller then calls again. The channel is nil for every
// other refusal: an open breaker and a yielding partition fail at once.
func (b *Breaker) AllowOrWait(part uint64) (Probe, <-chan struct{}, error) {
	return b.allow(part, true)
}

func (b *Breaker) allow(part uint64, wait bool) (Probe, <-chan struct{}, error) {
	if b == nil {
		return Probe{}, nil, nil
	}
	now := time.Now()
	b.mu.Lock()
	from := b.state
	b.expire(now)
	var p Probe
	var ended <-chan struct{}
	var err error
	switch {
	case b.state == Closed:
	case b.state == HalfOpen && b.yields(part) && now.Sub(b.halfSince) < min(probeYield, b.cfg.OpenFor/2):
		err = ErrCircuitOpen
	case b.state == HalfOpen && b.probes < b.allowed:
		b.probes++
		p = Probe{b.gen}
	case b.state == HalfOpen && wait:
		if b.ended == nil {
			b.ended = make(chan struct{})
		}
		ended, err = b.ended, ErrCircuitOpen
	default:
		err = ErrCircuitOpen
	}
	to := b.state
	b.mu.Unlock()
	b.notify(from, to)
	return p, ended, err
}

// wake releases the callers waiting in AllowOrWait. Caller holds b.mu.
func (b *Breaker) wake() {
	if b.ended != nil {
		close(b.ended)
		b.ended = nil
	}
}

// Remaining returns how long the current open period lasts, or 0 when the
// breaker is not open. The engine sends it as the Retry-After hint (04 §1.3).
func (b *Breaker) Remaining() time.Duration {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != Open {
		return 0
	}
	return max(time.Until(b.openUntil), 0)
}

// yields reports whether part's last probe failed. Never with MinPartitions
// off, which is also how callers without partitions use the breaker. Caller
// holds b.mu.
func (b *Breaker) yields(part uint64) bool {
	if b.cfg.MinPartitions <= 1 {
		return false
	}
	for _, y := range b.yield[:b.nyield] {
		if y == part {
			return true
		}
	}
	return false
}

// Record is RecordFor without a partition.
func (b *Breaker) Record(p Probe, o Outcome) { b.RecordFor(p, 0, o) }

// RecordFor counts the outcome of a fetch for partition part that AllowFor
// admitted. A probe's success closes the breaker and its failure reopens it
// (FR-CB-4). A probe from an earlier half-open period counts as an ordinary
// fetch.
func (b *Breaker) RecordFor(p Probe, part uint64, o Outcome) {
	if b == nil {
		return
	}
	fail := o == Failure || o == Status500 && b.cfg.CountStatus500
	now := time.Now()
	b.mu.Lock()
	from := b.state
	b.expire(now)
	switch {
	case b.state == HalfOpen && p.gen == b.gen && p.gen != 0:
		b.wake()
		if fail {
			d := b.openFor * 2
			if d <= 0 || d > b.cfg.MaxOpenFor { // <= 0: the doubling overflowed
				d = b.cfg.MaxOpenFor
			}
			b.open(now, d)
			if !b.yields(part) {
				if b.nyield == len(b.yield) { // drop the oldest
					copy(b.yield[:], b.yield[1:])
					b.nyield--
				}
				b.yield[b.nyield] = part
				b.nyield++
			}
		} else if b.allowed *= 2; b.allowed >= b.cfg.RampTo || b.allowed <= 0 { // <= 0: the doubling overflowed
			b.shut()
		} else {
			// The ramp: this probe is done, and twice as many may run.
			b.good = true
			b.probes--
		}
	case b.state == Closed:
		b.count(now, part, fail)
	}
	to := b.state
	b.mu.Unlock()
	b.notify(from, to)
}

// Cancel releases a fetch Allow admitted that never reached the origin
// (the limiter shed it, or its caller left first).
func (b *Breaker) Cancel(p Probe) {
	if b == nil || p.gen == 0 {
		return
	}
	b.mu.Lock()
	if b.state == HalfOpen && p.gen == b.gen && b.probes > 0 {
		b.probes--
		b.wake() // a waiter takes the freed probe
	}
	b.mu.Unlock()
}

// expire moves Open to HalfOpen once openUntil has passed. A half-open
// period lasting MaxOpenFor is replaced by a new one, so probes that never
// end in Record or Cancel cannot hold the breaker half-open forever; their
// late outcomes name the old period and are ignored.
//
// A half-open period in which a probe succeeded and none failed ends
// differently: the breaker closes. A site too quiet to fill the ramp to
// RampTo would otherwise stay half-open for as long as it stayed quiet.
func (b *Breaker) expire(now time.Time) {
	if b.state == Closed || now.Before(b.openUntil) {
		return
	}
	b.wake() // waiters of a period given up compete for the new one's probe
	if b.state == HalfOpen && b.good {
		b.shut()
		return
	}
	b.state = HalfOpen
	b.probes, b.allowed, b.good = 0, b.cfg.HalfOpenProbes, false
	b.gen++
	b.halfSince = now
	b.openUntil = now.Add(max(b.cfg.HalfOpenFor, b.cfg.MaxOpenFor))
}

// Ramping reports whether a probe of the current half-open period has
// succeeded, so that more than the first probe is being admitted (FR-CB-4).
func (b *Breaker) Ramping() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state == HalfOpen && b.good
}

// shut closes the breaker with an empty window: the next trip starts again
// from OpenFor (count). Caller holds b.mu.
func (b *Breaker) shut() {
	b.state = Closed
	b.buckets = [10]bucket{}
	b.nyield = 0
	b.good = false
}

// open starts an open period of d with ±20% jitter (FR-CB-3).
func (b *Breaker) open(now time.Time, d time.Duration) {
	b.state = Open
	b.openFor = d
	b.openUntil = now.Add(time.Duration(float64(d) * (0.8 + 0.4*b.rnd())))
}

// count adds one outcome to the current bucket and trips the breaker when
// the window holds MinRequests outcomes at FailureRatio, from at least
// MinPartitions partitions unless nothing in the window succeeded (FR-CB-2).
// Buckets rotate lazily: a slot whose start is not the current bucket's is
// reused.
func (b *Breaker) count(now time.Time, part uint64, fail bool) {
	// Times are monotonic offsets from base, so a wall-clock step cannot
	// misplace or resurrect a bucket, and n >= 0 keeps the slot index in
	// range. Start and slot come from the same number, so they agree for
	// any Window; width is at least 1 ns so a tiny Window cannot divide by
	// zero.
	n, w := int64(now.Sub(b.base)), max(int64(b.cfg.Window)/int64(len(b.buckets)), 1)
	start := n - n%w
	cur := &b.buckets[(n/w)%int64(len(b.buckets))]
	if cur.start != start {
		*cur = bucket{start: start}
	}
	if fail {
		cur.fail++
		cur.addPart(part)
	} else {
		cur.ok++
	}
	var ok, failed uint32
	for i := range b.buckets {
		if bk := &b.buckets[i]; n-bk.start < int64(b.cfg.Window) {
			ok += bk.ok
			failed += bk.fail
		}
	}
	total := ok + failed
	if int(total) >= b.cfg.MinRequests && float64(failed) >= b.cfg.FailureRatio*float64(total) &&
		(ok == 0 || b.failingParts(n) >= b.cfg.MinPartitions) {
		b.open(now, b.cfg.OpenFor)
	}
}

// failingParts counts the distinct partitions that failed in the window that
// ends at offset n, stopping at MinPartitions. A bucket that overflowed its
// set answers MinPartitions. Caller holds b.mu.
//
// ponytail: compares up to 10 buckets of 8 hashes pairwise, at most 80
// entries, and only on a failure that already met the ratio.
func (b *Breaker) failingParts(n int64) int {
	want := b.cfg.MinPartitions
	if want <= 1 {
		return want
	}
	var seen [len(b.buckets) * bucketParts]uint64
	k := 0
	for i := range b.buckets {
		bk := &b.buckets[i]
		if n-bk.start >= int64(b.cfg.Window) {
			continue
		}
		if bk.many {
			return want
		}
	next:
		for _, p := range bk.parts[:bk.nparts] {
			for _, s := range seen[:k] {
				if s == p {
					continue next
				}
			}
			seen[k] = p
			if k++; k >= want {
				return k
			}
		}
	}
	return k
}

func (b *Breaker) notify(from, to State) {
	if from != to && b.onChange != nil {
		b.onChange(from, to)
	}
}
