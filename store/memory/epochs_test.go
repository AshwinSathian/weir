package memory

import (
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir/store"
)

func numTag(n uint64) store.Tag {
	var tg store.Tag
	binary.BigEndian.PutUint64(tg[24:], n)
	return tg
}

// FR-LCY-1, 05 §5.1: New rejects invalid epoch settings.
func TestNewEpochConfig(t *testing.T) {
	for _, cfg := range []Config{
		{EpochSlots: 3}, {EpochSlots: -1}, {EpochSlots: 2 * MaxEpochSlots},
		{MaxHardEpochs: -1}, {MaxRetention: -time.Second},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) = nil error", cfg)
		}
	}
	s := newStore(t, Config{})
	if len(s.ep.planes[0]) != 1<<19 || s.ep.maxHard != 10000 || s.ep.retention != 24*time.Hour {
		t.Errorf("defaults: %d slots, %d hard, %v retention", len(s.ep.planes[0]), s.ep.maxHard, s.ep.retention)
	}
}

// FR-PRG-4, NFR-3, T-23, T-29, E-7..E-9: a flood of distinct soft and invalid epochs
// allocates nothing, and no tag's lookup falls below its own epoch.
func TestInvalidationFloodBounded(t *testing.T) {
	s := newStore(t, Config{})
	ctx := t.Context()
	base := time.Now().Add(time.Minute)
	const n = 1_000_000
	at := func(i int) time.Time { return base.Add(time.Duration(i) * time.Millisecond) }
	mode := func(i int) store.EpochMode { return store.EpochSoft + store.EpochMode(i%2) }
	i := 0
	allocs := testing.AllocsPerRun(n-1, func() {
		if err := s.SetEpoch(ctx, numTag(uint64(i)), store.Epoch{At: at(i), Mode: mode(i)}); err != nil { //nolint:gosec // i >= 0
			t.Fatal(err)
		}
		i++
	})
	if allocs != 0 {
		t.Errorf("SetEpoch allocates %v per call, want 0", allocs)
	}
	if len(s.ep.hard) != 0 {
		t.Errorf("hard table holds %d tags after soft epochs", len(s.ep.hard))
	}
	for j := range i {
		ep, ok, err := s.NewestEpoch(ctx, []store.Tag{numTag(uint64(j))}, at(j)) //nolint:gosec // j >= 0
		if err != nil || !ok || ep.Mode < mode(j) || ep.At.Before(at(j)) {
			t.Fatalf("tag %d: NewestEpoch = %+v, %v, %v; want mode >= %d at >= %v", j, ep, ok, err, mode(j), at(j))
		}
	}
}

// FR-PRG-7, E-7, §4.3: sketch cells hold whole seconds after the base instant,
// rounded up; the global tag is exact (E-5).
func TestEpochSketchRounding(t *testing.T) {
	s := newStore(t, Config{})
	ctx := t.Context()
	at := s.ep.base.Add(90*time.Second + 200*time.Millisecond)
	for _, c := range []struct {
		name string
		tag  store.Tag
		want time.Time
	}{
		{"sketch tag rounds up", numTag(1), s.ep.base.Add(91 * time.Second)},
		{"global tag is exact", store.TagGlobal(), at},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := s.SetEpoch(ctx, c.tag, store.Epoch{At: at, Mode: store.EpochInvalid}); err != nil {
				t.Fatal(err)
			}
			ep, ok, err := s.NewestEpoch(ctx, []store.Tag{c.tag}, at)
			if err != nil || !ok || !ep.At.Equal(c.want) || ep.Mode != store.EpochInvalid {
				t.Fatalf("NewestEpoch = %+v, %v, %v; want invalid at %v", ep, ok, err, c.want)
			}
		})
	}
}

// FR-PRG-7, E-8: epoch times far enough from the base instant to saturate
// the offset still apply; rounding must not overflow into a tiny cell, and
// a saturated past time must not read as "no epoch".
func TestEpochSaturatedTimesNotLost(t *testing.T) {
	ctx := t.Context()
	for _, c := range []struct {
		name      string
		at, since time.Time
	}{
		{"far future", time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), time.Now().Add(time.Hour)},
		{"far past", time.Date(1500, 1, 1, 0, 0, 0, 0, time.UTC), time.Time{}},
	} {
		for _, m := range []store.EpochMode{store.EpochSoft, store.EpochInvalid, store.EpochHard} {
			for _, tg := range []store.Tag{numTag(1), store.TagGlobal()} {
				s := newStore(t, Config{})
				if err := s.SetEpoch(ctx, tg, store.Epoch{At: c.at, Mode: m}); err != nil {
					t.Fatal(err)
				}
				if ep, ok, err := s.NewestEpoch(ctx, []store.Tag{tg}, c.since); err != nil || !ok || ep.Mode != m {
					t.Errorf("%s, mode %d, global %v: NewestEpoch = %+v, %v, %v; want the epoch",
						c.name, m, tg == store.TagGlobal(), ep, ok, err)
				}
			}
		}
	}
}

// E-2, 05 §5.4: concurrent raises of one tag keep the maximum, and each
// reader sees (mode, At) only move forward, never past the last write.
func TestEpochConcurrentRaise(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newStore(t, Config{})
		ctx := t.Context()
		base := time.Now().Add(time.Minute)
		tg := []store.Tag{numTag(7), store.TagGlobal()}
		const writers, per = 8, 500
		top := base.Add(writers * per * time.Second)
		var wg sync.WaitGroup
		for w := range writers {
			wg.Go(func() {
				for i := range per {
					at := base.Add(time.Duration(i*writers+w) * time.Second)
					mode := store.EpochSoft + store.EpochMode(i%2)
					for _, x := range tg {
						if err := s.SetEpoch(ctx, x, store.Epoch{At: at, Mode: mode}); err != nil {
							t.Error(err)
							return
						}
					}
				}
			})
			wg.Go(func() {
				var prev store.Epoch
				for range per {
					ep, ok, err := s.NewestEpoch(ctx, tg[:1], base)
					if err != nil || ok && ep.At.After(top) {
						t.Errorf("NewestEpoch = %+v, %v, %v", ep, ok, err)
						return
					}
					if !ok {
						continue
					}
					if ep.Mode < prev.Mode || ep.Mode == prev.Mode && ep.At.Before(prev.At) {
						t.Errorf("NewestEpoch went back from %+v to %+v", prev, ep)
						return
					}
					prev = ep
				}
			})
		}
		wg.Wait()
		last := base.Add(time.Duration(writers*per-1) * time.Second)
		for _, x := range tg {
			ep, ok, err := s.NewestEpoch(ctx, []store.Tag{x}, last)
			if err != nil || !ok || ep.At.Before(last) {
				t.Errorf("after raises: NewestEpoch = %+v, %v, %v; want at least %v", ep, ok, err, last)
			}
		}
	})
}

// FR-PRG-5, E-5, E-6: a hard purge of everything does not use a
// hard-table slot, and applies only to lookups that carry the global tag.
func TestGlobalHardEpochOutsideCap(t *testing.T) {
	s := newStore(t, Config{MaxHardEpochs: 1})
	ctx := t.Context()
	now := time.Now()
	for _, tg := range []store.Tag{numTag(1), store.TagGlobal()} {
		if err := s.SetEpoch(ctx, tg, store.Epoch{At: now, Mode: store.EpochHard}); err != nil {
			t.Fatalf("SetEpoch: %v", err)
		}
	}
	ep, ok, err := s.NewestEpoch(ctx, []store.Tag{numTag(2), store.TagGlobal()}, now)
	if err != nil || !ok || ep.Mode != store.EpochHard || !ep.At.Equal(now) {
		t.Fatalf("NewestEpoch = %+v, %v, %v; want hard at %v", ep, ok, err, now)
	}
	if ep, ok, _ := s.NewestEpoch(ctx, []store.Tag{numTag(2)}, now); ok {
		t.Errorf("global epoch applied without the global tag: %+v", ep)
	}
}

// FR-PRG-3, NFR-3, E-6, 05 §5.4: hard epochs older than MaxRetention are pruned by later
// SetEpoch calls, freeing their slots.
func TestHardEpochPrune(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newStore(t, Config{MaxHardEpochs: 1, MaxRetention: time.Hour})
		ctx := t.Context()
		old := time.Now()
		if err := s.SetEpoch(ctx, numTag(1), store.Epoch{At: old, Mode: store.EpochHard}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetEpoch(ctx, numTag(2), store.Epoch{At: old, Mode: store.EpochHard}); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("SetEpoch at cap = %v, want ErrUnavailable", err)
		}
		time.Sleep(time.Hour + time.Second)
		if err := s.SetEpoch(ctx, numTag(2), store.Epoch{At: time.Now(), Mode: store.EpochHard}); err != nil {
			t.Fatalf("SetEpoch after retention = %v, want the old tag pruned", err)
		}
		if ep, ok, _ := s.NewestEpoch(ctx, []store.Tag{numTag(1)}, old); ok && ep.Mode == store.EpochHard {
			t.Errorf("pruned hard epoch still reported: %+v", ep)
		}
	})
}

// FR-PRG-3, FR-PRG-7, E-11: retention is clamped to MaxRetention from the request time, so a
// pruned hard epoch (E-6) can never have a record left to apply to.
func TestRetentionClamp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newStore(t, Config{MaxRetention: time.Hour})
		ctx := t.Context()
		now := time.Now()
		recs := []struct {
			req, sto time.Time
			life     time.Duration // expected lifetime from now
		}{
			{now.Add(-10 * time.Minute), now, 50 * time.Minute},         // from request time
			{time.Time{}, now.Add(-20 * time.Minute), 40 * time.Minute}, // from StoredAt without it
			{time.Time{}, time.Time{}, time.Hour},                       // from now without either
		}
		for i, c := range recs {
			e := entry(10)
			e.RequestTime, e.StoredAt, e.Expires = c.req, c.sto, now.Add(48*time.Hour)
			if err := s.Set(ctx, numKey(uint64(i)), e); err != nil { //nolint:gosec // i >= 0
				t.Fatal(err)
			}
		}
		// 05 §2.3: a record already past its clamped expiry is not stored.
		old := entry(10)
		old.RequestTime, old.Expires = now.Add(-25*time.Hour), now.Add(time.Hour)
		before := s.Bytes()
		if err := s.Set(ctx, numKey(99), old); err != nil || s.Bytes() != before {
			t.Fatalf("Set past clamped expiry: err %v, bytes %d -> %d", err, before, s.Bytes())
		}
		for _, at := range []time.Duration{39, 41, 49, 51, 59, 61} {
			time.Sleep(time.Until(now.Add(at * time.Minute)))
			for i, c := range recs {
				_, err := s.Get(ctx, numKey(uint64(i))) //nolint:gosec // i >= 0
				if live, want := err == nil, at*time.Minute < c.life; live != want {
					t.Errorf("after %v, record %d live = %v, want %v", at*time.Minute, i, live, want)
				}
			}
		}
	})
}
