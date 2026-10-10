package weircaddy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/caddyserver/caddy/v2"
	dto "github.com/prometheus/client_model/go"
)

var metricSeq atomic.Int32

func metricName() string { return fmt.Sprintf("met-%d", metricSeq.Add(1)) }

func gather(t *testing.T, ctx caddy.Context) map[string]*dto.MetricFamily {
	t.Helper()
	fams, err := ctx.GetMetricsRegistry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*dto.MetricFamily{}
	for _, f := range fams {
		out[f.GetName()] = f
	}
	return out
}

// value returns the sum of the series of family fam whose name label is name
// (and, when label is not empty, whose label equals val).
func value(t *testing.T, ctx caddy.Context, fam, name, label, val string) float64 {
	t.Helper()
	f := gather(t, ctx)[fam]
	if f == nil {
		return 0
	}
	var sum float64
	for _, m := range f.GetMetric() {
		match := label == ""
		isName := false
		for _, l := range m.GetLabel() {
			if l.GetName() == "name" && l.GetValue() == name {
				isName = true
			}
			if l.GetName() == label && l.GetValue() == val {
				match = true
			}
		}
		if !isName || !match {
			continue
		}
		switch {
		case m.Counter != nil:
			sum += m.GetCounter().GetValue()
		case m.Gauge != nil:
			sum += m.GetGauge().GetValue()
		}
	}
	return sum
}

func serveOne(t *testing.T, h *Handler, path string) {
	t.Helper()
	var calls atomic.Int32
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com"+path, nil)
	if err := h.ServeHTTP(w, r, respond(&calls, "x")); err != nil {
		t.Fatal(err)
	}
}

// FR-OBS-4, 04 §9.3: names match the mapping, every series carries the
// handler's name, and the load's pedantic registry gathers them.
func TestMetricsNamesAndNameLabel(t *testing.T) {
	ctx, name := newCtx(t), metricName()
	h := mustLoad(t, ctx, fmt.Sprintf(`{"name":%q,"max_bytes":"200MiB"}`, name))
	serveOne(t, h, "/a")
	serveOne(t, h, "/a")
	fams := gather(t, ctx)
	for _, want := range []string{
		"weir_requests_total", "weir_origin_fetches_total", "weir_origin_fetch_seconds",
		"weir_origin_inflight", "weir_limiter_queue_depth", "weir_breaker_state",
		"weir_store_bytes", "weir_coalesced_total", "weir_negative_served_total",
	} {
		f := fams[want]
		if f == nil {
			t.Errorf("%s missing", want)
			continue
		}
		for _, m := range f.GetMetric() {
			var got string
			for _, l := range m.GetLabel() {
				if l.GetName() == "name" {
					got = l.GetValue()
				}
			}
			if got != name {
				t.Errorf("%s series has name label %q, want %q", want, got, name)
			}
		}
	}
	if got := value(t, ctx, "weir_requests_total", name, "", ""); got != 2 {
		t.Errorf("weir_requests_total = %v, want 2", got)
	}
	if got := value(t, ctx, "weir_origin_fetches_total", name, "result", "ok"); got != 1 {
		t.Errorf("ok fetches = %v, want 1", got)
	}
}

// 08 §8: two handlers in one load, with the same name or different ones, do
// not collide on the pedantic registry. Same-name handlers share a set.
func TestMetricsRegisteredOncePerRegistry(t *testing.T) {
	ctx, a, b := newCtx(t), metricName(), metricName()
	h1 := mustLoad(t, ctx, fmt.Sprintf(`{"name":%q,"max_bytes":"200MiB"}`, a))
	h2 := mustLoad(t, ctx, fmt.Sprintf(`{"name":%q,"max_bytes":"200MiB"}`, a))
	h3 := mustLoad(t, ctx, fmt.Sprintf(`{"name":%q,"max_bytes":"200MiB"}`, b))
	if h1.metrics != h2.metrics || h1.metrics == h3.metrics {
		t.Fatal("same-name handlers must share a set and different names must not")
	}
	serveOne(t, h1, "/1")
	serveOne(t, h2, "/2")
	serveOne(t, h3, "/3")
	if got := value(t, ctx, "weir_requests_total", a, "", ""); got != 2 {
		t.Errorf("name %s requests = %v, want 2", a, got)
	}
	if got := value(t, ctx, "weir_requests_total", b, "", ""); got != 1 {
		t.Errorf("name %s requests = %v, want 1", b, got)
	}
	// Dropping one of two sharers keeps the series; dropping the last
	// unregisters them and frees the entry.
	if err := h1.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if got := value(t, ctx, "weir_requests_total", a, "", ""); got != 2 {
		t.Errorf("after one Cleanup requests = %v, want 2", got)
	}
	if err := h2.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if f := gather(t, ctx)["weir_requests_total"]; f != nil && value(t, ctx, "weir_requests_total", a, "", "") != 0 {
		t.Error("series of a released set are still registered")
	}
	metrics.mu.Lock()
	_, left := metrics.sets[metricKey{ctx.GetMetricsRegistry(), a}]
	metrics.mu.Unlock()
	if left {
		t.Error("metrics set not dropped with its last user")
	}
}

// 08 §8: the pooled store outlives the registry; each Provision repoints the
// eviction sink, and Cleanup of the older load does not undo the new target.
func TestEvictionSinkRepointsOnReload(t *testing.T) {
	old, fresh, name := newCtx(t), newCtx(t), metricName()
	raw := fmt.Sprintf(`{"name":%q,"max_bytes":"200MiB"}`, name)
	h1 := mustLoad(t, old, raw)
	h1.pool.sink.emit("small", 3)
	if got := value(t, old, "weir_evictions_total", name, "queue", "small"); got != 3 {
		t.Fatalf("old registry evictions = %v, want 3", got)
	}
	h2 := mustLoad(t, fresh, raw) // reload overlap: same pooled store
	if h1.pool != h2.pool {
		t.Fatal("reload built a second store")
	}
	h2.pool.sink.emit("main", 2)
	if got := value(t, fresh, "weir_evictions_total", name, "queue", "main"); got != 2 {
		t.Errorf("new registry evictions = %v, want 2", got)
	}
	if got := value(t, old, "weir_evictions_total", name, "queue", "main"); got != 0 {
		t.Errorf("old registry received evictions after the reload: %v", got)
	}
	if err := h1.Cleanup(); err != nil { // the old load stops after the new one started
		t.Fatal(err)
	}
	h2.pool.sink.emit("main", 1)
	if got := value(t, fresh, "weir_evictions_total", name, "queue", "main"); got != 3 {
		t.Errorf("new registry evictions after old Cleanup = %v, want 3", got)
	}
	// The store itself calls the sink: OnEvict is wired at construction.
	if (&Handler{}).memoryConfig(0, new(evictSink)).OnEvict == nil {
		t.Error("memoryConfig leaves OnEvict unset")
	}
}

// 08 §8: a reload gets a fresh registry, hence fresh collectors and counters
// at zero, without a duplicate-registration panic.
func TestMetricsSurviveReload(t *testing.T) {
	old, fresh, name := newCtx(t), newCtx(t), metricName()
	raw := fmt.Sprintf(`{"name":%q,"max_bytes":"200MiB"}`, name)
	h1 := mustLoad(t, old, raw)
	serveOne(t, h1, "/r")
	h2 := mustLoad(t, fresh, raw)
	if got := value(t, fresh, "weir_requests_total", name, "", ""); got != 0 {
		t.Errorf("fresh registry requests = %v, want 0", got)
	}
	serveOne(t, h2, "/r")
	if got := value(t, fresh, "weir_requests_total", name, "", ""); got != 1 {
		t.Errorf("fresh registry requests = %v, want 1", got)
	}
	if got := value(t, old, "weir_requests_total", name, "", ""); got != 1 {
		t.Errorf("old registry requests = %v, want 1", got)
	}
	if err := h1.Cleanup(); err != nil {
		t.Fatal(err)
	}
	serveOne(t, h2, "/s")
	if got := value(t, fresh, "weir_requests_total", name, "", ""); got != 2 {
		t.Errorf("fresh registry requests after old Cleanup = %v, want 2", got)
	}
}

// The gauges aggregate the engines that share a name in one load.
func TestMetricsGaugesAcrossEngines(t *testing.T) {
	ctx, name := newCtx(t), metricName()
	raw := fmt.Sprintf(`{"name":%q,"max_bytes":"200MiB"}`, name)
	h1, h2 := mustLoad(t, ctx, raw), mustLoad(t, ctx, raw)
	serveOne(t, h1, "/g")
	if got := value(t, ctx, "weir_store_bytes", name, "", ""); got <= 0 {
		t.Errorf("weir_store_bytes = %v, want > 0 (one shared store, not a sum)", got)
	}
	if err := h1.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if got := value(t, ctx, "weir_breaker_state", name, "", ""); got != 0 {
		t.Errorf("breaker state = %v, want 0", got)
	}
	_ = h2
}
