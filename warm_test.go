package weir_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

func warmReqs(paths ...string) iter.Seq[*weir.Request] {
	return func(yield func(*weir.Request) bool) {
		for _, p := range paths {
			if !yield(getReq(p)) {
				return
			}
		}
	}
}

// FR-WRM-1, FR-WRM-2, T6.4: Warm fetches at most Warm.Concurrency at once,
// stores what is storable, counts each outcome, and skips fresh entries on
// a second call.
func TestWarm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		b := cacheable("v")
		b.Delay = 10 * time.Millisecond
		o.Default(b)
		o.Route("/no-store", testorigin.Behavior{Header: http.Header{"Cache-Control": {"no-store"}}})
		o.Route("/down", testorigin.Behavior{Err: errors.New("refused")})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		var paths []string
		for i := range 100 {
			paths = append(paths, fmt.Sprintf("/p%d", i))
		}
		all := append(slices.Clone(paths), "/no-store", "/down")
		st, err := e.Warm(t.Context(), warmReqs(all...), o)
		if err != nil {
			t.Fatalf("Warm: %v", err)
		}
		if want := (weir.WarmStats{Fetched: 100, NotStored: 1, Failed: 1}); st != want {
			t.Fatalf("stats = %+v, want %+v", st, want)
		}
		if n := o.MaxInflight(); n != 4 {
			t.Fatalf("origin max in-flight = %d, want Warm.Concurrency (4)", n)
		}
		for _, p := range paths {
			if resp, body := serve(t, e, getReq(p), o); !resp.Cache.Hit || body != "v" {
				t.Fatalf("%s: hit=%v body=%q, want the warmed entry", p, resp.Cache.Hit, body)
			}
		}
		calls := o.TotalCalls()
		st, err = e.Warm(t.Context(), warmReqs(paths...), o)
		if err != nil || st != (weir.WarmStats{Skipped: 100}) || o.TotalCalls() != calls {
			t.Fatalf("second Warm = %+v, %v with %d new origin calls; want 100 skipped, none sent", st, err, o.TotalCalls()-calls)
		}
	})
}

// FR-WRM-1, FR-LIM-4: with foreground load holding every unreserved slot,
// a warm fetch waits instead of taking the reserved one, foreground still
// gets the reserve, and Warm stops when its context ends.
func TestWarmDoesNotUseReserve(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 4, 4)
		o.Default(cacheable("v"))
		gate := make(chan struct{})
		o.Route("/busy", testorigin.Behavior{Gate: gate})
		cfg := cacheCfg
		cfg.Limiter = weir.LimiterConfig{MaxConcurrent: 4, ReserveForeground: 1, MaxQueueWait: time.Minute}
		cfg.Timeouts.Origin = 2 * time.Minute
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		busy := make([]<-chan timedServe, 3) // MaxConcurrent - ReserveForeground
		for i := range busy {
			busy[i] = serveTimed(t, e, postReq("/busy"), o)
		}
		synctest.Wait()

		type warmResult struct {
			st  weir.WarmStats
			err error
		}
		warmed := make(chan warmResult, 1)
		go func() {
			st, err := e.Warm(t.Context(), warmReqs("/w"), o)
			warmed <- warmResult{st, err}
		}()
		ctx, cancel := context.WithCancel(t.Context())
		canceled := make(chan warmResult, 1)
		go func() {
			st, err := e.Warm(ctx, warmReqs("/c"), o)
			canceled <- warmResult{st, err}
		}()
		time.Sleep(90 * time.Second) // past MaxQueueWait: warm waits on ctx only
		synctest.Wait()
		if n := o.Calls("/w") + o.Calls("/c"); n != 0 {
			t.Fatalf("warm origin calls = %d with only the reserved slot free, want 0", n)
		}
		// A queued warm fetch holds no flight, so foreground on its key
		// takes the reserved slot at once instead of joining and waiting.
		if n := weir.Flights(e); n != 0 {
			t.Fatalf("flights while warm is queued = %d, want 0", n)
		}
		start := time.Now()
		if resp, _ := serve(t, e, getReq("/w"), o); resp.Cache.Hit || time.Since(start) != 0 {
			t.Fatalf("foreground /w: hit=%v after %v, want an immediate fetch on the reserved slot", resp.Cache.Hit, time.Since(start))
		}

		cancel()
		if r := <-canceled; !errors.Is(r.err, context.Canceled) || o.Calls("/c") != 0 {
			t.Fatalf("canceled Warm = %+v, %v with %d calls; want context.Canceled, no fetch", r.st, r.err, o.Calls("/c"))
		}
		close(gate)
		for _, ch := range busy {
			if r := <-ch; r.err != nil {
				t.Fatalf("busy request: %v", r.err)
			}
		}
		// The foreground request stored /w while warm waited, so warm looks
		// again after its slot arrives and sends nothing.
		if r := <-warmed; r.err != nil || r.st != (weir.WarmStats{Skipped: 1}) || o.Calls("/w") != 1 {
			t.Fatalf("Warm = %+v, %v with %d /w calls; want 1 skipped, 1 call", r.st, r.err, o.Calls("/w"))
		}
	})
}

// FR-WRM-2, 04 §6.8: a Warm call that joined another Warm call's flight,
// whose caller then left, fetches the key itself instead of failing.
func TestWarmRetriesDroppedFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		gate := make(chan struct{})
		b := cacheable("v")
		b.Gate = gate
		o.Default(b)
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		ctx, cancel := context.WithCancel(t.Context())
		first := make(chan error, 1)
		go func() {
			_, err := e.Warm(ctx, warmReqs("/k"), o)
			first <- err
		}()
		synctest.Wait()
		type warmResult struct {
			st  weir.WarmStats
			err error
		}
		second := make(chan warmResult, 1)
		go func() {
			st, err := e.Warm(t.Context(), warmReqs("/k"), o)
			second <- warmResult{st, err}
		}()
		synctest.Wait()
		cancel()
		if err := <-first; !errors.Is(err, context.Canceled) {
			t.Fatalf("first Warm = %v, want context.Canceled", err)
		}
		close(gate)
		if r := <-second; r.err != nil || r.st != (weir.WarmStats{Fetched: 1}) || o.Calls("/k") != 2 {
			t.Fatalf("second Warm = %+v, %v with %d calls; want 1 fetched after its own call (2 calls)", r.st, r.err, o.Calls("/k"))
		}
	})
}

// FR-WRM-1, FR-COA-6, 04 §6.8: a foreground request that joined a warm
// flight whose caller then left fetches for itself instead of failing.
func TestWarmCanceledFollowerFetches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		gate := make(chan struct{})
		b := cacheable("v")
		b.Gate = gate
		o.Default(b)
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		ctx, cancel := context.WithCancel(t.Context())
		warmErr := make(chan error, 1)
		go func() {
			_, err := e.Warm(ctx, warmReqs("/k"), o)
			warmErr <- err
		}()
		synctest.Wait()
		follower := serveTimed(t, e, getReq("/k"), o)
		synctest.Wait()
		cancel()
		if err := <-warmErr; !errors.Is(err, context.Canceled) {
			t.Fatalf("Warm = %v, want context.Canceled", err)
		}
		close(gate)
		if r := <-follower; r.err != nil {
			t.Fatalf("follower of the canceled warm flight: %v, want a response", r.err)
		}
	})
}

// FR-WRM-2: a key another request's flight is fetching is not fetched
// again; once that flight stores it, it counts as skipped.
func TestWarmJoinsRunningFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		gate := make(chan struct{})
		b := cacheable("v")
		b.Gate = gate
		o.Default(b)
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		fg := serveTimed(t, e, getReq("/r"), o)
		synctest.Wait()
		type warmResult struct {
			st  weir.WarmStats
			err error
		}
		warmed := make(chan warmResult, 1)
		go func() {
			st, err := e.Warm(t.Context(), warmReqs("/r"), o)
			warmed <- warmResult{st, err}
		}()
		synctest.Wait()
		close(gate)
		if r := <-fg; r.err != nil {
			t.Fatalf("foreground: %v", r.err)
		}
		if r := <-warmed; r.err != nil || r.st != (weir.WarmStats{Skipped: 1}) || o.Calls("/r") != 1 {
			t.Fatalf("Warm = %+v, %v with %d origin calls; want 1 skipped, 1 call", r.st, r.err, o.Calls("/r"))
		}
	})
}

// FR-LCY-2: Close does not wait on a Warm whose iterator never yields
// again; the workers stop and Warm returns ErrClosed at the next yield.
func TestCloseDuringBlockedWarm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(cacheable("v"))
		e := newEngine(t, cacheCfg)

		next := make(chan string)
		reqs := func(yield func(*weir.Request) bool) {
			for p := range next {
				if !yield(getReq(p)) {
					return
				}
			}
		}
		warmErr := make(chan error, 1)
		go func() {
			_, err := e.Warm(t.Context(), reqs, o)
			warmErr <- err
		}()
		synctest.Wait()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := e.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close = %v, want the grace period to run out", err)
		}
		next <- "/late"
		if err := <-warmErr; !errors.Is(err, weir.ErrClosed) {
			t.Fatalf("Warm = %v, want ErrClosed", err)
		}
		close(next)
	})
}

// NFR-2: an origin body that panics on Read during a warm fetch that leads
// no flight (Authorization) counts as failed instead of ending the process.
func TestWarmOriginBodyPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := weir.OriginFunc(func(context.Context, *weir.Request) (*weir.Response, error) {
			return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: panicBody{}}, nil
		})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		req := getReq("/a")
		req.Header.Set("Authorization", "Bearer x")
		st, err := e.Warm(t.Context(), func(yield func(*weir.Request) bool) { yield(req) }, o)
		if err != nil || st != (weir.WarmStats{Failed: 1}) {
			t.Fatalf("Warm = %+v, %v; want 1 failed", st, err)
		}
	})
}

// FR-LCY-2, 01 §4 (Close rejects new Warm work): once Close starts, Warm
// stops taking requests and returns ErrClosed. Requests it never tried are
// not counted, and the fetches already running finish within the grace.
func TestWarmStopsOnClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		b := cacheable("v")
		b.Delay = time.Second
		o.Default(b)
		e := newEngine(t, cacheCfg)

		var paths []string
		for i := range 100 {
			paths = append(paths, fmt.Sprintf("/p%d", i))
		}
		type warmResult struct {
			st  weir.WarmStats
			err error
		}
		warmed := make(chan warmResult, 1)
		go func() {
			st, err := e.Warm(t.Context(), warmReqs(paths...), o)
			warmed <- warmResult{st, err}
		}()
		time.Sleep(1500 * time.Millisecond) // 4 fetched, 4 in flight
		closeEngine(t, e)
		r := <-warmed
		if !errors.Is(r.err, weir.ErrClosed) || r.st != (weir.WarmStats{Fetched: 8}) || o.TotalCalls() != 8 {
			t.Fatalf("Warm = %+v, %v with %d origin calls; want ErrClosed, 8 fetched, 8 calls", r.st, r.err, o.TotalCalls())
		}
	})
}

// FR-TMO-1, 04 §6.7: fetches no request waits on (Warm, background
// refresh) are bounded by Timeouts.Background, foreground ones by
// Timeouts.Origin.
func TestTimeoutByClass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		b := cacheable("slow")
		b.Delay = 20 * time.Second
		o.Default(b)
		cfg := cacheCfg
		cfg.Timeouts.Origin = 10 * time.Second
		cfg.Timeouts.Background = 30 * time.Second
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		if _, err := e.Serve(t.Context(), getReq("/fg"), o); !errors.Is(err, weir.ErrOriginTimeout) {
			t.Fatalf("foreground: err %v, want ErrOriginTimeout", err)
		}
		if st, err := e.Warm(t.Context(), warmReqs("/bg"), o); err != nil || st != (weir.WarmStats{Fetched: 1}) {
			t.Fatalf("Warm = %+v, %v; want 1 fetched under Timeouts.Background", st, err)
		}

		cfg.Timeouts.Background = 5 * time.Second
		e2 := newEngine(t, cfg)
		defer closeEngine(t, e2)
		if st, _ := e2.Warm(t.Context(), warmReqs("/bg2"), o); st != (weir.WarmStats{Failed: 1}) {
			t.Fatalf("Warm with a 5 s Background timeout = %+v, want 1 failed", st)
		}
	})
}
