package weir

import "github.com/AshwinSathian/weir/store"

// Stats returns point-in-time gauges for exporters: in-flight origin fetches,
// queued fetches, breaker state, and store bytes when the store reports them
// (04 §9.2). Both limiter pools count (FR-LIM-7). It only reads: an open
// breaker whose period has ended reports half-open without moving, so Stats
// never calls the Observer (FR-OBS-2) and is safe inside a collector's lock.
func (e *Engine) Stats() EngineStats {
	in, queued := e.lim.Counts()
	upIn, upQueued := e.upl.Counts()
	st := EngineStats{
		Inflight:     in + upIn,
		Queued:       queued + upQueued,
		BreakerState: BreakerState(e.cb.Peek()),
		StoreBytes:   -1,
	}
	if sz, ok := e.cfg.Store.(store.Sizer); ok {
		st.StoreBytes = sz.Bytes()
	}
	return st
}
