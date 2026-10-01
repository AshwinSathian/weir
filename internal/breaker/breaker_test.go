package breaker

import (
	"errors"
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

		// Success closes and resets OpenFor.
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
		time.Sleep(10 * time.Second)
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
