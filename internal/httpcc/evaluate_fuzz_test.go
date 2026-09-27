package httpcc

import (
	"testing"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// FuzzEvaluate checks properties that must hold for any stored entry,
// including corrupt decoded values: time only makes an entry staler, a soft
// purge never makes it fresher, and forbidden stale serving stays forbidden.
func FuzzEvaluate(f *testing.F) {
	// FR-STL-1, FR-STL-2, FR-STL-3, FR-PRG-2; NFR-2, T-9
	f.Add(int64(0), int64(60e9), int64(30e9), int64(120e9), uint16(0), int64(10e9), int64(20e9), int64(5e9), uint8(1))
	f.Add(int64(-1), int64(-1), int64(-1), int64(-1), uint16(store.FlagNoCache), int64(-1<<62), int64(1<<62), int64(1<<62), uint8(0))
	f.Fuzz(func(t *testing.T, cia, lt, swr, sie int64, flags uint16, now, later, epAt int64, mode uint8) {
		e := &store.Entry{
			Kind: store.KindResponse, ResponseTime: respTime,
			CorrectedInitialAge: time.Duration(cia), Lifetime: time.Duration(lt),
			SWR: time.Duration(swr), SIE: time.Duration(sie), Flags: store.Flags(flags),
		}
		t1 := respTime.Add(time.Duration(now))
		t2 := t1.Add(max(time.Duration(later), 0))
		ep := store.Epoch{At: respTime.Add(time.Duration(epAt)), Mode: store.EpochMode(mode)}

		st, stal, sieOK := Evaluate(e, ep, true, t1)
		if st < Fresh || st > Unusable {
			t.Fatalf("invalid state %d", st)
		}
		if ep.Mode != store.EpochSoft {
			want := Unusable // hard, zero and unknown modes fail closed
			if ep.Mode == store.EpochInvalid {
				want = NeedsValidation
			}
			if st != want || sieOK {
				t.Fatalf("mode %d: got %d sieOK=%v, want %d", ep.Mode, st, sieOK, want)
			}
			return
		}

		// A soft purge never makes the entry fresher.
		st0, stal0, _ := Evaluate(e, ep, false, t1)
		if stal < stal0 || st < st0 {
			t.Fatalf("soft purge made entry fresher: %d/%v vs %d/%v", st, stal, st0, stal0)
		}
		// Time only makes the entry staler.
		st2, stal2, sie2 := Evaluate(e, ep, true, t2)
		if stal2 < stal || st2 < st || (sie2 && !sieOK && st != Fresh) {
			t.Fatalf("later lookup fresher: %d/%v/%v then %d/%v/%v", st, stal, sieOK, st2, stal2, sie2)
		}
		if e.Flags&store.FlagNoCache != 0 && (st != NeedsValidation || sieOK) {
			t.Fatalf("no-cache served: %d sieOK=%v", st, sieOK)
		}
		if st == StaleSWR && (stal < 0 || stal > e.SWR) {
			t.Fatalf("StaleSWR outside window: %v > %v", stal, e.SWR)
		}
		if sieOK && (stal < 0 || stal > e.SIE) {
			t.Fatalf("sieOK outside window: %v > %v", stal, e.SIE)
		}
		if st == Fresh && stal >= 0 {
			t.Fatalf("Fresh with staleness %v", stal)
		}
	})
}
