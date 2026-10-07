package prom

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/AshwinSathian/weir"
)

// StatsSource is what a Collector polls; *weir.Engine implements it.
type StatsSource interface{ Stats() weir.EngineStats }

// Collector exports the gauges of 04 §9.3 by calling Stats at scrape time.
// Stats only reads and emits no event (FR-OBS-2), so a scrape never feeds
// back into the Observer.
type Collector struct {
	src      StatsSource
	inflight *prometheus.Desc
	queued   *prometheus.Desc
	breaker  *prometheus.Desc
	bytes    *prometheus.Desc
}

// NewCollector returns a Collector that polls src.
func NewCollector(src StatsSource) *Collector {
	return &Collector{
		src:      src,
		inflight: prometheus.NewDesc("weir_origin_inflight", "Origin fetches holding a limiter slot.", nil, nil),
		queued:   prometheus.NewDesc("weir_limiter_queue_depth", "Origin fetches waiting for a limiter slot.", nil, nil),
		breaker:  prometheus.NewDesc("weir_breaker_state", "Origin breaker state: 0 closed, 1 half-open, 2 open.", nil, nil),
		bytes:    prometheus.NewDesc("weir_store_bytes", "Bytes held by the store.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.inflight
	ch <- c.queued
	ch <- c.breaker
	ch <- c.bytes
}

// Collect implements prometheus.Collector. A negative StoreBytes means the
// store cannot report its size, so weir_store_bytes is left out.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	st := c.src.Stats()
	ch <- prometheus.MustNewConstMetric(c.inflight, prometheus.GaugeValue, float64(st.Inflight))
	ch <- prometheus.MustNewConstMetric(c.queued, prometheus.GaugeValue, float64(st.Queued))
	ch <- prometheus.MustNewConstMetric(c.breaker, prometheus.GaugeValue, float64(st.BreakerState))
	if st.StoreBytes >= 0 {
		ch <- prometheus.MustNewConstMetric(c.bytes, prometheus.GaugeValue, float64(st.StoreBytes))
	}
}
