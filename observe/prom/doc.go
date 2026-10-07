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
// Register one Observer and one Collector per registry and per engine: a
// second of either panics on the duplicate descriptor. Labelled series appear
// on their first event, so alert on absent() as well as rate(). The breaker
// gauge reports half-open as soon as the open period ends, while the
// transition counter moves on the next call that reaches the breaker.
// weir_evictions_total only counts a store that New built; a store passed in
// Config.Store reports through its own memory.Config.OnEvict. Events that
// 04 §9.3 does not map (store breaker, vary overflow, refresh dropped, mode)
// export nothing.
//
// Event.Partition is derived from request input and is never a label.
package prom
