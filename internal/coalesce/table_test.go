package coalesce

import (
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir/store"
)

const maxAge = 10 * time.Second

func key(b byte) store.Key {
	var k store.Key
	k[0], k[31] = b, b
	return k
}

func isDone(f *Flight) bool {
	select {
	case <-f.Done():
		return true
	default:
		return false
	}
}

// FR-COA-1: concurrent requests for one key share one flight; followers
// wait on Done (a channel, P8) and all read the published result.
func TestJoinAndShare(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var tab Table
		f, created := tab.Join(key(1), time.Now(), maxAge)
		if !created {
			t.Fatal("first Join did not create a flight")
		}
		other, created := tab.Join(key(2), time.Now(), maxAge)
		if !created || other == f {
			t.Fatal("a different key joined the same flight")
		}

		const followers = 50
		got := make([]any, followers)
		var wg sync.WaitGroup
		for i := range followers {
			g, created := tab.Join(key(1), time.Now(), maxAge)
			if created || g != f {
				t.Fatalf("follower %d did not join the existing flight", i)
			}
			wg.Go(func() {
				<-g.Done()
				got[i] = g.Result()
			})
		}
		synctest.Wait() // every follower is durably blocked on Done
		if isDone(f) {
			t.Fatal("Done closed before Publish")
		}

		f.Publish("result")
		wg.Wait()
		for i, r := range got {
			if r != "result" {
				t.Fatalf("follower %d read %v", i, r)
			}
		}

		// Published flights leave the table; the next request starts afresh.
		g, created := tab.Join(key(1), time.Now(), maxAge)
		if !created || g == f {
			t.Fatal("Join after Publish returned the published flight")
		}
		g.Publish(nil)
		other.Publish(nil)
	})
}

// FR-COA-3: a flight older than LeaderMaxAge is no longer joinable; the
// first request after that replaces the map entry with a new flight.
func TestAgingReplacesEntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var tab Table
		old, _ := tab.Join(key(1), time.Now(), maxAge)

		time.Sleep(maxAge - time.Nanosecond)
		if g, created := tab.Join(key(1), time.Now(), maxAge); created || g != old {
			t.Fatal("flight aged before LeaderMaxAge")
		}

		time.Sleep(time.Nanosecond)
		fresh, created := tab.Join(key(1), time.Now(), maxAge)
		if !created || fresh == old {
			t.Fatal("aged flight still joinable")
		}
		if g, created := tab.Join(key(1), time.Now(), maxAge); created || g != fresh {
			t.Fatal("map entry not replaced by the new flight")
		}
		if isDone(old) {
			t.Fatal("aging closed the old flight; it must run to completion")
		}
		old.Publish(nil)
		fresh.Publish(nil)
	})
}

// FR-COA-3 (03 §3.3): an aged flight that publishes after being replaced
// must not remove its replacement from the table.
func TestPublishRemovesOnlyCurrent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var tab Table
		old, _ := tab.Join(key(1), time.Now(), maxAge)
		time.Sleep(maxAge)
		fresh, _ := tab.Join(key(1), time.Now(), maxAge)

		old.Publish("old")
		if !isDone(old) {
			t.Fatal("Publish did not close Done")
		}
		if g, created := tab.Join(key(1), time.Now(), maxAge); created || g != fresh {
			t.Fatal("publishing the aged flight removed the current one")
		}

		fresh.Publish("fresh")
		if g, created := tab.Join(key(1), time.Now(), maxAge); !created || g == fresh {
			t.Fatal("publishing the current flight left it in the table")
		}
	})
}

// FR-COA-5 (04 §6.4): the creator's ClaimStream and the flight goroutine's
// AbandonStream race; exactly one wins, so an oversized stream is closed
// exactly once and never leaked.
func TestStreamClaimedOrAbandonedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for i := range 1000 {
			var tab Table
			f, _ := tab.Join(key(byte(i)), time.Now(), maxAge)
			var claimed, abandoned bool
			var wg sync.WaitGroup
			wg.Go(func() { claimed = f.ClaimStream() })
			wg.Go(func() { abandoned = f.AbandonStream() })
			wg.Wait()
			if claimed == abandoned {
				t.Fatalf("iteration %d: claimed=%v abandoned=%v", i, claimed, abandoned)
			}
			if f.ClaimStream() || f.AbandonStream() {
				t.Fatalf("iteration %d: stream won a second time", i)
			}
			f.Publish(nil)
		}
	})
}

// FR-COA-5 (04 §6.4): a creator that stops waiting marks the flight so
// runFlight closes an unclaimed stream.
func TestCreatorGone(t *testing.T) {
	var tab Table
	f, _ := tab.Join(key(1), time.Now(), maxAge)
	if f.CreatorIsGone() {
		t.Fatal("new flight reports its creator gone")
	}
	if f.CreatorGone() {
		t.Fatal("CreatorGone reported an unpublished flight as published")
	}
	if !f.CreatorIsGone() {
		t.Fatal("CreatorGone not recorded")
	}
	f.Publish(nil)

	g, _ := tab.Join(key(1), time.Now(), maxAge)
	g.Publish(nil)
	if !g.CreatorGone() {
		t.Fatal("CreatorGone missed an earlier Publish")
	}
}

// FR-COA-5 (04 §6.4): the creator giving up races runFlight publishing a
// stream. Whatever the order, exactly one side closes the stream: runFlight
// when it sees the creator gone, or the creator when CreatorGone reports
// the flight already published.
func TestStreamClosedWhenCreatorLeavesDuringPublish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for i := range 1000 {
			var tab Table
			f, _ := tab.Join(key(byte(i)), time.Now(), maxAge)
			var closes atomic.Int32
			var wg sync.WaitGroup
			wg.Go(func() { // runFlight
				f.Publish("stream")
				if f.CreatorIsGone() && f.AbandonStream() {
					closes.Add(1)
				}
			})
			wg.Go(func() { // creator's timeout branch
				if f.CreatorGone() && f.AbandonStream() {
					closes.Add(1)
				}
			})
			wg.Wait()
			if n := closes.Load(); n != 1 {
				t.Fatalf("iteration %d: stream closed %d times", i, n)
			}
		}
	})
}

// FR-COA-10: PublishCounted reports exactly the requests that will
// take the result. A request that leaves before it is published is not
// counted; one that leaves after gets the result back to release it.
func TestPublishCountsWaiters(t *testing.T) {
	var tb Table
	k := key(7)
	f, created := tb.Join(k, time.Now(), maxAge)
	if !created {
		t.Fatal("first Join did not create")
	}
	for range 3 {
		if g, c := tb.Join(k, time.Now(), maxAge); c || g != f {
			t.Fatal("Join did not return the flight")
		}
	}
	if res, published := f.Leave(); published || res != nil {
		t.Fatal("Leave before Publish reported a result")
	}
	got := -1
	f.PublishCounted("r", func(n int) { got = n })
	if got != 3 {
		t.Fatalf("counted %d waiters, want 3 (creator and three joins, less one that left)", got)
	}
	if res, published := f.Leave(); !published || res != "r" {
		t.Fatalf("Leave after Publish = %v, %v", res, published)
	}
	if g, c := tb.Join(k, time.Now(), maxAge); !c || g == f {
		t.Fatal("a published flight was joined")
	}
}

// FR-COA-10: CreatorGone takes the creator out of the count, so a background
// flight whose creator never waits counts only its followers.
func TestCreatorGoneLeavesCount(t *testing.T) {
	var tb Table
	f, _ := tb.Join(key(8), time.Now(), maxAge)
	if f.CreatorGone() {
		t.Fatal("published before Publish")
	}
	tb.Join(key(8), time.Now(), maxAge)
	got := -1
	f.PublishCounted(nil, func(n int) { got = n })
	if got != 1 {
		t.Fatalf("counted %d, want 1", got)
	}
}

// FR-COA-10: Join, Leave and Publish racing never lose or double a count.
func TestPublishCountRace(t *testing.T) {
	for range 200 {
		var tb Table
		k := key(9)
		f, _ := tb.Join(k, time.Now(), maxAge)
		var joined, left, gotBack atomic.Int64
		var wg sync.WaitGroup
		for range 16 {
			wg.Go(func() {
				g, created := tb.Join(k, time.Now(), maxAge)
				if created {
					g.Publish(nil) // a new flight after the first published
					return
				}
				if g != f { // a follower of that new flight
					g.Leave()
					return
				}
				joined.Add(1)
				if _, published := g.Leave(); published {
					gotBack.Add(1)
				} else {
					left.Add(1)
				}
			})
		}
		var counted int
		f.PublishCounted(nil, func(n int) { counted = n })
		wg.Wait()
		// The creator never left, so it is counted; every follower that was
		// counted got the result back from Leave.
		if int64(counted) != 1+gotBack.Load() {
			t.Fatalf("counted %d, creator plus %d got back, %d left, %d joined", counted, gotBack.Load(), left.Load(), joined.Load())
		}
	}
}
