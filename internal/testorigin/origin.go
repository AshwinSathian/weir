// Package testorigin is a programmable weir.Origin for engine and load tests
// (docs/07-testing-strategy.md §3). Every wait blocks on channels or timers,
// so behaviors take fake time inside a synctest bubble.
package testorigin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/AshwinSathian/weir"
)

// ErrDown is the transport error every Fetch returns while SetDown(true) is in effect.
var ErrDown = errors.New("weir: test origin down")

// ErrOverConcurrency is returned by a checked Origin's Fetch when the call
// would exceed a concurrency bound.
var ErrOverConcurrency = errors.New("weir: test origin over concurrency bound")

// ErrBodyClosed is returned by a response body Read after Close, as
// net/http does.
var ErrBodyClosed = errors.New("weir: test origin read on closed body")

// Behavior is how the origin answers one path. Steps run in field order:
// Delay, Gate, Err, Panic, then Func or the static response. Func's
// response is returned as is: Status, Header, Body, Truncate and BodyDelay
// do not apply to it.
type Behavior struct {
	Status    int // 0 means 200
	Header    http.Header
	Body      []byte
	Delay     time.Duration   // before headers; fake time under synctest
	Gate      <-chan struct{} // if set, block until closed or ctx done
	Err       error           // return as a transport error
	Panic     bool
	Truncate  int                                         // body read fails after this many bytes (0 = off)
	BodyDelay time.Duration                               // delay between header and body bytes
	Func      func(*weir.Request) (*weir.Response, error) // full control
}

// Origin is a programmable weir.Origin. It is safe for concurrent use.
type Origin struct {
	tb            testing.TB // nil unless built by NewChecked
	maxConcurrent int
	maxPartition  int

	// ponytail: calls, partition and requests grow with distinct paths and
	// calls (requests keeps a header clone per call). Fine for unit tests;
	// loadtest calls Reset between phases, or add a recording switch if
	// that is not enough.
	mu          sync.Mutex
	routes      map[string]Behavior
	def         Behavior
	down        bool
	calls       map[string]int
	total       int
	inflight    int
	partition   map[string]int // in-flight calls per path
	maxInflight int
	maxPart     int
	requests    []*weir.Request
}

// New returns an Origin that answers 200 with an empty body until configured.
func New() *Origin {
	return &Origin{routes: map[string]Behavior{}, calls: map[string]int{}, partition: map[string]int{}}
}

// NewChecked returns an Origin that asserts INV-7 on every call: a Fetch that
// would put more than maxConcurrent calls in flight, or more than
// maxPerPartition for one path, fails tb and returns ErrOverConcurrency.
// Both bounds must be at least 1; a bound of 0 rejects every call.
func NewChecked(tb testing.TB, maxConcurrent, maxPerPartition int) *Origin {
	o := New()
	o.tb, o.maxConcurrent, o.maxPartition = tb, maxConcurrent, maxPerPartition
	return o
}

// Route sets the behavior for an exact path; Default applies otherwise.
func (o *Origin) Route(path string, b Behavior) {
	b = copyBehavior(b)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.routes[path] = b
}

// Default sets the behavior for paths without a Route.
func (o *Origin) Default(b Behavior) {
	b = copyBehavior(b)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.def = b
}

// copyBehavior detaches b from the caller's Header map and Body slice, so a
// test editing them later cannot race with Fetch on engine goroutines.
func copyBehavior(b Behavior) Behavior {
	b.Header = b.Header.Clone()
	b.Body = slices.Clone(b.Body)
	return b
}

// SetDown makes every call return ErrDown while down is true.
func (o *Origin) SetDown(down bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.down = down
}

// Calls returns the number of Fetch calls for path.
func (o *Origin) Calls(path string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls[path]
}

// TotalCalls returns the number of Fetch calls for all paths.
func (o *Origin) TotalCalls() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.total
}

// MaxInflight returns the high-water mark of concurrent Fetch calls.
func (o *Origin) MaxInflight() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.maxInflight
}

// MaxInflightPartition returns the high-water mark of concurrent Fetch calls
// for any one partition. The partition is the request path.
func (o *Origin) MaxInflightPartition() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.maxPart
}

// Requests returns every request Fetch received, in call order, for INV-1
// checks. Each is a copy taken at call time with a cloned Header.
func (o *Origin) Requests() []*weir.Request {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]*weir.Request(nil), o.requests...)
}

// Reset clears call counts, high-water marks and recorded requests. Routes,
// the default and the down state stay.
func (o *Origin) Reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	clear(o.calls)
	o.total, o.maxInflight, o.maxPart = 0, 0, 0
	o.requests = nil
}

// Fetch implements weir.Origin.
func (o *Origin) Fetch(ctx context.Context, req *weir.Request) (*weir.Response, error) {
	b, err := o.enter(req)
	if err != nil {
		return nil, err
	}
	defer o.exit(req.Path)

	// Like an http.Client, a done context fails the call even with no wait.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.Delay > 0 {
		if err := sleep(ctx, b.Delay, nil); err != nil {
			return nil, err
		}
	}
	if b.Gate != nil {
		select {
		case <-b.Gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if b.Err != nil {
		return nil, b.Err
	}
	if b.Panic {
		panic("testorigin: Panic behavior")
	}
	if b.Func != nil {
		return b.Func(req)
	}
	status := b.Status
	if status == 0 {
		status = http.StatusOK
	}
	data := b.Body
	if b.Truncate > 0 {
		data = data[:min(b.Truncate, len(data))]
	}
	header := b.Header.Clone() // values too: callers may append to them
	if header == nil {
		header = http.Header{}
	}
	return &weir.Response{
		StatusCode: status,
		Header:     header,
		Body: &body{ctx: ctx, delay: b.BodyDelay, r: bytes.NewReader(data), truncated: b.Truncate > 0,
			closed: make(chan struct{})},
	}, nil
}

// enter records the call and returns the behavior for it.
func (o *Origin) enter(req *weir.Request) (Behavior, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	snap := *req
	snap.Header = req.Header.Clone()
	o.requests = append(o.requests, &snap)
	o.calls[req.Path]++
	o.total++
	if o.down {
		return Behavior{}, ErrDown
	}
	if o.tb != nil && (o.inflight+1 > o.maxConcurrent || o.partition[req.Path]+1 > o.maxPartition) {
		// Errorf, not Fatalf: Fetch runs on engine goroutines.
		o.tb.Errorf("INV-7: Fetch %s would make %d in flight (max %d), %d for the partition (max %d)",
			req.Path, o.inflight+1, o.maxConcurrent, o.partition[req.Path]+1, o.maxPartition)
		return Behavior{}, ErrOverConcurrency
	}
	o.inflight++
	o.partition[req.Path]++
	o.maxInflight = max(o.maxInflight, o.inflight)
	o.maxPart = max(o.maxPart, o.partition[req.Path])
	b, ok := o.routes[req.Path]
	if !ok {
		b = o.def
	}
	return b, nil
}

func (o *Origin) exit(path string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.inflight--
	if o.partition[path]--; o.partition[path] == 0 {
		delete(o.partition, path)
	}
}

// sleep waits d, or until ctx is done or closed is closed. A nil closed
// never fires.
func sleep(ctx context.Context, d time.Duration, closed <-chan struct{}) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-closed:
		return ErrBodyClosed
	}
}

// body is a response body that waits delay before its first byte and, when
// truncated, fails with io.ErrUnexpectedEOF instead of ending cleanly. Like
// an http.Client body, reads fail once ctx is done or after Close, and Close
// unblocks a pending read (the engine aborts stalled streams that way).
type body struct {
	ctx       context.Context // kept past Fetch on purpose: an http.Client body also fails once its request context ends
	delay     time.Duration
	r         *bytes.Reader
	truncated bool
	closed    chan struct{}
	once      sync.Once
}

func (b *body) Read(p []byte) (int, error) {
	select {
	case <-b.closed:
		return 0, ErrBodyClosed
	default:
	}
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if b.delay > 0 {
		if err := sleep(b.ctx, b.delay, b.closed); err != nil {
			return 0, err
		}
		b.delay = 0
	}
	n, err := b.r.Read(p)
	if err == io.EOF && b.truncated {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func (b *body) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}
