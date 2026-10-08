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
	liveMiB     uint64
	hitRatio    float64
	h           *hist // paced load with forced cycles
	cycles      int   // forced cycles in the paced load
	cadence     time.Duration
	cpuPerCycle float64 // GC CPU seconds per cycle, measured over forced cycles under load

	// Saturated, unpaced load with natural cycles only. The paced load's CPU
	// per request is mostly the generator (sleeps, rand, path building), so
	// it cannot say what share of a busy server GC takes.
	satReqs     int64
	satCycles   uint64  // natural cycles that ended in the window
	satDirect   float64 // GC CPU over busy CPU as the runtime reported it
	satAllocReq float64 // bytes allocated per request
	satMutReq   float64 // non-GC CPU seconds per request
	liveBytes   float64
}

// satProjected is the GC share of busy CPU for a server saturated with hits:
// with GOGC=100 a cycle starts about every live-heap bytes allocated, each
// costing cpuPerCycle. It uses the cost per request of the saturated phase,
// whose cycles are too few in the window to give a direct figure.
func (r gcResult) satProjected() float64 {
	gcPerReq := r.cpuPerCycle * r.satAllocReq / r.liveBytes
	return gcPerReq / (r.satMutReq + gcPerReq)
}

// gcPhase fills an engine with n keys of 1 KiB, then reads random keys at
// rps for d. The runtime updates its CPU class metrics only when a cycle
// ends, and at 20 000 hits/s a 2.5 GiB heap needs minutes to trigger one,
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
	stop := make(chan struct{})
	time.AfterFunc(d, func() { close(stop) })
	cadence := min(5*time.Second, d/3)
	var cycles int
	var cycleCPU float64
	done := make(chan struct{})
	go func() {
		defer close(done)
		tk := time.NewTicker(cadence)
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
	runtime.ReadMemStats(&ms1)

	// Saturated stage: no pacing, no forced cycles.
	var sat counters
	paths := make([]string, n) // built outside the window: the generator should cost little
	for i := range paths {
		paths[i] = pathN("k", i)
	}
	var ms2, ms3 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms2)
	cy0, sb := readCycles(), readGC()
	stop = make(chan struct{})
	time.AfterFunc(dur(60*time.Second, 8*time.Second), func() { close(stop) })
	closedLoop(64, stop, func(r *rand.Rand, h *hist) { serveOnce(e, org, paths[r.IntN(n)], "", nil, &sat) })
	sa := readGC()
	cy1 := readCycles()
	runtime.ReadMemStats(&ms3)
	closeEngine(t, e)

	reqs := sat.total.Load()
	gc, busy := sa.gc-sb.gc, (sa.total-sb.total)-(sa.idle-sb.idle)
	return gcResult{
		heapMiB: ms0.HeapInuse >> 20, liveMiB: ms0.HeapAlloc >> 20,
		hitRatio: float64(c.hits.Load()) / float64(c.total.Load()), h: h,
		cycles: cycles, cadence: cadence, cpuPerCycle: cycleCPU / float64(max(cycles, 1)),
		satReqs: reqs, satCycles: cy1 - cy0, satDirect: gc / busy,
		satAllocReq: float64(ms3.TotalAlloc-ms2.TotalAlloc) / float64(reqs),
		satMutReq:   (busy - gc) / float64(reqs),
		liveBytes:   float64(ms0.HeapAlloc),
	}
}

func readCycles() uint64 {
	s := []metrics.Sample{{Name: "/gc/cycles/total:gc-cycles"}}
	metrics.Read(s)
	return s[0].Value.Uint64()
}

// TestGCAt1MEntries measures the cost of keeping 1M entries on the Go heap
// (D36, NFR-5, card M10-05): the GC share of CPU and the hit p99 at a fixed
// request rate, next to the same load on a 10 000-entry store. It asserts nothing: the
// 10% GC line is a decision recorded in docs/benchmarks.md. Machine-dependent: the reference
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
	f2 := func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
	table(t, "gc at scale",
		row("entries (small / large)", "10000 / "+strconv.Itoa(n)),
		row("heap in use / live after fill", pair(func(r gcResult) string {
			return strconv.FormatUint(r.heapMiB, 10) + " / " + strconv.FormatUint(r.liveMiB, 10) + " MiB"
		})),
		row("hit ratio (paced)", pair(func(r gcResult) string { return strconv.FormatFloat(r.hitRatio, 'f', 4, 64) })),
		row("GC CPU per forced cycle", pair(func(r gcResult) string {
			return f2(r.cpuPerCycle*1000) + " ms (" + strconv.Itoa(r.cycles) + " cycles, every " + r.cadence.String() + ")"
		})),
		row("paced "+strconv.Itoa(rps)+"/s p50", pair(func(r gcResult) string { return r.h.quantile(0.5).String() })),
		row("paced p99 with forced cycles", pair(func(r gcResult) string { return r.h.quantile(0.99).String() })),
		row("paced max", pair(func(r gcResult) string { return r.h.max.String() })),
		row("saturated requests", pair(func(r gcResult) string { return strconv.FormatInt(r.satReqs, 10) })),
		row("saturated alloc per request", pair(func(r gcResult) string { return f2(r.satAllocReq) + " B" })),
		row("saturated non-GC CPU per request", pair(func(r gcResult) string { return f2(r.satMutReq*1e6) + " µs" })),
		row("saturated natural cycles in window", pair(func(r gcResult) string { return strconv.FormatUint(r.satCycles, 10) })),
		row("saturated GC share, runtime-reported", pair(func(r gcResult) string { return pct(r.satDirect) })),
		row("saturated GC share, projected", pair(func(r gcResult) string { return pct(r.satProjected()) })),
	)
	// Reporting only. The 10% line decides whether a pointer-light layout card
	// is needed (PLAN M10.5b); that decision is in docs/benchmarks.md, and a
	// gate belongs here once such a layout exists.
	if share := large.satProjected(); share > 0.10 {
		t.Logf("saturated GC share %s is over the 10%% line: see docs/benchmarks.md and card M16-01", pct(share))
	}
}

func pct(f float64) string { return strconv.FormatFloat(f*100, 'f', 2, 64) + "%" }
