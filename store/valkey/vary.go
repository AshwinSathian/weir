package valkey

import (
	"context"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/AshwinSathian/weir/store"
)

// SetVarySpec stores next at k if the record there is still prev, the entry
// Get returned (nil: no live record), in one server-side script (store.VarySetter,
// 05 V-1). prev is compared as its encoding: the codec writes headers in
// sorted order, so decoding then encoding reproduces the stored bytes.
//
// A next the store would decline (past its expiry, or the codec rejects it)
// is reported as swapped with the record at k left alone, like Set (S-4).
func (s *Store) SetVarySpec(ctx context.Context, k store.Key, prev, next *store.Entry) (bool, error) {
	cl, err := s.acquire(ctx)
	if err != nil {
		return false, err
	}
	pxat, ok := s.expiryMillis(next, time.Now())
	if !ok {
		return true, nil
	}
	val, err := store.Encode(next)
	if err != nil {
		return true, nil
	}
	var want []byte
	if prev != nil {
		if want, err = store.Encode(prev); err != nil {
			return false, nil // cannot equal anything stored
		}
	}
	ctx, cancel := s.callCtx(ctx)
	defer cancel()
	key := s.entryKey(k)
	swapped, err := cl.evalVarySet(ctx, key, want, prev != nil, val, pxat)
	if err != nil {
		return false, mapError(err)
	}
	if swapped || prev != nil {
		return swapped, nil
	}
	return s.swapOverStale(ctx, cl, key, val, pxat)
}

// swapOverStale handles a nil prev that lost: Get reports a record past its
// Expires as absent while the server, on its own clock, may still hold it,
// and the swap must not fail until the server catches up (05 §2.2). If the
// key now holds a live record, the loss is real. Otherwise it swaps against
// whatever was seen there, which still loses to a concurrent writer. The
// engine's Get returns an error, not ErrNotFound, for an undecodable record,
// so it never passes a nil prev for one; this serves direct callers.
// The three round trips share one callCtx deadline (05 §7).
func (s *Store) swapOverStale(ctx context.Context, cl client, key string, val []byte, pxat int64) (bool, error) {
	raw, err := cl.get(ctx, key)
	switch {
	case err == nil:
		// An undecodable record counts as stale too: Get reports it as a miss,
		// and a plain Set would overwrite it, so the swap must as well.
		if e, derr := store.Decode(raw, maxValueBytes); derr == nil && (e.Expires.IsZero() || time.Now().Before(e.Expires)) {
			return false, nil
		}
	case valkey.IsValkeyNil(err):
		raw = nil // vanished since the script ran
	default:
		return false, mapError(err)
	}
	swapped, err := cl.evalVarySet(ctx, key, raw, raw != nil, val, pxat)
	if err != nil {
		return false, mapError(err)
	}
	return swapped, nil
}
