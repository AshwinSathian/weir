package weir_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

// barrier releases its n callers together, for the first rounds rounds. Later
// callers (the retries of a lost swap) pass at once.
type barrier struct {
	mu                  sync.Mutex
	cond                *sync.Cond
	n, rounds, at, gens int
}

func newBarrier(n, rounds int) *barrier {
	b := &barrier{n: n, rounds: rounds}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *barrier) wait() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.gens >= b.rounds {
		return
	}
	g := b.gens
	if b.at++; b.at == b.n {
		b.at, b.gens = 0, b.gens+1
		b.cond.Broadcast()
		return
	}
	for g == b.gens {
		b.cond.Wait()
	}
}

// specGate holds every read that returns a vary spec until n readers have
// one, so all writers of a test see the same spec before any of them writes.
// It embeds the memory store, so it keeps the VarySetter capability.
type specGate struct {
	*memory.Store
	on atomic.Bool
	b  *barrier
}

func (g *specGate) Get(ctx context.Context, k store.Key) (*store.Entry, error) {
	e, err := g.Store.Get(ctx, k)
	if err == nil && e.Kind == store.KindVarySpec && g.on.Load() {
		g.b.wait()
	}
	return e, err
}

// varyCASRun warms one variant, then sends 63 more at once through a gate that
// makes their read-modify-writes of the spec overlap. It returns how many of
// the 64 languages are hits afterwards and the overflow events.
func varyCASRun(t *testing.T) (hits, overflows int) {
	const writers, maxVariants = 64, 8
	mem, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// Round 1: the lookups. Round 2: the spec reads of setVariant.
	gate := &specGate{Store: mem, b: newBarrier(writers-1, 2)}
	o := testorigin.NewChecked(t, 256, 256)
	o.Default(varyBody("Accept-Language", 100*time.Millisecond))
	var ev eventCounter
	cfg := wideLimiter(cacheCfg)
	cfg.Store, cfg.Observer = gate, &ev
	cfg.Key.MaxVariants = maxVariants
	cfg.Forward.Allow = []string{"Accept-Language"}
	e := newEngine(t, cfg)
	defer closeEngine(t, e)

	lang := func(i int) string { return fmt.Sprintf("l%02d", i) }
	serve(t, e, varyReq("/v", "Accept-Language", lang(0)), o)
	gate.on.Store(true)
	chs := make([]<-chan served, 0, writers)
	for i := 1; i < writers; i++ {
		chs = append(chs, serveAsync(t.Context(), e, varyReq("/v", "Accept-Language", lang(i)), o))
	}
	for _, ch := range chs {
		if s := <-ch; s.err != nil {
			t.Fatal(s.err)
		}
	}
	gate.on.Store(false)
	for i := range writers {
		if resp, _ := serve(t, e, varyReq("/v", "Accept-Language", lang(i)), o); resp.Cache.Hit {
			hits++
		}
	}
	return hits, ev.count(weir.EvVaryOverflow.String() + "/")
}

// FR-KEY-10, NFR-3, 04 §6.7: 64 writers of one vary spec, all reading it
// before any writes, never lose each other's references: the spec ends with
// exactly MaxVariants refs, and the rest are refused as overflow.
func TestVaryCASConcurrentWriters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hits, overflows := varyCASRun(t)
		if hits != 8 {
			t.Errorf("hits = %d, want MaxVariants (8): a writer overwrote another's reference", hits)
		}
		if overflows < 56 {
			t.Errorf("overflow events = %d, want at least 56", overflows)
		}
	})
}

// 04 §6.7: a store without the capability keeps the read-modify-write and
// its cap: ten variants one after another leave eight hits and two overflows.
func TestVaryCASFallsBackWithoutCapability(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mem, err := memory.New(memory.Config{})
		if err != nil {
			t.Fatal(err)
		}
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(varyBody("Accept-Language", 0))
		var ev eventCounter
		cfg := cacheCfg
		cfg.Store, cfg.Observer = struct{ store.Store }{mem}, &ev // hides VarySetter
		cfg.Forward.Allow = []string{"Accept-Language"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		hits := 0
		for i := range 10 {
			serve(t, e, varyReq("/v", "Accept-Language", fmt.Sprint("l", i)), o)
			if resp, _ := serve(t, e, varyReq("/v", "Accept-Language", fmt.Sprint("l", i)), o); resp.Cache.Hit {
				hits++
			}
		}
		if n := ev.count(weir.EvVaryOverflow.String() + "/"); hits != 8 || n < 2 {
			t.Errorf("hits = %d, overflows = %d, want 8 and at least 2", hits, n)
		}
	})
}

// A store whose SetVarySpec fails is a store failure: the variant is not
// reported as stored and the next request tries again.
func TestVaryCASStoreError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mem, err := memory.New(memory.Config{})
		if err != nil {
			t.Fatal(err)
		}
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(varyBody("Accept-Language", 0))
		cfg := cacheCfg
		cfg.Store = failingVary{mem}
		cfg.Forward.Allow = []string{"Accept-Language"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, varyReq("/v", "Accept-Language", "en"), o)
		if resp, _ := serve(t, e, varyReq("/v", "Accept-Language", "en"), o); resp.Cache.Hit {
			t.Fatal("a variant whose spec write failed was served as a hit")
		}
	})
}

type failingVary struct{ *memory.Store }

func (failingVary) SetVarySpec(context.Context, store.Key, *store.Entry, *store.Entry) (bool, error) {
	return false, store.ErrUnavailable
}
