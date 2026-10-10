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
// error, and the count travels in EvPurge's Status (04 §13.5). Only an eager
// purge sets a non-zero Status, and only the admin API issues one, so summing
// the Status values seen during a serialized call yields that call's count.
type purgeTap struct {
	mu       sync.Mutex // serializes admin purges on one engine
	scrubbed atomic.Int64
}

// Observe implements weir.Observer.
func (t *purgeTap) Observe(ev weir.Event) {
	if ev.Kind == weir.EvPurge {
		t.scrubbed.Add(int64(ev.Status))
	}
}

var _ weir.Observer = (*purgeTap)(nil)
