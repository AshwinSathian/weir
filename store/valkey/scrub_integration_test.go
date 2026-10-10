//go:build integration

package valkey

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store"
)

// rawClient returns the valkey-go client under s, for assertions about what
// is on the server.
func rawClient(t *testing.T, s *Store) valkey.Client {
	t.Helper()
	cl, err := s.acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return cl.(valkeyClient).c
}

func keyExists(t *testing.T, c valkey.Client, key string) bool {
	t.Helper()
	n, err := c.Do(t.Context(), c.B().Exists().Key(key).Build()).AsInt64()
	if err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func scrubStore(t *testing.T, mutate func(*Config)) *Store {
	t.Helper()
	cfg := Config{Addrs: []string{serverAddr(t)}, NoClockSkew: true, Prefix: "s" + strconv.FormatInt(time.Now().UnixNano(), 36) + sanitize(t.Name())}
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

// FR-PRG-8, T-29, 05 §7: against a real server, Scrub deletes the response
// records that carry the tag and leaves every other key under the prefix,
// epoch keys and 2 MiB sketch planes included.
func TestScrubByTag(t *testing.T) {
	for _, colo := range []bool{false, true} {
		t.Run(fmt.Sprintf("CoLocateEntries=%v", colo), func(t *testing.T) {
			s := scrubStore(t, func(c *Config) { c.CoLocateEntries = colo })
			ctx := t.Context()
			// Create epoch state (meta, planes) and a soft epoch so the keys
			// the scan will see are real.
			if err := s.SetEpoch(ctx, store.TagGlobal(), store.Epoch{Mode: store.EpochSoft, At: time.Now()}); err != nil {
				t.Fatal(err)
			}
			if err := s.SetEpoch(ctx, tagN(9), store.Epoch{Mode: store.EpochSoft, At: time.Now()}); err != nil {
				t.Fatal(err)
			}
			const n = 2500 // more than two SCAN batches
			for i := range n {
				k := store.Key{byte(i), byte(i >> 8), 1}
				tag := tagN(1)
				if i%5 == 0 {
					tag = tagN(2)
				}
				e := testEntry(time.Now())
				e.Tags = []store.Tag{store.TagGlobal(), tag}
				if err := s.Set(ctx, k, e); err != nil {
					t.Fatal(err)
				}
			}
			vary := testEntry(time.Now())
			vary.Kind, vary.Status, vary.Body = store.KindVarySpec, 0, nil
			vary.Tags = []store.Tag{tagN(1)}
			if err := s.Set(ctx, store.Key{0xee}, vary); err != nil {
				t.Fatal(err)
			}
			c := rawClient(t, s)
			planted := s.cfg.Prefix + ":planted"
			if err := c.Do(ctx, c.B().Set().Key(planted).Value("x").Build()).Error(); err != nil {
				t.Fatal(err)
			}
			// Another application's hash at an entry-shaped key: GET fails with
			// WRONGTYPE, which is "not ours", not a failed scrub.
			wrongType := s.entryKey(store.Key{0xdd})
			if err := c.Do(ctx, c.B().Hset().Key(wrongType).FieldValue().FieldValue("f", "v").Build()).Error(); err != nil {
				t.Fatal(err)
			}
			got, err := s.Scrub(ctx, []store.Tag{tagN(2)})
			if err != nil || got != n/5 {
				t.Fatalf("Scrub(tag 2) = %d, %v; want %d, nil", got, err, n/5)
			}
			for i := range n {
				_, err := s.Get(ctx, store.Key{byte(i), byte(i >> 8), 1})
				if i%5 == 0 && !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("scrubbed key %d: Get err = %v, want ErrNotFound", i, err)
				}
				if i%5 != 0 && err != nil {
					t.Fatalf("unscrubbed key %d: Get err = %v", i, err)
				}
			}
			if _, err := s.Get(ctx, store.Key{0xee}); err != nil {
				t.Fatalf("a vary spec was scrubbed: %v", err)
			}
			if !keyExists(t, c, planted) || !keyExists(t, c, wrongType) {
				t.Fatal("a non-entry key under the prefix was deleted")
			}
			for _, name := range []string{"meta", "global", "sketch:soft", "sketch:invalid"} {
				if !keyExists(t, c, epochKey(s, name)) {
					t.Fatalf("epoch key %q was deleted", name)
				}
			}
			if again, err := s.Scrub(ctx, []store.Tag{tagN(2)}); err != nil || again != 0 {
				t.Fatalf("second Scrub = %d, %v; want 0, nil", again, err)
			}
			// The rest go with the second tag, and the epochs still answer.
			if rest, err := s.Scrub(ctx, []store.Tag{tagN(1), tagN(2)}); err != nil || rest != n-n/5 {
				t.Fatalf("Scrub(tag 1) = %d, %v; want %d, nil", rest, err, n-n/5)
			}
			ep, ok, err := s.NewestEpoch(ctx, []store.Tag{tagN(9)}, time.Now().Add(-time.Hour))
			if err != nil || !ok || ep.Mode != store.EpochSoft {
				t.Fatalf("epoch after Scrub = %+v, %v, %v; want the soft epoch intact", ep, ok, err)
			}
		})
	}
}

// epochKey is the server key of an epoch key name as the store lays it out.
func epochKey(s *Store, name string) string {
	return s.cfg.Prefix + ":{" + s.cfg.HashTag + "}:" + name
}

// scanHook wraps a client so a test can act between SCAN steps.
type scanHook struct {
	client
	after func()
}

func (h scanHook) scanNode(ctx context.Context, addr string, cursor uint64, match string, count int64) ([]string, uint64, error) {
	keys, next, err := h.client.scanNode(ctx, addr, cursor, match, count)
	h.after()
	return keys, next, err
}

// FR-PRG-8, 05 §7: a context cancelled during the scan returns the count so
// far with ErrUnavailable, and what it did not reach is still there.
func TestScrubCancelled(t *testing.T) {
	cfg := Config{Addrs: []string{serverAddr(t)}, NoClockSkew: true, Prefix: "c" + strconv.FormatInt(time.Now().UnixNano(), 36)}
	var steps atomic.Int32
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s, err := newStore(cfg, func(ctx context.Context, cfg Config) (client, error) {
		cl, err := dialValkey(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return scanHook{cl, func() {
			if steps.Add(1) == 2 { // the first batch is done; the second is cut off
				cancel()
			}
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const n = 5000
	for i := range n {
		e := testEntry(time.Now())
		e.Tags = []store.Tag{tagN(1)}
		if err := s.Set(t.Context(), store.Key{byte(i), byte(i >> 8)}, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Scrub(ctx, []store.Tag{tagN(1)})
	if !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if got <= 0 || got >= n {
		t.Fatalf("count so far = %d, want in (0, %d): the first batch only", got, n)
	}
	if rest, err := s.Scrub(t.Context(), []store.Tag{tagN(1)}); err != nil || got+rest != n {
		t.Fatalf("second Scrub = %d, %v after %d; want the remainder of %d", rest, err, got, n)
	}
}

// FR-PRG-8: an eager hard purge through the engine deletes the records now,
// not only at expiry.
func TestEngineEagerHardPurgeScrubs(t *testing.T) {
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(cc(200, "max-age=3600", "v"))
	st := engineStore(t)
	e := engineWith(t, weir.Config{Store: st})
	engineServe(t, e, "/a", o)
	engineServe(t, e, "/keep", o)
	c := rawClient(t, st)
	entries := func() int {
		n := 0
		keys, err := c.Do(t.Context(), c.B().Keys().Pattern(st.eprfx+"*").Build()).AsStrSlice()
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range keys {
			if st.isEntryKey(k) {
				n++
			}
		}
		return n
	}
	if got := entries(); got != 2 {
		t.Fatalf("entries before purge = %d, want 2", got)
	}
	if err := e.Purge(t.Context(), weir.Purge{Mode: weir.PurgeHard, Eager: true, URLs: []string{"https://example.com/a"}}); err != nil {
		t.Fatal(err)
	}
	if got := entries(); got != 1 {
		t.Fatalf("entries after an eager purge of /a = %d, want 1", got)
	}
}

// stormAddr is the dedicated small server for the eviction storm: maxmemory
// 16mb and volatile-lfu (CI starts a second Valkey on it). The test flushes
// it, so it refuses any server that is not obviously a throwaway.
func stormAddr(t *testing.T) string {
	t.Helper()
	a := os.Getenv("WEIR_VALKEY_STORM_ADDR")
	if a == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("WEIR_VALKEY_STORM_ADDR is not set in CI")
		}
		t.Skip("WEIR_VALKEY_STORM_ADDR is not set; start a server with --maxmemory 16mb --maxmemory-policy volatile-lfu")
	}
	c, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{a}, ForceSingleClient: true, DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	m, err := c.Do(t.Context(), c.B().ConfigGet().Parameter("maxmemory").Build()).AsStrMap()
	if err != nil {
		t.Fatal(err)
	}
	mem, _ := strconv.ParseInt(m["maxmemory"], 10, 64)
	if mem <= 0 || mem > 64<<20 {
		t.Fatalf("WEIR_VALKEY_STORM_ADDR has maxmemory %q; refusing to FLUSHALL a server that is not a small throwaway", m["maxmemory"])
	}
	if err := c.Do(t.Context(), c.B().Flushall().Build()).Error(); err != nil {
		t.Fatal(err)
	}
	return a
}

func serverInt(t *testing.T, c valkey.Client, section, field string) int64 {
	t.Helper()
	info, err := c.Do(t.Context(), c.B().Info().Section(section).Build()).ToString()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(info, "\r\n") {
		if v, ok := strings.CutPrefix(line, field+":"); ok {
			n, _ := strconv.ParseInt(v, 10, 64)
			return n
		}
	}
	t.Fatalf("INFO %s has no %s", section, field)
	return 0
}

// T6.11, T-29, 05 §7: a flood of one-hit entries into a server with
// maxmemory 16mb and volatile-lfu evicts entries, never epoch state. The
// engine keeps serving through it, and an entry hard-purged mid-flood is not
// served again, whether the server still holds it or not.
func TestEvictionStormEngineStaysCorrect(t *testing.T) {
	addr := stormAddr(t)
	st := engineStoreAt(t, addr)
	o := testorigin.NewChecked(t, 64, 16)
	body := strings.Repeat("h", 1024)
	o.Default(cc(200, "max-age=3600", body))
	o.Route("/victim", cc(200, "max-age=3600", "v1"))
	e := engineWith(t, weir.Config{Store: st})

	const hot = 40
	hotPaths := make([]string, hot)
	for i := range hot {
		hotPaths[i] = "/hot/" + strconv.Itoa(i)
		engineServe(t, e, hotPaths[i], o)
	}
	engineServe(t, e, "/victim", o)
	hits := func() int {
		n := 0
		for _, p := range hotPaths {
			resp, _, err := engineServeErr(t, e, p, o)
			if err != nil {
				t.Fatalf("Serve %s: %v", p, err)
			}
			if resp.Cache.Hit {
				n++
			}
		}
		return n
	}
	for range 5 { // warm the server's access counters
		hits()
	}
	before := hits()
	if before != hot {
		t.Fatalf("baseline hits = %d of %d, want all", before, hot)
	}
	c := rawClient(t, st)
	evictedBefore := serverInt(t, c, "stats", "evicted_keys")

	// The flood: one-hit entries straight into the store, a hot-key reader
	// through the engine, and a hard purge of /victim a third of the way in.
	const total, writers = 30000, 8
	var next, setErrs, serveErrs, stale atomic.Int64
	purged := make(chan struct{}) // closed once the hard purge has returned
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			for {
				i := next.Add(1)
				if i > total {
					return
				}
				e := testEntry(time.Now())
				e.Body = []byte(body + body)
				e.Tags = []store.Tag{store.TagGlobal(), {byte(i), byte(i >> 8), byte(i >> 16), 0xf1}}
				if err := st.Set(t.Context(), store.Key{byte(i), byte(i >> 8), byte(i >> 16), 0xf1}, e); err != nil {
					setErrs.Add(1)
				}
			}
		})
	}
	done := make(chan struct{})
	var readers sync.WaitGroup
	readers.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			resp, b, err := engineServeErr(t, e, hotPaths[i%hot], o)
			if err != nil || resp.StatusCode != 200 {
				serveErrs.Add(1)
			}
			_ = b
		}
	})
	readers.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			select {
			case <-purged:
			case <-done:
				return
			}
			_, b, err := engineServeErr(t, e, "/victim", o)
			if err != nil {
				serveErrs.Add(1)
			} else if b == "v1" {
				stale.Add(1)
			}
		}
	})
	// Real-clock poll (CLAUDE.md rule 6, integration tag): the writers run on
	// a real server and expose no signal at a third of the way.
	for next.Load() < total/3 {
		time.Sleep(10 * time.Millisecond)
	}
	// The purge is only a test of the epoch if the entry is still cached: the
	// flood may have evicted it, so fetch it again and require a hit.
	engineServe(t, e, "/victim", o)
	if resp, _ := engineServe(t, e, "/victim", o); !resp.Cache.Hit {
		t.Fatal("/victim is not cached right before the purge; the purge would prove nothing")
	}
	o.Route("/victim", cc(200, "max-age=3600", "v2"))
	if err := e.Purge(t.Context(), weir.Purge{Mode: weir.PurgeHard, URLs: []string{"https://example.com/victim"}}); err != nil {
		t.Fatal(err)
	}
	close(purged)
	wg.Wait()
	time.Sleep(2100 * time.Millisecond) // the epoch is ceil(now); a v2 fetched inside that second is purged once more (E-7)
	close(done)
	readers.Wait()

	evicted := serverInt(t, c, "stats", "evicted_keys") - evictedBefore
	if evicted == 0 {
		t.Fatalf("no key was evicted: the flood did not fill the %d byte server", serverInt(t, c, "memory", "maxmemory"))
	}
	for _, name := range []string{"meta", "global", "sketch:soft", "sketch:invalid", "hardidx"} {
		if !keyExists(t, c, epochKey(st, name)) {
			t.Errorf("epoch key %q was evicted", name)
		}
	}
	if n := stale.Load(); n != 0 {
		t.Errorf("a hard-purged entry was served %d times after the purge", n)
	}
	if n := serveErrs.Load(); n != 0 {
		t.Errorf("the engine returned %d errors or non-200s during the flood", n)
	}
	resp, b, err := engineServeErr(t, e, "/victim", o)
	if err != nil || b != "v2" {
		t.Fatalf("victim after the flood = %q, %v (hit=%v); want v2", b, err, resp != nil && resp.Cache.Hit)
	}
	after := hits()
	if after == 0 {
		t.Errorf("no hot key is cached after the flood")
	}
	t.Logf("eviction storm: %d one-hit writes (%d failed), %d keys evicted; hot hits %d/%d before, %d/%d after",
		total, setErrs.Load(), evicted, before, hot, after, hot)
}

// T-29, 05 §7: against a real server, allkeys-* is refused (an epoch key
// could be evicted) and the store works again once the policy is safe. The
// storm server is a throwaway, so the test may change its policy.
func TestAllKeysPolicyRefused(t *testing.T) {
	addr := stormAddr(t)
	c, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{addr}, ForceSingleClient: true, DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	setPolicy := func(p string) {
		t.Helper()
		if err := c.Do(t.Context(), c.B().ConfigSet().ParameterValue().ParameterValue("maxmemory-policy", p).Build()).Error(); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = c.Do(context.Background(), c.B().ConfigSet().ParameterValue().ParameterValue("maxmemory-policy", "volatile-lfu").Build()).Error()
	})
	prefix := "p" + strconv.FormatInt(time.Now().UnixNano(), 36)
	for _, p := range []string{"allkeys-lfu", "allkeys-lru", "allkeys-random"} {
		setPolicy(p)
		s, err := New(Config{Addrs: []string{addr}, Prefix: prefix})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Get(t.Context(), store.Key{1})
		_ = s.Close()
		if !errors.Is(err, store.ErrUnavailable) || !errors.Is(err, errPolicy) || !strings.Contains(err.Error(), p) {
			t.Fatalf("policy %s: Get err = %v, want ErrUnavailable naming the policy", p, err)
		}
	}
	setPolicy("volatile-lfu")
	s, err := New(Config{Addrs: []string{addr}, Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Get(t.Context(), store.Key{1}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("volatile-lfu: Get err = %v, want ErrNotFound", err)
	}
}

// FR-PRG-8, 05 §7: a store whose only address is a replica has no primary to
// scan, and Scrub says so instead of reporting an empty success. Needs a
// replica of WEIR_VALKEY_ADDR in WEIR_VALKEY_REPLICA_ADDR (as the HardEpochWait
// test); skips without one, so CI does not run it.
func TestScrubOnReplicaIsUnavailable(t *testing.T) {
	ra := os.Getenv("WEIR_VALKEY_REPLICA_ADDR")
	if ra == "" {
		t.Skip("WEIR_VALKEY_REPLICA_ADDR is not set (a replica of WEIR_VALKEY_ADDR)")
	}
	s, err := New(Config{Addrs: []string{ra}, SkipPolicyCheck: true, Prefix: "r" + strconv.FormatInt(time.Now().UnixNano(), 36)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.Scrub(t.Context(), []store.Tag{tagN(1)})
	if !errors.Is(err, store.ErrUnavailable) || n != 0 {
		t.Fatalf("Scrub on a replica = %d, %v; want 0, ErrUnavailable", n, err)
	}
}
