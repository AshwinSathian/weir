package valkey

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// FR-COA-11: with CoalesceAcrossNodes one caller holds a key's lock; the
// release removes only its own lock. Without it everyone holds it and
// nobody is ever held.
func TestLock(t *testing.T) {
	var k store.Key
	k[0] = 1
	t.Run("off: everyone holds, nobody waits", func(t *testing.T) {
		s, cl := connected(t, nil)
		for range 2 {
			if _, ok, err := s.Lock(t.Context(), k, time.Second); !ok || err != nil {
				t.Fatalf("Lock = %v, %v", ok, err)
			}
		}
		if held, err := s.Held(t.Context(), k); held || err != nil {
			t.Fatalf("Held = %v, %v", held, err)
		}
		if cl.kv.sets != 0 || len(cl.kv.vals) != 0 {
			t.Fatal("the lock reached the server while off")
		}
	})
	t.Run("on: one holder at a time", func(t *testing.T) {
		s, cl := connected(t, func(c *Config) { c.CoalesceAcrossNodes = true })
		release, ok, err := s.Lock(t.Context(), k, time.Second)
		if !ok || err != nil {
			t.Fatalf("first Lock = %v, %v", ok, err)
		}
		if _, ok, _ := s.Lock(t.Context(), k, time.Second); ok {
			t.Fatal("a second caller took a held lock")
		}
		if held, _ := s.Held(t.Context(), k); !held {
			t.Fatal("Held = false while held")
		}
		// Another holder took over after this one's lock expired: this
		// release must not remove it.
		cl.kv.mu.Lock()
		cl.kv.vals[s.lockKey(k)] = []byte("someone-else")
		cl.kv.mu.Unlock()
		release()
		if held, _ := s.Held(t.Context(), k); !held {
			t.Fatal("a release removed another holder's lock")
		}
		cl.kv.mu.Lock()
		delete(cl.kv.vals, s.lockKey(k))
		cl.kv.mu.Unlock()
		release2, ok, _ := s.Lock(t.Context(), k, time.Second)
		if !ok {
			t.Fatal("lock not free after its holder left")
		}
		release2()
		if held, _ := s.Held(t.Context(), k); held {
			t.Fatal("release left the lock")
		}
	})
}

// FR-STF-5, 05 §7 local tier: a record read once is served from memory, and so is a
// purge check within EpochTTL; an epoch this node writes applies at once.
func TestLocalTier(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, cl := connected(t, func(c *Config) { c.LocalTier = &LocalTierConfig{} })
		var k store.Key
		k[0] = 2
		now := time.Now()
		if err := s.Set(t.Context(), k, testEntry(now)); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if e, err := s.Get(t.Context(), k); err != nil || string(e.Body) != "hello" {
				t.Fatalf("Get = %v, %v", e, err)
			}
		}
		if cl.kv.gets != 0 {
			t.Fatalf("server reads = %d, want 0: Set keeps a local copy", cl.kv.gets)
		}
		tags := []store.Tag{store.TagGlobal(), tag(1)}
		for range 3 {
			if _, _, err := s.NewestEpoch(t.Context(), tags, now); err != nil {
				t.Fatal(err)
			}
		}
		if _, reads, _ := cl.counts(); reads != 1 {
			t.Fatalf("epoch reads = %d, want 1 within EpochTTL", reads)
		}
		time.Sleep(time.Second) // EpochTTL default
		if _, _, err := s.NewestEpoch(t.Context(), tags, now); err != nil {
			t.Fatal(err)
		}
		if _, reads, _ := cl.counts(); reads != 2 {
			t.Fatalf("epoch reads = %d, want 2 after EpochTTL", reads)
		}
		if err := s.SetEpoch(t.Context(), tag(1), store.Epoch{Mode: store.EpochHard, At: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.NewestEpoch(t.Context(), tags, now); err != nil {
			t.Fatal(err)
		}
		if _, reads, _ := cl.counts(); reads != 3 {
			t.Fatalf("epoch reads = %d, want 3: this node's epoch must apply at once", reads)
		}
		if err := s.Delete(t.Context(), k); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(t.Context(), k); err == nil {
			t.Fatal("Get after Delete found the local copy")
		}
	})
}

// 05 §7: LocalTier bounds are checked by Validate.
func TestLocalTierConfig(t *testing.T) {
	for name, lt := range map[string]LocalTierConfig{
		"too small":    {MaxBytes: 1 << 20},
		"too large":    {MaxBytes: 8 << 30},
		"negative ttl": {EpochTTL: -time.Second},
		"ttl too long": {EpochTTL: time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := (Config{Addrs: []string{"h:1"}, LocalTier: &lt}).Validate(); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	c, err := Config{Addrs: []string{"h:1"}, LocalTier: &LocalTierConfig{}}.Validate()
	if err != nil || c.LocalTier.MaxBytes != defaultLocalBytes || c.LocalTier.EpochTTL != time.Second {
		t.Fatalf("defaults: %+v, %v", c.LocalTier, err)
	}
}
