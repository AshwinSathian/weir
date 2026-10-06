package weir

import (
	"time"

	"github.com/AshwinSathian/weir/internal/missrate"
)

// missWindow receives the tracker's report for the window that closed at
// end (04 §8.4). It raises an event and a warning per anomalous partition
// (FR-MR-2) and, with MissRate.Throttle, caps those partitions at one fetch
// for the following window (FR-MR-3). Only the main pool is capped: the
// tracker counts cacheable requests, and those never use the upload pool.
//
// The report can arrive long after end, because the tracker closes a window
// on the next request. It throttles only while end + Window is ahead: a
// flood, an idle hour and one request must not throttle on the hour-old
// window. The event carries end as its Time and says "throttle" only when
// the caps were applied.
//
// The caps hold until end + 2*Window, or until the next report replaces
// them. The next report cannot come before end + Window, and comes a little
// after it under load, so caps that ended at end + Window would lapse at
// every boundary and let MaxPerPartition fetches through
// (TestMissRateThrottleHoldsAcrossWindows). The second window is the
// allowance for that gap; when traffic stops, the caps end there by
// themselves.
func (e *Engine) missWindow(end time.Time, found []missrate.Anomaly) {
	reason := "flag"
	if m := &e.cfg.MissRate; m.Throttle {
		var caps map[uint64]int // nil for an empty or late report: it only lifts the last one
		if len(found) > 0 && time.Now().Before(end.Add(m.Window)) {
			caps = make(map[uint64]int, len(found))
			for _, a := range found {
				caps[a.Partition] = 1
			}
		}
		if e.lim.Throttle(end, end.Add(2*m.Window), caps) {
			reason = "throttle"
		}
	}
	for _, a := range found {
		emit(e.cfg.Observer, Event{Kind: EvMissRateAnomaly, Time: end, Partition: a.Sample, Reason: reason})
		// The partition string is safe to log: validation rejected control
		// bytes (FR-VAL-1) and the tracker cut it to 256 bytes.
		e.cfg.Logger.Warn("weir: miss-rate anomaly", "partition", a.Sample, "window_end", end,
			"requests", a.Reqs, "misses", a.Misses, "throttled", reason == "throttle")
	}
}
