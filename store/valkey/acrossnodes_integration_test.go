//go:build integration

package valkey

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store"
)

// nodeStores returns n stores on one fresh prefix, as n nodes would have,
// each with mutate applied, after the prefix's first-use repair has passed.
func nodeStores(t *testing.T, n int, mutate func(*Config)) []*Store {
	t.Helper()
	prefix := "n" + strconv.FormatInt(time.Now().UnixNano(), 36) + sanitize(t.Name())
	out := make([]*Store, n)
	for i := range out {
		cfg := Config{Addrs: []string{serverAddr(t)}, NoClockSkew: true, Prefix: prefix}
		if mutate != nil {
			mutate(&cfg)
		}
		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = s
	}
	if _, _, err := out[0].NewestEpoch(t.Context(), []store.Tag{store.TagGlobal()}, time.Now()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond) // the first-use repair epoch, as in engineStoreAt (E-7)
	return out
}

// FR-COA-11, 05 §7: against a real server, two nodes with
// CoalesceAcrossNodes fetch a cold key once between them.
func TestAcrossNodesOneFetch(t *testing.T) {
	stores := nodeStores(t, 2, func(c *Config) { c.CoalesceAcrossNodes = true })
	o := testorigin.New()
	o.Default(testorigin.Behavior{Delay: 300 * time.Millisecond, Header: map[string][]string{"Cache-Control": {"max-age=60"}}, Body: []byte("one")})
	engines := []*weir.Engine{engineWith(t, weir.Config{Store: stores[0]}), engineWith(t, weir.Config{Store: stores[1]})}
	var wg sync.WaitGroup
	for _, e := range engines {
		wg.Go(func() {
			if _, body := engineServe(t, e, "/cold", o); body != "one" {
				t.Errorf("body %q", body)
			}
		})
		time.Sleep(20 * time.Millisecond) // real clock: the first node takes the lock
	}
	wg.Wait()
	if n := o.TotalCalls(); n != 1 {
		t.Fatalf("origin calls = %d, want 1", n)
	}
}

// FR-STF-5, 05 §7: a purge one node writes reaches another node's local
// copy within EpochTTL, and the node that wrote it at once.
func TestLocalTierPurgeAcrossNodes(t *testing.T) {
	stores := nodeStores(t, 2, func(c *Config) { c.LocalTier = &LocalTierConfig{EpochTTL: 500 * time.Millisecond} })
	o := testorigin.New()
	o.Default(purgeable("v1"))
	a, b := engineWith(t, weir.Config{Store: stores[0]}), engineWith(t, weir.Config{Store: stores[1]})
	engineServe(t, a, "/p", o)
	if resp, _ := engineServe(t, b, "/p", o); !resp.Cache.Hit {
		t.Fatal("node b missed what node a stored")
	}
	if resp, _ := engineServe(t, b, "/p", o); !resp.Cache.Hit || o.TotalCalls() != 1 {
		t.Fatal("node b's second request was not a hit")
	}
	o.Default(purgeable("v2"))
	time.Sleep(time.Second) // epochs are whole server seconds (E-7)
	if err := a.Purge(t.Context(), weir.Purge{Mode: weir.PurgeHard, URLs: []string{"https://example.com/p"}}); err != nil {
		t.Fatal(err)
	}
	// Each node sees the purge once the epoch's second has passed (E-7); node
	// b's cached purge check adds at most EpochTTL to that.
	for name, e := range map[string]*weir.Engine{"a": a, "b": b} {
		waitFor(t, "node "+name+" to see the purge", func() bool {
			_, body := engineServe(t, e, "/p", o)
			return body == "v2"
		})
	}
}
