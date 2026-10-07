// Package prom exports Weir's events and gauges as Prometheus metrics
// (docs/04-lld.md §9.3, FR-OBS-4).
//
// Two collectors cover the mapping. [Observer] implements [weir.Observer] and
// owns the counters and the fetch histogram. [Collector] reads
// [weir.Engine.Stats] at scrape time for the gauges. Register both:
//
//	obs := prom.NewObserver()
//	eng, _ := weir.New(weir.Config{Observer: obs})
//	reg.MustRegister(obs, prom.NewCollector(eng))
//
// Event.Partition is derived from request input and is never a label.
package prom
