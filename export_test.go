package weir

import (
	"context"
	"time"

	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/internal/missrate"
)

// GoBackground exposes goBackground to external tests.
func GoBackground(e *Engine, f func(context.Context)) { e.goBackground(f) }

// Flights returns the number of flights in e's coalescing table.
func Flights(e *Engine) int { return e.flights.Len() }

// MissWindow delivers a miss-rate report for the window that closed at end,
// naming the partitions of reqs as anomalous, as the tracker would.
func MissWindow(e *Engine, end time.Time, reqs ...*Request) {
	var found []missrate.Anomaly
	for _, r := range reqs {
		c, _ := keys.Classify((*keys.Request)(r), &e.kcfg)
		found = append(found, missrate.Anomaly{Partition: c.PartitionH, Sample: c.Partition})
	}
	e.missWindow(end, found)
}
