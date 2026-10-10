package valkey

import (
	"context"
	"fmt"
	"slices"

	"github.com/AshwinSathian/weir/store"
)

var _ store.Scrubber = (*Store)(nil)

// scrubBatch is the SCAN COUNT hint, so one step reads about this many keys
// (05 §7).
const scrubBatch = 1000

// scrubChunk bounds how many values one GET pipeline holds, so a scrub never
// has more than scrubChunk records in memory (P5, NFR-3): the SCAN hint above
// bounds keys, not bytes.
const scrubChunk = 100

// Scrub deletes the response records whose Tags intersect tags and returns
// how many it deleted (store.Scrubber, FR-PRG-8, 05 §7). It walks every
// primary with SCAN, reads each key that looks like an entry, and DELs the
// matches. Epoch keys, the 2 MiB sketch planes and anything else under the
// prefix that is not a hex entry key are never read (T-29).
//
// ponytail: O(keyspace) per call, because Valkey has no tag index: every
// entry under the prefix is downloaded and decoded. The upgrade path is a
// tag-to-keys index maintained by Set, which costs a write per tag.
//
// A fresh Set between the read and the DEL can be deleted too, an extra miss
// as in the memory store; the epoch, not the scan, makes entries unreachable.
// On a cancelled context Scrub returns the count so far with ErrUnavailable.
// Each batch (one SCAN step and its GET and DEL pipelines) runs under
// CallTimeout or the caller's deadline, not the whole scan. At most
// scrubChunk values are held at once (P5). With no primary node it fails with
// ErrUnavailable rather than report an empty success.
func (s *Store) Scrub(ctx context.Context, tags []store.Tag) (int, error) {
	if len(tags) == 0 {
		if err := ctx.Err(); err != nil {
			return 0, mapError(err)
		}
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return 0, errClosed
		}
		return 0, nil // no tags, no work, and no dial
	}
	cl, err := s.acquire(ctx)
	if err != nil {
		return 0, err
	}
	want := make(map[store.Tag]struct{}, len(tags))
	for _, t := range tags {
		want[t] = struct{}{}
	}
	cctx, cancel := s.callCtx(ctx)
	nodes, err := cl.primaries(cctx)
	cancel()
	if err != nil {
		return 0, mapError(err)
	}
	if len(nodes) == 0 {
		// A replica-only address list (or a failover in progress) would
		// otherwise report success after deleting nothing.
		return 0, fmt.Errorf("store: valkey: %w: scrub found no primary node", store.ErrUnavailable)
	}
	total := 0
	for _, addr := range nodes {
		var cursor uint64
		for {
			if err := ctx.Err(); err != nil {
				return total, mapError(err)
			}
			n, next, err := s.scrubStep(ctx, cl, addr, cursor, want)
			total += n
			if err != nil {
				return total, mapError(err)
			}
			if cursor = next; cursor == 0 {
				break
			}
		}
	}
	return total, nil
}

// scrubStep scans one batch on addr and deletes the matching records in it.
func (s *Store) scrubStep(ctx context.Context, cl client, addr string, cursor uint64, want map[store.Tag]struct{}) (int, uint64, error) {
	ctx, cancel := s.callCtx(ctx)
	defer cancel()
	keys, next, err := cl.scanNode(ctx, addr, cursor, s.eprfx+"*", scrubBatch)
	if err != nil {
		return 0, 0, err
	}
	// SCAN can return a key twice; a duplicate reads twice and deletes once,
	// and the count comes from DEL.
	entries := keys[:0]
	for _, k := range keys {
		if s.isEntryKey(k) {
			entries = append(entries, k)
		}
	}
	if len(entries) == 0 {
		return 0, next, nil
	}
	deleted := 0
	for chunk := range slices.Chunk(entries, scrubChunk) {
		n, err := s.scrubChunk(ctx, cl, chunk, want)
		deleted += n
		if err != nil {
			return deleted, 0, err
		}
	}
	return deleted, next, nil
}

// scrubChunk reads the records at keys and deletes the response records that
// carry a wanted tag.
func (s *Store) scrubChunk(ctx context.Context, cl client, keys []string, want map[store.Tag]struct{}) (int, error) {
	vals, err := cl.getMulti(ctx, keys)
	if err != nil {
		return 0, err
	}
	var doomed []string
	for i, v := range vals {
		if v == nil {
			continue
		}
		// A value that does not decode is not ours to judge: Get already
		// reports it as ErrUnavailable and the server expires it.
		e, err := store.Decode(v, maxValueBytes)
		if err != nil || e.Kind != store.KindResponse {
			continue
		}
		for _, t := range e.Tags {
			if _, hit := want[t]; hit {
				doomed = append(doomed, keys[i])
				break
			}
		}
	}
	if len(doomed) == 0 {
		return 0, nil
	}
	n, err := cl.delMulti(ctx, doomed)
	return int(n), err
}

// isEntryKey reports whether key is <prefix>:[{tag}:]<64 lowercase hex>, an
// entry key. SCAN MATCH <prefix>:* also returns the epoch keys, including
// the sketch planes (05 §7).
func (s *Store) isEntryKey(key string) bool {
	if len(key) != len(s.eprfx)+2*len(store.Key{}) || key[:len(s.eprfx)] != s.eprfx {
		return false
	}
	for i := len(s.eprfx); i < len(key); i++ {
		c := key[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
