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
	ch := make(chan result, 1)
	go func() {
		p, err := l.Acquire(ctx, c, part)
		ch <- result{p, err}
	}()
	return ch
}

func mustAcquire(t *testing.T, l *Limiter, c Class, part uint64) *Permit {
	t.Helper()
	p, err := l.Acquire(context.Background(), c, part)
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

// FR-LIM-3, T-11, T6.8: one partition holds at most PerPartition queued
// waiters; more shed with ErrQueueFull at once, so a flood on one path
// cannot fill the shared queue and shed every other path.
func TestLimiterPartitionQueueCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLimiter(2, 8, 2, 0)
		held := []*Permit{mustAcquire(t, l, Foreground, 1), mustAcquire(t, l, Foreground, 1)}
		queued := []<-chan result{acquireAsync(t.Context(), l, Foreground, 1), acquireAsync(t.Context(), l, Warm, 1)}
		synctest.Wait()
		if _, err := l.Acquire(t.Context(), Foreground, 1); !errors.Is(err, ErrQueueFull) {
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
			_, err := l.Acquire(t.Context(), Foreground, 2)
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
			if _, err := l.Acquire(t.Context(), Foreground, 3); !errors.Is(err, ErrQueueFull) {
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
			if _, err := l.Acquire(t.Context(), Background, 2); !errors.Is(err, ErrShed) || errors.Is(err, ErrQueueFull) {
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
			l := New(Config{
				Max: slots, MaxQueue: 1 + r.IntN(6), PerPartition: per,
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
				queued := map[uint64]int{}
				for _, w := range l.queue {
					if l.canRun(w.class, w.part) {
						t.Fatalf("seed %d: runnable waiter left in the queue", seed)
					}
					queued[w.part]++
				}
				if !maps.Equal(queued, l.queuedBy) {
					t.Fatalf("seed %d: queuedBy %v, queue holds %v", seed, l.queuedBy, queued)
				}
				for part, n := range queued {
					if n > per {
						t.Fatalf("seed %d: partition %d has %d queued, cap %d", seed, part, n, per)
					}
				}
			}
			for range 200 {
				switch r.IntN(4) {
				case 0, 1:
					c, part := Class(r.IntN(3)), uint64(r.IntN(4))
					ctx, cancel := context.WithTimeout(t.Context(), time.Duration(r.IntN(8))*time.Millisecond)
					go func() {
						defer cancel()
						if p, err := l.Acquire(ctx, c, part); err == nil {
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
				p, err := l.Acquire(ctx, Foreground, part%32)
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
			go func() { _, _ = l.Acquire(ctx, Warm, part) }() // parked: its partition is full
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
	p, err := l.Acquire(context.Background(), Foreground, part)
	if err != nil {
		b.Fatal(err)
	}
	return p
}
