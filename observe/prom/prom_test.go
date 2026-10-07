package prom_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/observe/prom"
)

var _ weir.Observer = (*prom.Observer)(nil)

type fakeStats struct{ st weir.EngineStats }

func (f fakeStats) Stats() weir.EngineStats { return f.st }

// FR-OBS-4, 04 §9.3: each event kind moves the counter the table names, with
// the labels it names.
func TestObserverCounters(t *testing.T) {
	tests := []struct {
		name   string
		ev     weir.Event
		metric string
		want   string
	}{
		{"request outcome", weir.Event{Kind: weir.EvRequest, Reason: "hit"}, "weir_requests_total",
			`# HELP weir_requests_total Requests served, by outcome.
# TYPE weir_requests_total counter
weir_requests_total{outcome="hit"} 1
`},
		{"fetch end ok", weir.Event{Kind: weir.EvFetchEnd, Reason: "foreground", Status: 200}, "weir_origin_fetches_total",
			`# HELP weir_origin_fetches_total Origin fetches, by class and result.
# TYPE weir_origin_fetches_total counter
weir_origin_fetches_total{class="foreground",result="ok"} 1
`},
		{"fetch end 503 is a gateway failure", weir.Event{Kind: weir.EvFetchEnd, Reason: "background", Status: 503}, "weir_origin_fetches_total",
			`# HELP weir_origin_fetches_total Origin fetches, by class and result.
# TYPE weir_origin_fetches_total counter
weir_origin_fetches_total{class="background",result="gateway_failure"} 1
`},
		{"fetch end 500 is ok", weir.Event{Kind: weir.EvFetchEnd, Reason: "pass", Status: 500}, "weir_origin_fetches_total",
			`# HELP weir_origin_fetches_total Origin fetches, by class and result.
# TYPE weir_origin_fetches_total counter
weir_origin_fetches_total{class="pass",result="ok"} 1
`},
		{"fetch end status 0 is an error", weir.Event{Kind: weir.EvFetchEnd, Reason: "warm"}, "weir_origin_fetches_total",
			`# HELP weir_origin_fetches_total Origin fetches, by class and result.
# TYPE weir_origin_fetches_total counter
weir_origin_fetches_total{class="warm",result="error"} 1
`},
		{"shed", weir.Event{Kind: weir.EvShed, Reason: "queue-full"}, "weir_shed_total",
			`# HELP weir_shed_total Requests the limiter refused, by reason.
# TYPE weir_shed_total counter
weir_shed_total{reason="queue-full"} 1
`},
		{"stale served", weir.Event{Kind: weir.EvStaleServed, Reason: "sie"}, "weir_stale_served_total",
			`# HELP weir_stale_served_total Stale responses served, by reason.
# TYPE weir_stale_served_total counter
weir_stale_served_total{reason="sie"} 1
`},
		{"coalesce join", weir.Event{Kind: weir.EvCoalesceJoin}, "weir_coalesced_total",
			`# HELP weir_coalesced_total Requests that joined an existing flight.
# TYPE weir_coalesced_total counter
weir_coalesced_total 1
`},
		{"coalesce timeout", weir.Event{Kind: weir.EvCoalesceTimeout, Reason: "direct"}, "weir_coalesce_timeouts_total",
			`# HELP weir_coalesce_timeouts_total Flight waits that expired, by fallback taken.
# TYPE weir_coalesce_timeouts_total counter
weir_coalesce_timeouts_total{fallback="direct"} 1
`},
		{"breaker transition", weir.Event{Kind: weir.EvBreakerState, Reason: "open"}, "weir_breaker_transitions_total",
			`# HELP weir_breaker_transitions_total Origin breaker transitions, by new state.
# TYPE weir_breaker_transitions_total counter
weir_breaker_transitions_total{to="open"} 1
`},
		{"store error", weir.Event{Kind: weir.EvStoreError, Reason: "get"}, "weir_store_errors_total",
			`# HELP weir_store_errors_total Store operations that reported unavailable, by operation.
# TYPE weir_store_errors_total counter
weir_store_errors_total{op="get"} 1
`},
		{"not stored", weir.Event{Kind: weir.EvNotStored, Reason: "private"}, "weir_not_stored_total",
			`# HELP weir_not_stored_total Responses not stored, by reason.
# TYPE weir_not_stored_total counter
weir_not_stored_total{reason="private"} 1
`},
		{"key rejected", weir.Event{Kind: weir.EvKeyRejected, Reason: "bad-host"}, "weir_key_rejections_total",
			`# HELP weir_key_rejections_total Requests refused by key validation, by reason.
# TYPE weir_key_rejections_total counter
weir_key_rejections_total{reason="bad-host"} 1
`},
		{"negative served", weir.Event{Kind: weir.EvNegativeServed}, "weir_negative_served_total",
			`# HELP weir_negative_served_total Negative cache entries served.
# TYPE weir_negative_served_total counter
weir_negative_served_total 1
`},
		{"purge", weir.Event{Kind: weir.EvPurge, Reason: "soft"}, "weir_purges_total",
			`# HELP weir_purges_total Purges and invalidations, by mode.
# TYPE weir_purges_total counter
weir_purges_total{mode="soft"} 1
`},
		{"miss-rate anomaly", weir.Event{Kind: weir.EvMissRateAnomaly, Reason: "flag"}, "weir_miss_rate_anomalies_total",
			`# HELP weir_miss_rate_anomalies_total Miss-rate anomalies, by action.
# TYPE weir_miss_rate_anomalies_total counter
weir_miss_rate_anomalies_total{action="flag"} 1
`},
		{"evictions add the batch size", weir.Event{Kind: weir.EvEvict, Reason: "small", Status: 7}, "weir_evictions_total",
			`# HELP weir_evictions_total Records the memory store evicted, by queue.
# TYPE weir_evictions_total counter
weir_evictions_total{queue="small"} 7
`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := prom.NewObserver()
			o.Observe(tc.ev)
			if err := testutil.CollectAndCompare(o, strings.NewReader(tc.want), tc.metric); err != nil {
				t.Error(err)
			}
		})
	}
}

// 04 §9.3: the fetch histogram is labelled by class and fed by fetch-end
// durations.
func TestFetchSecondsHistogram(t *testing.T) {
	o := prom.NewObserver()
	o.Observe(weir.Event{Kind: weir.EvFetchEnd, Reason: "foreground", Status: 200, Duration: 20 * time.Millisecond})
	o.Observe(weir.Event{Kind: weir.EvFetchEnd, Reason: "foreground", Status: 200, Duration: 3 * time.Second})
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(o)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "weir_origin_fetch_seconds" {
			continue
		}
		h := mf.GetMetric()[0].GetHistogram()
		if h.GetSampleCount() != 2 || h.GetSampleSum() < 3.01 || h.GetSampleSum() > 3.03 {
			t.Errorf("count=%d sum=%v, want 2 and about 3.02", h.GetSampleCount(), h.GetSampleSum())
		}
		if got := mf.GetMetric()[0].GetLabel()[0].GetValue(); got != "foreground" {
			t.Errorf("class label = %q", got)
		}
		return
	}
	t.Error("weir_origin_fetch_seconds not exported")
}

// 04 §9.3: gauges come from Engine.Stats at scrape time, not from events.
func TestCollectorGauges(t *testing.T) {
	c := prom.NewCollector(fakeStats{weir.EngineStats{Inflight: 3, Queued: 5, BreakerState: weir.BreakerOpen, StoreBytes: 4096}})
	want := `# HELP weir_breaker_state Origin breaker state: 0 closed, 1 half-open, 2 open.
# TYPE weir_breaker_state gauge
weir_breaker_state 2
# HELP weir_limiter_queue_depth Origin fetches waiting for a limiter slot.
# TYPE weir_limiter_queue_depth gauge
weir_limiter_queue_depth 5
# HELP weir_origin_inflight Origin fetches holding a limiter slot.
# TYPE weir_origin_inflight gauge
weir_origin_inflight 3
# HELP weir_store_bytes Bytes held by the store.
# TYPE weir_store_bytes gauge
weir_store_bytes 4096
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want)); err != nil {
		t.Error(err)
	}
}

// 04 §9.2: a store that cannot report its size reports -1; the exporter omits
// weir_store_bytes rather than export a negative size.
func TestCollectorOmitsStoreBytesWhenUnknown(t *testing.T) {
	c := prom.NewCollector(fakeStats{weir.EngineStats{StoreBytes: -1}})
	if got := testutil.CollectAndCount(c, "weir_store_bytes"); got != 0 {
		t.Errorf("weir_store_bytes series = %d, want 0", got)
	}
	if got := testutil.CollectAndCount(c); got != 3 {
		t.Errorf("series = %d, want 3", got)
	}
}

// 04 §9.3: every name in the table registers together without clashes, and
// follows Prometheus naming rules.
func TestAllMetricNamesRegisterAndLint(t *testing.T) {
	o := prom.NewObserver()
	c := prom.NewCollector(fakeStats{weir.EngineStats{StoreBytes: 1}})
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(o, c)
	// Touch every kind once so each vector exports a series.
	for _, ev := range []weir.Event{
		{Kind: weir.EvRequest, Reason: "miss"}, {Kind: weir.EvFetchEnd, Reason: "pass", Status: 200},
		{Kind: weir.EvShed, Reason: "background"}, {Kind: weir.EvStaleServed, Reason: "swr"},
		{Kind: weir.EvCoalesceJoin}, {Kind: weir.EvCoalesceTimeout, Reason: "stale"},
		{Kind: weir.EvBreakerState, Reason: "closed"}, {Kind: weir.EvStoreError, Reason: "set"},
		{Kind: weir.EvNotStored, Reason: "status"}, {Kind: weir.EvKeyRejected, Reason: "x"},
		{Kind: weir.EvNegativeServed}, {Kind: weir.EvPurge, Reason: "hard"},
		{Kind: weir.EvMissRateAnomaly, Reason: "throttle"}, {Kind: weir.EvEvict, Reason: "main", Status: 1},
	} {
		o.Observe(ev)
	}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, mf := range mfs {
		got[mf.GetName()] = true
	}
	for _, name := range []string{
		"weir_requests_total", "weir_origin_fetches_total", "weir_origin_fetch_seconds",
		"weir_origin_inflight", "weir_limiter_queue_depth", "weir_shed_total",
		"weir_stale_served_total", "weir_coalesced_total", "weir_coalesce_timeouts_total",
		"weir_breaker_state", "weir_breaker_transitions_total", "weir_store_errors_total",
		"weir_not_stored_total", "weir_key_rejections_total", "weir_negative_served_total",
		"weir_purges_total", "weir_miss_rate_anomalies_total", "weir_evictions_total",
		"weir_store_bytes",
	} {
		if !got[name] {
			t.Errorf("metric %s missing", name)
		}
	}
	if len(got) != 19 {
		t.Errorf("exported %d metric families, want 19", len(got))
	}
	for _, c := range []prometheus.Collector{o, c} {
		if problems, err := testutil.CollectAndLint(c); err != nil || len(problems) > 0 {
			t.Errorf("lint: %v %v", problems, err)
		}
	}
}

// FR-OBS-2: observers are called from many goroutines at once.
func TestObserveConcurrently(t *testing.T) {
	o := prom.NewObserver()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				o.Observe(weir.Event{Kind: weir.EvRequest, Reason: "hit"})
				o.Observe(weir.Event{Kind: weir.EvFetchEnd, Reason: "foreground", Status: 200, Duration: time.Millisecond})
			}
		})
	}
	wg.Wait()
	want := `# HELP weir_requests_total Requests served, by outcome.
# TYPE weir_requests_total counter
weir_requests_total{outcome="hit"} 800
`
	if err := testutil.CollectAndCompare(o, strings.NewReader(want), "weir_requests_total"); err != nil {
		t.Error(err)
	}
}

// 04 §9.3: kinds the mapping table does not list (and the zero kind) mint no
// series and never panic.
func TestUnknownKindsAreIgnored(t *testing.T) {
	o := prom.NewObserver()
	o.Observe(weir.Event{Kind: 0})
	o.Observe(weir.Event{Kind: weir.EvFetchStart, Reason: "foreground"})
	o.Observe(weir.Event{Kind: weir.EvMode, Reason: "bypass"})
	// The two label-free counters always export a zero series; nothing else.
	if got := testutil.CollectAndCount(o); got != 2 {
		t.Errorf("series = %d, want 2", got)
	}
}

// FR-OBS-2: Weir does not recover observer panics, and client_golang panics
// on invalid UTF-8 labels, so the exporter scrubs them. A negative duration
// or status must not panic either.
func TestInvalidReasonDoesNotPanic(t *testing.T) {
	o := prom.NewObserver()
	for k := weir.EvRequest; k <= weir.EvMode; k++ {
		o.Observe(weir.Event{Kind: k, Reason: "\xff\xfe", Status: -5, Duration: -time.Second})
	}
	want := `# HELP weir_requests_total Requests served, by outcome.
# TYPE weir_requests_total counter
weir_requests_total{outcome="?"} 1
`
	if err := testutil.CollectAndCompare(o, strings.NewReader(want), "weir_requests_total"); err != nil {
		t.Error(err)
	}
}

// NFR-3, 04 §9.2: series count is set by the reason vocabularies, never by
// request data; Partition is not a label.
func TestSeriesBoundedByVocabulary(t *testing.T) {
	vocab := map[weir.EventKind][]string{
		weir.EvRequest:         {"hit", "stale", "miss", "revalidated", "collapsed", "pass", "bypass", "negative", "error"},
		weir.EvFetchEnd:        {"foreground", "background", "warm", "pass"},
		weir.EvShed:            {"queue-full", "queue-timeout", "background"},
		weir.EvStaleServed:     {"swr", "sie", "shed", "circuit-open", "coalesce-timeout"},
		weir.EvCoalesceTimeout: {"stale", "direct"},
		weir.EvPurge:           {"soft", "hard", "invalid", "group", "group-invalid"},
	}
	o := prom.NewObserver()
	feed := func(partition string) {
		for k, reasons := range vocab {
			for _, r := range reasons {
				o.Observe(weir.Event{Kind: k, Reason: r, Partition: partition, Status: 200})
			}
		}
	}
	feed("a")
	before := testutil.CollectAndCount(o)
	for i := range 50 {
		feed(strings.Repeat("p", i+1))
	}
	if after := testutil.CollectAndCount(o); after != before {
		t.Errorf("series grew from %d to %d with new partitions", before, after)
	}
}
