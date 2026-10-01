package weir

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

// FR-FRS-6, T6.1: the XFetch trigger fires with probability
// exp(-remaining / (Δ·β)).
func TestEarlyRefreshProbability(t *testing.T) {
	const draws = 100_000
	for _, tc := range []struct {
		name      string
		remaining time.Duration
		beta      float64
		want      float64
	}{
		{"one second left, beta 1", time.Second, 1, math.Exp(-2)},
		{"nothing left always triggers", 0, 1, 1},
		{"beta 2 doubles the horizon", time.Second, 2, math.Exp(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rand.New(rand.NewPCG(1, 2))
			hits := 0
			for range draws {
				if xfetch(tc.remaining, 500*time.Millisecond, tc.beta, 1-r.Float64()) {
					hits++
				}
			}
			if tc.want == 1 && hits != draws {
				t.Fatalf("triggered %d of %d, want every draw", hits, draws)
			}
			if got := float64(hits) / draws; math.Abs(got-tc.want) > 0.05*tc.want {
				t.Fatalf("rate = %.4f, want %.4f within 5%%", got, tc.want)
			}
		})
	}
}

// FR-FRS-6: Δ is clamped to [1 ms, 10 s], so a zero fetch duration still
// refreshes an entry at its last instant and a huge one cannot refresh an
// entry with most of its lifetime left.
func TestEarlyRefreshDeltaClamp(t *testing.T) {
	u := math.Exp(-1) // -ln(u) == 1, so the trigger is clamp(Δ)·β >= remaining
	for _, tc := range []struct {
		name      string
		delta     time.Duration
		remaining time.Duration
		want      bool
	}{
		{"zero delta reads as 1ms", 0, time.Millisecond - 1, true},
		{"zero delta not beyond 1ms", 0, 2 * time.Millisecond, false},
		{"huge delta reads as 10s", time.Hour, 11 * time.Second, false},
		{"huge delta within 10s", time.Hour, 9 * time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := xfetch(tc.remaining, tc.delta, 1, u); got != tc.want {
				t.Fatalf("xfetch = %v, want %v", got, tc.want)
			}
		})
	}
}

// FR-FRS-6: the draw is skipped only where no u in (0, 1] that a Rand in
// [0, 1) can produce would trigger, so the shortcut never changes a decision.
func TestEarlyRefreshShortcutIsExact(t *testing.T) {
	minU := 1 - math.Nextafter(1, 0) // the smallest u: 2^-53
	for _, delta := range []time.Duration{0, time.Millisecond, 500 * time.Millisecond, 10 * time.Second, time.Hour} {
		for _, beta := range []float64{0.01, 1, 3.5} {
			bound := time.Duration(37 * float64(clampDelta(delta)) * beta)
			if xfetch(bound+1, delta, beta, minU) {
				t.Fatalf("Δ=%v β=%v: triggers past the shortcut bound %v", delta, beta, bound)
			}
		}
	}
}
