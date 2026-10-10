package weircaddy

import (
	"fmt"
	"log/slog"
	"math"
	"runtime/debug"
)

// Memory sizing (08 §7, FR-MEM-1, T-43). Stores without max_bytes share 40%
// of the memory limit. Provision sees one handler at a time and Caddy gives
// it no look-ahead, so an even split needs a count nobody has. Instead the
// k-th new auto-sized store of a load takes half of the part of the budget
// the load has not granted yet (20%, 10%, 5% of the limit). The sum stays
// under 40% by construction until the 160 MiB floor binds.
const (
	budgetPercent = 40
	minBudget     = 16 << 20
	maxBudget     = 8 << 30
)

// memoryBudget is FR-MEM-1's 40% of limit, clamped to [16 MiB, 8 GiB].
// limited is false when no limit is set (debug.SetMemoryLimit(-1) is
// math.MaxInt64), where FR-MEM-1 says each store takes 256 MiB and the
// operator is warned.
func memoryBudget(limit int64) (budget int64, limited bool) {
	if limit == math.MaxInt64 {
		return defaultStoreBytes, false
	}
	return min(max(limit/100*budgetPercent, minBudget), maxBudget), true
}

// nextShare is the size of the next auto-sized store of a load that has
// granted `granted` bytes already. floored reports that the 160 MiB floor
// (minStoreBytes) raised the share, so the load can exceed its budget.
func nextShare(budget, granted int64) (size int64, floored bool) {
	size = max(budget-granted, 0) / 2
	if size < minStoreBytes {
		return minStoreBytes, true
	}
	return size, false
}

// autoSize picks the size for a store about to be built for h in load, which
// has no max_bytes. limit is debug.SetMemoryLimit(-1). It is called from the
// pool's constructor, so the grants it reads are those of the stores built
// or reused earlier in the same load (noteSize).
func (r *storeRegistry) autoSize(load any, limit int64, log *slog.Logger, name string) int64 {
	budget, limited := memoryBudget(limit)
	if !limited {
		if r.warnedNoLimit.CompareAndSwap(false, true) {
			log.Warn("weir: no memory limit is set, auto-sized stores take 256 MiB each; set GOMEMLIMIT or max_bytes (docs/08 §7)",
				"name", name)
		}
		return defaultStoreBytes
	}
	r.mu.Lock()
	var granted int64
	for _, c := range r.loads[load] {
		granted += c.size
	}
	r.mu.Unlock()
	size, floored := nextShare(budget, granted)
	if floored {
		log.Warn("weir: the auto-sized memory share is below the 160 MiB floor and was raised; set max_bytes on the sites that need more or fewer bytes (docs/08 §7)",
			"name", name, "bytes", size)
	}
	return size
}

// noteSize records on the load's claim for p's name how many bytes an
// auto-sized store holds, so the next autoSize in the same load subtracts it.
// A store reused from an earlier load counts at the size it was built with.
func (r *storeRegistry) noteSize(load any, p *pooledStore) {
	if p.spec.maxBytes != 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.loads[load][p.spec.name]; c != nil {
		c.size = p.size.Load()
	}
}

// overcommit sums the live auto-sized stores of every load (a reload that
// adds a site cannot shrink the old ones, 08 §7) and reports whether they
// exceed the 40% budget. With no limit set there is no budget to exceed.
func (r *storeRegistry) overcommit(limit int64) (sum, budget int64, over bool) {
	budget, limited := memoryBudget(limit)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.live {
		for _, p := range l {
			if p.spec.maxBytes == 0 {
				sum += p.size.Load()
			}
		}
	}
	return sum, budget, limited && sum > budget
}

// checkOvercommit logs the T-43 warning, naming max_bytes as the remedy.
func (r *storeRegistry) checkOvercommit(log *slog.Logger, name string) {
	if sum, budget, over := r.overcommit(debug.SetMemoryLimit(-1)); over {
		log.Warn(fmt.Sprintf("weir: auto-sized stores hold %d MiB, above the %d MiB budget (40%% of the memory limit); set max_bytes on each site (docs/08 §7)",
			sum>>20, budget>>20), "name", name)
	}
}
