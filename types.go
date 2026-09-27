package weir

// Purge describes what Engine.Purge invalidates (FR-PRG-1). Invalid input
// rejects the whole call before any epoch is written.
type Purge struct {
	Mode   PurgeMode // PurgeSoft (zero value) or PurgeHard
	All    bool
	URLs   []string // absolute http(s) URLs; parsed and rewritten exactly like requests
	Origin string   // "scheme://host[:port]"; required when Groups is non-empty
	Groups []string
	Eager  bool // M15: with PurgeHard, also delete matching records now (store.Scrubber)
}

// PurgeMode selects how purged entries are treated.
type PurgeMode uint8

// PurgeMode values.
const (
	PurgeSoft PurgeMode = iota // entries become stale and may still be served stale
	PurgeHard                  // entries become unreachable
)

// WarmStats counts the outcome of a warm call per URL.
type WarmStats struct{ Fetched, Skipped, NotStored, Failed int }

// EngineStats is a point-in-time snapshot for gauges, polled by exporters.
type EngineStats struct {
	Inflight     int          // origin fetches holding limiter slots
	Queued       int          // fetches waiting for a slot
	BreakerState BreakerState // BreakerClosed, BreakerHalfOpen or BreakerOpen
	StoreBytes   int64        // -1 when the store does not implement store.Sizer
}

// BreakerState is the origin circuit breaker's state.
type BreakerState uint8

// BreakerState values.
const (
	BreakerClosed BreakerState = iota
	BreakerHalfOpen
	BreakerOpen
)

// Mode is an incident mode set with Engine.SetMode (FR-MODE-1, D33).
type Mode uint8

// Mode values.
const (
	ModeNormal       Mode = iota
	ModeStaleOnError      // serve stale on any error, up to 24 h (FR-MODE-2)
	ModeBypass            // pass every request through, store nothing (FR-MODE-3)
)
