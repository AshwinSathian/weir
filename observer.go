package weir

import (
	"strings"
	"time"
)

// Observer receives engine events (04 §9). Observe is called synchronously on
// the request path, so implementations must be fast and safe for concurrent
// use.
type Observer interface{ Observe(Event) }

// Event is one engine event. Reason uses a fixed vocabulary per Kind and
// never contains request data.
type Event struct {
	Kind      EventKind
	Time      time.Time
	Partition string        // truncated to 256 bytes; empty for engine-wide events; never a metric label
	Duration  time.Duration // fetch, wait, or open duration where relevant
	Status    int
	Reason    string    // fixed vocabulary per kind, safe as a metric label
	Info      CacheInfo // for EvRequest
}

// EventKind identifies an event. Its String form is safe as a metric label.
type EventKind uint8

// EventKind values (04 §9.2).
const (
	EvRequest         EventKind = iota + 1 // every Serve return
	EvFetchStart                           // before every Origin.Fetch
	EvFetchEnd                             // after every Origin.Fetch
	EvCoalesceJoin                         // a request joined an existing flight
	EvCoalesceTimeout                      // a follower wait expired
	EvShed                                 // the limiter refused
	EvStaleServed                          // a stale response was served
	EvRefreshDropped                       // a background refresh was not started
	EvBreakerState                         // breaker transition
	EvStoreError                           // the store guard saw store.ErrUnavailable
	EvStoreBreaker                         // the store guard opened or closed
	EvKeyRejected                          // request validation failed
	EvNotStored                            // storability failed
	EvVaryOverflow                         // the variant cap was reached
	EvNegativeServed                       // a negative entry was used
	EvPurge                                // Purge or invalidation wrote epochs
	EvMissRateAnomaly                      // a window closed with an anomalous partition
	EvEvict                                // the memory store evicted
	EvMode                                 // the incident mode changed (FR-MODE-1)
	evCount
)

var eventKindNames = [evCount]string{
	EvRequest:         "request",
	EvFetchStart:      "fetch-start",
	EvFetchEnd:        "fetch-end",
	EvCoalesceJoin:    "coalesce-join",
	EvCoalesceTimeout: "coalesce-timeout",
	EvShed:            "shed",
	EvStaleServed:     "stale-served",
	EvRefreshDropped:  "refresh-dropped",
	EvBreakerState:    "breaker-state",
	EvStoreError:      "store-error",
	EvStoreBreaker:    "store-breaker",
	EvKeyRejected:     "key-rejected",
	EvNotStored:       "not-stored",
	EvVaryOverflow:    "vary-overflow",
	EvNegativeServed:  "negative-served",
	EvPurge:           "purge",
	EvMissRateAnomaly: "miss-rate-anomaly",
	EvEvict:           "evict",
	EvMode:            "mode",
}

// String returns the kind's label name, or "unknown".
func (k EventKind) String() string {
	if k == 0 || k >= evCount {
		return "unknown"
	}
	return eventKindNames[k]
}

// maxPartitionBytes caps Event.Partition, the one field built from request
// input, so an observer that retains events holds bounded data (04 §9.1).
const maxPartitionBytes = 256

// emit delivers ev to obs; a nil obs discards it.
func emit(obs Observer, ev Event) {
	if obs == nil {
		return
	}
	if len(ev.Partition) > maxPartitionBytes {
		ev.Partition = strings.Clone(ev.Partition[:maxPartitionBytes]) // drop the long backing array
	}
	obs.Observe(ev)
}
