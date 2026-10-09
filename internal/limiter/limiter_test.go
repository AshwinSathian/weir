package limiter

import (
	"context"
	"errors"
	"maps"
	"math/rand/v2"
	"runtime"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

const wait = 2 * time.Second

func newLimiter(slots, queue, perPart, reserve int) *Limiter {
	return New(Config{Max: slots, MaxQueue: queue, PerPartition: perPart, Reserve: reserve, MaxWait: wait})
}

type result struct {
	p   *Permit
	err error
}

// acquireAsync starts an Acquire and returns the channel its result lands on.
func acquireAsync(ctx context.Context, l *Limiter, c Class, part uint64) <-chan result {
	return acquireAsyncHost(ctx, l, c, part, 0)
}

func acquireAsyncHost(ctx context.Context, l *Limiter, c Class, part, host uint64) <-chan result {
	ch := make(chan result, 1)
	go func() {
		p, err := l.Acquire(ctx, c, part, host)
		ch <- result{p, err}
	}()
	return ch
}

func mustAcquire(t *testing.T, l *Limiter, c Class, part uint64) *Permit {
	t.Helper()
	p, err := l.Acquire(context.Background(), c, part, 0)
	if err != nil {
		t.Fatalf("Acquire(%v, %d): %v", c, part, err)
	}
	return p
}

func pending(ch <-chan result) bool {
	select {
	case <-ch:
		return false
	default:
		return true
	}
}

func (l *Limiter) state() (inflight, queued, parts int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inflight, len(l.queue), len(l.byPart)
}

// FR-LIM-3, ADR-8, T6.3: a queue head blocked by its partition cap is
// skipped, not dropped, so a later waiter of another partition runs first.
func TestLimiterSkipsFullPartition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLimiter(2, 8, 1, 0)
		a1 := mustAcquire(t, l, Foreground, 1)
		x := mustAcquire(t, l, Foreground, 3)

		a2 := acquireAsync(t.Context(), l, Foreground, 1)
		synctest.Wait()
		b := acquireAsync(t.Context(), l, Foreground, 2)
		synctest.Wait()

		x.Release()
		synctest.Wait()
		if !pending(a2) {
			t.Fatal("waiter over its partition cap was granted")
		}
		rb := <-b
		if rb.err != nil {
			t.Fatalf("waiter behind a capped partition: %v", rb.err)
		}

		a1.Release()
		synctest.Wait()
		ra := <-a2
		if ra.err != nil {
			t.Fatalf("capped waiter after its partition freed: %v", ra.err)
		}
		ra.p.Release()
		rb.p.Release()
		if in, q, parts := l.state(); in != 0 || q != 0 || parts != 0 {
			t.Fatalf("state after release: inflight=%d queued=%d parts=%d", in, q, parts)
		}
	})
}

// FR-LIM-3, T-11, T6.8: one partition holds at most queueCap (here
// PerPartition = MaxQueue/4 = 2) queued
// waiters; more shed with ErrQueueFull at once, so a flood on one path
// cannot fill the shared queue and shed every other path.
func TestLimiterPartitionQueueCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLimiter(2, 8, 2, 0)
		held := []*Permit{mustAcquire(t, l, Foreground, 1), mustAcquire(t, l, Foreground, 1)}
		queued := []<-chan result{acquireAsync(t.Context(), l, Foreground, 1), acquireAsync(t.Context(), l, Warm, 1)}
		synctest.Wait()
		if _, err := l.Acquire(t.Context(), Foreground, 1, 0); !errors.Is(err, ErrQueueFull) {
			t.Fatalf("third waiter of a partition: %v, want ErrQueueFull", err)
		}
		other := acquireAsync(t.Context(), l, Foreground, 2)
		synctest.Wait()
		if !pending(other) {
			t.Fatal("another partition's waiter did not queue")
		}
		if _, q, _ := l.state(); q != 3 {
			t.Fatalf("queued = %d, want 3", q)
		}
		// A waiter leaving by timeout frees its partition's queue place.
		time.Sleep(wait)
		synctest.Wait()
		if r := <-queued[0]; !errors.Is(r.err, ErrQueueTimeout) {
			t.Fatalf("first waiter: %v, want ErrQueueTimeout", r.err)
		}
		if r := <-other; !errors.Is(r.err, ErrQueueTimeout) {
			t.Fatalf("other partition: %v, want ErrQueueTimeout", r.err)
		}
		again := acquireAsync(t.Context(), l, Foreground, 1)
		synctest.Wait()
		if !pending(again) {
			t.Fatal("waiter after a timeout did not queue")
		}
		// A granted waiter frees its place too.
		held[0].Release()
		synctest.Wait()
		rw := <-queued[1]
		if rw.err != nil {
			t.Fatalf("warm waiter: %v", rw.err)
		}
		held[1].Release()
		rw.p.Release()
		synctest.Wait()
		ra := <-again
		if ra.err != nil {
			t.Fatalf("requeued waiter: %v", ra.err)
		}
		ra.p.Release()
		if in, q, parts := l.state(); in != 0 || q != 0 || parts != 0 || len(l.queuedBy) != 0 {
			t.Fatalf("state: inflight=%d queued=%d parts=%d queuedBy=%d", in, q, parts, len(l.queuedBy))
		}
	})
}

// FR-LIM-3, T-11: a partition may queue a quarter of MaxQueue when that is
// more than PerPartition, so a legitimate cold start on one path keeps
// queueing instead of shedding past PerPartition waiters.
func TestLimiterPartitionQueueCapQuarter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLimiter(1, 16, 1, 0)     // queue cap max(1, 16/4) = 4
		mustAcquire(t, l, Foreground, 1) // held to the end; waiters time out
		for range 4 {
			acquireAsync(t.Context(), l, Foreground, 1)
		}
		synctest.Wait()
		if _, err := l.Acquire(t.Context(), Foreground, 1, 0); !errors.Is(err, ErrQueueFull) {
			t.Fatalf("fifth waiter: %v, want ErrQueueFull", err)
		}
		acquireAsync(t.Context(), l, Foreground, 2) // another partition still queues
		synctest.Wait()
		if _, q, _ := l.state(); q != 5 {
			t.Fatalf("queued = %d, want 5", q)
		}
	})
}

// FR-LIM-2, FR-LIM-3: runnable waiters are granted in arrival order.
func TestLimiterFIFO(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLimiter(1, 8, 1, 0)
		p := mustAcquire(t, l, Foreground, 1)
		a := acquireAsync(t.Context(), l, Foreground, 2)
		synctest.Wait()
		b := acquireAsync(t.Context(), l, Foreground, 3)
		synctest.Wait()
		p.Release()
		synctest.Wait()
		if !pending(b) {
			t.Fatal("later waiter granted before the earlier one")
		}
		ra := <-a
		if ra.err != nil {
			t.Fatalf("first waiter: %v", ra.err)
		}
		ra.p.Release()
		rb := <-b
		if rb.err != nil {
			t.Fatalf("second waiter: %v", rb.err)
		}
		rb.p.Release()
	})
}

// FR-LIM-2, FR-LIM-4, T6.3, T6.4: foreground waits at most MaxWait,
// a full queue sheds at once, background never queues and stays out of
// the reserve, warm respects the reserve but waits past MaxWait.
func TestLimiterQueueTimeout(t *testing.T) {
	t.Run("foreground sheds after MaxWait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := newLimiter(1, 8, 1, 0)
			p := mustAcquire(t, l, Foreground, 1)
			defer p.Release()
			start := time.Now()
			_, err := l.Acquire(t.Context(), Foreground, 2, 0)
			if !errors.Is(err, ErrQueueTimeout) {
				t.Fatalf("err = %v, want ErrQueueTimeout", err)
			}
			if d := time.Since(start); d != wait {
				t.Fatalf("waited %v, want %v", d, wait)
			}
			if _, q, _ := l.state(); q != 0 {
				t.Fatalf("timed-out waiter left in queue (%d)", q)
			}
		})
	})
	t.Run("full queue sheds without waiting", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := newLimiter(1, 1, 1, 0)
			p := mustAcquire(t, l, Foreground, 1)
			queued := acquireAsync(t.Context(), l, Foreground, 2)
			synctest.Wait()
			start := time.Now()
			if _, err := l.Acquire(t.Context(), Foreground, 3, 0); !errors.Is(err, ErrQueueFull) {
				t.Fatalf("err = %v, want ErrQueueFull", err)
			}
			if time.Since(start) != 0 {
				t.Fatal("shed on a full queue waited")
			}
			p.Release()
			(<-queued).p.Release()
		})
	})
	t.Run("background never queues and leaves the reserve", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := newLimiter(4, 8, 4, 1)
			var held []*Permit
			for range 3 {
				held = append(held, mustAcquire(t, l, Background, 1))
			}
			if _, err := l.Acquire(t.Context(), Background, 2, 0); !errors.Is(err, ErrShed) || errors.Is(err, ErrQueueFull) {
				t.Fatalf("background into the reserve: err = %v, want ErrShed", err)
			}
			if _, q, _ := l.state(); q != 0 {
				t.Fatal("background queued")
			}
			held = append(held, mustAcquire(t, l, Foreground, 2)) // the reserve is foreground's
			for _, p := range held {
				p.Release()
			}
		})
	})
	t.Run("warm respects the reserve and waits past MaxWait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := newLimiter(2, 8, 2, 1)
			fg := mustAcquire(t, l, Foreground, 1)
			warm := acquireAsync(t.Context(), l, Warm, 2)
			time.Sleep(3 * wait)
			synctest.Wait()
			if !pending(warm) {
				t.Fatal("warm took a reserved slot or timed out")
			}
			fg.Release()
			r := <-warm
			if r.err != nil {
				t.Fatalf("warm after release: %v", r.err)
			}
			r.p.Release()
		})
	})
}

// FR-LIM-2: a cancelled waiter returns ctx.Err() and leaves the queue.
func TestLimiterCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLimiter(1, 8, 1, 0)
		p := mustAcquire(t, l, Foreground, 1)
		ctx, cancel := context.WithCancel(t.Context())
		ch := acquireAsync(ctx, l, Warm, 2)
		synctest.Wait()
		cancel()
		r := <-ch
		if !errors.Is(r.err, context.Canceled) || r.p != nil {
			t.Fatalf("got (%v, %v), want (nil, context.Canceled)", r.p, r.err)
		}
		if _, q, _ := l.state(); q != 0 {
			t.Fatalf("cancelled waiter left in queue (%d)", q)
		}
		p.Release()
		p.Release() // a second Release is a no-op
		if in, _, _ := l.state(); in != 0 {
			t.Fatalf("inflight = %d after release", in)
		}
	})
}

// FR-LIM-1, FR-LIM-2, P8: a grant that races the waiter's timeout or
// cancellation hands the slot back exactly once, either to the caller or
// to the limiter. 1 000 rounds with the release shuffled around the deadline.
func TestLimiterGrantRace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLimiter(1, 8, 1, 0)
		r := rand.New(rand.NewPCG(1, 2))
		var granted, shed int
		for range 1000 {
			holder := mustAcquire(t, l, Foreground, 1)
			ctx, cancel := context.WithCancel(t.Context())
			ch := acquireAsync(ctx, l, Foreground, 2)
			synctest.Wait()
			at := wait + time.Duration(r.IntN(3)-1)*time.Millisecond
			released := make(chan struct{})
			go func() {
				time.Sleep(at)
				holder.Release()
				close(released)
			}()
			if r.IntN(2) == 0 {
				cancelAt := wait - time.Duration(r.IntN(2))*time.Millisecond
				go func() {
					time.Sleep(cancelAt)
					cancel()
				}()
			}
			res := <-ch
			<-released
			in, _, _ := l.state()
			switch {
			case res.err == nil:
				granted++
				if in != 1 {
					t.Fatalf("granted, inflight = %d, want 1", in)
				}
				res.p.Release()
			case errors.Is(res.err, ErrShed), errors.Is(res.err, context.Canceled):
				shed++
				if res.p != nil || in != 0 {
					t.Fatalf("refused (%v) with permit %v, inflight = %d", res.err, res.p, in)
				}
			default:
				t.Fatalf("unexpected error %v", res.err)
			}
			cancel()
			if in, q, _ := l.state(); in != 0 || q != 0 {
				t.Fatalf("round end: inflight=%d queued=%d", in, q)
			}
		}
		if granted == 0 || shed == 0 {
			t.Fatalf("race not exercised: granted=%d shed=%d", granted, shed)
		}
	})
}

// FR-LIM-6, T6.8: per-partition state holds only partitions with work in
// flight, so 5 000 distinct partitions leave nothing behind.
func TestLimiterStateBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLimiter(4, 4, 1, 0)
		for part := range uint64(5000) {
			p := mustAcquire(t, l, Foreground, part)
			if _, _, parts := l.state(); parts > 4 {
				t.Fatalf("byPart has %d entries, max 4", parts)
			}
			p.Release()
		}
		if in, q, parts := l.state(); in != 0 || q != 0 || parts != 0 {
			t.Fatalf("state: inflight=%d queued=%d parts=%d", in, q, parts)
		}
	})
}

// FR-LIM-1, FR-LIM-3, FR-LIM-4, FR-LIM-6, P8: a randomized model check.
// After every step of random acquires (all classes, short deadlines),
// releases and clock advances: inflight stays within Max and equals the
// permits handed out, partition counts stay within the cap and sum to
// inflight, and no queued waiter is left runnable (the FIFO invariant the
// fast path in Acquire relies on). 200 seeds.
func TestLimiterInvariants(t *testing.T) {
	for seed := range uint64(200) {
		synctest.Test(t, func(t *testing.T) {
			r := rand.New(rand.NewPCG(seed, 7))
			slots, per := 1+r.IntN(6), 1+r.IntN(3)
			perHost := r.IntN(3) // 0: off (FR-FAIR-1)
			l := New(Config{
				Max: slots, MaxQueue: 1 + r.IntN(6), PerPartition: per, PerHost: perHost,
				Reserve: r.IntN(slots), MaxWait: time.Duration(1+r.IntN(5)) * time.Millisecond,
			})
			var held []*Permit
			got := make(chan *Permit, 256)
			check := func() {
				t.Helper()
				l.mu.Lock()
				defer l.mu.Unlock()
				sum := 0
				for part, n := range l.byPart {
					if n <= 0 || n > per {
						t.Fatalf("seed %d: partition %d count %d, cap %d", seed, part, n, per)
					}
					sum += n
				}
				if l.inflight > slots || sum != l.inflight || l.inflight != len(held) {
					t.Fatalf("seed %d: inflight %d, max %d, partition sum %d, permits %d",
						seed, l.inflight, slots, sum, len(held))
				}
				hostSum := 0
				for host, n := range l.byHost {
					if perHost == 0 || n <= 0 || n > perHost {
						t.Fatalf("seed %d: host %d count %d, cap %d", seed, host, n, perHost)
					}
					hostSum += n
				}
				if perHost > 0 && hostSum != l.inflight {
					t.Fatalf("seed %d: host sum %d, inflight %d", seed, hostSum, l.inflight)
				}
				queued := map[uint64]int{}
				for _, w := range l.queue {
					if l.canRun(w.class, w.part, w.host) {
						t.Fatalf("seed %d: runnable waiter left in the queue", seed)
					}
					queued[w.part]++
				}
				if !maps.Equal(queued, l.queuedBy) {
					t.Fatalf("seed %d: queuedBy %v, queue holds %v", seed, l.queuedBy, queued)
				}
				for part, n := range queued {
					if n > l.queueCap() {
						t.Fatalf("seed %d: partition %d has %d queued, cap %d", seed, part, n, l.queueCap())
					}
				}
			}
			for range 200 {
				switch r.IntN(5) {
				case 0, 1:
					c, part, host := Class(r.IntN(3)), uint64(r.IntN(4)), uint64(r.IntN(3))
					ctx, cancel := context.WithTimeout(t.Context(), time.Duration(r.IntN(8))*time.Millisecond)
					go func() {
						defer cancel()
						if p, err := l.Acquire(ctx, c, part, host); err == nil {
							got <- p
						}
					}()
				case 2:
					if len(held) > 0 {
						i := r.IntN(len(held))
						held[i].Release()
						held = slices.Delete(held, i, i+1)
					}
				case 3:
					time.Sleep(time.Duration(r.IntN(3)) * time.Millisecond)
				case 4: // FR-MR-3: throttles come, go and expire between steps
					now := time.Now()
					l.Throttle(now, now.Add(time.Duration(r.IntN(4))*time.Millisecond), map[uint64]int{uint64(r.IntN(4)): 1})
				}
				synctest.Wait()
				for len(got) > 0 {
					held = append(held, <-got)
				}
				check()
			}
			for _, p := range held {
				p.Release()
			}
			held = nil
			time.Sleep(time.Second) // every waiter's deadline passes
			synctest.Wait()
			for len(got) > 0 {
				(<-got).Release()
			}
			check()
		})
	}
}

// BenchmarkLimiterAcquireRelease measures the fast path, and Release's
// O(queue) walk with MaxQueue-1 waiters parked behind full partitions.
func BenchmarkLimiterAcquireRelease(b *testing.B) {
	b.Run("uncontended", func(b *testing.B) {
		l := New(Config{Max: 64, MaxQueue: 1024, PerPartition: 16, Reserve: 16, MaxWait: wait})
		ctx := context.Background()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			var part uint64
			for pb.Next() {
				part++
				p, err := l.Acquire(ctx, Foreground, part%32, 0)
				if err != nil {
					b.Fatal(err)
				}
				p.Release()
			}
		})
	})
	b.Run("full queue", func(b *testing.B) {
		// One parked waiter per full partition, since each partition queues
		// at most PerPartition (FR-LIM-3).
		l := New(Config{Max: 1100, MaxQueue: 1024, PerPartition: 1, Reserve: 16, MaxWait: wait})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		for part := range uint64(1023) {
			hold := mustAcquireB(b, l, part)
			defer hold.Release()
			go func() { _, _ = l.Acquire(ctx, Warm, part, 0) }() // parked: its partition is full
		}
		for {
			if _, q, _ := l.state(); q == 1023 {
				break
			}
			runtime.Gosched()
		}
		b.ReportAllocs()
		for b.Loop() {
			mustAcquireB(b, l, 2000).Release()
		}
	})
}

func mustAcquireB(b *testing.B, l *Limiter, part uint64) *Permit {
	p, err := l.Acquire(context.Background(), Foreground, part, 0)
	if err != nil {
		b.Fatal(err)
	}
	return p
}

// FR-MR-3, 04 §8.2: a throttle report caps its partitions until its
// deadline. The limiter keeps the report with the latest end, ignores one
// that is already over, and runs the grant walk whenever a cap rises.
func TestLimiterThrottle(t *testing.T) {
	one := map[uint64]int{1: 1}
	t.Run("caps the partition and leaves the others", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := newLimiter(8, 8, 4, 0)
			now := time.Now()
			l.Throttle(now, now.Add(time.Minute), one)
			p := mustAcquire(t, l, Foreground, 1)
			second := acquireAsync(t.Context(), l, Foreground, 1)
			synctest.Wait()
			if !pending(second) {
				t.Fatal("second fetch of a throttled partition ran")
			}
			for range 4 {
				defer mustAcquire(t, l, Foreground, 2).Release()
			}
			p.Release()
			(<-second).p.Release()
		})
	})
	t.Run("drops at the deadline and grants parked waiters first", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := newLimiter(8, 8, 2, 0)
			now := time.Now()
			l.Throttle(now, now.Add(time.Second), one)
			p := mustAcquire(t, l, Foreground, 1)
			parked := acquireAsync(t.Context(), l, Foreground, 1)
			synctest.Wait()
			time.Sleep(time.Second)
			// The cap is 2 again. The newcomer must not take the place the
			// parked waiter is owed (FIFO, 04 §8.2 step 1).
			late := acquireAsync(t.Context(), l, Foreground, 1)
			synctest.Wait()
			if !pending(late) {
				t.Fatal("a newcomer ran ahead of the parked waiter")
			}
			select {
			case r := <-parked:
				defer r.p.Release()
			default:
				t.Fatal("parked waiter not granted when the throttle ended")
			}
			l.mu.Lock()
			kept := l.throttled
			l.mu.Unlock()
			if kept != nil {
				t.Fatalf("throttle map kept past its deadline: %v", kept)
			}
			p.Release()
			(<-late).p.Release()
		})
	})
	t.Run("a waiter's own timeout drops an ended throttle", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			// Nothing else touches the limiter after the deadline, so the
			// waiter's timeout is the call that must notice it.
			l := newLimiter(8, 8, 2, 0)
			now := time.Now()
			l.Throttle(now, now.Add(time.Second), one)
			defer mustAcquire(t, l, Foreground, 1).Release()
			time.Sleep(900 * time.Millisecond)
			w := acquireAsync(t.Context(), l, Foreground, 1)
			time.Sleep(wait + time.Second)
			synctest.Wait()
			r := <-w
			if r.err != nil {
				t.Fatalf("waiter shed (%v) after the throttle ended with room in its partition", r.err)
			}
			r.p.Release()
		})
	})
	t.Run("Release drops an ended throttle", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := newLimiter(8, 8, 2, 0)
			a := mustAcquire(t, l, Foreground, 1)
			b := mustAcquire(t, l, Foreground, 1)
			now := time.Now()
			l.Throttle(now, now.Add(time.Second), one)
			w := acquireAsync(t.Context(), l, Warm, 1)
			time.Sleep(1500 * time.Millisecond)
			a.Release() // one fetch still in flight: only the lifted cap frees a slot
			synctest.Wait()
			if pending(w) {
				t.Fatal("waiter still parked after a Release past the deadline")
			}
			b.Release()
		})
	})
	t.Run("an older report does not replace a newer one", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := newLimiter(8, 8, 4, 0)
			now := time.Now()
			l.Throttle(now, now.Add(time.Minute), one)
			l.Throttle(now.Add(-time.Second), now.Add(time.Minute), nil) // the earlier window, delivered late
			defer mustAcquire(t, l, Foreground, 1).Release()
			second := acquireAsync(t.Context(), l, Foreground, 1)
			synctest.Wait()
			if !pending(second) {
				t.Fatal("a late report of an earlier window lifted the throttle")
			}
			// A newer empty report lifts it and wakes the waiter.
			l.Throttle(now.Add(time.Second), now.Add(time.Minute), nil)
			(<-second).p.Release()
		})
	})
	t.Run("a report past its deadline is not applied", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := newLimiter(8, 8, 2, 0)
			now := time.Now()
			l.Throttle(now.Add(-time.Hour), now.Add(-time.Hour+time.Second), one)
			defer mustAcquire(t, l, Foreground, 1).Release()
			defer mustAcquire(t, l, Foreground, 1).Release()
		})
	})
	t.Run("a cap below 1 counts as 1", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			// A cap of 0 would park waiters with no fetch in flight to wake them.
			l := newLimiter(8, 8, 2, 0)
			now := time.Now()
			l.Throttle(now, now.Add(time.Minute), map[uint64]int{1: 0, 2: -5})
			mustAcquire(t, l, Foreground, 1).Release()
			mustAcquire(t, l, Foreground, 2).Release()
		})
	})
	t.Run("a cap above PerPartition does not raise it", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := newLimiter(8, 8, 2, 0)
			now := time.Now()
			l.Throttle(now, now.Add(time.Minute), map[uint64]int{1: 5})
			defer mustAcquire(t, l, Foreground, 1).Release()
			defer mustAcquire(t, l, Foreground, 1).Release()
			if _, err := l.Acquire(t.Context(), Background, 1, 0); !errors.Is(err, ErrShed) {
				t.Fatalf("third fetch: %v, want ErrShed", err)
			}
		})
	})
}

// FR-FAIR-1, D16: a host at its cap queues its own fetches and leaves the
// other hosts' slots alone, and a waiter of a full host does not block the
// FIFO behind it.
func TestPerHostLimiterCap(t *testing.T) {
	newHostLimiter := func(perHost int) *Limiter {
		return New(Config{Max: 8, MaxQueue: 16, PerPartition: 8, PerHost: perHost, MaxWait: wait})
	}
	acq := func(t *testing.T, l *Limiter, part, host uint64) *Permit {
		t.Helper()
		p, err := l.Acquire(context.Background(), Foreground, part, host)
		if err != nil {
			t.Fatalf("Acquire(part %d, host %d): %v", part, host, err)
		}
		return p
	}

	t.Run("a full host queues while another host still runs", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := newHostLimiter(2)
			a1, a2 := acq(t, l, 1, 100), acq(t, l, 2, 100)
			third := acquireAsyncHost(t.Context(), l, Foreground, 3, 100)
			synctest.Wait()
			if !pending(third) {
				t.Fatal("third fetch of a full host ran")
			}
			b := acq(t, l, 4, 200) // another host, free slots globally
			b.Release()
			a1.Release()
			synctest.Wait()
			r := <-third
			if r.err != nil {
				t.Fatalf("queued fetch after a release: %v", r.err)
			}
			r.p.Release()
			a2.Release()
		})
	})

	t.Run("a queued waiter of a full host does not block other hosts behind it", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l := newHostLimiter(1)
			held := acq(t, l, 1, 100)
			blocked := acquireAsyncHost(t.Context(), l, Foreground, 2, 100)
			synctest.Wait()
			other := acq(t, l, 3, 200)
			if !pending(blocked) {
				t.Fatal("blocked waiter ran past the host cap")
			}
			other.Release()
			held.Release()
			synctest.Wait()
			r := <-blocked
			if r.err != nil {
				t.Fatal(r.err)
			}
			r.p.Release()
		})
	})

	t.Run("a background fetch of a full host is shed", func(t *testing.T) {
		l := newHostLimiter(1)
		defer acq(t, l, 1, 100).Release()
		if _, err := l.Acquire(context.Background(), Background, 2, 100); !errors.Is(err, ErrShed) {
			t.Fatalf("err = %v, want ErrShed", err)
		}
	})

	t.Run("zero disables the cap", func(t *testing.T) {
		l := newHostLimiter(0)
		for i := range 8 {
			defer acq(t, l, uint64(i), 100).Release()
		}
		if got := l.hostEntries(); got != 0 {
			t.Fatalf("host map holds %d entries with the cap off", got)
		}
	})

	t.Run("the host map empties when permits are released", func(t *testing.T) {
		l := newHostLimiter(2)
		var held []*Permit
		for i := range 6 {
			held = append(held, acq(t, l, uint64(i), uint64(i)))
		}
		if got := l.hostEntries(); got != 6 {
			t.Fatalf("host map = %d, want 6 while held", got)
		}
		for _, p := range held {
			p.Release()
		}
		if got := l.hostEntries(); got != 0 {
			t.Fatalf("host map = %d after all releases, want 0", got)
		}
	})
}

func (l *Limiter) hostEntries() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.byHost)
}

// FR-FAIR-1, ponytail in Acquire: waiters held back by PerHost share MaxQueue,
// so with the pool saturated a flood on one host sheds a third host. The test
// pins that ceiling; a per-host queued count would turn it into a pass.
func TestHostFloodFillsSharedQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := New(Config{Max: 2, MaxQueue: 4, PerPartition: 8, PerHost: 1, MaxWait: wait})
		held := []*Permit{mustAcquire(t, l, Foreground, 1), nil}
		var err error
		held[1], err = l.Acquire(t.Context(), Foreground, 2, 200)
		if err != nil {
			t.Fatal(err)
		}
		held[0].Release()
		held[0], _ = l.Acquire(t.Context(), Foreground, 1, 100)
		for i := range 4 {
			acquireAsyncHost(t.Context(), l, Foreground, uint64(10+i), 100)
		}
		synctest.Wait()
		if _, err := l.Acquire(t.Context(), Foreground, 99, 300); !errors.Is(err, ErrQueueFull) {
			t.Fatalf("third host: err = %v, want ErrQueueFull (known ceiling)", err)
		}
		for _, p := range held {
			p.Release()
		}
		time.Sleep(wait) // the flood's waiters time out
		synctest.Wait()
	})
}
