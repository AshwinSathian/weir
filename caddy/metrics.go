package weircaddy

import (
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/observe/prom"
)

// Metrics on Caddy's registry (docs/08 §8, 04 §9.3).
//
// The card wraps observe/prom instead of hand-rolling collectors: the names,
// help strings, buckets and the reason sanitizing then stay in one place, and a
// change to 04 §9.3 reaches the Caddy module without a second edit.
// prometheus.WrapRegistererWith adds the `name` label as a constant label, so
// observe/prom needs no knowledge of Caddy. The price is that observe/prom
// stays a dependency of this module.

// metricSet is the collector set of one handler name inside one config load.
// Every handler with that name in the load shares it (they share a store too),
// so the pedantic registry sees each descriptor once.
type metricSet struct {
	obs  *prom.Observer
	regr prometheus.Registerer
	cols []prometheus.Collector

	mu      sync.Mutex
	engines []*weir.Engine
	refs    int
}

type metricKey struct {
	reg  *prometheus.Registry
	name string
}

// metricsRegistry maps (registry, name) to its set. Caddy creates a registry
// per config load, so the pointer ties a set to one load.
//
// Bound (rule 5): one entry per live handler name per live load (one or two
// loads, three while a failed load has not cleaned up). Provision adds,
// the last Cleanup of a key drops it; nothing request-driven reaches this map.
type metricsRegistry struct {
	mu   sync.Mutex
	sets map[metricKey]*metricSet
}

var metrics = &metricsRegistry{sets: make(map[metricKey]*metricSet)}

// acquire returns the set for (reg, name), registering its collectors when it
// is the first user. The caller must release it exactly once.
func (m *metricsRegistry) acquire(reg *prometheus.Registry, name string) (*metricSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := metricKey{reg, name}
	if s := m.sets[k]; s != nil {
		s.refs++
		return s, nil
	}
	s := &metricSet{
		obs:  prom.NewObserver(),
		regr: prometheus.WrapRegistererWith(prometheus.Labels{"name": name}, reg),
		refs: 1,
	}
	s.cols = []prometheus.Collector{s.obs, prom.NewCollector(s)}
	for i, c := range s.cols {
		if err := s.regr.Register(c); err != nil {
			for _, done := range s.cols[:i] {
				s.regr.Unregister(done)
			}
			return nil, err
		}
	}
	m.sets[k] = s
	return s, nil
}

// release gives back one reference and unregisters the collectors with the
// last one, so a registry that outlives its handlers (a failed load keeps
// the previous registry) holds no dead collectors.
func (m *metricsRegistry) release(reg *prometheus.Registry, name string, s *metricSet) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.refs--; s.refs > 0 {
		return
	}
	for _, c := range s.cols {
		s.regr.Unregister(c)
	}
	delete(m.sets, metricKey{reg, name})
}

func (s *metricSet) addEngine(e *weir.Engine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.engines = append(s.engines, e)
}

func (s *metricSet) removeEngine(e *weir.Engine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.engines {
		if x == e {
			s.engines = append(s.engines[:i:i], s.engines[i+1:]...)
			return
		}
	}
}

// Stats implements prom.StatsSource over every engine of the name in this
// load. In-flight fetches and queue depth add up. Breaker state takes the
// worst engine. Engines of one name share a store, so its size is the largest
// report, not the sum; a negative value means no engine can report it.
func (s *metricSet) Stats() weir.EngineStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := weir.EngineStats{StoreBytes: -1}
	for _, e := range s.engines {
		st := e.Stats()
		out.Inflight += st.Inflight
		out.Queued += st.Queued
		out.BreakerState = max(out.BreakerState, st.BreakerState)
		out.StoreBytes = max(out.StoreBytes, st.StoreBytes)
	}
	return out
}

// evictSink forwards a pooled store's eviction counts to the metric set of the
// newest load. The store outlives every registry (08 §3), and
// memory.Config.OnEvict is fixed at construction, so the callback points at
// this sink and each Provision repoints it.
type evictSink struct{ target atomic.Pointer[metricSet] }

func (k *evictSink) emit(queue string, n int) {
	if s := k.target.Load(); s != nil {
		s.obs.Observe(weir.Event{Kind: weir.EvEvict, Reason: queue, Status: n})
	}
}

// fanout delivers each event to every observer in order. The adapter's
// observers do not block (FR-OBS-2).
type fanout []weir.Observer

// Observe implements weir.Observer.
func (f fanout) Observe(ev weir.Event) {
	for _, o := range f {
		o.Observe(ev)
	}
}
