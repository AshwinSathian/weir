//go:build integration

package valkey

import (
	"context"
	"errors"
	"math"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/AshwinSathian/weir/store"
)

// newEpochStore builds a store under a fresh prefix so one test never sees
// another's epoch keys.
func newEpochStore(t *testing.T, mutate func(*Config)) *Store {
	t.Helper()
	cfg := Config{Addrs: []string{serverAddr(t)}, Prefix: "ep" + strconv.FormatInt(time.Now().UnixNano(), 36) + "x" + strconv.Itoa(nextID())}
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

var idCounter int

func nextID() int { idCounter++; return idCounter }

// raw returns the underlying client so a test can look at, or damage, the
// epoch keys the way a flush or an operator would.
func raw(t *testing.T, s *Store) valkey.Client {
	t.Helper()
	cl, err := s.acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return cl.(valkeyClient).c
}

func exists(t *testing.T, s *Store, key string) bool {
	t.Helper()
	c := raw(t, s)
	n, err := c.Do(t.Context(), c.B().Exists().Key(key).Build()).AsInt64()
	if err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func del(t *testing.T, s *Store, keys ...string) {
	t.Helper()
	c := raw(t, s)
	if err := c.Do(t.Context(), c.B().Del().Key(keys...).Build()).Error(); err != nil {
		t.Fatal(err)
	}
}

func hget(t *testing.T, s *Store, key, field string) string {
	t.Helper()
	c := raw(t, s)
	v, err := c.Do(t.Context(), c.B().Hget().Key(key).Field(field).Build()).ToString()
	if err != nil {
		t.Fatalf("HGET %s %s: %v", key, field, err)
	}
	return v
}

func zcard(t *testing.T, s *Store, key string) int64 {
	t.Helper()
	c := raw(t, s)
	n, err := c.Do(t.Context(), c.B().Zcard().Key(key).Build()).AsInt64()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// whole returns a second-aligned instant, so rounding cannot blur a test.
func whole(d time.Duration) time.Time { return time.Now().Add(d).Truncate(time.Second) }

func epochAt(t *testing.T, s *Store, tags []store.Tag, since time.Time) (store.Epoch, bool) {
	t.Helper()
	ep, ok, err := s.NewestEpoch(t.Context(), tags, since)
	if err != nil {
		t.Fatalf("NewestEpoch: %v", err)
	}
	return ep, ok
}

// E-5: the global tag keeps all three modes exactly, one per field, and
// applies only to lookups that name it. A hard epoch does not hide the soft
// one, and each is found by a lookup after the other's time.
func TestGlobalTagKeepsAllModes(t *testing.T) {
	s := newEpochStore(t, func(c *Config) { c.NoClockSkew = true }) // exact comparisons
	g := store.TagGlobal()
	base := whole(time.Minute)
	soft, inv, hard := base.Add(time.Minute), base.Add(2*time.Minute), base.Add(time.Minute/2)
	for _, e := range []store.Epoch{{At: soft, Mode: store.EpochSoft}, {At: inv, Mode: store.EpochInvalid}, {At: hard, Mode: store.EpochHard}} {
		if err := s.SetEpoch(t.Context(), g, e); err != nil {
			t.Fatalf("SetEpoch %d: %v", e.Mode, err)
		}
	}
	if ep, ok := epochAt(t, s, []store.Tag{g}, base); !ok || ep.Mode != store.EpochHard || !ep.At.Equal(hard) {
		t.Fatalf("since base: %+v, %v; want hard at %v", ep, ok, hard)
	}
	if ep, ok := epochAt(t, s, []store.Tag{g}, hard.Add(time.Second)); !ok || ep.Mode != store.EpochInvalid || !ep.At.Equal(inv) {
		t.Fatalf("since after hard: %+v, %v; want invalid at %v", ep, ok, inv)
	}
	if _, ok := epochAt(t, s, []store.Tag{g}, inv.Add(time.Second)); ok {
		t.Fatal("an epoch older than since was reported")
	}
	if _, ok := epochAt(t, s, []store.Tag{tag(1)}, base); ok {
		t.Fatal("the global epoch applied to a lookup that does not name the global tag")
	}
	// Max semantics: an older write never lowers a field.
	if err := s.SetEpoch(t.Context(), g, store.Epoch{At: hard.Add(-time.Hour), Mode: store.EpochHard}); err != nil {
		t.Fatal(err)
	}
	if ep, ok := epochAt(t, s, []store.Tag{g}, base); !ok || !ep.At.Equal(hard) {
		t.Fatalf("an older hard write lowered the epoch: %+v, %v", ep, ok)
	}
}

// E-2, E-3: a second store on the same server sees the first store's hard
// epoch on a tag, max semantics hold, and a lower write changes nothing.
func TestHardEpochIsSharedAndMaxed(t *testing.T) {
	a := newEpochStore(t, nil)
	b, err := New(Config{Addrs: a.cfg.Addrs, Prefix: a.cfg.Prefix})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	at := whole(time.Minute)
	for _, x := range []time.Time{at, at.Add(-time.Hour), at.Add(time.Minute)} {
		if err := a.SetEpoch(t.Context(), tag(1), hardAt(x)); err != nil {
			t.Fatal(err)
		}
	}
	if ep, ok := epochAt(t, b, []store.Tag{tag(1)}, at); !ok || ep.Mode != store.EpochHard || !ep.At.Equal(at.Add(time.Minute)) {
		t.Fatalf("second store: %+v, %v; want hard at %v", ep, ok, at.Add(time.Minute))
	}
}

// E-6, T-29: the table stops at MaxHardEpochs with ErrUnavailable; a tag
// already present can still move forward; members older than the retention
// margin are pruned on the next write and their slots come free.
func TestHardEpochCap(t *testing.T) {
	t.Run("cap reached at At = now", func(t *testing.T) {
		s := newEpochStore(t, func(c *Config) { c.MaxHardEpochs = 3 })
		now := time.Now()
		for i := range 3 {
			if err := s.SetEpoch(t.Context(), tag(i), hardAt(now)); err != nil {
				t.Fatalf("SetEpoch %d: %v", i, err)
			}
		}
		err := s.SetEpoch(t.Context(), tag(3), hardAt(now))
		if !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("SetEpoch beyond the cap = %v, want ErrUnavailable", err)
		}
		if n := zcard(t, s, s.ekeys[keyHardIdx]); n != 3 {
			t.Fatalf("table holds %d members, want 3", n)
		}
		if err := s.SetEpoch(t.Context(), tag(0), hardAt(now.Add(time.Minute))); err != nil {
			t.Fatalf("raising a tag already present: %v", err)
		}
		if err := s.SetEpoch(t.Context(), store.TagGlobal(), hardAt(now)); err != nil {
			t.Fatalf("the global tag is not in the table: %v", err)
		}
	})
	t.Run("old members are pruned and slots freed", func(t *testing.T) {
		s := newEpochStore(t, func(c *Config) { c.MaxHardEpochs = 3; c.MaxRetention = time.Minute; c.NoClockSkew = true })
		old := time.Now().Add(-time.Hour) // far behind the server margin of 61 s
		for i := range 3 {
			if err := s.SetEpoch(t.Context(), tag(i), hardAt(old)); err != nil {
				t.Fatalf("SetEpoch %d: %v", i, err)
			}
		}
		if err := s.SetEpoch(t.Context(), tag(3), hardAt(time.Now())); err != nil {
			t.Fatalf("a write after the old members aged out: %v", err)
		}
		if n := zcard(t, s, s.ekeys[keyHardIdx]); n != 1 {
			t.Fatalf("table holds %d members, want only the new one", n)
		}
	})
}

// 05 §7: an absent newest key is never read as "no epochs". The script
// answers from the other keys, and the next write rebuilds newest.
func TestAbsentNewestIsNotNoEpochs(t *testing.T) {
	s := newEpochStore(t, nil)
	at := whole(time.Minute)
	if err := s.SetEpoch(t.Context(), tag(1), hardAt(at)); err != nil {
		t.Fatal(err)
	}
	del(t, s, s.ekeys[keyNewest])
	if ep, ok := epochAt(t, s, []store.Tag{tag(1)}, at); !ok || ep.Mode != store.EpochHard || !ep.At.Equal(at) {
		t.Fatalf("after deleting newest: %+v, %v; want hard at %v", ep, ok, at)
	}
	if exists(t, s, s.ekeys[keyNewest]) {
		t.Fatal("a lookup rebuilt newest; only a write may")
	}
	if err := s.SetEpoch(t.Context(), tag(2), hardAt(at)); err != nil {
		t.Fatal(err)
	}
	if !exists(t, s, s.ekeys[keyNewest]) {
		t.Fatal("the next write did not rebuild newest")
	}
}

// 05 §7: meta present and newest absent with no epochs at all is the normal
// empty state of a store whose newest key was lost, not a loss: no repair,
// no global epoch, and the answer is "none".
func TestAbsentNewestWithMetaPresent(t *testing.T) {
	s := newEpochStore(t, nil)
	if err := s.SetEpoch(t.Context(), tag(1), hardAt(whole(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	del(t, s, s.ekeys[keyNewest], s.ekeys[keyGlobal], s.ekeys[keyHardIdx])
	if _, ok := epochAt(t, s, []store.Tag{store.TagGlobal(), tag(1)}, time.Now()); ok {
		t.Fatal("an epoch appeared from nothing")
	}
	if exists(t, s, s.ekeys[keyGlobal]) {
		t.Fatal("a lookup with meta present wrote a global epoch (treated as loss)")
	}
}

// 05 §7: deleting every epoch key while entries survive (a restart without
// persistence that kept the entries, or an operator mistake) must not make a
// purge disappear. The first lookup repairs with a global hard epoch, the
// entry fetched before the loss is purged, and meta and global.hard exist.
// A second store and a second lookup see the same.
func TestMetaLossRepairs(t *testing.T) {
	a := newEpochStore(t, nil)
	b, err := New(Config{Addrs: a.cfg.Addrs, Prefix: a.cfg.Prefix})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	fetched := time.Now().Add(-2 * time.Second)
	e := testEntry(fetched)
	e.Tags = []store.Tag{store.TagGlobal(), tag(7)}
	var k store.Key
	if err := a.Set(t.Context(), k, e); err != nil {
		t.Fatal(err)
	}
	if err := a.SetEpoch(t.Context(), tag(9), hardAt(time.Now())); err != nil { // creates meta
		t.Fatal(err)
	}
	del(t, a, a.ekeys...) // the loss: every epoch key, entries untouched
	for range 2 {
		ep, ok, err := b.NewestEpoch(t.Context(), e.Tags, e.RequestTime)
		if err != nil || !ok || ep.Mode != store.EpochHard {
			t.Fatalf("after the loss: %+v, %v, %v; want a hard epoch", ep, ok, err)
		}
		if ep.At.Before(e.RequestTime) {
			t.Fatalf("repair epoch %v is before the entry's request time %v", ep.At, e.RequestTime)
		}
	}
	if !exists(t, a, a.ekeys[keyMeta]) || hget(t, a, a.ekeys[keyGlobal], "hard") == "" {
		t.Fatal("repair left no meta or no global.hard")
	}
	// A write after another loss repairs too: it must not recreate meta alone.
	del(t, a, a.ekeys...)
	if err := a.SetEpoch(t.Context(), tag(5), hardAt(whole(time.Minute))); err != nil {
		t.Fatal(err)
	}
	if ep, ok := epochAt(t, b, e.Tags, e.RequestTime); !ok || ep.Mode != store.EpochHard {
		t.Fatalf("write after loss: %+v, %v; want the repair epoch", ep, ok)
	}
}

// 05 §7: an absent hardidx is the normal empty state, because Valkey deletes
// empty sorted sets. It is not loss: lookups answer "none" and write nothing.
func TestEmptyHardidxIsNotLoss(t *testing.T) {
	s := newEpochStore(t, nil)
	at := whole(-time.Hour)
	if err := s.SetEpoch(t.Context(), tag(1), hardAt(at)); err != nil {
		t.Fatal(err)
	}
	// The pruning a later write does, done by hand: the set empties and
	// disappears. Delete global too, so a stray repair would show.
	c := raw(t, s)
	if err := c.Do(t.Context(), c.B().Zremrangebyscore().Key(s.ekeys[keyHardIdx]).Min("-inf").Max("+inf").Build()).Error(); err != nil {
		t.Fatal(err)
	}
	del(t, s, s.ekeys[keyGlobal])
	if exists(t, s, s.ekeys[keyHardIdx]) {
		t.Fatal("test setup: hardidx should be gone")
	}
	for range 2 {
		if _, ok := epochAt(t, s, []store.Tag{store.TagGlobal(), tag(1)}, at.Add(-time.Minute)); ok {
			t.Fatal("an epoch appeared from an empty hardidx")
		}
	}
	if exists(t, s, s.ekeys[keyGlobal]) {
		t.Fatal("a lookup repaired a loss that was only an empty hardidx")
	}
}

// T-29, 05 §7: zero and far-future times reach the server as 1 and 2^32-1,
// not as wrapped values.
func TestSaturatingWriteOnServer(t *testing.T) {
	s := newEpochStore(t, nil)
	g := store.TagGlobal()
	if err := s.SetEpoch(t.Context(), g, store.Epoch{At: time.Time{}, Mode: store.EpochSoft}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEpoch(t.Context(), g, store.Epoch{At: time.Unix(1<<50, 0), Mode: store.EpochInvalid}); err != nil {
		t.Fatal(err)
	}
	if got := hget(t, s, s.ekeys[keyGlobal], "soft"); got != "1" {
		t.Errorf("global.soft = %s, want 1", got)
	}
	if got := hget(t, s, s.ekeys[keyGlobal], "invalid"); got != strconv.FormatUint(math.MaxUint32, 10) {
		t.Errorf("global.invalid = %s, want 2^32-1", got)
	}
}

// 4.3: the store adds MaxClockSkew to the comparison, so a lookup whose
// since is up to the skew after the epoch still finds it, on the fast path
// and on the script path, and one second past does not.
func TestSkewAddsConservatively(t *testing.T) {
	s := newEpochStore(t, func(c *Config) { c.MaxClockSkew = 2 * time.Second })
	at := whole(-time.Hour)
	if err := s.SetEpoch(t.Context(), tag(1), hardAt(at)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"fast path", "script path"} {
		if path == "script path" {
			del(t, s, s.ekeys[keyNewest])
		}
		for _, c := range []struct {
			name  string
			since time.Time
			want  bool
		}{
			{"since at the epoch", at, true},
			{"since within the skew", at.Add(2 * time.Second), true},
			{"since past the skew", at.Add(3 * time.Second), false},
		} {
			if _, ok := epochAt(t, s, []store.Tag{tag(1)}, c.since); ok != c.want {
				t.Errorf("%s, %s: found=%v, want %v", path, c.name, ok, c.want)
			}
		}
	}
}

// 05 §7 (Replication), T-29: HardEpochWait must wait for a replica. WAIT
// only covers writes on its own connection, so the script and WAIT share
// one. The test needs a replica of the test server that it can pause:
// WEIR_VALKEY_REPLICA_ADDR names one. With none set the test skips, since CI
// runs a single server.
func TestHardEpochWaitWaitsForReplica(t *testing.T) {
	ra := os.Getenv("WEIR_VALKEY_REPLICA_ADDR")
	if ra == "" {
		t.Skip("WEIR_VALKEY_REPLICA_ADDR is not set (a replica of WEIR_VALKEY_ADDR)")
	}
	const wait = 700 * time.Millisecond
	s := newEpochStore(t, func(c *Config) { c.HardEpochWait = wait })
	r, err := New(Config{Addrs: []string{ra}, Prefix: "replica-control", SkipPolicyCheck: true})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	rc := raw(t, r)
	// Pause the replica's command loop so it cannot acknowledge.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = rc.Do(context.Background(), rc.B().Arbitrary("DEBUG", "SLEEP").Args("1.5").Build()).Error()
	}()
	time.Sleep(100 * time.Millisecond) // real clock: the replica is another process
	start := time.Now()
	if err := s.SetEpoch(t.Context(), tag(1), hardAt(whole(time.Minute))); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < wait-100*time.Millisecond {
		t.Fatalf("SetEpoch returned after %v with the replica paused, want about %v", took, wait)
	}
	<-done
}
