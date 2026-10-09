package memory

import (
	"context"

	"github.com/AshwinSathian/weir/store"
)

// Scrub deletes the response records whose Tags intersect tags and returns
// how many it deleted (FR-PRG-8, 05 §5.3). It takes one shard's write lock at
// a time and walks that shard in full, so the work under any one lock is
// bounded by the shard and a Get on another shard never waits for it. The
// scan is exact (it compares tags, not the epoch sketch), and reaches every
// variant and keyed-header partition because each record carries its own
// tags. Other kinds are left alone: a vary spec or marker is covered by the
// epoch the caller wrote first.
func (s *Store) Scrub(ctx context.Context, tags []store.Tag) (int, error) {
	if s.closed.Load() {
		return 0, store.ErrUnavailable
	}
	if len(tags) == 0 {
		return 0, nil
	}
	total := 0
	for i := range s.shards {
		if err := ctx.Err(); err != nil {
			return total, store.ErrUnavailable // 05 S-2
		}
		total += s.shards[i].scrub(tags)
	}
	return total, nil
}

// scrub unlinks every response record in sh that carries one of tags.
func (sh *shard) scrub(tags []store.Tag) int {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	n := 0
	for _, nd := range sh.m { // deleting from a map during range is defined
		if nd.e.Kind == store.KindResponse && hasTag(nd.e.Tags, tags) {
			sh.unlink(nd)
			n++
		}
	}
	return n
}

// hasTag reports whether a and b share a tag. Both are short: an entry has
// at most 2 + MaxGroups tags and a purge names an operator-sized list.
func hasTag(a, b []store.Tag) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
