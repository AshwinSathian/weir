package weir

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// Store breaker settings (FR-STF-2).
const (
	storeBreakerFails = 5
	storeBackoffFirst = time.Second
	storeBackoffCap   = 30 * time.Second
)

// entryOwnTags is how many leading Entry.Tags belong to one entry: global
// and URI. buildEntry appends the group tags after them.
const entryOwnTags = 2

// errStoreTimeout is the cause set on a remote call's Timeouts.Store
// deadline, so a deadline from the caller's context is not counted.
var errStoreTimeout = errors.New("weir: store timeout")

// storeGuard bounds the engine's store calls (04 §5.2): a Timeouts.Store
// deadline for remote stores only (FR-STF-1) and a breaker that skips the
// store after 5 consecutive ErrUnavailable results (FR-STF-2).
type storeGuard struct {
	s       store.Store
	shared  store.SharedTagEpochs // nil when the store lacks the capability
	remote  bool
	timeout time.Duration
	obs     Observer
	fails   atomic.Int32 // read without mu so a healthy store's calls never lock

	mu      sync.Mutex
	openTil time.Time
	backoff time.Duration // 1s doubling to 30s
}

func newStoreGuard(s store.Store, timeout time.Duration, obs Observer) *storeGuard {
	sh, _ := s.(store.SharedTagEpochs)
	return &storeGuard{s: s, shared: sh, remote: s.Info().Remote, timeout: timeout, obs: obs, backoff: storeBackoffFirst}
}

func (g *storeGuard) get(ctx context.Context, k store.Key) (*store.Entry, error) {
	ctx, cancel, err := g.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	ent, err := g.s.Get(ctx, k)
	return ent, g.exit(ctx, "get", err)
}

func (g *storeGuard) set(ctx context.Context, k store.Key, ent *store.Entry) error {
	ctx, cancel, err := g.enter(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	return g.exit(ctx, "set", g.s.Set(ctx, k, ent))
}

func (g *storeGuard) newestEpoch(ctx context.Context, tags []store.Tag, since time.Time) (store.Epoch, bool, error) {
	ctx, cancel, err := g.enter(ctx)
	if err != nil {
		return store.Epoch{}, false, err
	}
	defer cancel()
	var ep store.Epoch
	var ok bool
	if g.shared != nil && len(tags) > entryOwnTags {
		// Entry.Tags is [global, URI, groups...] (04 §3): group tags are
		// shared by many entries and never carry an invalid epoch (T-29).
		ep, ok, err = g.shared.NewestEpochShared(ctx, tags[:entryOwnTags], tags[entryOwnTags:], since)
	} else {
		ep, ok, err = g.s.NewestEpoch(ctx, tags, since)
	}
	return ep, ok, g.exit(ctx, "epoch", err)
}

func (g *storeGuard) setEpoch(ctx context.Context, t store.Tag, ep store.Epoch) error {
	ctx, cancel, err := g.enter(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	return g.exit(ctx, "set-epoch", g.s.SetEpoch(ctx, t, ep))
}

// purgeEpoch is setEpoch for Engine.Purge. A refused write is reported but
// not counted toward the breaker: the memory store refuses a new hard tag
// past MaxHardEpochs with ErrUnavailable (05 E-6), and an operator retrying
// that must not turn every lookup into a miss or lock out Purge{All}. A
// store that is really down is counted by the requests that find it so.
func (g *storeGuard) purgeEpoch(ctx context.Context, t store.Tag, ep store.Epoch) error {
	ctx, cancel, err := g.enter(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	if err := g.s.SetEpoch(ctx, t, ep); err != nil {
		emit(g.obs, Event{Kind: EvStoreError, Time: time.Now(), Reason: "set-epoch"})
		return err
	}
	return g.exit(ctx, "set-epoch", nil)
}

// scrub runs the store's Scrubber under the same breaker gate and deadline
// as every other store call. Like purgeEpoch, a failure is reported and
// counted by nothing: a keyspace-wide scan is the call most likely to time
// out on a remote store, and an operator retrying eager purges must not turn
// every lookup into a miss. The epochs are already written, so a failed scrub
// costs memory, not correctness. The caller has checked that s implements it.
func (g *storeGuard) scrub(ctx context.Context, sc store.Scrubber, tags []store.Tag) (int, error) {
	ctx, cancel, err := g.enter(ctx)
	if err != nil {
		return 0, err
	}
	defer cancel()
	n, err := sc.Scrub(ctx, tags)
	if err != nil {
		emit(g.obs, Event{Kind: EvStoreError, Time: time.Now(), Reason: "scrub"})
		return n, err
	}
	return n, g.exit(ctx, "scrub", nil)
}

func noCancel() {}

// enter refuses the call while the breaker is open and adds the remote
// deadline. In-process stores get ctx as is, so the hit path allocates no
// timer (FR-STF-1).
func (g *storeGuard) enter(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if g.fails.Load() >= storeBreakerFails {
		g.mu.Lock()
		open := time.Now().Before(g.openTil)
		g.mu.Unlock()
		if open {
			return nil, nil, store.ErrUnavailable
		}
	}
	if !g.remote {
		return ctx, noCancel, nil
	}
	ctx, cancel := context.WithTimeoutCause(ctx, g.timeout, errStoreTimeout)
	return ctx, cancel, nil
}

// exit counts err toward the breaker. ErrNotFound is a success (FR-STF-4).
// Any other error counts as ErrUnavailable (05 S-3), except when the caller's
// context ended first: a store must then return ErrUnavailable (05 S-2), but
// a client leaving says nothing about the store, and counting it would let
// clients that disconnect open the breaker.
func (g *storeGuard) exit(ctx context.Context, op string, err error) error {
	if err == nil || errors.Is(err, store.ErrNotFound) {
		if g.fails.Load() != 0 {
			g.succeed()
		}
		return err
	}
	if ctx.Err() != nil && !errors.Is(context.Cause(ctx), errStoreTimeout) {
		return err
	}
	emit(g.obs, Event{Kind: EvStoreError, Time: time.Now(), Reason: op})
	g.fail()
	return err
}

func (g *storeGuard) succeed() {
	g.mu.Lock()
	wasOpen := g.fails.Load() >= storeBreakerFails
	g.fails.Store(0)
	g.openTil = time.Time{} // a late success ends an open period early
	g.backoff = storeBackoffFirst
	g.mu.Unlock()
	if wasOpen {
		emit(g.obs, Event{Kind: EvStoreBreaker, Time: time.Now(), Reason: "closed"})
	}
}

// fail opens the breaker at the 5th consecutive failure, and again on each
// failure after an open period ends. Failures of calls that were already in
// flight while it is open do not reopen it or double the backoff.
func (g *storeGuard) fail() {
	g.mu.Lock()
	now := time.Now()
	// fails stops at the threshold: a long outage would otherwise wrap it
	// negative and leave the breaker closed for good.
	n := min(g.fails.Load()+1, storeBreakerFails)
	g.fails.Store(n)
	if n < storeBreakerFails || now.Before(g.openTil) {
		g.mu.Unlock()
		return
	}
	d := g.backoff
	g.openTil = now.Add(d)
	g.backoff = min(2*d, storeBackoffCap)
	g.mu.Unlock()
	emit(g.obs, Event{Kind: EvStoreBreaker, Time: now, Duration: d, Reason: "open"})
}
