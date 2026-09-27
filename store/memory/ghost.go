package memory

// ghostFloor is the least number of fingerprints a ghost keeps (05 §5.3).
const ghostFloor = 1024

// ghost remembers fingerprints of keys evicted from the small queue, so a key
// that returns soon after goes straight to main (05 §5.3). It holds at most
// max(ghostFloor, entries in main) fingerprints (NFR-3).
type ghost struct {
	ring []uint64 // FIFO of fingerprints; live part is ring[head:]
	head int
	set  map[uint64]uint32 // fingerprint -> occurrences in ring
}

// add appends fp and drops the oldest fingerprints beyond limit.
func (g *ghost) add(fp uint64, limit int) {
	g.ring = append(g.ring, fp)
	g.set[fp]++
	g.trim(limit)
}

// trim drops the oldest fingerprints beyond max(ghostFloor, limit). The shard
// calls it whenever main shrinks, so the bound follows main's current size.
func (g *ghost) trim(limit int) {
	for len(g.ring)-g.head > max(ghostFloor, limit) {
		g.drop(g.ring[g.head])
		g.head++
	}
	// Compact once the dead prefix outgrows the live part, so the backing
	// array stays within twice the bound.
	if g.head > len(g.ring)-g.head {
		g.ring = append(g.ring[:0], g.ring[g.head:]...)
		g.head = 0
	}
}

// take reports whether fp is in the ghost and forgets it. Its ring slots stay
// until they age out; drop tolerates that. If fp is added again meanwhile,
// the stale slot aging out forgets the new occurrence early. That only costs
// an occasional ghost hit, never correctness.
func (g *ghost) take(fp uint64) bool {
	if _, ok := g.set[fp]; !ok {
		return false
	}
	delete(g.set, fp)
	return true
}

func (g *ghost) drop(fp uint64) {
	switch c := g.set[fp]; c {
	case 0:
	case 1:
		delete(g.set, fp)
	default:
		g.set[fp] = c - 1
	}
}
