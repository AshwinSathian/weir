package weircaddy

import (
	"slices"
	"sync"
	"sync/atomic"

	"github.com/AshwinSathian/weir"
)

// adminEntry is what the admin API needs from one live handler: its engine
// and the tap that reads the scrubbed count of an eager purge.
type adminEntry struct {
	engine *weir.Engine
	tap    *purgeTap
}

// engineRegistry maps a handler name to its live engines (08 §7). Caddy
// rebuilds the admin router on every load before the apps exist, so the router
// cannot hold an engine; it looks one up here instead. Several engines share a
// name while a reload overlaps or when site blocks share a store. Removal is by
// engine identity, so a failed load that cleans up its own engine leaves the
// serving one registered.
//
// Bound (rule 5): one entry per live handler, and a name is at most 64 bytes
// (validateName). Provision adds, Cleanup removes; nothing request-driven
// reaches this map.
type engineRegistry struct {
	mu     sync.RWMutex
	byName map[string][]*adminEntry
}

var engines = &engineRegistry{byName: make(map[string][]*adminEntry)}

func (r *engineRegistry) add(name string, e *adminEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byName[name] = append(r.byName[name], e)
}

// remove drops e by identity and forgets the name when it was the last one.
func (r *engineRegistry) remove(name string, e *adminEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := slices.DeleteFunc(slices.Clone(r.byName[name]), func(x *adminEntry) bool { return x == e })
	if len(l) == 0 {
		delete(r.byName, name)
		return
	}
	r.byName[name] = l
}

// list returns a copy of the live entries for name, oldest first.
func (r *engineRegistry) list(name string) []*adminEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Clone(r.byName[name])
}

// engineList is list on the process-wide registry.
func engineList(name string) []*adminEntry { return engines.list(name) }

// purgeTap is the Observer the adapter gives each engine so the admin API can
// report how many records an eager purge deleted: Engine.Purge returns only an
// error, and the count travels in EvPurge's Status (04 §13.5). Only events with
// Reason "hard" count: a group invalidation from an origin response also
// emits EvPurge with a non-zero Status (reason "group"), and origin data must
// not move an operator's number. A non-eager hard purge reports Status 0, and
// only the admin API issues an eager one, so the sum seen during a serialized
// call is that call's count.
type purgeTap struct {
	// sem is a one-slot semaphore, not a mutex: a purge waiting for its turn
	// stops when its request is cancelled (P8, rule 6).
	sem      chan struct{}
	scrubbed atomic.Int64
}

func newPurgeTap() *purgeTap { return &purgeTap{sem: make(chan struct{}, 1)} }

// Observe implements weir.Observer.
func (t *purgeTap) Observe(ev weir.Event) {
	if ev.Kind == weir.EvPurge && ev.Reason == "hard" {
		t.scrubbed.Add(int64(ev.Status))
	}
}

var _ weir.Observer = (*purgeTap)(nil)
