package httpcc

import (
	"math"
	"testing"
	"time"

	"github.com/AshwinSathian/weir/store"
)

func TestEvaluate(t *testing.T) {
	// FR-STL-1, FR-STL-2, FR-STL-3, FR-STL-5, FR-PRG-2, FR-PRG-3; 04 §4.3
	base := store.Entry{
		Kind:         store.KindResponse,
		ResponseTime: respTime,
		Lifetime:     time.Minute,
		SWR:          30 * time.Second,
		SIE:          2 * time.Minute,
	}
	at := respTime.Add
	soft := func(d time.Duration) store.Epoch { return store.Epoch{At: at(d), Mode: store.EpochSoft} }
	tests := []struct {
		name      string
		edit      func(*store.Entry)
		ep        store.Epoch
		epOK      bool
		now       time.Duration // offset from respTime
		st        State
		staleness time.Duration
		sieOK     bool
	}{
		{name: "fresh", now: 30 * time.Second, st: Fresh, staleness: -30 * time.Second},
		{name: "corrected initial age counts", edit: func(e *store.Entry) { e.CorrectedInitialAge = 50 * time.Second },
			now: 20 * time.Second, st: StaleSWR, staleness: 10 * time.Second, sieOK: true},
		{name: "expiry instant is stale", now: time.Minute, st: StaleSWR, staleness: 0, sieOK: true},
		{name: "no-cache fresh entry needs validation without stale-if-error",
			edit: func(e *store.Entry) { e.Flags = store.FlagNoCache },
			now:  time.Second, st: NeedsValidation, staleness: -59 * time.Second},
		{name: "no-cache stale entry needs validation without stale-if-error",
			edit: func(e *store.Entry) { e.Flags = store.FlagNoCache },
			now:  70 * time.Second, st: NeedsValidation, staleness: 10 * time.Second},
		{name: "SWR edge is served stale", now: 90 * time.Second, st: StaleSWR, staleness: 30 * time.Second, sieOK: true},
		{name: "just past SWR needs validation", now: 90*time.Second + 1, st: NeedsValidation, staleness: 30*time.Second + 1, sieOK: true},
		{name: "SIE edge still allows stale-if-error", now: 3 * time.Minute, st: NeedsValidation, staleness: 2 * time.Minute, sieOK: true},
		{name: "just past SIE", now: 3*time.Minute + 1, st: NeedsValidation, staleness: 2*time.Minute + 1},
		{name: "zero windows forbid stale", edit: func(e *store.Entry) { e.SWR, e.SIE = 0, 0 },
			now: time.Minute, st: NeedsValidation, staleness: 0},
		{name: "soft purge before expiry makes stale from purge time",
			ep: soft(20 * time.Second), epOK: true, now: 40 * time.Second, st: StaleSWR, staleness: 20 * time.Second, sieOK: true},
		{name: "soft purge at now makes a fresh entry stale",
			ep: soft(10 * time.Second), epOK: true, now: 10 * time.Second, st: StaleSWR, staleness: 0, sieOK: true},
		{name: "soft purge before expiry measures SWR from purge time",
			ep: soft(20 * time.Second), epOK: true, now: 51 * time.Second, st: NeedsValidation, staleness: 31 * time.Second, sieOK: true},
		{name: "soft purge after expiry keeps original expiry",
			ep: soft(70 * time.Second), epOK: true, now: 80 * time.Second, st: StaleSWR, staleness: 20 * time.Second, sieOK: true},
		{name: "soft purge in the future leaves a fresh entry fresh",
			ep: soft(time.Hour), epOK: true, now: 10 * time.Second, st: Fresh, staleness: -50 * time.Second},
		{name: "invalidated fresh entry needs validation without stale-if-error",
			ep: store.Epoch{At: at(10 * time.Second), Mode: store.EpochInvalid}, epOK: true,
			now: 20 * time.Second, st: NeedsValidation},
		{name: "hard purge is unusable",
			ep: store.Epoch{At: at(10 * time.Second), Mode: store.EpochHard}, epOK: true,
			now: 20 * time.Second, st: Unusable},
		{name: "unknown epoch mode fails closed",
			ep: store.Epoch{At: at(10 * time.Second), Mode: 0}, epOK: true,
			now: 20 * time.Second, st: Unusable},
		{name: "epoch ignored when not applicable",
			ep: store.Epoch{At: at(10 * time.Second), Mode: store.EpochHard}, epOK: false,
			now: 20 * time.Second, st: Fresh, staleness: -40 * time.Second},
		{name: "saturated age and negative lifetime do not wrap to fresh",
			edit: func(e *store.Entry) { e.CorrectedInitialAge = math.MaxInt64; e.Lifetime = -time.Hour },
			ep:   soft(0), epOK: true, now: time.Second, st: NeedsValidation, staleness: math.MaxInt64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := base
			if tt.edit != nil {
				tt.edit(&e)
			}
			st, staleness, sieOK := Evaluate(&e, tt.ep, tt.epOK, at(tt.now))
			if st != tt.st || sieOK != tt.sieOK {
				t.Fatalf("state, sieOK = %v, %v; want %v, %v", st, sieOK, tt.st, tt.sieOK)
			}
			if tt.st != Unusable && tt.ep.Mode != store.EpochInvalid && staleness != tt.staleness {
				t.Fatalf("staleness = %v; want %v", staleness, tt.staleness)
			}
		})
	}
}
