package valkey

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/AshwinSathian/weir/store"
)

// maxValueBytes is Valkey's own limit on a string (proto-max-bulk-len,
// 512 MiB). It bounds Decode's allocation for a value another writer put
// there (T-21); it is not a Weir limit. GET has already moved the bytes by
// then, so a hostile 500 MiB value costs one download per Get. Only a writer
// with access to the server can plant one; see 05 §7.
const maxValueBytes = 512 << 20

// entryKey builds the server key of an entry: <prefix>:<hex(k)>, or
// <prefix>:{<tag>}:<hex(k)> with CoLocateEntries, so the entry shares the
// epoch slot (05 §7). Prefix and HashTag are validated, so neither can hold
// a glob, brace or colon character.
func (s *Store) entryKey(k store.Key) string {
	b := make([]byte, 0, len(s.cfg.Prefix)+len(s.cfg.HashTag)+2*len(k)+4)
	b = append(b, s.cfg.Prefix...)
	b = append(b, ':')
	if s.cfg.CoLocateEntries {
		b = append(b, '{')
		b = append(b, s.cfg.HashTag...)
		b = append(b, '}', ':')
	}
	return string(hex.AppendEncode(b, k[:]))
}

// Get returns the record at k. A missing key is ErrNotFound; a value that
// does not decode is ErrUnavailable, which the engine treats as a miss
// (05 §6). The server expires keys with PXAT, but its clock is not ours, so
// the engine re-checks Expires (S-4).
func (s *Store) Get(ctx context.Context, k store.Key) (*store.Entry, error) {
	cl, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.callCtx(ctx)
	defer cancel()
	v, err := cl.get(ctx, s.entryKey(k))
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return nil, store.ErrNotFound
		}
		return nil, mapError(err)
	}
	e, err := store.Decode(v, maxValueBytes)
	if err != nil {
		return nil, mapError(err)
	}
	// The server expires on its own clock, so a record can arrive slightly
	// past Expires (05 §2.2); the memory store answers the same way.
	if !e.Expires.IsZero() && !time.Now().Before(e.Expires) {
		return nil, store.ErrNotFound
	}
	return e, nil
}

// Set stores e at k until e.Expires, clamped to MaxRetention after the
// request time (E-11, same rule as the memory store). A record already past
// its expiry is a no-op that leaves any older record in place (05 §2.3).
// A record the codec cannot represent is declined, not an error (S-4), and
// removes the record it would have replaced, so Set leaves the new record or
// nothing, never an older one (as the memory store does for an oversized
// record). The engine builds entries, so a rejection is a bug to find
// there, and a breaker trip would only hide it.
func (s *Store) Set(ctx context.Context, k store.Key, e *store.Entry) error {
	cl, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	// From RequestTime, not StoredAt: a hard epoch at P is pruned at
	// P + MaxRetention and applies to records requested at or before P
	// (E-6, E-11).
	from := e.RequestTime
	if from.IsZero() {
		from = e.StoredAt
	}
	if from.IsZero() || from.After(now) {
		from = now
	}
	expires := e.Expires
	if limit := from.Add(s.cfg.MaxRetention); limit.Before(expires) {
		expires = limit
	}
	// Compared in milliseconds because that is what PXAT stores: an expiry
	// that rounds to now would be a write the server drops at once.
	pxat := expires.UnixMilli()
	if pxat <= now.UnixMilli() {
		return nil
	}
	val, err := store.Encode(e)
	ctx, cancel := s.callCtx(ctx)
	defer cancel()
	if err != nil {
		// Best effort: a failed DEL leaves the old record, which S-4 allows.
		_ = cl.del(ctx, s.entryKey(k))
		return nil
	}
	return mapError(cl.set(ctx, s.entryKey(k), val, pxat))
}

// Delete removes the record at k; a missing key is fine.
func (s *Store) Delete(ctx context.Context, k store.Key) error {
	cl, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := s.callCtx(ctx)
	defer cancel()
	return mapError(cl.del(ctx, s.entryKey(k)))
}
