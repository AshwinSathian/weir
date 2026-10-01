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
	CountStatus500 bool
}

type bucket struct {
	start    time.Time
	ok, fail uint32
}

// Breaker is a failure-ratio circuit breaker over a rolling window
// (FR-CB-1 to FR-CB-4). A nil *Breaker allows everything and records
// nothing, so a disabled breaker needs no branches at call sites.
type Breaker struct {
	mu        sync.Mutex
	state     State
	buckets   [10]bucket
	openUntil time.Time
	openFor   time.Duration // current, doubles on reopen
	probes    int           // in flight while HalfOpen
	gen       uint64        // counts half-open periods; a Probe names its period
	cfg       Config
	rnd       func() float64
	onChange  func(from, to State)
}

// Probe is the ticket Allow returns. A zero Probe is an ordinary fetch;
// a non-zero one is a half-open probe of period gen.
type Probe struct{ gen uint64 }

// New returns a closed breaker. rnd returns values in [0, 1) for the open
// jitter; onChange, when not nil, is called after every transition, outside
// the lock (FR-CB-6). Concurrent transitions may reach onChange out of
// order, so a consumer that needs the current state reads State.
func New(cfg Config, rnd func() float64, onChange func(from, to State)) *Breaker {
	return &Breaker{cfg: cfg, rnd: rnd, onChange: onChange, openFor: cfg.OpenFor}
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

// Allow admits a fetch or returns ErrCircuitOpen (FR-CB-5). Every admitted
// fetch must end in Record or Cancel.
func (b *Breaker) Allow() (Probe, error) {
	if b == nil {
		return Probe{}, nil
	}
	b.mu.Lock()
	from := b.state
	b.expire(time.Now())
	var p Probe
	var err error
	switch {
	case b.state == Closed:
	case b.state == HalfOpen && b.probes < b.cfg.HalfOpenProbes:
		b.probes++
		p = Probe{b.gen}
	default:
		err = ErrCircuitOpen
	}
	to := b.state
	b.mu.Unlock()
	b.notify(from, to)
	return p, err
}

// Record counts the outcome of a fetch Allow admitted. A probe's success
// closes the breaker and its failure reopens it (FR-CB-4). A probe from an
// earlier half-open period counts as an ordinary fetch.
func (b *Breaker) Record(p Probe, o Outcome) {
	if b == nil {
		return
	}
	fail := o == Failure || o == Status500 && b.cfg.CountStatus500
	r := b.rnd() // drawn before the lock: Config.Rand is caller code
	now := time.Now()
	b.mu.Lock()
	from := b.state
	b.expire(now)
	switch {
	case b.state == HalfOpen && p.gen == b.gen && p.gen != 0:
		if fail {
			b.open(now, min(b.openFor*2, b.cfg.MaxOpenFor), r)
		} else {
			b.state = Closed
			b.buckets = [10]bucket{}
			b.openFor = b.cfg.OpenFor
		}
	case b.state == Closed:
		b.count(now, fail, r)
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
	}
	b.mu.Unlock()
}

// expire moves Open to HalfOpen once openUntil has passed.
func (b *Breaker) expire(now time.Time) {
	if b.state == Open && !now.Before(b.openUntil) {
		b.state = HalfOpen
		b.probes = 0
		b.gen++
	}
}

// open starts an open period of d with ±20% jitter (FR-CB-3).
func (b *Breaker) open(now time.Time, d time.Duration, r float64) {
	b.state = Open
	b.openFor = d
	b.openUntil = now.Add(time.Duration(float64(d) * (0.8 + 0.4*r)))
}

// count adds one outcome to the current bucket and trips the breaker when
// the window holds MinRequests outcomes at FailureRatio (FR-CB-2). Buckets
// rotate lazily: a slot whose start is not the current bucket's is reused.
func (b *Breaker) count(now time.Time, fail bool, r float64) {
	// Start and slot come from the same number, so they agree for any
	// Window; width is at least 1 ns so a tiny Window cannot divide by zero.
	n, w := now.UnixNano(), max(int64(b.cfg.Window)/int64(len(b.buckets)), 1)
	start := time.Unix(0, n-n%w)
	cur := &b.buckets[(n/w)%int64(len(b.buckets))]
	if !cur.start.Equal(start) {
		*cur = bucket{start: start}
	}
	if fail {
		cur.fail++
	} else {
		cur.ok++
		return
	}
	var ok, failed uint32
	for _, bk := range b.buckets {
		if now.Sub(bk.start) < b.cfg.Window {
			ok += bk.ok
			failed += bk.fail
		}
	}
	total := ok + failed
	if int(total) >= b.cfg.MinRequests && float64(failed) >= b.cfg.FailureRatio*float64(total) {
		b.open(now, b.cfg.OpenFor, r)
	}
}

func (b *Breaker) notify(from, to State) {
	if from != to && b.onChange != nil {
		b.onChange(from, to)
	}
}
