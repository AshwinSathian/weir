package valkey

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/AshwinSathian/weir/store"
)

// unavailable wraps store.ErrUnavailable for refusals that are not a client
// error (05 §7): the caller sees the same class as an outage.
func unavailable(format string, args ...any) error {
	return fmt.Errorf("store: valkey: %w: %s", store.ErrUnavailable, fmt.Sprintf(format, args...))
}

// SetEpoch raises the epoch for t in ep.Mode to at least ep.At (E-2). A hard
// epoch on a tag lives in one pruned sorted set capped at MaxHardEpochs (E-6);
// the global tag keeps all three modes exactly (E-5). Soft and invalid on any
// other tag need the sketch, which arrives in P25-03b, and are refused with
// ErrUnavailable until then; Purge reports it (FR-STF-2, T-29).
func (s *Store) SetEpoch(ctx context.Context, t store.Tag, ep store.Epoch) error {
	if ep.Mode < store.EpochSoft || ep.Mode > store.EpochHard {
		return unavailable("invalid epoch mode %d", ep.Mode)
	}
	global := t == store.TagGlobal()
	if !global && ep.Mode != store.EpochHard {
		return unavailable("soft and invalid epochs on a tag need the sketch (not implemented yet)")
	}
	cl, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := s.callCtx(ctx)
	defer cancel()
	args := s.writeArgs(ep.Mode, global, t, ep.At, false)
	if ep.Mode == store.EpochHard && s.cfg.HardEpochWait > 0 {
		// The replica count is not checked: with no replica WAIT answers 0 at
		// once, and a replica that misses the deadline is the trade-off
		// HardEpochWait names (05 §7, Replication).
		return s.retryOnce(ctx, func() error {
			return cl.evalWriteWait(ctx, s.ekeys, args, 1, s.cfg.HardEpochWait.Milliseconds())
		})
	}
	return s.writeEpoch(ctx, cl, args)
}

// writeEpoch runs the write script.
func (s *Store) writeEpoch(ctx context.Context, cl client, args []string) error {
	return s.retryOnce(ctx, func() error { return cl.evalWrite(ctx, s.ekeys, args) })
}

// retryOnce runs f, and once more after a network error. The script is a max
// or a replace, so running it twice is the same as once. A server reply
// (including the cap refusal) or an ended context is final.
func (s *Store) retryOnce(ctx context.Context, f func() error) error {
	err := f()
	if isNetworkError(err) && ctx.Err() == nil {
		err = f()
	}
	return mapError(err)
}

// isNetworkError reports an error worth one retry: not a server reply, not a
// context ending, not a closing client.
func isNetworkError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, valkey.ErrClosing) {
		return false
	}
	_, server := valkey.IsValkeyErr(err)
	return !server
}

// NewestEpoch returns the most severe, then newest, epoch among tags whose
// At plus MaxClockSkew reaches since (E-3, 4.3). The fast path is one GET: a
// newest key older than since answers "none" with no script (E-10). Anything
// else, including an absent newest, goes to the read script.
func (s *Store) NewestEpoch(ctx context.Context, tags []store.Tag, since time.Time) (store.Epoch, bool, error) {
	cl, err := s.acquire(ctx)
	if err != nil {
		return store.Epoch{}, false, err
	}
	if len(tags) == 0 {
		return store.Epoch{}, false, nil
	}
	ctx, cancel := s.callCtx(ctx)
	defer cancel()
	sinceSec, skew := sinceSeconds(since), wholeSeconds(s.cfg.MaxClockSkew)
	v, err := cl.get(ctx, s.ekeys[keyNewest])
	switch {
	case err == nil:
		if n, perr := strconv.ParseInt(string(v), 10, 64); perr == nil && n+skew < sinceSec {
			return store.Epoch{}, false, nil
		}
	case !valkey.IsValkeyNil(err):
		return store.Epoch{}, false, mapError(err)
	}
	return s.lookup(ctx, cl, tags, sinceSec, skew)
}

// lookup runs the read script. A loss reply makes the store repair with a
// global hard write at server time and look again once; a second loss is an
// error, not a loop (05 §7).
func (s *Store) lookup(ctx context.Context, cl client, tags []store.Tag, since, skew int64) (store.Epoch, bool, error) {
	args := s.readArgs(tags, since, skew)
	for attempt := 0; ; attempt++ {
		res, err := cl.evalRead(ctx, s.ekeys, args)
		if err != nil {
			return store.Epoch{}, false, mapError(err)
		}
		switch {
		case len(res) == 0:
			return store.Epoch{}, false, nil
		case len(res) == 2 && res[0] >= int64(store.EpochSoft) && res[0] <= int64(store.EpochHard):
			return store.Epoch{At: time.Unix(res[1], 0), Mode: store.EpochMode(res[0])}, true, nil //nolint:gosec // res[0] is range-checked in the case above
		case len(res) == 1 && res[0] == lossReply && attempt == 0:
			if err := s.repair(ctx, cl); err != nil {
				return store.Epoch{}, false, err
			}
		case len(res) == 1 && res[0] == lossReply:
			return store.Epoch{}, false, unavailable("epoch state is missing after a repair")
		default:
			return store.Epoch{}, false, unavailable("unexpected epoch script reply %v", res)
		}
	}
}

// repair writes the global hard epoch at server time. It is the same script
// as SetEpoch(TagGlobal, {Hard, now}): it recreates meta and raises
// global.hard and newest, so everything fetched before the loss is refetched
// once (05 §7, T-29). Nothing here counts toward a breaker the lookup does
// not already count.
func (s *Store) repair(ctx context.Context, cl client) error {
	return s.writeEpoch(ctx, cl, s.writeArgs(store.EpochHard, true, store.TagGlobal(), time.Time{}, true))
}

// readArgs builds the read script's arguments: since, skew, whether the
// global tag is named, then the other tags raw (E-5). Callers pass at most a
// handful of tags (the engine: 2 + MaxGroups), so the argument list is
// bounded by the caller (P5).
func (s *Store) readArgs(tags []store.Tag, since, skew int64) []string {
	g := store.TagGlobal()
	a := make([]string, argFirstTag, argFirstTag+len(tags))
	a[argSince], a[argSkew], a[argHasGlobal] = itoa(since), itoa(skew), "0"
	for _, t := range tags {
		if t == g {
			a[argHasGlobal] = "1"
			continue
		}
		a = append(a, string(t[:]))
	}
	return a
}
