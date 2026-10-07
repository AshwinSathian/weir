package prom

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/AshwinSathian/weir"
)

// Observer turns engine events into the counters and the fetch histogram of
// 04 §9.3. It is safe for concurrent use (FR-OBS-2) and registers as a
// prometheus.Collector.
type Observer struct {
	requests        *prometheus.CounterVec
	fetches         *prometheus.CounterVec
	fetchSeconds    *prometheus.HistogramVec
	shed            *prometheus.CounterVec
	staleServed     *prometheus.CounterVec
	coalesced       prometheus.Counter
	coalesceTimeout *prometheus.CounterVec
	breakerTrans    *prometheus.CounterVec
	storeErrors     *prometheus.CounterVec
	notStored       *prometheus.CounterVec
	keyRejections   *prometheus.CounterVec
	negativeServed  prometheus.Counter
	purges          *prometheus.CounterVec
	anomalies       *prometheus.CounterVec
	evictions       *prometheus.CounterVec
	all             []prometheus.Collector
}

var _ weir.Observer = (*Observer)(nil)

// NewObserver returns an Observer with every counter at zero.
func NewObserver() *Observer {
	o := &Observer{
		requests:        vec("weir_requests_total", "Requests served, by outcome.", "outcome"),
		fetches:         vec("weir_origin_fetches_total", "Origin fetches, by class and result.", "class", "result"),
		shed:            vec("weir_shed_total", "Requests the limiter refused, by reason.", "reason"),
		staleServed:     vec("weir_stale_served_total", "Stale responses served, by reason.", "reason"),
		coalesceTimeout: vec("weir_coalesce_timeouts_total", "Flight waits that expired, by fallback taken.", "fallback"),
		breakerTrans:    vec("weir_breaker_transitions_total", "Origin breaker transitions, by new state.", "to"),
		storeErrors:     vec("weir_store_errors_total", "Store operations that reported unavailable, by operation.", "op"),
		notStored:       vec("weir_not_stored_total", "Responses not stored, by reason.", "reason"),
		keyRejections:   vec("weir_key_rejections_total", "Requests refused by key validation, by reason.", "reason"),
		purges:          vec("weir_purges_total", "Purges and invalidations, by mode.", "mode"),
		anomalies:       vec("weir_miss_rate_anomalies_total", "Miss-rate anomalies, by action.", "action"),
		evictions:       vec("weir_evictions_total", "Records the memory store evicted, by queue.", "queue"),
		fetchSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "weir_origin_fetch_seconds",
			Help:    "Time from origin call to response headers or error, by class.",
			Buckets: prometheus.DefBuckets,
		}, []string{"class"}),
		coalesced:      prometheus.NewCounter(prometheus.CounterOpts{Name: "weir_coalesced_total", Help: "Requests that joined an existing flight."}),
		negativeServed: prometheus.NewCounter(prometheus.CounterOpts{Name: "weir_negative_served_total", Help: "Negative cache entries served."}),
	}
	o.all = []prometheus.Collector{
		o.requests, o.fetches, o.fetchSeconds, o.shed, o.staleServed, o.coalesced,
		o.coalesceTimeout, o.breakerTrans, o.storeErrors, o.notStored, o.keyRejections,
		o.negativeServed, o.purges, o.anomalies, o.evictions,
	}
	return o
}

func vec(name, help string, labels ...string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
}

// Observe implements weir.Observer. Kinds that 04 §9.3 does not map are
// ignored. Reasons are a fixed vocabulary per kind (04 §9.2), so label values
// stay bounded (NFR-3).
func (o *Observer) Observe(ev weir.Event) {
	switch ev.Kind {
	case weir.EvRequest:
		o.requests.WithLabelValues(ev.Reason).Inc()
	case weir.EvFetchEnd:
		o.fetches.WithLabelValues(ev.Reason, fetchResult(ev.Status)).Inc()
		o.fetchSeconds.WithLabelValues(ev.Reason).Observe(ev.Duration.Seconds())
	case weir.EvCoalesceJoin:
		o.coalesced.Inc()
	case weir.EvCoalesceTimeout:
		o.coalesceTimeout.WithLabelValues(ev.Reason).Inc()
	case weir.EvShed:
		o.shed.WithLabelValues(ev.Reason).Inc()
	case weir.EvStaleServed:
		o.staleServed.WithLabelValues(ev.Reason).Inc()
	case weir.EvBreakerState:
		o.breakerTrans.WithLabelValues(ev.Reason).Inc()
	case weir.EvStoreError:
		o.storeErrors.WithLabelValues(ev.Reason).Inc()
	case weir.EvKeyRejected:
		o.keyRejections.WithLabelValues(ev.Reason).Inc()
	case weir.EvNotStored:
		o.notStored.WithLabelValues(ev.Reason).Inc()
	case weir.EvNegativeServed:
		o.negativeServed.Inc()
	case weir.EvPurge:
		o.purges.WithLabelValues(ev.Reason).Inc()
	case weir.EvMissRateAnomaly:
		o.anomalies.WithLabelValues(ev.Reason).Inc()
	case weir.EvEvict:
		// Status is the batch size (04 §9.2).
		o.evictions.WithLabelValues(ev.Reason).Add(float64(max(ev.Status, 0)))
	}
}

// fetchResult classifies an EvFetchEnd status: 0 is a transport error or
// timeout, 502, 503 and 504 are the gateway failures the breaker counts, and
// anything else is a response.
func fetchResult(status int) string {
	switch status {
	case 0:
		return "error"
	case 502, 503, 504:
		return "gateway_failure"
	default:
		return "ok"
	}
}

// Describe implements prometheus.Collector.
func (o *Observer) Describe(ch chan<- *prometheus.Desc) {
	for _, c := range o.all {
		c.Describe(ch)
	}
}

// Collect implements prometheus.Collector.
func (o *Observer) Collect(ch chan<- prometheus.Metric) {
	for _, c := range o.all {
		c.Collect(ch)
	}
}
