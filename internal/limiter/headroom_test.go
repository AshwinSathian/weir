package limiter

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func parked(t *testing.T, ch <-chan result) {
	t.Helper()
	synctest.Wait()
	select {
	case r := <-ch:
		t.Fatalf("Acquire returned (%v, %v), want it queued", r.p, r.err)
	default:
	}
}

func granted(t *testing.T, ch <-chan result) *Permit {
	t.Helper()
	synctest.Wait()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("Acquire = %v, want a slot", r.err)
		}
		return r.p
	default:
		t.Fatal("Acquire still queued, want a slot")
		return nil
	}
}

func overAsync(ctx context.Context, l *Limiter, part uint64) <-chan result {
	ch := make(chan result, 1)
	go func() {
		p, err := l.AcquireOver(ctx, part, 0)
		ch <- result{p, err}
	}()
	return ch
}

// FR-LIM-3, T-11: a vouched fetch runs past its partition's cap only while
// Headroom slots stay free, any other partition still starts at once, and
// the partition falls back to its cap when the limiter fills.
func TestHeadroomLeavesSlotsForOtherPartitions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		l := New(Config{Max: 8, MaxQueue: 64, PerPartition: 2, Headroom: 2, MaxWait: time.Hour})
		var hot []*Permit
		for range 6 { // 8 slots less 2 of headroom
			hot = append(hot, granted(t, overAsync(ctx, l, 1)))
		}
		seventh := overAsync(ctx, l, 1)
		parked(t, seventh)

		a := granted(t, acquireAsync(ctx, l, Foreground, 2))
		b := granted(t, acquireAsync(ctx, l, Foreground, 3))

		// Full limiter: a slot the hot partition frees is not handed back to it.
		hot[0].Release()
		parked(t, seventh)
		c := granted(t, acquireAsync(ctx, l, Foreground, 4))

		// Room again: the parked fetch runs.
		a.Release()
		b.Release()
		c.Release()
		p := granted(t, seventh)
		p.Release()
		for _, h := range hot[1:] {
			h.Release()
		}
		if in, q := l.Counts(); in != 0 || q != 0 {
			t.Fatalf("Counts = %d in flight, %d queued, want 0, 0", in, q)
		}
	})
}

// FR-LIM-3, T-11, FR-MR-3: the cap stays strict for a fetch nobody vouched
// for, for a limiter without headroom, and for a throttled partition.
func TestHeadroomStrictCases(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		for name, tc := range map[string]struct {
			headroom int
			over     bool
			throttle bool
		}{
			"plain Acquire with headroom":   {headroom: 2},
			"AcquireOver without headroom":  {over: true},
			"AcquireOver while throttled":   {headroom: 2, over: true, throttle: true},
			"plain Acquire, nothing set up": {},
		} {
			l := New(Config{Max: 8, MaxQueue: 64, PerPartition: 2, Headroom: tc.headroom, MaxWait: time.Hour})
			if tc.throttle {
				now := time.Now()
				l.Throttle(now, now.Add(time.Hour), map[uint64]int{1: 2})
			}
			first := granted(t, acquireAsync(ctx, l, Foreground, 1))
			second := granted(t, acquireAsync(ctx, l, Foreground, 1))
			cctx, cancel := context.WithCancel(ctx)
			var third <-chan result
			if tc.over {
				third = overAsync(cctx, l, 1)
			} else {
				third = acquireAsync(cctx, l, Foreground, 1)
			}
			synctest.Wait()
			select {
			case r := <-third:
				t.Errorf("%s: third fetch of a partition capped at 2 returned (%v, %v), want it queued", name, r.p, r.err)
			default:
			}
			cancel()
			<-third
			first.Release()
			second.Release()
		}
	})
}
