package weircaddy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	dto "github.com/prometheus/client_model/go"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
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
	for fname, f := range fams {
		if !strings.HasPrefix(fname, "weir_") {
			continue
		}
		for _, m := range f.GetMetric() {
			if !slices.ContainsFunc(m.GetLabel(), func(l *dto.LabelPair) bool { return l.GetName() == "name" }) {
				t.Errorf("%s has a series without the name label", fname)
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

// FR-OBS-4, 08 §8: two handlers in one load, with the same name or different ones, do
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
	metricSets.mu.Lock()
	_, left := metricSets.sets[metricKey{ctx.GetMetricsRegistry(), a}]
	metricSets.mu.Unlock()
	if left {
		t.Error("metrics set not dropped with its last user")
	}
}

// FR-OBS-4, 08 §8: the pooled store outlives the registry; each Provision repoints the
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
	// Both loads expose the one store while they overlap, so both count.
	if got := value(t, old, "weir_evictions_total", name, "queue", "main"); got != 2 {
		t.Errorf("old registry evictions during overlap = %v, want 2", got)
	}
	if err := h1.Cleanup(); err != nil { // the old load stops after the new one started
		t.Fatal(err)
	}
	h2.pool.sink.emit("main", 1)
	if got := value(t, fresh, "weir_evictions_total", name, "queue", "main"); got != 3 {
		t.Errorf("new registry evictions after old Cleanup = %v, want 3", got)
	}
}

// FR-OBS-4: a real store eviction reaches the metric through the sink buildStore
// wires into the store, not just through a direct emit.
func TestStoreEvictionReachesMetric(t *testing.T) {
	ctx, name := newCtx(t), metricName()
	h := mustLoad(t, ctx, fmt.Sprintf(`{"name":%q,"max_bytes":"200MiB"}`, name))
	sink := new(evictSink)
	sink.attach(h.metrics)
	var kg [sha256.Size]byte
	st, err := h.buildStore(kg, 4<<20, sink)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	val := bytes.Repeat([]byte("x"), 2<<10)
	for i := range 8000 {
		e := &store.Entry{Status: 200, Body: val, RequestTime: time.Now(), ResponseTime: time.Now(), Date: time.Now(), Expires: time.Now().Add(time.Hour)}
		if err := st.Set(context.Background(), store.Key{byte(i), byte(i >> 8)}, e); err != nil {
			t.Fatal(err)
		}
	}
	var total float64
	for _, q := range []string{"small", "main", "expired"} {
		total += value(t, ctx, "weir_evictions_total", name, "queue", q)
	}
	if total == 0 {
		t.Error("filling a 4 MiB store evicted nothing into weir_evictions_total")
	}
}

// 08 §3: the entry the pool keeps owns the very sink its store was built with.
func TestPoolKeepsTheSinkItBuiltWith(t *testing.T) {
	r := newStoreRegistry()
	var built *evictSink
	build := func(s *evictSink) (store.Store, int64, error) {
		built = s
		st, err := memory.New(memory.Config{OnEvict: s.emit})
		return st, 0, err
	}
	load := new(int)
	p, _, err := r.acquire(load, storeSpec{name: "sinkpool"}, [sha256.Size]byte{}, build)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.release(load, p) }()
	if built == nil || p.sink != built {
		t.Fatal("pooledStore.sink is not the sink the store was built with")
	}
}

// FR-OBS-4, 08 §8: a reload gets a fresh registry, hence fresh collectors and counters
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

// FR-OBS-4: the gauges aggregate the engines that share a name in one load.
// Two engines share one store, so weir_store_bytes is that store's size, not
// twice it, and a Cleanup of one engine leaves the other reporting.
func TestMetricsGaugesAcrossEngines(t *testing.T) {
	ctx, name := newCtx(t), metricName()
	raw := fmt.Sprintf(`{"name":%q,"max_bytes":"200MiB"}`, name)
	h1, h2 := mustLoad(t, ctx, raw), mustLoad(t, ctx, raw)
	serveOne(t, h1, "/g")
	want := float64(h1.engine.Stats().StoreBytes)
	if want <= 0 {
		t.Fatalf("store bytes = %v, want > 0", want)
	}
	if got := value(t, ctx, "weir_store_bytes", name, "", ""); got != want {
		t.Errorf("weir_store_bytes = %v, want the one store's %v", got, want)
	}
	if err := h1.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if got := value(t, ctx, "weir_store_bytes", name, "", ""); got != want {
		t.Errorf("weir_store_bytes after sibling Cleanup = %v, want %v", got, want)
	}
	if got := value(t, ctx, "weir_breaker_state", name, "", ""); got != 0 {
		t.Errorf("breaker state = %v, want 0", got)
	}
	// In-flight and queue depth add; the worst breaker state wins.
	two := &metricSet{engines: []*weir.Engine{h2.engine, h2.engine}}
	if got, one := two.Stats(), h2.engine.Stats(); got.StoreBytes != one.StoreBytes {
		t.Errorf("two engines over one store: StoreBytes %d, want %d (max, not sum)", got.StoreBytes, one.StoreBytes)
	}
}

// FR-OBS-4, FR-OBS-2: cleaning up one of two same-name handlers keeps the
// shared set and the eviction sink alive for the one still serving.
func TestSiblingCleanupKeepsEvictionSink(t *testing.T) {
	ctx, name := newCtx(t), metricName()
	raw := fmt.Sprintf(`{"name":%q,"max_bytes":"200MiB"}`, name)
	h1, h2 := mustLoad(t, ctx, raw), mustLoad(t, ctx, raw)
	if err := h1.Cleanup(); err != nil {
		t.Fatal(err)
	}
	h2.pool.sink.emit("small", 4)
	if got := value(t, ctx, "weir_evictions_total", name, "queue", "small"); got != 4 {
		t.Errorf("evictions after sibling Cleanup = %v, want 4", got)
	}
}

// FR-OBS-4: a load that fails after it attached to the pooled store must not
// silence the load that still serves (08 §8: a failed load overlaps).
func TestFailedLoadCleanupKeepsOlderSink(t *testing.T) {
	old, failed, name := newCtx(t), newCtx(t), metricName()
	raw := fmt.Sprintf(`{"name":%q,"max_bytes":"200MiB"}`, name)
	h1 := mustLoad(t, old, raw)
	h2 := mustLoad(t, failed, raw)
	if err := h2.Cleanup(); err != nil { // the failed load cleans up its handlers
		t.Fatal(err)
	}
	h1.pool.sink.emit("main", 2)
	if got := value(t, old, "weir_evictions_total", name, "queue", "main"); got != 2 {
		t.Errorf("older load evictions = %v, want 2", got)
	}
}

// 08 §8: a Provision that fails after the metrics were acquired gives the
// reference back once and leaves a sibling holder of the set working.
func TestProvisionFailureReleasesMetricSetOnce(t *testing.T) {
	ctx, name := newCtx(t), metricName()
	h1 := mustLoad(t, ctx, fmt.Sprintf(`{"name":%q,"max_bytes":"200MiB"}`, name))
	// Same name, different store settings in one load: stores.acquire fails.
	if _, err := loadIn(t, ctx, fmt.Sprintf(`{"name":%q,"max_bytes":"300MiB"}`, name)); err == nil {
		t.Fatal("conflicting store settings provisioned")
	}
	k := metricKey{ctx.GetMetricsRegistry(), name}
	metricSets.mu.Lock()
	refs := metricSets.sets[k].refs
	metricSets.mu.Unlock()
	if refs != 1 {
		t.Fatalf("set refs after failed Provision = %d, want 1", refs)
	}
	serveOne(t, h1, "/f")
	if got := value(t, ctx, "weir_requests_total", name, "", ""); got != 1 {
		t.Errorf("sibling requests = %v, want 1", got)
	}
	// Cleanup is idempotent and a handler that never provisioned is a no-op.
	for range 2 {
		if err := h1.Cleanup(); err != nil {
			t.Fatal(err)
		}
	}
	if err := new(Handler).Cleanup(); err != nil {
		t.Fatal(err)
	}
	metricSets.mu.Lock()
	_, left := metricSets.sets[k]
	metricSets.mu.Unlock()
	if left {
		t.Error("set left behind after the last Cleanup")
	}
}

// 08 §8: a Provision that fails in weir.New, after the metric set and the
// store were acquired, gives both back once and leaves a sibling working.
func TestProvisionFailureInEngineReleasesMetricSet(t *testing.T) {
	ctx, name := newCtx(t), metricName()
	h1 := mustLoad(t, ctx, fmt.Sprintf(`{"name":%q,"max_bytes":"200MiB"}`, name))
	bad := fmt.Sprintf(`{"name":%q,"max_bytes":"200MiB","limiter":{"max_concurrent":-1}}`, name)
	if _, err := loadIn(t, ctx, bad); err == nil {
		t.Fatal("weir.New accepted a negative max_concurrent")
	}
	k := metricKey{ctx.GetMetricsRegistry(), name}
	metricSets.mu.Lock()
	refs := metricSets.sets[k].refs
	metricSets.mu.Unlock()
	if refs != 1 {
		t.Fatalf("set refs after failed Provision = %d, want 1", refs)
	}
	if n, _ := stores.pool.References(h1.pool.spec); n != 1 {
		t.Fatalf("store refs after failed Provision = %d, want 1", n)
	}
	h1.pool.sink.mu.RLock()
	attached := len(h1.pool.sink.sets)
	h1.pool.sink.mu.RUnlock()
	if attached != 1 {
		t.Errorf("sink holds %d sets, want 1", attached)
	}
	serveOne(t, h1, "/e")
	if got := value(t, ctx, "weir_requests_total", name, "", ""); got != 1 {
		t.Errorf("sibling requests = %v, want 1", got)
	}
}
