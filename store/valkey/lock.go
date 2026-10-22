package valkey

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/AshwinSathian/weir/store"
)

var _ store.Locker = (*Store)(nil)

func (s *Store) lockKey(k store.Key) string {
	return s.cfg.Prefix + ":lock:" + hex.EncodeToString(k[:])
}

// Lock takes k's lock for ttl with SET NX PX and a random token, so the
// engines sharing the server fetch a missing key once among them (01
// FR-COA-11). While the holder holds it, a goroutine of the store renews
// it every ttl/3 (only while it still holds the token), so a fetch longer
// than ttl keeps it and a holder that dies loses it within ttl. The release
// stops the renewal and deletes the lock only while it still holds this
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
	px := max(ttl.Milliseconds(), 1)
	ok, err := cl.setNX(ctx, key, token, px)
	if err != nil || !ok {
		return nil, false, mapError(err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	s.wg.Add(1) // stopped by the release or by Close (FR-LCY-2)
	go func() {
		defer s.wg.Done()
		defer close(done)
		t := time.NewTicker(max(ttl/3, time.Millisecond))
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-s.closing:
				return
			case <-t.C:
				rctx, cancel := context.WithTimeout(context.Background(), s.cfg.CallTimeout)
				held, err := cl.renewIfEqual(rctx, key, token, px)
				cancel()
				if err == nil && !held {
					return // the lock expired and is someone else's now
				}
			}
		}
	}()
	var once sync.Once
	// The release runs after the fetch, when the caller's context may be
	// gone; store.Locker gives it none, so it bounds itself.
	return func() { //nolint:contextcheck // see above
		once.Do(func() {
			close(stop)
			<-done
			ctx, cancel := context.WithTimeout(context.Background(), s.cfg.CallTimeout)
			defer cancel()
			_ = cl.delIfEqual(ctx, key, token) // a lock left behind ends at its ttl
		})
	}, true, nil
}

// Held reports whether any engine holds k's lock. Always false without
// Config.CoalesceAcrossNodes. Only an engine waiting for another's fetch
// asks, and it reads the key next, so a local copy of k is dropped here:
// the read must see what the other node stored, not this node's older copy.
func (s *Store) Held(ctx context.Context, k store.Key) (bool, error) {
	if !s.cfg.CoalesceAcrossNodes {
		return false, nil
	}
	if s.local != nil {
		s.local.writes[k[0]].Add(1)
		_ = s.local.m.Delete(ctx, k)
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
