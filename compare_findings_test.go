package weir_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// openBreaker fails 20 fetches on distinct paths, which opens the breaker.
func openBreaker(t *testing.T, e *weir.Engine, o *testorigin.Origin) {
	t.Helper()
	o.SetDown(true)
	for i := range 20 {
		if resp, err := e.Serve(t.Context(), getReq(fmt.Sprintf("/down%d", i)), o); err == nil {
			resp.Body.Close()
		}
	}
	if s := e.Stats().BreakerState; s != weir.BreakerOpen {
		t.Fatalf("breaker = %v after 20 failures, want open", s)
	}
}

// FR-CB-4: requests that arrive while the half-open probe is in flight wait
// for it. When it succeeds they are served; when it fails they get
// ErrCircuitOpen as soon as it does.
func TestBurstBehindProbe(t *testing.T) {
	for name, recovered := range map[string]bool{"origin recovered": true, "origin still down": false} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const probe = 100 * time.Millisecond
				o := testorigin.New()
				cfg := cacheCfg
				cfg.Rand = func() float64 { return 0.5 }
				e := newEngine(t, cfg)
				defer closeEngine(t, e)
				openBreaker(t, e, o)
				o.SetDown(false)
				b := cacheable("ok")
				if !recovered {
					b = testorigin.Behavior{Status: http.StatusBadGateway}
				}
				b.Delay = probe
				o.Default(b)
				time.Sleep(5*time.Second + time.Millisecond)
				o.Reset()

				chs := make([]<-chan timedServe, 50)
				for i := range chs {
					chs[i] = serveTimed(t, e, getReq(fmt.Sprintf("/k%d", i)), o)
				}
				var refused int
				for i, ch := range chs {
					r := <-ch
					switch {
					case recovered && r.err != nil:
						t.Errorf("/k%d: %v, want it served once the probe succeeded", i, r.err)
					case !recovered && errors.Is(r.err, weir.ErrCircuitOpen):
						refused++
						if r.elapsed > probe {
							t.Errorf("/k%d: refused after %v, want at most the probe's %v", i, r.elapsed, probe)
						}
					}
				}
				if !recovered {
					if refused != len(chs)-1 || o.TotalCalls() != 1 {
						t.Errorf("%d refused and %d origin calls, want %d refused behind one probe", refused, o.TotalCalls(), len(chs)-1)
					}
				}
			})
		})
	}
}

// FR-CB-4, FR-STL-2: a request with a stale response to serve does not wait
// for the probe.
func TestStaleNotHeldBehindProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		defer close(gate)
		o := testorigin.New()
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=1"}}, Body: []byte("old")})
		cfg := cacheCfg
		cfg.Rand = func() float64 { return 0.5 }
		cfg.Freshness.DefaultStaleIfError = time.Hour
		e := newEngine(t, cfg)
		defer closeEngine(t, e)
		serve(t, e, getReq("/page"), o)
		openBreaker(t, e, o)
		o.SetDown(false)
		o.Default(testorigin.Behavior{Gate: gate})
		time.Sleep(5*time.Second + time.Millisecond)

		held := serveTimed(t, e, getReq("/probe"), o) // takes the probe and hangs
		synctest.Wait()
		r := <-serveTimed(t, e, getReq("/page"), o)
		if r.err != nil || r.stale != weir.StaleCircuitOpen || r.elapsed != 0 {
			t.Fatalf("/page: err %v, stale %v after %v, want the stale response at once", r.err, r.stale, r.elapsed)
		}

		// FR-CB-4: a request with nothing stale waits MaxQueueWait behind the
		// hung probe and no longer.
		r = <-serveTimed(t, e, getReq("/new"), o)
		if !errors.Is(r.err, weir.ErrCircuitOpen) || r.elapsed != 5*time.Second {
			t.Fatalf("/new: %v after %v, want ErrCircuitOpen after MaxQueueWait", r.err, r.elapsed)
		}
		_ = held // ends when the engine closes
	})
}

// FR-CB-4, NFR-3: at most MaxQueue/4 requests wait for a probe; the rest are
// refused at once, and the waiters are served when the probe succeeds.
func TestProbeWaitersBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		o := testorigin.New()
		cfg := cacheCfg
		cfg.Rand = func() float64 { return 0.5 }
		cfg.Limiter.MaxQueue = 16 // 4 waiters
		e := newEngine(t, cfg)
		defer closeEngine(t, e)
		openBreaker(t, e, o)
		o.SetDown(false)
		o.Default(testorigin.Behavior{Gate: gate, Header: http.Header{"Cache-Control": {"no-store"}}})
		time.Sleep(5*time.Second + time.Millisecond)
		o.Reset()

		ctx, leave := context.WithCancel(t.Context())
		probe := make(chan error, 1)
		go func() {
			_, err := e.Serve(ctx, getReq("/probe"), o)
			probe <- err
		}()
		synctest.Wait()
		chs := make([]<-chan timedServe, 10)
		for i := range chs {
			chs[i] = serveTimed(t, e, getReq(fmt.Sprintf("/w%d", i)), o)
		}
		synctest.Wait()
		var refused []int
		for i, ch := range chs {
			select {
			case r := <-ch:
				if !errors.Is(r.err, weir.ErrCircuitOpen) || r.elapsed != 0 {
					t.Errorf("/w%d: %v after %v, want ErrCircuitOpen at once", i, r.err, r.elapsed)
				}
				refused = append(refused, i)
			default:
			}
		}
		if len(refused) != 6 {
			t.Fatalf("%d of 10 refused at once, want 6 with 4 places to wait", len(refused))
		}
		leave() // the probe's client goes away
		if err := <-probe; !errors.Is(err, context.Canceled) {
			t.Fatalf("probe: %v", err)
		}
		synctest.Wait()
		close(gate) // the probe succeeds and the waiters follow
		for i, ch := range chs {
			if slices.Contains(refused, i) {
				continue
			}
			if r := <-ch; r.err != nil {
				t.Errorf("/w%d: %v, want it served once the probe succeeded", i, r.err)
			}
		}
	})
}

// FR-LIM-3, T-11: clients of a URL a marker says cannot be stored run past
// the partition cap, up to MaxConcurrent less ReserveForeground and
// PartitionHeadroom, and another path still starts at once. PartitionHeadroom at MaxConcurrent keeps the
// cap strict.
func TestMarkedURLUsesFreeSlots(t *testing.T) {
	for name, tc := range map[string]struct{ headroom, want int }{
		"default headroom": {0, 32}, // 64 less the reserve of 16 and the headroom of 16
		"strict":           {64, 16},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				private := testorigin.Behavior{Header: http.Header{"Cache-Control": {"private"}}, Body: []byte("me")}
				o := testorigin.New()
				o.Default(private)
				cfg := cacheCfg
				cfg.Limiter.PartitionHeadroom = tc.headroom
				e := newEngine(t, cfg)
				defer closeEngine(t, e)
				serve(t, e, getReq("/account"), o) // writes the marker

				gate := make(chan struct{})
				private.Gate = gate
				o.Default(private)
				chs := make([]<-chan timedServe, 100)
				for i := range chs {
					chs[i] = serveTimed(t, e, getReq("/account"), o)
				}
				synctest.Wait()
				if n := o.MaxInflightPartition(); n != tc.want {
					t.Errorf("%d fetches of /account in flight, want %d", n, tc.want)
				}
				other := serveTimed(t, e, getReq("/other"), o)
				synctest.Wait()
				if n := o.MaxInflight(); n != tc.want+1 {
					t.Errorf("%d fetches in flight with /other, want %d: another path must start at once", n, tc.want+1)
				}
				close(gate)
				for i, ch := range append(chs, other) {
					if r := <-ch; r.err != nil {
						t.Errorf("request %d: %v", i, r.err)
					}
				}
			})
		})
	}
}

// FR-LIM-2: the default queue holds a burst the origin can drain inside
// MaxQueueWait. 5 000 distinct keys against a 20 ms origin take 1.6 s at 64
// at a time; the 1 024 places of v1.0 shed 3 900 of them.
func TestDefaultQueueHoldsExpiryBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := cacheable("ok")
		b.Delay = 20 * time.Millisecond
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(b)
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		chs := make([]<-chan timedServe, 5000)
		for i := range chs {
			chs[i] = serveTimed(t, e, getReq(fmt.Sprintf("/k%d", i)), o)
		}
		for i, ch := range chs {
			if r := <-ch; r.err != nil {
				t.Fatalf("/k%d: %v, want every key served", i, r.err)
			}
		}
	})
}
