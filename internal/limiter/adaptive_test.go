package limiter

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func adaptive(floor, ceiling int) *Limiter {
	return New(Config{Max: floor, Ceiling: ceiling, MaxQueue: 64, PerPartition: ceiling, MaxWait: wait})
}

// round fills the limiter to its limit, queues one more so the limit is what
// held it back, and reports every fetch with the given outcome and latency.
// It returns the limit afterwards.
func round(t *testing.T, l *Limiter, ok bool, rtt time.Duration) int {
	t.Helper()
	var held []*Permit
	for part := uint64(0); ; part++ {
		l.mu.Lock()
		full := l.inflight >= l.limit()
		l.mu.Unlock()
		if full {
			break
		}
		held = append(held, mustAcquire(t, l, Foreground, part))
	}
	waiter := acquireAsync(t.Context(), l, Foreground, 1<<40)
	synctest.Wait()
	for _, p := range held {
		l.Report(ok, rtt)
		p.Release()
	}
	r := <-waiter
	if r.err != nil {
		t.Fatalf("waiter: %v", r.err)
	}
	l.Report(ok, rtt)
	r.p.Release()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limit()
}

// FR-LIM-8 (v1.2), D13, CMP-F5: with a Ceiling above Max the limit grows
// while fetches are waiting for a slot and the origin answers well, by an
// eighth per window, and never past the Ceiling. Without waiters it does not
// grow: an origin nobody is queueing for needs no more slots.
func TestAdaptiveGrowsWhileLimited(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := adaptive(16, 24)
		for range 3 { // busy, nobody waiting
			for i := range 16 {
				p := mustAcquire(t, l, Foreground, uint64(i))
				l.Report(true, 10*time.Millisecond)
				p.Release()
			}
		}
		if got := l.limit(); got != 16 {
			t.Fatalf("limit %d with no fetch ever waiting, want the floor of 16", got)
		}
		want := []int{18, 20, 22, 24, 24}
		for i, w := range want {
			if got := round(t, l, true, 10*time.Millisecond); got != w {
				t.Fatalf("round %d: limit %d, want %d", i+1, got, w)
			}
		}
	})
}

// FR-LIM-8: an origin failure in the window takes a quarter off the limit,
// never below Max, and the level that failed is remembered: for 10 s the
// limit grows back only to one step below it, so the origin is not pushed
// over the same edge every few windows.
func TestAdaptiveBacksOffAndRemembers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := adaptive(16, 64)
		for range 6 {
			round(t, l, true, 10*time.Millisecond)
		}
		top := l.limit() // 16, 18, 20, 22, 24, 27, 30
		if top != 30 {
			t.Fatalf("limit %d after six good rounds, want 30", top)
		}
		if got := round(t, l, false, 10*time.Millisecond); got != 22 {
			t.Fatalf("limit %d after a failing window at 30, want 22", got)
		}
		for range 8 {
			round(t, l, true, 10*time.Millisecond)
		}
		if got := l.limit(); got != 27 {
			t.Fatalf("limit %d while the failure at 30 is remembered, want it held at 27", got)
		}
		for range 11 { // a round a second: never idle, and past the 10 s the failure is remembered
			time.Sleep(time.Second)
			round(t, l, true, 10*time.Millisecond)
		}
		if got := l.limit(); got <= 27 {
			t.Fatalf("limit %d once the memory has lapsed, want it probing past 27 again", got)
		}
		for range 20 {
			round(t, l, false, 10*time.Millisecond)
		}
		if got := l.limit(); got != 16 {
			t.Fatalf("limit %d after 20 failing windows, want the floor of 16", got)
		}
	})
}

// FR-LIM-8: latency is the earlier signal. A window whose mean fetch time is
// more than twice the baseline counts as overload although nothing failed.
func TestAdaptiveBacksOffOnLatency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := adaptive(16, 64)
		for range 4 {
			round(t, l, true, 10*time.Millisecond)
		}
		before := l.limit()
		if got := round(t, l, true, 15*time.Millisecond); got <= before {
			t.Fatalf("limit %d after a window at 1.5 times the baseline, want it still growing from %d", got, before)
		}
		before = l.limit()
		if got := round(t, l, true, 40*time.Millisecond); got >= before {
			t.Fatalf("limit %d after a window at 4 times the baseline, want it below %d", got, before)
		}
	})
}

// FR-LIM-1, FR-LIM-8: without a Ceiling the limit is Max, whatever is
// reported, and after 10 s with no fetch an adaptive limit is back at Max:
// a burst after a quiet spell meets the budget the operator vouched for.
func TestAdaptiveOffAndIdleReset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixed := New(Config{Max: 16, MaxQueue: 64, PerPartition: 64, MaxWait: wait})
		for range 5 {
			if got := round(t, fixed, true, time.Millisecond); got != 16 {
				t.Fatalf("limit %d with no Ceiling, want 16", got)
			}
		}
		l := adaptive(16, 64)
		for range 5 {
			round(t, l, true, 10*time.Millisecond)
		}
		if l.limit() == 16 {
			t.Fatal("the limit did not grow")
		}
		time.Sleep(10 * time.Second)
		p := mustAcquire(t, l, Foreground, 1)
		defer p.Release()
		if got := l.limit(); got != 16 {
			t.Fatalf("limit %d after 10 idle seconds, want the floor of 16", got)
		}
	})
}

// FR-LIM-8, FR-LIM-3, CMP-F5: one busy partition never fills the limit. It
// stops at its cap plus what the headroom rule lets it borrow, so "every
// slot is taken" would never be true for it. A fetch held back by that rule
// is pressure all the same: the limit grows, and with it the room to borrow.
func TestAdaptiveGrowsForABorrowingPartition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := New(Config{Max: 16, Ceiling: 64, MaxQueue: 256, PerPartition: 4, Headroom: 4, Reserve: 4, MaxWait: wait})
		const part = 7
		start := l.limit()
		var held []*Permit
		acquire := func() <-chan result {
			ch := make(chan result, 1)
			go func() {
				p, err := l.AcquireOver(t.Context(), part, 0)
				ch <- result{p, err}
			}()
			return ch
		}
		for range 6 { // each round: fill what the partition may hold, queue one more, finish them all
			var chs []<-chan result
			for range 40 {
				chs = append(chs, acquire())
			}
			synctest.Wait()
			for len(chs) > 0 {
				var rest []<-chan result
				for _, ch := range chs {
					if len(ch) == 0 {
						rest = append(rest, ch)
						continue
					}
					r := <-ch
					if r.err != nil {
						t.Fatal(r.err)
					}
					held = append(held, r.p)
				}
				chs = rest
				for _, p := range held {
					l.Report(true, 10*time.Millisecond)
					p.Release()
				}
				held = held[:0]
				synctest.Wait()
			}
		}
		if got := l.limit(); got <= start {
			t.Fatalf("limit %d after a partition kept waiting behind the borrow rule, want it above %d", got, start)
		}
	})
}

// FR-LIM-8: a refusal comes back at once. Its speed must not teach the
// limiter that the origin is fast: after an overloaded window of instant
// refusals, a window at the origin's ordinary latency still grows the limit.
func TestAdaptiveBaselineIgnoresRefusals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := adaptive(16, 64)
		for range 3 {
			round(t, l, true, 100*time.Millisecond)
		}
		round(t, l, false, 0) // the origin refused everything, instantly
		low := l.limit()
		for range 6 {
			round(t, l, true, 100*time.Millisecond)
		}
		if got := l.limit(); got <= low {
			t.Fatalf("limit %d after six healthy windows, want it above %d: 100 ms is this origin's speed, not a slowdown", got, low)
		}
	})
}

// FR-LIM-8, FR-LIM-3, T-11: a limit that drops while fetches admitted under
// the higher one are still in flight does not shut the other partitions
// out. It stops the reserve and the headroom above what is in flight, and
// comes down the rest of the way as those fetches end.
func TestAdaptiveDropKeepsRoomForOthers(t *testing.T) {
	for name, drop := range map[string]func(*Limiter){
		"a failing window":    func(l *Limiter) { l.Report(false, 0) },
		"ten idle seconds":    func(*Limiter) { time.Sleep(adaptIdle) },
		"both, one then both": func(l *Limiter) { l.Report(false, 0); time.Sleep(adaptIdle) },
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				l := New(Config{Max: 8, Ceiling: 32, MaxQueue: 64, PerPartition: 2, Headroom: 2, Reserve: 2, MaxWait: time.Second})
				for l.limit() < 32 {
					round(t, l, true, 10*time.Millisecond)
				}
				var held []*Permit // one partition takes all it may: its cap and everything it can borrow
				for {
					ctx, cancel := context.WithCancel(t.Context())
					ch := make(chan result, 1)
					go func() {
						p, err := l.AcquireOver(ctx, 1, 0)
						ch <- result{p, err}
					}()
					synctest.Wait()
					if len(ch) == 0 {
						cancel()
						<-ch
						break
					}
					cancel()
					held = append(held, (<-ch).p)
				}
				if len(held) != 28 {
					t.Fatalf("%d slots held by one partition at a limit of 32, want 28", len(held))
				}
				for range 32 { // a whole window, so the limiter decides
					drop(l)
				}
				p, err := l.AcquireOver(t.Context(), 2, 0)
				if err != nil {
					t.Fatalf("another partition after the drop: %v (limit %d, %d in flight)", err, l.limit(), len(held))
				}
				p.Release()
				for _, h := range held {
					h.Release()
				}
			})
		})
	}
}
