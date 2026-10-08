//go:build load

package loadtest

import (
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/store/memory"
)

// bodyOrigin answers every path with a cacheable 1 KiB body and no
// bookkeeping, so filling a million keys does not grow test-side maps.
type bodyOrigin struct{ body string }

func (o bodyOrigin) Fetch(context.Context, *weir.Request) (*weir.Response, error) {
	return &weir.Response{
		StatusCode: 200,
		Header:     http.Header{"Cache-Control": {"max-age=3600"}, "Content-Type": {"text/plain"}},
		Body:       io.NopCloser(strings.NewReader(o.body)),
	}, nil
}

type gcSample struct{ gc, idle, total float64 }

func readGC() gcSample {
	s := []metrics.Sample{{Name: "/cpu/classes/gc/total:cpu-seconds"}, {Name: "/cpu/classes/idle:cpu-seconds"}, {Name: "/cpu/classes/total:cpu-seconds"}}
	metrics.Read(s)
	return gcSample{s[0].Value.Float64(), s[1].Value.Float64(), s[2].Value.Float64()}
}

// gcResult is one phase of TestGCAt1MEntries.
type gcResult struct {
	heapMiB     uint64
	hitRatio    float64
	h           *hist
	cpuPerCycle float64 // GC CPU seconds per cycle, measured over forced cycles under load
	allocPerSec float64 // bytes allocated per second of load
	busyCPU     float64 // non-idle CPU seconds per second of load
	liveBytes   float64
}

// projectedShare is the GC share of busy CPU in steady state: with GOGC=100
// a cycle starts each time the program allocates as much as the live heap.
func (r gcResult) projectedShare() float64 {
	cyclesPerSec := r.allocPerSec / r.liveBytes
	return r.cpuPerCycle * cyclesPerSec / r.busyCPU
}

// gcPhase fills an engine with n keys of 1 KiB, then reads random keys at
// rps for d. The runtime updates its CPU class metrics only when a cycle
// ends, and at 20 000 hits/s a 2.7 GiB heap needs minutes to trigger one,
// so the phase forces a cycle every few seconds under load: that measures
// the cost per cycle (and puts the cycles' effect into the p99), and the
// result projects the natural frequency from the allocation rate.
func gcPhase(t *testing.T, n, rps int, d time.Duration) gcResult {
	t.Helper()
	st, err := memory.New(memory.Config{MaxBytes: 8 << 30})
	if err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, weir.Config{Store: st})
	org := bodyOrigin{strings.Repeat("x", 1024)}

	var next atomic.Int64
	var fill counters
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				serveOnce(e, org, pathN("k", i), "", nil, &fill)
			}
		})
	}
	wg.Wait()
	runtime.GC() // fill garbage is not the steady-state cost
	var ms0, ms1 runtime.MemStats
	runtime.ReadMemStats(&ms0)

	var c counters
	start, before := time.Now(), readGC()
	stop := make(chan struct{})
	time.AfterFunc(d, func() { close(stop) })
	var cycles int
	var cycleCPU float64
	done := make(chan struct{})
	go func() {
		defer close(done)
		tk := time.NewTicker(max(d/6, time.Second))
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				b := readGC()
				runtime.GC()
				cycleCPU += readGC().gc - b.gc
				cycles++
			}
		}
	}()
	h := closedLoopPaced(64, rps, stop, func(r *rand.Rand, h *hist) {
		serveOnce(e, org, pathN("k", r.IntN(n)), "", h, &c)
	})
	<-done
	elapsed := time.Since(start).Seconds()
	after := readGC()
	runtime.ReadMemStats(&ms1)
	closeEngine(t, e)

	// Busy CPU excludes the forced cycles' GC time so the projection does
	// not count them twice.
	busy := (after.total - before.total) - (after.idle - before.idle) - cycleCPU
	return gcResult{
		heapMiB: ms0.HeapInuse >> 20, hitRatio: float64(c.hits.Load()) / float64(c.total.Load()), h: h,
		cpuPerCycle: cycleCPU / float64(max(cycles, 1)),
		allocPerSec: float64(ms1.TotalAlloc-ms0.TotalAlloc) / elapsed,
		busyCPU:     busy / elapsed,
		liveBytes:   float64(ms0.HeapAlloc),
	}
}

// TestGCAt1MEntries measures the cost of keeping 1M entries on the Go heap
// (D36, card M10-05): the GC share of CPU and the hit p99 at a fixed
// request rate, next to the same load on a 10 000-entry store. Not a gate on
// latency; the one assertion is the 10% GC budget that decides whether a
// pointer-light layout card is needed. Machine-dependent: the reference
// machine's numbers are the ones docs/benchmarks.md keeps.
func TestGCAt1MEntries(t *testing.T) {
	n := 1_000_000
	if v, err := strconv.Atoi(os.Getenv("WEIR_GC_ENTRIES")); err == nil && v > 0 {
		n = v
	}
	const rps = 20000
	d := dur(30*time.Second, 5*time.Second)

	small := gcPhase(t, 10000, rps, d)
	large := gcPhase(t, n, rps, d)
	pair := func(f func(gcResult) string) string { return f(small) + " / " + f(large) }
	table(t, "gc at scale",
		row("entries (small / large)", "10000 / "+strconv.Itoa(n)),
		row("offered rate", rps),
		row("heap in use after fill", pair(func(r gcResult) string { return strconv.FormatUint(r.heapMiB, 10) + " MiB" })),
		row("hit ratio", pair(func(r gcResult) string { return strconv.FormatFloat(r.hitRatio, 'f', 4, 64) })),
		row("GC CPU per cycle", pair(func(r gcResult) string { return strconv.FormatFloat(r.cpuPerCycle*1000, 'f', 1, 64) + " ms" })),
		row("alloc rate", pair(func(r gcResult) string { return strconv.FormatFloat(r.allocPerSec/(1<<20), 'f', 1, 64) + " MiB/s" })),
		row("busy CPU (cores, excl. forced GC)", pair(func(r gcResult) string { return strconv.FormatFloat(r.busyCPU, 'f', 2, 64) })),
		row("projected GC share of busy CPU", pair(func(r gcResult) string { return pct(r.projectedShare()) })),
		row("p50", pair(func(r gcResult) string { return r.h.quantile(0.5).String() })),
		row("p99 (forced cycle every ~5 s)", pair(func(r gcResult) string { return r.h.quantile(0.99).String() })),
		row("max", pair(func(r gcResult) string { return r.h.max.String() })),
	)
	if share := large.projectedShare(); share > 0.10 {
		t.Errorf("projected GC share %s of busy CPU at %d entries, over the 10%% budget: add the pointer-light layout card to docs/cards/11-phase1x.md", pct(share), n)
	}
}

func pct(f float64) string { return strconv.FormatFloat(f*100, 'f', 2, 64) + "%" }
