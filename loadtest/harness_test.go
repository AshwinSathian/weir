//go:build load

package loadtest

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/bits"
	"math/rand/v2"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AshwinSathian/weir"
)

// scale shortens long phases for smoke runs (WEIR_LOAD_SCALE=0.1).
var scale = func() float64 {
	if v, err := strconv.ParseFloat(os.Getenv("WEIR_LOAD_SCALE"), 64); err == nil && v > 0 && v <= 1 {
		return v
	}
	return 1
}()

// dur scales d but never below floor.
func dur(d, floor time.Duration) time.Duration {
	return max(time.Duration(float64(d)*scale), floor)
}

// capOrigin is the origin of 07 §9: a semaphore of capacity N and a service
// time distribution. It also keeps the bounds INV-7 talks about: the peak
// in-flight total and per path, and a call counter sampled once a second.
type capOrigin struct {
	sem     chan struct{}
	service func(path string) time.Duration
	header  func(path string) http.Header

	mu       sync.Mutex
	inflight int
	peak     int
	part     map[string]int // bounded by the distinct paths a scenario uses (at most 10 000)
	partPeak map[string]int

	calls  atomic.Int64
	maxSvc atomic.Int64 // slowest service time drawn, ns
}

func newCapOrigin(capacity int, service func(string) time.Duration, header func(string) http.Header) *capOrigin {
	return &capOrigin{
		sem: make(chan struct{}, capacity), service: service, header: header,
		part: map[string]int{}, partPeak: map[string]int{},
	}
}

func (o *capOrigin) Fetch(ctx context.Context, req *weir.Request) (*weir.Response, error) {
	o.calls.Add(1)
	o.mu.Lock()
	o.inflight++
	o.peak = max(o.peak, o.inflight)
	o.part[req.Path]++
	o.partPeak[req.Path] = max(o.partPeak[req.Path], o.part[req.Path])
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		o.inflight--
		o.part[req.Path]--
		o.mu.Unlock()
	}()
	select {
	case o.sem <- struct{}{}:
		defer func() { <-o.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	svc := o.service(req.Path)
	for old := o.maxSvc.Load(); int64(svc) > old && !o.maxSvc.CompareAndSwap(old, int64(svc)); old = o.maxSvc.Load() {
	}
	t := time.NewTimer(svc)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &weir.Response{
		StatusCode: 200, Header: o.header(req.Path),
		Body: io.NopCloser(strings.NewReader("payload-payload-payload-payload")),
	}, nil
}

func (o *capOrigin) peakInflight() int { o.mu.Lock(); defer o.mu.Unlock(); return o.peak }
func (o *capOrigin) peakPartition(path string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.partPeak[path]
}

// svcTime draws a service time around median: lognormal with sigma 0.4, so
// the tail is long enough to matter and the median stays put.
func svcTime(median time.Duration) time.Duration {
	return time.Duration(float64(median) * math.Exp(0.4*rand.NormFloat64()))
}

func cacheControl(v string) func(string) http.Header {
	return func(string) http.Header { return http.Header{"Cache-Control": {v}} }
}

// hist is a log-linear latency histogram (8 sub-buckets per power of two,
// so a quantile is exact to 12.5%). One per goroutine, merged at the end,
// which keeps a 60 s steady run from holding tens of millions of samples.
type hist struct {
	b   [64 * 8]uint32
	n   uint64
	max time.Duration
}

func (h *hist) add(d time.Duration) {
	ns := uint64(max(d, 1))
	e := bits.Len64(ns) - 1
	sub := 0
	if e >= 3 {
		sub = int(ns>>(e-3)) & 7
	}
	h.b[e*8+sub]++
	h.n++
	h.max = max(h.max, d)
}

func (h *hist) merge(o *hist) {
	for i, c := range o.b {
		h.b[i] += c
	}
	h.n += o.n
	h.max = max(h.max, o.max)
}

// quantile returns the upper bound of the bucket holding the q-th sample.
func (h *hist) quantile(q float64) time.Duration {
	if h.n == 0 {
		return 0
	}
	want := uint64(math.Ceil(q * float64(h.n)))
	var seen uint64
	for i, c := range h.b {
		seen += uint64(c)
		if seen >= want {
			e, sub := i/8, i%8
			if e < 3 {
				return time.Duration(1<<e + 1)
			}
			return time.Duration((uint64(8+sub+1) << (e - 3)))
		}
	}
	return h.max
}

// counters tallies Serve outcomes for one phase.
type counters struct {
	total, hits, stale, errs, shed, dropped, offered atomic.Int64
	statuses                                         [6]atomic.Int64 // by status/100
}

// serveOnce runs one request and records its latency and outcome. The body
// is drained inside the measured span: the reader pays for it too.
func serveOnce(e *weir.Engine, o weir.Origin, path, query string, h *hist, c *counters) {
	req := &weir.Request{Method: "GET", Scheme: "https", Host: "load.test", Path: path, RawQuery: query, Header: http.Header{}}
	start := time.Now()
	resp, err := e.Serve(context.Background(), req, o)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if h != nil {
		h.add(time.Since(start))
	}
	c.total.Add(1)
	switch {
	case err != nil:
		c.errs.Add(1)
	default:
		if resp.Cache.Hit {
			c.hits.Add(1)
		}
		if resp.Cache.Stale != 0 {
			c.stale.Add(1)
		}
		c.statuses[min(resp.StatusCode/100, 5)].Add(1)
	}
}

// closedLoop runs n goroutines of fn until stop closes and returns their merged histogram.
func closedLoop(n int, stop <-chan struct{}, fn func(r *rand.Rand, h *hist)) *hist {
	hs := make([]*hist, n)
	var wg sync.WaitGroup
	for i := range n {
		hs[i] = &hist{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewPCG(uint64(i)+1, 99))
			for {
				select {
				case <-stop:
					return
				default:
				}
				fn(r, hs[i])
			}
		}()
	}
	wg.Wait()
	all := &hist{}
	for _, h := range hs {
		all.merge(h)
	}
	return all
}

// openLoop issues rps requests per second for d, each in its own goroutine,
// at most maxOut at a time (the excess is counted in c.dropped). It returns
// the merged histogram once every issued request has finished.
func openLoop(rps int, d time.Duration, maxOut int, fn func(r *rand.Rand, h *hist, i int), c *counters) *hist {
	const tick = 5 * time.Millisecond
	var (
		mu  sync.Mutex
		all = &hist{}
		wg  sync.WaitGroup
		out atomic.Int64
	)
	end := time.Now().Add(d)
	tk := time.NewTicker(tick)
	defer tk.Stop()
	// Wall-clock accounting: a Ticker drops ticks when the process stalls,
	// which would silently lower the offered load.
	start := time.Now()
	var i int
	for now := range tk.C {
		if now.After(end) {
			break
		}
		for float64(i) < float64(rps)*now.Sub(start).Seconds() {
			i++
			if out.Load() >= int64(maxOut) {
				c.dropped.Add(1)
				continue
			}
			out.Add(1)
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				defer out.Add(-1)
				h := &hist{}
				fn(rand.New(rand.NewPCG(uint64(i), 7)), h, i)
				mu.Lock()
				all.merge(h)
				mu.Unlock()
			}(i)
		}
	}
	wg.Wait()
	c.offered.Add(int64(i))
	return all
}

// sampler records goroutines once a second and, when e is set, the breaker
// state and per-second origin calls.
type sampler struct {
	stop   chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	gor    []int
	calls  []int64
	states []weir.BreakerState
	peakQ  int
}

func startSampler(e *weir.Engine, o *capOrigin) *sampler {
	s := &sampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		tk := time.NewTicker(time.Second)
		defer tk.Stop()
		var last int64
		if o != nil {
			last = o.calls.Load() // calls made before sampling started are not part of any second
		}
		for {
			select {
			case <-s.stop:
				return
			case <-tk.C:
			}
			s.mu.Lock()
			s.gor = append(s.gor, runtime.NumGoroutine())
			if o != nil {
				n := o.calls.Load()
				s.calls = append(s.calls, n-last)
				last = n
			}
			if e != nil {
				st := e.Stats()
				s.states = append(s.states, st.BreakerState)
				s.peakQ = max(s.peakQ, st.Queued)
			}
			s.mu.Unlock()
		}
	}()
	return s
}

func (s *sampler) peakGoroutines() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.gor) == 0 {
		return 0
	}
	return slices.Max(s.gor)
}

func (s *sampler) close() { close(s.stop); <-s.done }

func (s *sampler) peakCalls() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return 0
	}
	return slices.Max(s.calls)
}

func (s *sampler) sawBreaker(st weir.BreakerState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.states, st)
}

// settle fails the test unless the goroutine count returns to base ±10
// within 5 s (07 §9), sampled every 100 ms.
func settle(t *testing.T, base int) int {
	t.Helper()
	var n int
	for end := time.Now().Add(5 * time.Second); ; {
		n = runtime.NumGoroutine()
		if n <= base+10 {
			return n
		}
		if time.Now().After(end) {
			t.Errorf("goroutines did not return to baseline: base %d, now %d after 5s", base, n)
			buf := make([]byte, 1<<16)
			t.Logf("%s", buf[:runtime.Stack(buf, true)])
			return n
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// baseline returns the goroutine count after letting earlier tests' goroutines go.
func baseline() int {
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	return runtime.NumGoroutine()
}

func newEngine(t *testing.T, cfg weir.Config) *weir.Engine {
	t.Helper()
	e, err := weir.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func closeEngine(t *testing.T, e *weir.Engine) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Close(ctx); err != nil {
		t.Errorf("close: %v", err)
	}
}

// table prints a summary table (07 §9: each scenario prints one).
func table(t *testing.T, title string, rows ...[2]string) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s (scale %g, %d CPU, %s)\n", title, scale, runtime.NumCPU(), runtime.Version())
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-34s %s\n", r[0], r[1])
	}
	t.Log(b.String())
}

func row(k string, v any) [2]string { return [2]string{k, fmt.Sprint(v)} }
