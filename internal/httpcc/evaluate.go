package httpcc

import (
	"time"

	"github.com/AshwinSathian/weir/store"
)

// State is what a stored response may be used for at lookup (04 §4.3).
type State uint8

// State values. The zero State is invalid, so a forgotten assignment never
// reads as Fresh.
const (
	Fresh           State = iota + 1 // serve as is
	StaleSWR                         // serve stale and refresh in the background (FR-STL-1)
	NeedsValidation                  // revalidate in the foreground before serving
	Unusable                         // treat as a miss (FR-PRG-3)
)

// Evaluate classifies e at now. ep is the newest epoch that applies to e;
// the caller has already matched it against e.RequestTime (FR-PRG-7), and
// epOK is false when none applies. staleness is negative while fresh; for
// hard-purged and invalidated entries it ignores the epoch. sieOK
// reports whether stale-if-error may serve e (FR-STL-2); it is false for
// Fresh and Unusable.
func Evaluate(e *store.Entry, ep store.Epoch, epOK bool, now time.Time) (st State, staleness time.Duration, sieOK bool) {
	// Neither operand is negative, so the difference cannot wrap.
	staleness = CurrentAge(e, now) - max(e.Lifetime, 0)
	if epOK {
		switch ep.Mode {
		case store.EpochHard:
			return Unusable, staleness, false
		case store.EpochInvalid: // FR-STL-5
			return NeedsValidation, staleness, false
		case store.EpochSoft:
			// FR-PRG-2: expiry becomes min(expiry, age at purge), so staleness
			// becomes max(staleness, time since purge). time.Sub saturates.
			staleness = max(staleness, now.Sub(ep.At))
		default: // T-9: a zero or unknown mode from a decoder fails closed
			return Unusable, staleness, false
		}
	}
	if e.Flags&store.FlagNoCache != 0 {
		return NeedsValidation, staleness, false
	}
	if staleness < 0 {
		return Fresh, staleness, false
	}
	// SWR and SIE are already zero when stale serving is forbidden (FR-STL-3).
	sieOK = e.SIE > 0 && staleness <= e.SIE
	if e.SWR > 0 && staleness <= e.SWR {
		return StaleSWR, staleness, sieOK
	}
	return NeedsValidation, staleness, sieOK
}
