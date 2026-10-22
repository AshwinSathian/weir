package valkey

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/AshwinSathian/weir/store"
)

var _ store.Locker = (*Store)(nil)

func (s *Store) lockKey(k store.Key) string {
	return s.cfg.Prefix + ":lock:" + hex.EncodeToString(k[:])
}

// Lock takes k's lock for ttl with SET NX PX and a random token, so the
// engines sharing the server fetch a missing key once among them (01
// FR-COA-11). The release deletes the lock only while it still holds this
// token, under its own CallTimeout. Without Config.CoalesceAcrossNodes every
// caller holds the lock at once, at no cost: each engine fetches for itself.
func (s *Store) Lock(ctx context.Context, k store.Key, ttl time.Duration) (func(), bool, error) {
	if !s.cfg.CoalesceAcrossNodes {
		return func() {}, true, nil
	}
	cl, err := s.acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	ctx, cancel := s.callCtx(ctx)
	defer cancel()
	token := []byte(rand.Text())
	key := s.lockKey(k)
	ok, err := cl.setNX(ctx, key, token, max(ttl.Milliseconds(), 1))
	if err != nil || !ok {
		return nil, false, mapError(err)
	}
	// The release runs after the caller's fetch, when ctx (bounded by the
	// engine's Timeouts.Store) has long ended: keep its values, not its end.
	detached := context.WithoutCancel(ctx)
	return func() {
		ctx, cancel := context.WithTimeout(detached, s.cfg.CallTimeout)
		defer cancel()
		_ = cl.delIfEqual(ctx, key, token) // a lock left behind ends at its ttl
	}, true, nil
}

// Held reports whether any engine holds k's lock. Always false without
// Config.CoalesceAcrossNodes.
func (s *Store) Held(ctx context.Context, k store.Key) (bool, error) {
	if !s.cfg.CoalesceAcrossNodes {
		return false, nil
	}
	cl, err := s.acquire(ctx)
	if err != nil {
		return false, err
	}
	ctx, cancel := s.callCtx(ctx)
	defer cancel()
	ok, err := cl.exists(ctx, s.lockKey(k))
	return ok, mapError(err)
}
