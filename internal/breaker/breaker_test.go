package breaker

import (
	"errors"
	"math"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func testConfig() Config {
	return Config{
		Window:         10 * time.Second,
		MinRequests:    20,
		FailureRatio:   0.5,
		OpenFor:        5 * time.Second,
		MaxOpenFor:     12 * time.Second,
		HalfOpenProbes: 1,
	}
}

type transition struct{ from, to State }

func record(b *Breaker, o Outcome, n int) {
	for range n {
		p, err := b.Allow()
		if err != nil {
			return
		}
		b.Record(p, o)
	}
}

// openAt asserts the breaker rejects 1 ms before d and admits a probe 1 ms
// after it.
func openAt(t *testing.T, b *Breaker, d time.Duration) Probe {
	t.Helper()
	time.Sleep(d - time.Millisecond)
	if _, err := b.Allow(); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("Allow %v before %v = %v, want ErrCircuitOpen", d-time.Millisecond, d, err)
	}
	time.Sleep(2 * time.Millisecond)
	p, err := b.Allow()
	if err != nil {
		t.Fatalf("Allow after %v = %v, want probe", d, err)
	}
	if b.State() != HalfOpen {
		t.Fatalf("state = %v, want HalfOpen", b.State())
	}
	return p
}

// FR-CB-1, FR-CB-3, FR-CB-4, FR-CB-6, ADR-7
func TestBreakerOpensHalfOpenCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := 0.0
		var got []transition
		b := New(testConfig(), func() float64 { return r }, func(from, to State) {
			got = append(got, transition{from, to})
		})

		// 30 gateway failures in 2 s open it.
		for range 30 {
			record(b, Failure, 1)
			time.Sleep(2 * time.Second / 30)
		}
		if b.State() != Open {
			t.Fatalf("state = %v after 30 failures, want Open", b.State())
		}
		if _, err := b.Allow(); !errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("Allow while open = %v, want ErrCircuitOpen", err)
		}

		// Jitter low bound: rnd 0 gives 0.8 × OpenFor, counted from the
		// 20th failure, 11 sleeps before the loop ended.
		elapsed := 30 * (2 * time.Second / 30)
		openedAt := 19 * (2 * time.Second / 30)
		p := openAt(t, b, 4*time.Second-(elapsed-openedAt))
		if _, err := b.Allow(); !errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("second Allow while half-open = %v, want ErrCircuitOpen", err)
		}

		// Probe failure reopens with OpenFor doubled (10 s); rnd near 1
		// gives the high bound, 10 s × 1.19996, then the cap: 12 s at
		// 0.8 = 9.6 s.
		r = 0.9999
		b.Record(p, Failure)
		p = openAt(t, b, 11999600*time.Microsecond)
		r = 0
		b.Record(p, Failure)
		p = openAt(t, b, 9600*time.Millisecond)

		// Success closes, forgets the window and resets OpenFor.
		b.Record(p, Success)
		if b.State() != Closed {
			t.Fatalf("state = %v after probe success, want Closed", b.State())
		}
		record(b, Failure, 20)
		openAt(t, b, 4*time.Second)

		want := []transition{
			{Closed, Open}, {Open, HalfOpen}, {HalfOpen, Open}, {Open, HalfOpen},
			{HalfOpen, Open}, {Open, HalfOpen}, {HalfOpen, Closed},
			{Closed, Open}, {Open, HalfOpen},
		}
		if len(got) != len(want) {
			t.Fatalf("transitions = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("transitions = %v, want %v", got, want)
			}
		}
	})
}

// FR-CB-2, T-16
func TestBreaker500DoesNotTrip(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := New(testConfig(), func() float64 { return 0 }, nil)
		record(b, Status500, 100)
		if b.State() != Closed {
			t.Fatalf("state = %v after 100 × 500, want Closed", b.State())
		}

		// CountStatus500 makes 500 count.
		cfg := testConfig()
		cfg.CountStatus500 = true
		b = New(cfg, func() float64 { return 0 }, nil)
		record(b, Status500, 20)
		if b.State() != Open {
			t.Fatalf("state = %v with CountStatus500, want Open", b.State())
		}
	})
}

// FR-CB-2: buckets stay aligned for any Window, including ones that are not
// a whole number of seconds or are shorter than 10 ns.
func TestBreakerOddWindow(t *testing.T) {
	for _, w := range []time.Duration{7 * time.Second, 1234567 * time.Microsecond, 5} {
		synctest.Test(t, func(t *testing.T) {
			cfg := testConfig()
			cfg.Window = w
			b := New(cfg, func() float64 { return 0 }, nil)
			for range 20 {
				record(b, Failure, 1)
				time.Sleep(w / 40)
			}
			if b.State() != Open {
				t.Fatalf("Window %v: state = %v after 20 failures inside it, want Open", w, b.State())
			}
		})
	}
}

// FR-CB-2, T-16
func TestBreakerNeedsVolume(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := New(testConfig(), func() float64 { return 0 }, nil)
		record(b, Failure, 19)
		if b.State() != Closed {
			t.Fatalf("state = %v after 19 failures, want Closed", b.State())
		}

		// Below the ratio: 10 failures in 21 outcomes.
		b = New(testConfig(), func() float64 { return 0 }, nil)
		record(b, Success, 11)
		record(b, Failure, 10)
		if b.State() != Closed {
			t.Fatalf("state = %v at ratio 10/21, want Closed", b.State())
		}

		// The ratio is inclusive: 10 failures in 20 outcomes trips it.
		b = New(testConfig(), func() float64 { return 0 }, nil)
		record(b, Success, 10)
		record(b, Failure, 10)
		if b.State() != Open {
			t.Fatalf("state = %v at ratio 10/20, want Open", b.State())
		}

		// Outcomes older than Window drop out of the count.
		b = New(testConfig(), func() float64 { return 0 }, nil)
		record(b, Failure, 19)
		time.Sleep(15 * time.Second) // a slot no later outcome reuses
		record(b, Failure, 1)
		if b.State() != Closed {
			t.Fatalf("state = %v with 19 failures outside the window, want Closed", b.State())
		}
		record(b, Failure, 19)
		if b.State() != Open {
			t.Fatalf("state = %v with 20 failures in the window, want Open", b.State())
		}
	})
}

// FR-CB-4: a cancelled probe frees its place; a probe from an earlier
// half-open period neither closes nor reopens a later one.
func TestBreakerProbeLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()
		cfg.HalfOpenProbes = 2
		b := New(cfg, func() float64 { return 0 }, nil)
		record(b, Failure, 20)
		p := openAt(t, b, 4*time.Second)
		b.Cancel(p)
		b.Cancel(p) // a second Cancel frees nothing
		old, err := b.Allow()
		if err != nil {
			t.Fatalf("Allow after Cancel = %v, want probe", err)
		}
		if _, err := b.Allow(); err != nil {
			t.Fatalf("second probe = %v", err)
		}
		if _, err := b.Allow(); !errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("third probe with HalfOpenProbes 2 = %v, want ErrCircuitOpen", err)
		}

		// A probe fails and reopens.
		p, err = old, nil
		if err != nil {
			t.Fatal(err)
		}
		b.Record(p, Failure)
		p = openAt(t, b, 8*time.Second)

		b.Record(old, Success)
		if b.State() != HalfOpen {
			t.Fatalf("state = %v after a stale probe succeeded, want HalfOpen", b.State())
		}
		b.Cancel(old)
		if _, err := b.Allow(); err != nil {
			t.Fatalf("Allow with one of two probes in flight = %v", err)
		}
		if _, err := b.Allow(); !errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("Allow with two probes in flight = %v, want ErrCircuitOpen", err)
		}
		b.Record(p, Success)
		if b.State() != Closed {
			t.Fatalf("state = %v, want Closed", b.State())
		}
	})
}

// FR-CB-1, 04 §8.3: a nil breaker (Breaker.Disable) allows everything and
// records nothing.
func TestBreakerNilReceiver(t *testing.T) {
	var b *Breaker
	for range 100 {
		p, err := b.Allow()
		if err != nil {
			t.Fatalf("nil Allow = %v", err)
		}
		b.Record(p, Failure)
		b.Cancel(p)
	}
	if b.State() != Closed {
		t.Fatalf("nil State = %v, want Closed", b.State())
	}
}

// FR-CB-4: a probe that never ends in Record or Cancel (a bug, or a fetch
// stuck past every timeout) does not hold the breaker half-open forever:
// after MaxOpenFor a new half-open period admits fresh probes.
func TestBreakerLeakedProbeExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := New(testConfig(), func() float64 { return 0 }, nil)
		record(b, Failure, 20)
		leaked := openAt(t, b, 4*time.Second)
		time.Sleep(12*time.Second - time.Millisecond)
		if _, err := b.Allow(); !errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("Allow before MaxOpenFor with the probe held = %v, want ErrCircuitOpen", err)
		}
		time.Sleep(time.Millisecond)
		p, err := b.Allow()
		if err != nil {
			t.Fatalf("Allow after MaxOpenFor with a leaked probe = %v, want probe", err)
		}
		b.Record(leaked, Failure) // late: names the superseded period
		if b.State() != HalfOpen {
			t.Fatalf("state = %v after a superseded probe failed, want HalfOpen", b.State())
		}
		b.Record(p, Success)
		if b.State() != Closed {
			t.Fatalf("state = %v, want Closed", b.State())
		}
	})
}

// FR-CB-3, NFR-1: Config.Rand is drawn only when the breaker opens, never on
// an ordinary fetch.
func TestBreakerDrawsOnlyOnOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		draws := 0
		b := New(testConfig(), func() float64 { draws++; return 0 }, nil)
		record(b, Success, 100)
		record(b, Status500, 100)
		if draws != 0 {
			t.Fatalf("Rand drawn %d times while closed, want 0", draws)
		}
		record(b, Failure, 300) // 200 successes above: trips at 200 of 400
		if draws != 1 {
			t.Fatalf("Rand drawn %d times for one open, want 1", draws)
		}
	})
}

// FR-CB-2: the check runs on every outcome, so a success that brings the
// window to MinRequests at FailureRatio trips it.
func TestBreakerTripsWhenVolumeArrives(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := New(testConfig(), func() float64 { return 0 }, nil)
		record(b, Failure, 10)
		record(b, Success, 10)
		if b.State() != Open {
			t.Fatalf("state = %v at 10 failures in 20, want Open", b.State())
		}
	})
}

// FR-CB-3: doubling an enormous open period does not overflow into the past.
func TestBreakerDoublingSaturates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()
		cfg.OpenFor = math.MaxInt64/2 + 1
		cfg.MaxOpenFor = math.MaxInt64 / 2
		b := New(cfg, func() float64 { return 0 }, nil)
		b.mu.Lock()
		b.state, b.gen, b.probes, b.openFor = HalfOpen, 1, 1, cfg.OpenFor
		b.openUntil = time.Now().Add(time.Hour)
		b.mu.Unlock()
		b.Record(Probe{1}, Failure)
		if _, err := b.Allow(); !errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("Allow right after reopen = %v, want ErrCircuitOpen", err)
		}
	})
}

// FR-CB-4: under concurrent Allow, Record and Cancel, no more than
// HalfOpenProbes probes are in flight at once.
func TestBreakerConcurrentProbesBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()
		cfg.HalfOpenProbes = 3
		b := New(cfg, func() float64 { return 0.5 }, nil)
		var mu sync.Mutex
		inflight, peak := 0, 0
		var wg sync.WaitGroup
		for i := range 64 {
			wg.Go(func() {
				for j := range 200 {
					p, err := b.Allow()
					if err != nil {
						time.Sleep(time.Second)
						continue
					}
					if p.gen != 0 {
						mu.Lock()
						inflight++
						peak = max(peak, inflight)
						mu.Unlock()
					}
					time.Sleep(time.Duration(i+j) % 7 * 100 * time.Millisecond)
					if p.gen != 0 {
						mu.Lock()
						inflight--
						mu.Unlock()
					}
					switch (i + j) % 3 {
					case 0:
						b.Record(p, Failure)
					case 1:
						b.Record(p, Success)
					default:
						b.Cancel(p)
					}
				}
			})
		}
		wg.Wait()
		if peak > 3 {
			t.Fatalf("peak probes in flight = %d, want <= 3", peak)
		}
		if peak == 0 {
			t.Fatal("no probe ever ran; the test did not reach half-open")
		}
	})
}

// FR-CB-4: closing forgets the window, so the failures that opened the
// breaker do not reopen it on the next single failure.
func TestBreakerCloseClearsWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := New(testConfig(), func() float64 { return 0 }, nil)
		record(b, Failure, 20)
		p := openAt(t, b, 4*time.Second) // the 20 failures are still inside Window
		b.Record(p, Success)
		record(b, Failure, 1)
		if b.State() != Closed {
			t.Fatalf("state = %v: failures from before the close still counted", b.State())
		}
	})
}
