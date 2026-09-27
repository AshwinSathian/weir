// Package storetest is the store conformance suite (05 §8). Every
// store.Store implementation calls Run from its tests.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// Option adjusts how Run exercises a store.
type Option func(*options)

type options struct {
	noEpochs bool
	synctest bool
	hardCap  int
}

// WithoutEpochs skips the epoch cases, for stores that do not implement
// SetEpoch and NewestEpoch yet. ContextCanceled and ClosedStore still call
// them, so stubs must return nil (or ErrUnavailable once closed).
func WithoutEpochs() Option { return func(o *options) { o.noEpochs = true } }

// Synctest runs each time-dependent case inside its own synctest bubble, so
// in-process stores that read time.Now expire records on the fake clock.
// Without it those cases sleep on the real clock, as remote stores must.
// (synctest.Test forbids t.Run inside a bubble, so the bubble is per case.)
func Synctest() Option { return func(o *options) { o.synctest = true } }

// HardEpochCap tells Run the store's hard-epoch cap (E-6), so EpochHardCap
// can fill it. Without it that case is skipped. Stores built by newStore
// should use a small cap here.
func HardEpochCap(n int) Option { return func(o *options) { o.hardCap = n } }

// Run runs every conformance case against stores built by newStore. Each
// case gets a fresh store, closed by t.Cleanup (inside the bubble under
// Synctest, so store goroutines end there); Close is idempotent, so
// newStore may register its own Close too.
func Run(t *testing.T, newStore func(t *testing.T) store.Store, opts ...Option) {
	build := newStore
	newStore = func(t *testing.T) store.Store {
		s := build(t)
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	cases := []struct {
		name  string
		epoch bool
		f     func(*testing.T, func(*testing.T) store.Store, options)
	}{
		{"GetMissing", false, testGetMissing},
		{"SetGetRoundTrip", false, testSetGetRoundTrip},
		{"SetReplacesAnyKind", false, testSetReplacesAnyKind},
		{"ExpiredIsNotFound", false, testExpiredIsNotFound},
		{"SetPastExpiresIsNoop", false, testSetPastExpiresIsNoop},
		{"DeleteMissingOK", false, testDeleteMissingOK},
		{"ContextCanceled", false, testContextCanceled},
		{"ClosedStore", false, testClosedStore},
		{"ConcurrentSetGet", false, testConcurrentSetGet},
		{"NoMutationAfterSet", false, testNoMutationAfterSet},
		{"CodecRoundTrip", false, testCodecRoundTrip},
		{"EpochPerModeKept", true, testEpochPerModeKept},
		{"EpochNeverUnderInvalidates", true, testEpochNeverUnderInvalidates},
		{"EpochHardCap", true, testEpochHardCap},
		{"EpochSinceBoundary", true, testEpochSinceBoundary},
		{"EpochFastPath", true, testEpochFastPath},
		{"EpochsMaxAcrossTags", true, testEpochsMaxAcrossTags},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.epoch && o.noEpochs {
				t.Skip("store does not support epochs (WithoutEpochs)")
			}
			c.f(t, newStore, o)
		})
	}
}

func key(n int) store.Key {
	var k store.Key
	copy(k[:], fmt.Sprintf("storetest-key-%d", n))
	return k
}

func tag(n int) store.Tag {
	var tg store.Tag
	copy(tg[:], fmt.Sprintf("storetest-tag-%d", n))
	return tg
}

// entries returns one record of every kind with every field that kind uses
// set to a non-zero value. Expires stays well inside the memory store's
// 24 h retention clamp (E-11).
func entries(now time.Time) []*store.Entry {
	exp := now.Add(time.Hour)
	return []*store.Entry{
		{
			Kind:                store.KindResponse,
			StoredAt:            now,
			Status:              200,
			Header:              http.Header{"Content-Type": {"text/plain"}, "X-Multi": {"a", "b"}},
			Body:                []byte("hello"),
			RequestTime:         now.Add(-2 * time.Second),
			ResponseTime:        now.Add(-time.Second),
			Date:                now.Add(-time.Second).Truncate(time.Second),
			CorrectedInitialAge: 3 * time.Second,
			Lifetime:            5 * time.Minute,
			SWR:                 time.Minute,
			SIE:                 2 * time.Minute,
			Flags:               store.FlagMustRevalidate | store.FlagSMaxAge | store.FlagPublic,
			ETag:                `"v1"`,
			LastModified:        now.Add(-time.Hour).Truncate(time.Second),
			FetchDuration:       40 * time.Millisecond,
			VaryNames:           []string{"accept-encoding"},
			Tags:                []store.Tag{tag(1), tag(2)},
			Owner:               tag(3),
			Expires:             exp,
		},
		{
			Kind:      store.KindVarySpec,
			StoredAt:  now,
			VaryNames: []string{"accept-encoding", "accept-language"},
			Variants:  []store.VariantRef{{Key: key(90), Expires: exp}, {Key: key(91), Expires: exp.Add(-time.Minute)}},
			Expires:   exp,
		},
		{Kind: store.KindHitForMiss, StoredAt: now, Expires: exp},
		{Kind: store.KindNegative, StoredAt: now, Status: 503, RetryAfter: 30 * time.Second, Expires: exp},
	}
}

// normalize returns a copy of e whose times have no monotonic reading and
// UTC location, so reflect.DeepEqual compares instants like time.Equal, and
// whose empty maps and slices are nil: a decoder may produce either.
func normalize(e *store.Entry) *store.Entry {
	c := clone(e)
	if len(c.Header) == 0 {
		c.Header = nil
	}
	if len(c.Body) == 0 {
		c.Body = nil
	}
	if len(c.VaryNames) == 0 {
		c.VaryNames = nil
	}
	if len(c.Tags) == 0 {
		c.Tags = nil
	}
	if len(c.Variants) == 0 {
		c.Variants = nil
	}
	for _, p := range []*time.Time{&c.StoredAt, &c.RequestTime, &c.ResponseTime, &c.Date, &c.LastModified, &c.Expires} {
		*p = p.Round(0).UTC()
	}
	for i := range c.Variants {
		c.Variants[i].Expires = c.Variants[i].Expires.Round(0).UTC()
	}
	return c
}

// clone deep-copies e.
func clone(e *store.Entry) *store.Entry {
	c := *e
	c.Header = e.Header.Clone()
	c.Body = cloneSlice(e.Body)
	c.VaryNames = cloneSlice(e.VaryNames)
	c.Tags = cloneSlice(e.Tags)
	c.Variants = cloneSlice(e.Variants)
	return &c
}

// cloneSlice copies s, keeping nil and empty distinct so DeepEqual
// comparisons against the copy stay exact.
func cloneSlice[S ~[]E, E any](s S) S {
	if s == nil {
		return nil
	}
	return append(S{}, s...)
}

func sameEntry(t *testing.T, got, want *store.Entry) {
	t.Helper()
	if g, w := normalize(got), normalize(want); !reflect.DeepEqual(g, w) {
		t.Fatalf("entry mismatch\n got: %+v\nwant: %+v", g, w)
	}
}

func mustSet(t *testing.T, s store.Store, k store.Key, e *store.Entry) {
	t.Helper()
	if err := s.Set(t.Context(), k, e); err != nil {
		t.Fatalf("Set: %v", err)
	}
}

func mustGet(t *testing.T, s store.Store, k store.Key) *store.Entry {
	t.Helper()
	e, err := s.Get(t.Context(), k)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return e
}

func wantNotFound(t *testing.T, s store.Store, k store.Key) {
	t.Helper()
	if e, err := s.Get(t.Context(), k); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get = %+v, %v; want ErrNotFound", e, err)
	}
}

// S-3, 05 §2.2
func testGetMissing(t *testing.T, newStore func(*testing.T) store.Store, _ options) {
	wantNotFound(t, newStore(t), key(1))
}

// 05 §2.2, §2.3, §3: every kind survives a round trip.
func testSetGetRoundTrip(t *testing.T, newStore func(*testing.T) store.Store, _ options) {
	s := newStore(t)
	set := entries(time.Now())
	for i, e := range set {
		mustSet(t, s, key(i), clone(e))
	}
	for i, e := range set {
		sameEntry(t, mustGet(t, s, key(i)), e)
	}
}

// 05 §2.3: Set replaces a record of any kind.
func testSetReplacesAnyKind(t *testing.T, newStore func(*testing.T) store.Store, _ options) {
	s := newStore(t)
	es := entries(time.Now())
	resp, vary := es[0], es[1]
	for _, e := range []*store.Entry{resp, vary, resp} {
		mustSet(t, s, key(1), e)
		sameEntry(t, mustGet(t, s, key(1)), e)
	}
}

// S-4, 05 §2.2: a record past Expires is not returned.
func testExpiredIsNotFound(t *testing.T, newStore func(*testing.T) store.Store, o options) {
	run := func(t *testing.T) {
		s := newStore(t)
		now := time.Now()
		e := &store.Entry{Kind: store.KindHitForMiss, StoredAt: now, Expires: now.Add(time.Second)}
		mustSet(t, s, key(1), e)
		mustGet(t, s, key(1))
		// 2 s margin covers remote clock granularity (1 s for Valkey PX).
		time.Sleep(3 * time.Second)
		wantNotFound(t, s, key(1))
	}
	if o.synctest {
		synctest.Test(t, run)
		return
	}
	run(t)
}

// 05 §2.3: Set with a past Expires returns nil and stores nothing.
func testSetPastExpiresIsNoop(t *testing.T, newStore func(*testing.T) store.Store, _ options) {
	s := newStore(t)
	now := time.Now()
	mustSet(t, s, key(1), &store.Entry{Kind: store.KindHitForMiss, StoredAt: now, Expires: now.Add(-time.Second)})
	wantNotFound(t, s, key(1))
}

// 05 §2.4: deleting a missing key is fine; deleting a present key removes it.
func testDeleteMissingOK(t *testing.T, newStore func(*testing.T) store.Store, _ options) {
	s := newStore(t)
	if err := s.Delete(t.Context(), key(1)); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
	mustSet(t, s, key(2), entries(time.Now())[2])
	if err := s.Delete(t.Context(), key(2)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantNotFound(t, s, key(2))
}

// S-2: a canceled context yields ErrUnavailable within 100 ms. In-process
// stores may ignore the context and answer normally.
func testContextCanceled(t *testing.T, newStore func(*testing.T) store.Store, _ options) {
	s := newStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	remote := s.Info().Remote
	e := entries(time.Now())[2]
	calls := []struct {
		name string
		call func() error
		ok   error // an error an in-process store may return besides nil
	}{
		{"Set", func() error { return s.Set(ctx, key(1), e) }, nil},
		{"Get", func() error { _, err := s.Get(ctx, key(1)); return err }, store.ErrNotFound},
		{"Delete", func() error { return s.Delete(ctx, key(1)) }, nil},
		{"SetEpoch", func() error { return s.SetEpoch(ctx, tag(1), store.Epoch{At: time.Now(), Mode: store.EpochSoft}) }, nil},
		{"NewestEpoch", func() error { _, _, err := s.NewestEpoch(ctx, []store.Tag{tag(1)}, time.Now()); return err }, nil},
	}
	for _, c := range calls {
		start := time.Now()
		err := c.call()
		if d := time.Since(start); d > 100*time.Millisecond {
			t.Errorf("%s took %v with a canceled context, want <= 100ms", c.name, d)
		}
		if errors.Is(err, store.ErrUnavailable) {
			continue
		}
		if remote {
			t.Errorf("%s = %v, want ErrUnavailable from a remote store", c.name, err)
		} else if err != nil && (c.ok == nil || !errors.Is(err, c.ok)) {
			t.Errorf("%s = %v, want nil or ErrUnavailable", c.name, err)
		}
	}
}

// 05 §2.7: after Close every method returns ErrUnavailable; Close twice is fine.
func testClosedStore(t *testing.T, newStore func(*testing.T) store.Store, _ options) {
	s := newStore(t)
	mustSet(t, s, key(1), entries(time.Now())[2])
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	ctx := t.Context()
	_, err := s.Get(ctx, key(1))
	errs := map[string]error{"Get": err}
	errs["Set"] = s.Set(ctx, key(2), entries(time.Now())[2])
	errs["Delete"] = s.Delete(ctx, key(1))
	errs["SetEpoch"] = s.SetEpoch(ctx, tag(1), store.Epoch{At: time.Now(), Mode: store.EpochSoft})
	_, _, errs["NewestEpoch"] = s.NewestEpoch(ctx, []store.Tag{tag(1)}, time.Now())
	for name, err := range errs {
		if !errors.Is(err, store.ErrUnavailable) {
			t.Errorf("%s after Close = %v, want ErrUnavailable", name, err)
		}
	}
}

// S-1: concurrent Set and Get on shared keys are race-free, and every Get
// returns ErrNotFound or an entry that was Set at that key.
func testConcurrentSetGet(t *testing.T, newStore func(*testing.T) store.Store, _ options) {
	const goroutines, ops, keys = 64, 200, 16
	s := newStore(t)
	exp := time.Now().Add(time.Hour)
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Go(func() {
			r := rand.New(rand.NewPCG(uint64(g), 0)) //nolint:gosec // seeded for reproducible key choice, not security
			for i := range ops {
				n := r.IntN(keys)
				if r.IntN(2) == 0 {
					e := &store.Entry{Kind: store.KindResponse, Status: 200, ETag: fmt.Sprintf("%d/%d/%d", n, g, i), Expires: exp}
					if err := s.Set(t.Context(), key(n), e); err != nil {
						t.Errorf("Set: %v", err)
					}
					continue
				}
				e, err := s.Get(t.Context(), key(n))
				switch {
				case errors.Is(err, store.ErrNotFound):
				case err != nil:
					t.Errorf("Get: %v", err)
				case !strings.HasPrefix(e.ETag, fmt.Sprintf("%d/", n)):
					t.Errorf("Get(key %d) returned entry %q set at another key", n, e.ETag)
				}
			}
		})
	}
	wg.Wait()
}

// S-5: a store never alters the entry passed to Set.
func testNoMutationAfterSet(t *testing.T, newStore func(*testing.T) store.Store, _ options) {
	s := newStore(t)
	for i, e := range entries(time.Now()) {
		want := clone(e)
		mustSet(t, s, key(i), e)
		mustGet(t, s, key(i))
		mustSet(t, s, key(i), entries(time.Now())[(i+1)%4])
		if err := s.Delete(t.Context(), key(i)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if !reflect.DeepEqual(e, want) {
			t.Fatalf("entry %d changed after Set\n got: %+v\nwant: %+v", i, e, want)
		}
	}
}

// FR-SNP-1, FR-FRS-8, 05 §6, §8: Encode then Decode is the identity for
// every kind. The case
// does not use the store; it runs here so every store's suite covers the
// format remote stores share.
func testCodecRoundTrip(t *testing.T, _ func(*testing.T) store.Store, _ options) {
	for _, e := range entries(time.Now()) {
		b, err := store.Encode(e)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		got, err := store.Decode(b, int64(len(b)))
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		sameEntry(t, got, e)
	}
}

func mustSetEpoch(t *testing.T, s store.Store, tg store.Tag, at time.Time, m store.EpochMode) {
	t.Helper()
	if err := s.SetEpoch(t.Context(), tg, store.Epoch{At: at, Mode: m}); err != nil {
		t.Fatalf("SetEpoch: %v", err)
	}
}

// wantEpoch checks the mode exactly and At within [at, at+1s]: sketch stores
// round epoch times up to whole seconds (E-7, E-8), which only
// over-invalidates, but At must still be that mode's own latest (E-3).
func wantEpoch(t *testing.T, s store.Store, tags []store.Tag, since time.Time, mode store.EpochMode, at time.Time) {
	t.Helper()
	ep, ok, err := s.NewestEpoch(t.Context(), tags, since)
	if err != nil {
		t.Fatalf("NewestEpoch: %v", err)
	}
	if !ok || ep.Mode != mode || ep.At.Before(at) || ep.At.After(at.Add(time.Second)) {
		t.Fatalf("NewestEpoch = %+v, %v; want mode %d at %v (+1s rounding)", ep, ok, mode, at)
	}
}

func wantNoEpoch(t *testing.T, s store.Store, tags []store.Tag, since time.Time) {
	t.Helper()
	ep, ok, err := s.NewestEpoch(t.Context(), tags, since)
	if err != nil || ok {
		t.Fatalf("NewestEpoch = %+v, %v, %v; want ok=false", ep, ok, err)
	}
}

// Epoch times sit whole minutes after the store is built, so second
// rounding (E-7) cannot move one past another or past a since value.
func epochBase(t *testing.T, newStore func(*testing.T) store.Store) (store.Store, time.Time) {
	s := newStore(t)
	return s, time.Now().Add(time.Minute)
}

// E-1: a soft epoch after a hard one on the same tag keeps the hard one.
func testEpochPerModeKept(t *testing.T, newStore func(*testing.T) store.Store, _ options) {
	s, base := epochBase(t, newStore)
	tg := []store.Tag{tag(1)}
	hard, soft := base.Add(time.Minute), base.Add(2*time.Minute)
	mustSetEpoch(t, s, tg[0], hard, store.EpochHard)
	mustSetEpoch(t, s, tg[0], soft, store.EpochSoft)
	wantEpoch(t, s, tg, base, store.EpochHard, hard)
	wantEpoch(t, s, tg, hard.Add(30*time.Second), store.EpochSoft, soft)
}

// E-3: an epoch with At == since applies, in every mode.
func testEpochSinceBoundary(t *testing.T, newStore func(*testing.T) store.Store, _ options) {
	s, base := epochBase(t, newStore)
	for i, m := range []store.EpochMode{store.EpochSoft, store.EpochInvalid, store.EpochHard} {
		at := base.Add(time.Duration(i) * time.Minute)
		mustSetEpoch(t, s, tag(i), at, m)
		wantEpoch(t, s, []store.Tag{tag(i)}, at, m, at)
	}
}

// E-3, E-10: no epoch at or after since means ok=false.
func testEpochFastPath(t *testing.T, newStore func(*testing.T) store.Store, _ options) {
	s, base := epochBase(t, newStore)
	tg := []store.Tag{tag(1), tag(2)}
	wantNoEpoch(t, s, tg, base)
	mustSetEpoch(t, s, tag(1), base, store.EpochSoft)
	mustSetEpoch(t, s, tag(2), base, store.EpochHard)
	wantNoEpoch(t, s, tg, base.Add(time.Minute))
	// An unset tag is unaffected by other tags' epochs. since sits a minute
	// after the store's base instant: a sketch compares against its zero
	// cells, and near the base instant over-invalidating would be allowed.
	wantNoEpoch(t, s, []store.Tag{tag(3)}, base)
}

// E-3: across tags the most severe qualifying mode wins, then its newest At.
func testEpochsMaxAcrossTags(t *testing.T, newStore func(*testing.T) store.Store, _ options) {
	s, base := epochBase(t, newStore)
	tg := []store.Tag{tag(1), tag(2), tag(3)}
	t1, t2, t3 := base.Add(time.Minute), base.Add(2*time.Minute), base.Add(3*time.Minute)
	mustSetEpoch(t, s, tag(3), t1, store.EpochInvalid)
	mustSetEpoch(t, s, tag(2), t2, store.EpochInvalid)
	mustSetEpoch(t, s, tag(1), t3, store.EpochSoft)
	wantEpoch(t, s, tg, base, store.EpochInvalid, t2)
	wantEpoch(t, s, tg, t2.Add(30*time.Second), store.EpochSoft, t3)
}

// FR-PRG-7, E-8, T-29: whatever other tags share its cells, a tag's lookup
// never reports less than its own epoch, in either sketch mode.
func testEpochNeverUnderInvalidates(t *testing.T, newStore func(*testing.T) store.Store, _ options) {
	s, base := epochBase(t, newStore)
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // reproducible test data
	const n = 200_000
	at := make([]time.Time, n)
	for i := range n {
		at[i] = base.Add(time.Duration(rng.Int64N(int64(time.Hour))))
		mustSetEpoch(t, s, tag(i), at[i], store.EpochSoft+store.EpochMode(i%2))
	}
	for i := range n {
		ep, ok, err := s.NewestEpoch(t.Context(), []store.Tag{tag(i)}, at[i])
		if err != nil || !ok || ep.At.Before(at[i]) || ep.Mode < store.EpochSoft+store.EpochMode(i%2) {
			t.Fatalf("tag %d: NewestEpoch = %+v, %v, %v; want at least %v", i, ep, ok, err, at[i])
		}
	}
}

// FR-PRG-3, NFR-3, E-6: a new hard tag beyond the cap fails with ErrUnavailable; tags
// already held can still move forward, and soft epochs have no cap.
func testEpochHardCap(t *testing.T, newStore func(*testing.T) store.Store, o options) {
	if o.hardCap <= 0 {
		t.Skip("hard-epoch cap unknown (HardEpochCap not given)")
	}
	s, base := epochBase(t, newStore)
	for i := range o.hardCap {
		mustSetEpoch(t, s, tag(i), base, store.EpochHard)
	}
	err := s.SetEpoch(t.Context(), tag(o.hardCap), store.Epoch{At: base, Mode: store.EpochHard})
	if !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("SetEpoch beyond the hard cap = %v, want ErrUnavailable", err)
	}
	wantNoEpoch(t, s, []store.Tag{tag(o.hardCap)}, base)
	mustSetEpoch(t, s, tag(0), base.Add(time.Minute), store.EpochHard)
	wantEpoch(t, s, []store.Tag{tag(0)}, base.Add(time.Minute), store.EpochHard, base.Add(time.Minute))
	for i := range 10 * o.hardCap {
		mustSetEpoch(t, s, tag(o.hardCap+i), base, store.EpochSoft)
	}
}
