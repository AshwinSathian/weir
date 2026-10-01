package weir

import (
	"time"

	"github.com/AshwinSathian/weir/internal/httpcc"
	"github.com/AshwinSathian/weir/store"
)

// maxModeTTL bounds every incident mode (FR-MODE-1, T-42) and the staleness
// ModeStaleOnError allows (FR-MODE-2).
const maxModeTTL = 24 * time.Hour

var modeNames = [...]string{ModeNormal: "normal", ModeStaleOnError: "stale-on-error", ModeBypass: "bypass"}

// modeState is the active incident mode and when it reverts to normal.
type modeState struct {
	mode  Mode
	until time.Time
}

// SetMode switches the incident mode for ttl, after which it reverts to
// ModeNormal (FR-MODE-1, D33). ttl must be in (0, 24 h]. Modes are not
// persisted. It returns an error wrapping ErrInvalidConfig for a bad mode
// or ttl. Expiry is noticed, and its EvMode emitted, by the first Serve
// after the ttl runs out.
func (e *Engine) SetMode(m Mode, ttl time.Duration) error {
	if int(m) >= len(modeNames) {
		return invalid("mode", "unknown")
	}
	if ttl <= 0 || ttl > maxModeTTL {
		return invalid("ttl", "must be in (0, 24h]") // T-42: no mode is forgotten for days
	}
	var st *modeState
	if m != ModeNormal {
		st = &modeState{mode: m, until: time.Now().Add(ttl)}
	}
	e.mode.Store(st)
	e.modeChanged(m)
	return nil
}

// currentMode returns the incident mode, reverting an expired one first.
func (e *Engine) currentMode() Mode {
	st := e.mode.Load()
	if st == nil {
		return ModeNormal
	}
	if time.Now().Before(st.until) {
		return st.mode
	}
	if e.mode.CompareAndSwap(st, nil) { // one caller reports the expiry
		e.modeChanged(ModeNormal)
	}
	return ModeNormal
}

// modeChanged reports a mode change (FR-MODE-1).
func (e *Engine) modeChanged(m Mode) {
	emit(e.cfg.Observer, Event{Kind: EvMode, Time: time.Now(), Reason: modeNames[m]})
	e.cfg.Logger.Info("weir: incident mode", "mode", modeNames[m])
}

// staleOK evaluates sp's stale entry for an error condition (01 §7.2): its
// stale-if-error window, widened under ModeStaleOnError to 24 h for entries
// the RFC lets a disconnected cache serve (FR-MODE-2, RFC 9111 §4.2.4). The
// flags that forbid stale serving (FR-STL-3) still apply; s-maxage implies
// proxy-revalidate (RFC 9111 §5.2.2.10).
func (e *Engine) staleOK(sp *fetchSpec, now time.Time) (staleness time.Duration, ok bool) {
	ent := sp.lk.entry
	st, staleness, ok := httpcc.Evaluate(ent, sp.lk.epoch, sp.lk.epochOK, now)
	if ok || st == httpcc.Unusable || e.currentMode() != ModeStaleOnError {
		return staleness, ok
	}
	const forbid = store.FlagMustRevalidate | store.FlagProxyRevalidate | store.FlagNoCache | store.FlagSMaxAge
	invalidated := sp.lk.epochOK && sp.lk.epoch.Mode == store.EpochInvalid
	return staleness, ent.Flags&forbid == 0 && !invalidated && staleness >= 0 && staleness <= maxModeTTL
}
