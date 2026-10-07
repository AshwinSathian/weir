package weir_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
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

func postReq(path string) *weir.Request {
	r := getReq(path)
	r.Method = http.MethodPost
	return r
}

// unsafeAnswer answers unsafe methods with status and header, and GET with
// a cacheable body.
func unsafeAnswer(status int, h http.Header, body string) testorigin.Behavior {
	return testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
		if r.Method != http.MethodGet {
			return &weir.Response{StatusCode: status, Header: h.Clone(), Body: http.NoBody}, nil
		}
		return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"` + body + `"`}},
			Body: io.NopCloser(strings.NewReader(body))}, nil
	}}
}

// FR-INV-1, FR-INV-3: a 2xx response to an unsafe method invalidates the
// target URI and same-origin Location and Content-Location URIs; a
// cross-origin one and a 4xx answer invalidate nothing. An invalidated
// entry with a validator is revalidated, not refetched in full.
func TestUnsafeMethodInvalidates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(cacheable("v"))
		o.Route("/a", unsafeAnswer(http.StatusCreated, http.Header{
			"Location":         {"b?q=1"},
			"Content-Location": {"https://EXAMPLE.com:443/c"},
		}, "a"))
		o.Route("/x", unsafeAnswer(http.StatusCreated, http.Header{"Location": {"https://other.example/y"}}, "x"))
		o.Route("/f", unsafeAnswer(http.StatusSeeOther, http.Header{"Location": {"/g"}}, "f"))
		o.Route("/d", unsafeAnswer(http.StatusNotFound, http.Header{"Location": {"/e"}}, "d"))
		// FR-INV-2: Cache-Group-Invalidation on a response to a safe method is ignored.
		o.Route("/gm", grouped(`"g"`, nil))
		o.Route("/gi", grouped(`"g"`, http.Header{"Cache-Group-Invalidation": {`"g"`}}))
		obs := &purgeObserver{}
		cfg := cacheCfg
		cfg.Observer = obs
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		other := func(path string) *weir.Request { r := getReq(path); r.Host = "other.example"; return r }
		reqs := map[string]func() *weir.Request{
			"/a":   func() *weir.Request { return getReq("/a") },
			"/b?q": func() *weir.Request { r := getReq("/b"); r.RawQuery = "q=1"; return r },
			"/c":   func() *weir.Request { return getReq("/c") },
			"/d":   func() *weir.Request { return getReq("/d") },
			"/e":   func() *weir.Request { return getReq("/e") },
			"/x":   func() *weir.Request { return getReq("/x") },
			"/g":   func() *weir.Request { return getReq("/g") },
			"oy":   func() *weir.Request { return other("/y") },
		}
		for _, r := range reqs {
			serve(t, e, r(), o)
		}
		time.Sleep(time.Second)
		serve(t, e, postReq("/a"), o)
		serve(t, e, postReq("/x"), o)
		serve(t, e, postReq("/f"), o)
		serve(t, e, postReq("/d"), o)
		serve(t, e, getReq("/gm"), o)
		time.Sleep(time.Second)
		serve(t, e, getReq("/gi"), o)
		time.Sleep(2 * time.Second) // epochs round up to whole seconds (E-7)

		// A group invalidation is soft, so /gm must be fresh, not just a hit.
		if resp, _ := serve(t, e, getReq("/gm"), o); !resp.Cache.Hit || resp.Cache.Stale != weir.StaleNone {
			t.Errorf("/gm: hit=%v stale=%v, want a fresh hit: a GET response invalidated its group", resp.Cache.Hit, resp.Cache.Stale)
		}
		for name, want := range map[string]bool{"/a": false, "/b?q": false, "/c": false, "/x": false, "/g": false, "/d": true, "/e": true, "oy": true} {
			resp, _ := serve(t, e, reqs[name](), o)
			if resp.Cache.Hit != want {
				t.Errorf("%s: hit = %v, want %v (%s)", name, resp.Cache.Hit, want, resp.Header.Get("Cache-Status"))
			}
		}
		var last *weir.Request
		for _, r := range o.Requests() {
			if r.Path == "/a" && r.Method == http.MethodGet {
				last = r
			}
		}
		if got := last.Header.Get("If-None-Match"); got != `"a"` {
			t.Errorf("revalidation of /a sent If-None-Match %q, want %q", got, `"a"`)
		}
		// 04 §9.2: one EvPurge{invalid} per invalidating response, none for the 404.
		if got := obs.count(); got != 3 {
			t.Errorf("EvPurge{invalid} events = %d, want 3", got)
		}
	})
}

// purgeObserver counts EvPurge events with reason "invalid".
type purgeObserver struct {
	mu sync.Mutex
	n  int
}

func (o *purgeObserver) Observe(ev weir.Event) {
	if ev.Kind == weir.EvPurge && ev.Reason == "invalid" {
		o.mu.Lock()
		o.n++
		o.mu.Unlock()
	}
}

func (o *purgeObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.n
}

// FR-PRG-7, T-10: a fetch sent before an invalidation and stored after it
// does not survive the invalidation.
func TestPurgeDuringInflightFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		var mu sync.Mutex
		gets := 0
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			if r.Method == http.MethodPost {
				return &weir.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
			}
			mu.Lock()
			gets++
			first := gets == 1
			mu.Unlock()
			if first {
				<-gate
			}
			return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{"Cache-Control": {"max-age=60"}},
				Body: io.NopCloser(strings.NewReader("old"))}, nil
		}})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		done := make(chan struct{})
		go func() {
			defer close(done)
			serve(t, e, getReq("/a"), o)
		}()
		synctest.Wait() // the GET is at the origin
		time.Sleep(time.Second)
		serve(t, e, postReq("/a"), o)
		time.Sleep(time.Second)
		close(gate)
		<-done

		time.Sleep(2 * time.Second)
		resp, _ := serve(t, e, getReq("/a"), o)
		if resp.Cache.Hit {
			t.Fatalf("pre-invalidation fetch served as a hit: %s", resp.Header.Get("Cache-Status"))
		}
		mu.Lock()
		defer mu.Unlock()
		if gets != 2 {
			t.Fatalf("origin GETs = %d, want 2", gets)
		}
	})
}

// purgeable answers with a 60 s lifetime, a 30 s stale-while-revalidate
// window, a 600 s stale-if-error window and a validator.
func purgeable(body string) testorigin.Behavior {
	return testorigin.Behavior{Header: http.Header{
		"Cache-Control": {"max-age=60, stale-while-revalidate=30, stale-if-error=600"},
		"Etag":          {`"` + body + `"`},
	}, Body: []byte(body)}
}

func mustPurge(t *testing.T, e *weir.Engine, p weir.Purge) {
	t.Helper()
	if err := e.Purge(t.Context(), p); err != nil {
		t.Fatalf("Purge(%+v): %v", p, err)
	}
}

// lastGet returns the newest GET the origin saw for path.
func lastGet(t *testing.T, o *testorigin.Origin, path string) *weir.Request {
	t.Helper()
	var last *weir.Request
	for _, r := range o.Requests() {
		if r.Path == path && r.Method == http.MethodGet {
			last = r
		}
	}
	if last == nil {
		t.Fatalf("origin saw no GET %s", path)
	}
	return last
}

// purgeReasons records the Reason of every EvPurge event.
type purgeReasons struct {
	mu sync.Mutex
	rs []string
}

func (o *purgeReasons) Observe(ev weir.Event) {
	if ev.Kind == weir.EvPurge {
		o.mu.Lock()
		o.rs = append(o.rs, ev.Reason)
		o.mu.Unlock()
	}
}

// FR-PRG-2, T6.12: a soft-purged entry is stale as of the purge time. Inside
// its stale-while-revalidate window, measured from the purge, it is served
// stale while one conditional request refreshes it; past that window it is
// revalidated before it is served, although its own lifetime has not ended.
func TestSoftPurgeServesStaleWhileRevalidating(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(purgeable("v"))
		obs := &purgeReasons{}
		cfg := cacheCfg
		cfg.Observer = obs
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for _, p := range []string{"/a", "/late", "/keep"} {
			serve(t, e, getReq(p), o)
		}
		time.Sleep(time.Second)
		mustPurge(t, e, weir.Purge{URLs: []string{"https://example.com/a", "https://example.com/late"}})
		if len(obs.rs) != 1 || obs.rs[0] != "soft" {
			t.Fatalf("EvPurge reasons = %v, want [soft]", obs.rs)
		}
		time.Sleep(2 * time.Second) // epochs round up to whole seconds (E-7)

		resp, body := serve(t, e, getReq("/a"), o)
		if body != "v" || !resp.Cache.Hit || resp.Cache.Stale != weir.StaleWhileRevalidate {
			t.Fatalf("after soft purge: %q hit=%v stale=%v, want a stale-while-revalidate hit", body, resp.Cache.Hit, resp.Cache.Stale)
		}
		synctest.Wait() // the background refresh finishes
		if n := o.Calls("/a"); n != 2 {
			t.Fatalf("origin calls for /a = %d, want 2", n)
		}
		if got := lastGet(t, o, "/a").Header.Get("If-None-Match"); got != `"v"` {
			t.Errorf("refresh sent If-None-Match %q, want %q", got, `"v"`)
		}
		if resp, _ = serve(t, e, getReq("/a"), o); !resp.Cache.Hit || resp.Cache.Stale != weir.StaleNone {
			t.Errorf("after the refresh: hit=%v stale=%v, want a fresh hit", resp.Cache.Hit, resp.Cache.Stale)
		}
		if resp, _ = serve(t, e, getReq("/keep"), o); !resp.Cache.Hit || resp.Cache.Stale != weir.StaleNone || o.Calls("/keep") != 1 {
			t.Errorf("unpurged entry: hit=%v stale=%v calls=%d, want an untouched fresh hit", resp.Cache.Hit, resp.Cache.Stale, o.Calls("/keep"))
		}

		time.Sleep(35 * time.Second) // 37 s after the purge: past the 30 s window, inside max-age=60
		if resp, _ = serve(t, e, getReq("/late"), o); resp.Cache.Stale != weir.StaleNone || o.Calls("/late") != 2 {
			t.Errorf("past the window: stale=%v calls=%d, want a revalidation before serving", resp.Cache.Stale, o.Calls("/late"))
		}
		if got := lastGet(t, o, "/late").Header.Get("If-None-Match"); got != `"v"` {
			t.Errorf("late revalidation sent If-None-Match %q, want %q", got, `"v"`)
		}
	})
}

// FR-PRG-2, 05 E-7: a soft purge shows at the next lookup, although the
// memory store reports its time rounded up to the next whole second. The
// refreshed entry is fresh again at once.
func TestSoftPurgeAppliesAtOnce(t *testing.T) {
	for _, p := range []weir.Purge{
		{URLs: []string{"https://example.com/a"}},
		{All: true},
	} {
		synctest.Test(t, func(t *testing.T) {
			o := testorigin.NewChecked(t, 64, 16)
			o.Default(purgeable("v"))
			e := newEngine(t, cacheCfg)
			defer closeEngine(t, e)

			serve(t, e, getReq("/a"), o)
			time.Sleep(1300 * time.Millisecond)
			mustPurge(t, e, p)
			time.Sleep(time.Millisecond) // a refresh sent in the purge's own clock tick is purged too (05 E-3)
			resp, _ := serve(t, e, getReq("/a"), o)
			if !resp.Cache.Hit || resp.Cache.Stale != weir.StaleWhileRevalidate {
				t.Fatalf("%+v: right after the purge: hit=%v stale=%v, want stale-while-revalidate", p, resp.Cache.Hit, resp.Cache.Stale)
			}
			synctest.Wait()
			time.Sleep(100 * time.Millisecond)
			if resp, _ = serve(t, e, getReq("/a"), o); !resp.Cache.Hit || resp.Cache.Stale != weir.StaleNone || o.Calls("/a") != 2 {
				t.Fatalf("%+v: after the refresh: hit=%v stale=%v calls=%d, want a fresh hit and 2 calls", p, resp.Cache.Hit, resp.Cache.Stale, o.Calls("/a"))
			}
		})
	}
}

// 05 E-7, E-8: the memory store times a soft purge by URL to the next whole
// second, so while other epochs keep arriving a refresh sent inside that
// second is purged again. The repeats end once the second has passed: the
// cost is bounded and never serves the entry as fresh in between.
func TestSoftPurgeRepeatsEndWithTheSecond(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(purgeable("v"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(1300 * time.Millisecond)
		mustPurge(t, e, weir.Purge{URLs: []string{"https://example.com/a"}}) // timed 2 s
		stale := func(at time.Duration, want bool, calls int) {
			t.Helper()
			resp, _ := serve(t, e, getReq("/a"), o)
			synctest.Wait()
			if got := resp.Cache.Stale == weir.StaleWhileRevalidate; got != want || !resp.Cache.Hit || o.Calls("/a") != calls {
				t.Fatalf("at %v: stale=%v hit=%v calls=%d, want stale=%v and %d calls", at, resp.Cache.Stale, resp.Cache.Hit, o.Calls("/a"), want, calls)
			}
		}
		time.Sleep(100 * time.Millisecond)
		stale(1400*time.Millisecond, true, 2)
		time.Sleep(100 * time.Millisecond)
		mustPurge(t, e, weir.Purge{URLs: []string{"https://example.com/other"}}) // an unrelated epoch defeats the E-10 fast path
		stale(1500*time.Millisecond, true, 3)                                    // requested at 1.4 s, at or before 2 s
		time.Sleep(700 * time.Millisecond)
		stale(2200*time.Millisecond, true, 4) // requested at 1.5 s
		time.Sleep(100 * time.Millisecond)
		stale(2300*time.Millisecond, false, 4) // requested at 2.2 s: past the epoch
	})
}

// 05 E-6, FR-STF-2: a hard purge the store refuses at its cap is reported
// to the operator but is not a store outage. A retry loop of such purges
// leaves the store breaker closed, so cached entries stay hits and
// Purge{All}, the remedy E-6 names, still works.
func TestCappedHardPurgeKeepsStoreBreakerClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, err := memory.New(memory.Config{MaxHardEpochs: 1})
		if err != nil {
			t.Fatal(err)
		}
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(purgeable("v"))
		obs := &kindCounter{}
		cfg := cacheCfg
		cfg.Store = st
		cfg.Observer = obs
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/keep"), o)
		time.Sleep(time.Second)
		mustPurge(t, e, weir.Purge{Mode: weir.PurgeHard, URLs: []string{"https://example.com/first"}})
		for i := range 8 {
			err := e.Purge(t.Context(), weir.Purge{Mode: weir.PurgeHard, URLs: []string{"https://example.com/n" + strconv.Itoa(i)}})
			if !errors.Is(err, store.ErrUnavailable) {
				t.Fatalf("capped purge %d = %v, want store.ErrUnavailable", i, err)
			}
		}
		if n := obs.count(weir.EvStoreBreaker); n != 0 {
			t.Fatalf("store breaker events = %d, want 0", n)
		}
		if n := obs.count(weir.EvStoreError); n != 8 {
			t.Errorf("EvStoreError events = %d, want 8 (each refusal is still reported)", n)
		}
		if resp, _ := serve(t, e, getReq("/keep"), o); !resp.Cache.Hit || o.Calls("/keep") != 1 {
			t.Fatalf("/keep after capped purges: hit=%v calls=%d, want a hit", resp.Cache.Hit, o.Calls("/keep"))
		}
		mustPurge(t, e, weir.Purge{Mode: weir.PurgeHard, All: true})
		if resp, _ := serve(t, e, getReq("/keep"), o); resp.Cache.Hit {
			t.Fatalf("/keep is a hit after Purge{All} hard")
		}
	})
}

// kindCounter counts events by kind.
type kindCounter struct {
	mu sync.Mutex
	n  map[weir.EventKind]int
}

func (o *kindCounter) Observe(ev weir.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.n == nil {
		o.n = map[weir.EventKind]int{}
	}
	o.n[ev.Kind]++
}

func (o *kindCounter) count(k weir.EventKind) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.n[k]
}

// FR-PRG-1, FR-PRG-3, T6.12: a hard-purged entry is a miss: fetched in full
// with no validator, and never served stale when the origin is down. The
// purge URL is rewritten like a request, so another spelling of the same URI
// reaches the entry.
func TestHardPurgeIsMiss(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(purgeable("v"))
		obs := &purgeReasons{}
		cfg := cacheCfg
		cfg.Observer = obs
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		// `/q|"x` holds bytes net/url would escape; the key keeps them as sent.
		// "@" is legal in a path and a query, and only userinfo in the host.
		for _, p := range []string{"/", "/a", `/q|"x`, "/@scope/pkg", "/down", "/keep"} {
			serve(t, e, getReq(p), o)
		}
		time.Sleep(time.Second)
		mustPurge(t, e, weir.Purge{Mode: weir.PurgeHard, URLs: []string{"https://EXAMPLE.com:443/a", "https://example.com/down", "https://example.com", `https://example.com/q|"x`, "https://example.com/@scope/pkg", "https://example.com/mail?to=a@b.c"}})
		if len(obs.rs) != 1 || obs.rs[0] != "hard" {
			t.Fatalf("EvPurge reasons = %v, want [hard]", obs.rs)
		}
		time.Sleep(2 * time.Second)

		for _, p := range []string{"/a", "/", `/q|"x`, "/@scope/pkg"} {
			resp, _ := serve(t, e, getReq(p), o)
			if resp.Cache.Hit || resp.Cache.Stale != weir.StaleNone || o.Calls(p) != 2 {
				t.Fatalf("%s after hard purge: hit=%v stale=%v calls=%d, want a miss", p, resp.Cache.Hit, resp.Cache.Stale, o.Calls(p))
			}
			if got := lastGet(t, o, p).Header.Get("If-None-Match"); got != "" {
				t.Errorf("%s: hard-purged entry was revalidated with If-None-Match %q", p, got)
			}
		}
		if resp, _ := serve(t, e, getReq("/keep"), o); !resp.Cache.Hit || o.Calls("/keep") != 1 {
			t.Errorf("unpurged entry: hit=%v calls=%d, want a hit", resp.Cache.Hit, o.Calls("/keep"))
		}
		o.SetDown(true)
		if resp, body, err := serveResult(t, e, getReq("/down"), o); err == nil {
			t.Fatalf("origin down: served %q (stale=%v), want an error, not the purged entry", body, resp.Cache.Stale)
		}
	})
}

// FR-PRG-3, 04 §7: a soft purge after a hard purge does not bring the entry
// back for stale serving or conditional revalidation.
func TestSoftAfterHardStaysHard(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(purgeable("v"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		serve(t, e, getReq("/down"), o)
		time.Sleep(time.Second)
		urls := []string{"https://example.com/a", "https://example.com/down"}
		mustPurge(t, e, weir.Purge{Mode: weir.PurgeHard, URLs: urls})
		time.Sleep(2 * time.Second)
		mustPurge(t, e, weir.Purge{URLs: urls})
		mustPurge(t, e, weir.Purge{All: true})
		time.Sleep(2 * time.Second)

		resp, _ := serve(t, e, getReq("/a"), o)
		if resp.Cache.Hit || resp.Cache.Stale != weir.StaleNone {
			t.Fatalf("hit=%v stale=%v, want a miss", resp.Cache.Hit, resp.Cache.Stale)
		}
		if got := lastGet(t, o, "/a").Header.Get("If-None-Match"); got != "" {
			t.Errorf("hard-purged entry was revalidated with If-None-Match %q", got)
		}
		o.SetDown(true)
		if resp, body, err := serveResult(t, e, getReq("/down"), o); err == nil {
			t.Fatalf("origin down: served %q (stale=%v), want an error", body, resp.Cache.Stale)
		}
	})
}

// FR-PRG-5, T6.13: Purge{All: true} bumps the global epoch. Every entry
// revalidates exactly once, and none is served stale beyond its
// stale-while-revalidate window.
func TestGlobalEpochSoft(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const n = 20
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(purgeable("v"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		path := func(i int) string { return "/k" + strconv.Itoa(i) }
		for i := range n {
			serve(t, e, getReq(path(i)), o)
		}
		time.Sleep(time.Second)
		mustPurge(t, e, weir.Purge{All: true})
		time.Sleep(2 * time.Second)

		for i := range n / 2 {
			if resp, _ := serve(t, e, getReq(path(i)), o); resp.Cache.Stale != weir.StaleWhileRevalidate {
				t.Fatalf("%s: stale=%v, want stale-while-revalidate", path(i), resp.Cache.Stale)
			}
		}
		synctest.Wait()
		time.Sleep(35 * time.Second) // past the window measured from the purge
		for i := n / 2; i < n; i++ {
			if resp, _ := serve(t, e, getReq(path(i)), o); resp.Cache.Stale != weir.StaleNone {
				t.Fatalf("%s: served stale (%v) 37 s after the purge with a 30 s window", path(i), resp.Cache.Stale)
			}
		}
		for i := range n {
			resp, _ := serve(t, e, getReq(path(i)), o)
			if !resp.Cache.Hit || resp.Cache.Stale != weir.StaleNone || o.Calls(path(i)) != 2 {
				t.Fatalf("%s: hit=%v stale=%v calls=%d, want a fresh hit after one revalidation", path(i), resp.Cache.Hit, resp.Cache.Stale, o.Calls(path(i)))
			}
		}
	})
}

// FR-PRG-4, INV-7, T6.12: after a soft purge of 5 000 cached keys, 5 000
// concurrent requests are all served stale at once and the origin never sees
// more than MaxConcurrent fetches in flight (NewChecked fails the test if it
// does). The purge names the keys by URL, or by one group they all share
// (FR-PRG-6): one epoch then covers all 5 000.
func TestPurge5000KeysBounded(t *testing.T) {
	t.Run("by URL", func(t *testing.T) { purge5000(t, false) })
	t.Run("by shared group", func(t *testing.T) { purge5000(t, true) })
}

func purge5000(t *testing.T, byGroup bool) {
	synctest.Test(t, func(t *testing.T) {
		const n = 5000
		o := testorigin.NewChecked(t, 64, 16)
		answer := purgeable("v")
		answer.Header.Set("Cache-Groups", `"all"`)
		o.Default(answer)
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		urls := make([]string, n)
		for i := range n {
			p := "/k" + strconv.Itoa(i)
			urls[i] = "https://example.com" + p
			serve(t, e, getReq(p), o)
		}
		time.Sleep(time.Second)
		p := weir.Purge{URLs: urls}
		if byGroup {
			p = weir.Purge{Origin: "https://example.com", Groups: []string{"all"}}
		}
		mustPurge(t, e, p)
		time.Sleep(2 * time.Second)
		b := answer
		b.Delay = 100 * time.Millisecond // refreshes overlap
		o.Default(b)
		o.Reset()

		var wg sync.WaitGroup
		var slow, notStale atomic.Int64
		for i := range n {
			wg.Go(func() {
				start := time.Now()
				resp, err := e.Serve(t.Context(), getReq("/k"+strconv.Itoa(i)), o)
				if err != nil {
					t.Errorf("Serve: %v", err)
					return
				}
				resp.Body.Close()
				if time.Since(start) > time.Millisecond {
					slow.Add(1)
				}
				if resp.Cache.Stale != weir.StaleWhileRevalidate {
					notStale.Add(1)
				}
			})
		}
		wg.Wait()
		if slow.Load() != 0 || notStale.Load() != 0 {
			t.Fatalf("%d requests took over 1 ms, %d were not served stale-while-revalidate", slow.Load(), notStale.Load())
		}
		synctest.Wait()
		time.Sleep(time.Minute) // the refreshes that got a slot finish; the rest were dropped, not queued
		// 48 = MaxConcurrent (64) - ReserveForeground (16): the herd fills the
		// background share exactly and never touches the foreground reserve.
		if got, calls := o.MaxInflight(), o.TotalCalls(); got != 48 || calls != 48 {
			t.Fatalf("origin max in flight = %d, calls = %d, want 48 and 48", got, calls)
		}
	})
}

// FR-PRG-1: any invalid input rejects the whole call before an epoch is
// written, so the valid URL and All in the same call purge nothing.
func TestPurgeRejectsInvalidInput(t *testing.T) {
	const good = "https://example.com/a"
	tests := []struct {
		name string
		p    weir.Purge
	}{
		{"relative URL", weir.Purge{URLs: []string{good, "/a"}}},
		{"scheme other than http", weir.Purge{URLs: []string{good, "ftp://example.com/a"}}},
		{"URL without a host", weir.Purge{URLs: []string{good, "https:///a"}}},
		{"opaque URL", weir.Purge{URLs: []string{good, "https:example.com/a"}}},
		{"URL with userinfo", weir.Purge{URLs: []string{good, "https://u:p@example.com/a"}}},
		{"URL with a fragment", weir.Purge{URLs: []string{good, "https://example.com/a#f"}}},
		{"URL that does not parse", weir.Purge{URLs: []string{good, "https://example.com/%zz"}}},
		{"URL with a control byte", weir.Purge{URLs: []string{good, "https://example.com/a\x00"}}},
		{"empty URL", weir.Purge{URLs: []string{good, ""}}},
		{"groups without an origin", weir.Purge{URLs: []string{good}, Groups: []string{"g"}}},
		{"origin with a path", weir.Purge{URLs: []string{good}, Origin: "https://example.com/x", Groups: []string{"g"}}},
		{"origin with a trailing slash", weir.Purge{URLs: []string{good}, Origin: "https://example.com/", Groups: []string{"g"}}},
		{"origin with a query", weir.Purge{URLs: []string{good}, Origin: "https://example.com?x", Groups: []string{"g"}}},
		{"origin without a scheme", weir.Purge{URLs: []string{good}, Origin: "example.com", Groups: []string{"g"}}},
		{"origin with a bad host", weir.Purge{URLs: []string{good}, Origin: "https://exa mple.com", Groups: []string{"g"}}},
		{"URL with a fragment after the query", weir.Purge{URLs: []string{good, "https://example.com/a?x=1#f"}}},
		{"URL with userinfo and no path", weir.Purge{URLs: []string{good, "https://u@example.com"}}},
		{"origin without groups", weir.Purge{URLs: []string{good}, Origin: "garbage"}},
		{"unknown mode", weir.Purge{Mode: weir.PurgeHard + 1, URLs: []string{good}}},
		{"eager with soft mode (FR-PRG-8)", weir.Purge{Eager: true, URLs: []string{good}}},
		// FR-STO-10: no stored response can carry these names, so they never match.
		{"group name over MaxGroupBytes", weir.Purge{Origin: "https://example.com", Groups: []string{"g", strings.Repeat("x", 129)}}},
		{"group name with DEL", weir.Purge{Origin: "https://example.com", Groups: []string{"g\x7f"}}},
		{"group name with a control byte", weir.Purge{Origin: "https://example.com", Groups: []string{"g\n"}}},
		{"group name with a non-ASCII byte", weir.Purge{Origin: "https://example.com", Groups: []string{"caf\xc3\xa9"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.NewChecked(t, 64, 16)
				o.Default(purgeable("v"))
				obs := &purgeReasons{}
				cfg := cacheCfg
				cfg.Observer = obs
				e := newEngine(t, cfg)
				defer closeEngine(t, e)

				serve(t, e, getReq("/a"), o)
				time.Sleep(time.Second)
				tc.p.All = true
				err := e.Purge(t.Context(), tc.p)
				if !errors.Is(err, weir.ErrInvalidRequest) {
					t.Fatalf("Purge = %v, want ErrInvalidRequest", err)
				}
				time.Sleep(2 * time.Second)
				if resp, _ := serve(t, e, getReq("/a"), o); !resp.Cache.Hit || resp.Cache.Stale != weir.StaleNone || o.Calls("/a") != 1 {
					t.Fatalf("rejected purge still wrote an epoch: hit=%v stale=%v calls=%d", resp.Cache.Hit, resp.Cache.Stale, o.Calls("/a"))
				}
				if len(obs.rs) != 0 {
					t.Fatalf("rejected purge emitted EvPurge %v", obs.rs)
				}
			})
		})
	}
}

// FR-PRG-1: a group purge with a valid origin is accepted
// (TestGroupsScopedByOrigin shows it reaches entries). FR-LCY-2: Purge on a closed engine is ErrClosed.
// 05 E-6: a hard purge past the store's cap reports the store's error, and
// the epochs written before it stay and are reported (04 §9.2). FR-PRG-8: an
// eager hard purge writes its epoch and returns ErrEagerUnsupported.
func TestPurgeGroupsAndStoreErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, err := memory.New(memory.Config{MaxHardEpochs: 1})
		if err != nil {
			t.Fatal(err)
		}
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(purgeable("v"))
		obs := &purgeReasons{}
		cfg := cacheCfg
		cfg.Store = st
		cfg.Observer = obs
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(time.Second)
		mustPurge(t, e, weir.Purge{Origin: "HTTPS://Example.com:443", Groups: []string{"g", "h", "", strings.Repeat("x", 128)}})
		mustPurge(t, e, weir.Purge{})
		err = e.Purge(t.Context(), weir.Purge{Mode: weir.PurgeHard, URLs: []string{"https://example.com/a", "https://example.com/b"}})
		if !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("hard purge past MaxHardEpochs = %v, want store.ErrUnavailable", err)
		}
		if want := []string{"soft", "hard"}; !slices.Equal(obs.rs, want) {
			t.Fatalf("EvPurge reasons = %v, want %v (the group purge, then the half-written hard purge)", obs.rs, want)
		}
		err = e.Purge(t.Context(), weir.Purge{Mode: weir.PurgeHard, Eager: true, URLs: []string{"https://example.com/a"}})
		if !errors.Is(err, weir.ErrEagerUnsupported) {
			t.Fatalf("eager hard purge = %v, want ErrEagerUnsupported", err)
		}
		time.Sleep(2 * time.Second)
		if resp, _ := serve(t, e, getReq("/a"), o); resp.Cache.Hit {
			t.Fatalf("/a is a hit: the epoch written before the failure was lost")
		}
		if err := e.Purge(t.Context(), weir.Purge{URLs: []string{"https://example.com/a", "/bad"}}); err == nil ||
			err.Error() != "weir: invalid request: purge-url: purge url 1" {
			t.Fatalf("URL error = %v, want the reason and the URL's index", err)
		}
		closeEngine(t, e)
		if err := e.Purge(t.Context(), weir.Purge{All: true}); !errors.Is(err, weir.ErrClosed) {
			t.Fatalf("Purge after Close = %v, want ErrClosed", err)
		}
	})
}

// FR-PRG-7, T-10: a fetch sent before a Purge and stored after it does not
// survive the purge, soft or hard.
func TestPurgeDuringInflightFetchPurgeAPI(t *testing.T) {
	for _, mode := range []weir.PurgeMode{weir.PurgeSoft, weir.PurgeHard} {
		synctest.Test(t, func(t *testing.T) {
			gate := make(chan struct{})
			o := testorigin.NewChecked(t, 64, 16)
			b := cacheable("old")
			b.Gate = gate
			o.Default(b)
			e := newEngine(t, cacheCfg)
			defer closeEngine(t, e)

			done := make(chan struct{})
			go func() {
				defer close(done)
				serve(t, e, getReq("/a"), o)
			}()
			synctest.Wait() // the GET is at the origin
			time.Sleep(time.Second)
			mustPurge(t, e, weir.Purge{Mode: mode, URLs: []string{"https://example.com/a"}})
			time.Sleep(time.Second)
			close(gate)
			<-done

			time.Sleep(2 * time.Second)
			if resp, _ := serve(t, e, getReq("/a"), o); resp.Cache.Hit || o.Calls("/a") != 2 {
				t.Fatalf("mode %d: pre-purge fetch served as a hit (%s), origin calls %d", mode, resp.Header.Get("Cache-Status"), o.Calls("/a"))
			}
		})
	}
}

// grouped answers GET with a purgeable response in the Cache-Groups list
// groups, plus extra; unsafe methods get 201 with extra alone.
func grouped(groups string, extra http.Header) testorigin.Behavior {
	return testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
		h := extra.Clone()
		if h == nil {
			h = http.Header{}
		}
		if r.Method != http.MethodGet {
			return &weir.Response{StatusCode: http.StatusCreated, Header: h, Body: http.NoBody}, nil
		}
		h.Set("Cache-Control", "max-age=60, stale-while-revalidate=30")
		h.Set("Etag", `"v"`)
		if groups != "" {
			h.Set("Cache-Groups", groups)
		}
		return &weir.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(strings.NewReader("v"))}, nil
	}}
}

func hostReq(host, path string) *weir.Request {
	r := getReq(path)
	r.Host = host
	return r
}

// wantCache serves req and checks whether it was a fresh hit or a
// stale-while-revalidate hit, then lets a background refresh finish.
func wantCache(t *testing.T, e *weir.Engine, o *testorigin.Origin, req *weir.Request, stale weir.StaleReason) {
	t.Helper()
	resp, _ := serve(t, e, req, o)
	if !resp.Cache.Hit || resp.Cache.Stale != stale {
		t.Errorf("%s%s: hit=%v stale=%v, want a hit with stale=%v (%s)", req.Host, req.Path, resp.Cache.Hit, resp.Cache.Stale, stale, resp.Header.Get("Cache-Status"))
	}
	synctest.Wait()
}

// FR-PRG-6, FR-STO-10, T-25: a group purge reaches the entries whose
// Cache-Groups list names the group on that origin, and no entry on another
// origin. Names are compared byte for byte; a member's parameters are dropped.
func TestGroupsScopedByOrigin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(grouped(`"g"`, nil))
		o.Route("/two", grouped(`"x";v=1, "g"`, nil))
		o.Route("/upper", grouped(`"G"`, nil))
		o.Route("/none", grouped("", nil))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		all := []*weir.Request{
			hostReq("a.example", "/1"), hostReq("a.example", "/two"), hostReq("a.example", "/upper"),
			hostReq("a.example", "/none"), hostReq("b.example", "/1"), hostReq("a.example:8443", "/1"),
		}
		for _, r := range all {
			serve(t, e, r, o)
		}
		time.Sleep(time.Second)
		mustPurge(t, e, weir.Purge{Origin: "https://a.example", Groups: []string{"g"}})
		time.Sleep(2 * time.Second)

		for i, stale := range []weir.StaleReason{weir.StaleWhileRevalidate, weir.StaleWhileRevalidate, weir.StaleNone, weir.StaleNone, weir.StaleNone, weir.StaleNone} {
			wantCache(t, e, o, all[i], stale)
		}

		// A hard group purge makes the entry a miss, again only on its origin.
		mustPurge(t, e, weir.Purge{Mode: weir.PurgeHard, Origin: "https://b.example", Groups: []string{"g"}})
		time.Sleep(2 * time.Second)
		if resp, _ := serve(t, e, all[4], o); resp.Cache.Hit {
			t.Errorf("b.example/1 is a hit after a hard purge of its group")
		}
		wantCache(t, e, o, all[5], weir.StaleNone)
	})
}

// FR-INV-2, T-28: a 2xx response to an unsafe method that carries
// Cache-Group-Invalidation soft-purges the listed groups on its own origin,
// so their entries are still served inside the stale-while-revalidate
// window. A list that does not parse or exceeds Limits invalidates no group,
// and CacheGroups.Ignore switches the field off while the entries keep their
// group tags.
func TestGroupInvalidationIsSoft(t *testing.T) {
	tests := []struct {
		name   string
		field  []string
		ignore bool
		host   string           // of the unsafe request; "" is the entries' own
		want   weir.StaleReason // for the entries in group g
		events string           // EvPurge reasons, with the group count where one is set (04 §9.2)
	}{
		{"listed group is soft-purged", []string{`"g", "nobody"`}, false, "", weir.StaleWhileRevalidate, "invalid group:2"},
		{"field split over two lines", []string{`"nobody"`, `"g"`}, false, "", weir.StaleWhileRevalidate, "invalid group:2"},
		{"a repeated name is one epoch", []string{`"g", "g", "g"`}, false, "", weir.StaleWhileRevalidate, "invalid group:1"},
		{"empty field writes nothing", []string{""}, false, "", weir.StaleNone, "invalid"},
		{"token member invalidates nothing", []string{`"g", g`}, false, "", weir.StaleNone, "invalid group-invalid"},
		{"33 members invalidate nothing", []string{`"g"` + strings.Repeat(`, "g"`, 32)}, false, "", weir.StaleNone, "invalid group-invalid"},
		{"CacheGroups.Ignore", []string{`"g"`}, true, "", weir.StaleNone, "invalid"},
		// T-25, FR-PRG-6: the groups named are those of the response's own origin.
		{"response from another origin", []string{`"g"`}, false, "third.example", weir.StaleNone, "invalid group:1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.NewChecked(t, 64, 16)
				o.Default(grouped(`"g"`, nil))
				o.Route("/h", grouped(`"h"`, nil))
				o.Route("/post", grouped("", http.Header{"Cache-Group-Invalidation": tc.field}))
				obs := &purgeEvents{}
				cfg := cacheCfg
				cfg.CacheGroups.Ignore = tc.ignore
				cfg.Observer = obs
				e := newEngine(t, cfg)
				defer closeEngine(t, e)

				all := []*weir.Request{getReq("/a"), getReq("/b"), getReq("/h"), hostReq("other.example", "/a")}
				for _, r := range all {
					serve(t, e, r, o)
				}
				time.Sleep(time.Second)
				post := postReq("/post")
				if tc.host != "" {
					post.Host = tc.host
				}
				serve(t, e, post, o)
				time.Sleep(2 * time.Second)

				wantCache(t, e, o, all[0], tc.want)
				wantCache(t, e, o, all[1], tc.want)
				wantCache(t, e, o, all[2], weir.StaleNone)
				wantCache(t, e, o, all[3], weir.StaleNone) // T-25: same origin only
				if got := strings.Join(obs.evs, " "); got != tc.events {
					t.Errorf("EvPurge events = %q, want %q", got, tc.events)
				}
			})
		})
	}
}

// T-29, 05 E-8: 60 000 unsafe requests to distinct URIs inside one
// 60 s entry lifetime. Of 1 000 entries nothing invalidated, at most 8%
// are revalidated early (the sketch's expected rate is about 4%), and every
// entry whose URI was invalidated is validated before it is served.
func TestInvalidationFloodBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const hot, hit, flood = 1000, 100, 60000
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(unsafeAnswer(http.StatusCreated, http.Header{"Cache-Group-Invalidation": {`"flood"`}}, "v"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		for i := range hot {
			serve(t, e, getReq("/hot"+strconv.Itoa(i)), o)
		}
		for i := range hit {
			serve(t, e, getReq("/x"+strconv.Itoa(i)), o)
		}
		time.Sleep(time.Second)
		for i := range flood {
			serve(t, e, postReq("/x"+strconv.Itoa(i)), o)
		}
		time.Sleep(2 * time.Second)
		o.Reset()

		early := 0
		for i := range hot {
			if resp, _ := serve(t, e, getReq("/hot"+strconv.Itoa(i)), o); !resp.Cache.Hit {
				early++
			}
		}
		if early > hot*8/100 {
			t.Errorf("%d of %d untouched entries were revalidated early, want at most 8%%", early, hot)
		}
		for i := range hit {
			p := "/x" + strconv.Itoa(i)
			if resp, _ := serve(t, e, getReq(p), o); resp.Cache.Hit || o.Calls(p) != 1 {
				t.Fatalf("%s: hit=%v calls=%d, want a validation before serving", p, resp.Cache.Hit, o.Calls(p))
			}
		}
	})
}

// purgeEvents records every EvPurge as its reason, plus ":Status" when set.
type purgeEvents struct {
	mu  sync.Mutex
	evs []string
}

func (o *purgeEvents) Observe(ev weir.Event) {
	if ev.Kind != weir.EvPurge {
		return
	}
	s := ev.Reason
	if ev.Status != 0 {
		s += ":" + strconv.Itoa(ev.Status)
	}
	o.mu.Lock()
	o.evs = append(o.evs, s)
	o.mu.Unlock()
}

// FR-STO-10, FR-STO-12: a response whose Cache-Groups field is not a List of
// Strings is delivered but not stored, EvNotStored{groups} says why, and the
// hit-for-miss marker it leaves makes the next request fetch on its own.
func TestMalformedCacheGroupsNotStored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(grouped(`"a", b`, nil))
		var ev eventCounter
		cfg := cacheCfg
		cfg.CacheGroups.Ignore = true // FR-STO-10 holds with Ignore set
		cfg.Observer = &ev
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		resp, body := serve(t, e, getReq("/a"), o)
		if body != "v" || resp.Cache.Stored || resp.Header.Get("Cache-Groups") != `"a", b` {
			t.Fatalf("first response: body %q stored=%v Cache-Groups %q, want it delivered whole and not stored", body, resp.Cache.Stored, resp.Header.Get("Cache-Groups"))
		}
		resp, _ = serve(t, e, getReq("/a"), o)
		if resp.Cache.Hit || resp.Cache.Detail != "hit-for-miss" || o.Calls("/a") != 2 {
			t.Fatalf("second request: hit=%v detail=%q calls=%d, want a hit-for-miss fetch", resp.Cache.Hit, resp.Cache.Detail, o.Calls("/a"))
		}
		if n := ev.count(weir.EvNotStored.String() + "/groups"); n != 2 {
			t.Fatalf("EvNotStored{groups} = %d, want 2", n)
		}
	})
}

// T-23, T-28: unsafe requests whose responses each invalidate 32 new groups
// fill the store's soft sketch plane, so entries in no group at all go
// soft-stale. The damage stops there: every entry is still served from the
// cache inside its stale-while-revalidate window, refreshes stay inside the
// limiter, and CacheGroups.Ignore switches the attack off. The sketch here
// has 4 096 cells; 500 responses write 16 000 epochs into it.
func TestGroupInvalidationFloodStaysServable(t *testing.T) {
	for _, ignore := range []bool{false, true} {
		t.Run("Ignore="+strconv.FormatBool(ignore), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const hot, flood = 200, 500
				st, err := memory.New(memory.Config{EpochSlots: 1 << 12})
				if err != nil {
					t.Fatal(err)
				}
				var n atomic.Int64
				o := testorigin.NewChecked(t, 64, 16)
				o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
					if r.Method == http.MethodGet {
						return grouped("", nil).Func(r)
					}
					id := strconv.FormatInt(n.Add(1), 10)
					var list []string
					for i := range 32 {
						list = append(list, `"`+id+"-"+strconv.Itoa(i)+`"`)
					}
					return &weir.Response{StatusCode: http.StatusNoContent, Header: http.Header{"Cache-Group-Invalidation": {strings.Join(list, ", ")}}, Body: http.NoBody}, nil
				}})
				var ev eventCounter
				cfg := cacheCfg
				cfg.Store = st
				cfg.CacheGroups.Ignore = ignore
				cfg.Observer = &ev
				e := newEngine(t, cfg)
				defer closeEngine(t, e)

				for i := range hot {
					serve(t, e, getReq("/hot"+strconv.Itoa(i)), o)
				}
				time.Sleep(time.Second)
				for range flood {
					serve(t, e, postReq("/post"), o)
				}
				time.Sleep(2 * time.Second)

				stale := 0
				for i := range hot {
					resp, _ := serve(t, e, getReq("/hot"+strconv.Itoa(i)), o)
					if !resp.Cache.Hit {
						t.Fatalf("/hot%d was not served from the cache (%s)", i, resp.Header.Get("Cache-Status"))
					}
					if resp.Cache.Stale == weir.StaleWhileRevalidate {
						stale++
					}
					synctest.Wait()
				}
				groups := ev.count(weir.EvPurge.String() + "/group")
				if ignore {
					if stale != 0 || groups != 0 {
						t.Fatalf("with Ignore: %d entries went stale, %d group events; want 0 and 0", stale, groups)
					}
					return
				}
				// 32 000 cell writes into 4 096 cells: a miss needs a cell nothing hit.
				if stale < hot*9/10 || groups != flood {
					t.Fatalf("%d of %d entries went stale, %d group events; want the plane saturated and %d events", stale, hot, groups, flood)
				}
			})
		})
	}
}

// FR-OBS-3, FR-INV-2: refused Cache-Group-Invalidation fields are logged
// once per engine, however many arrive, and the line names the origin, never
// the field's text. Each one still emits EvPurge{group-invalid}.
func TestInvalidGroupInvalidationLoggedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(grouped("", http.Header{"Cache-Group-Invalidation": {`"SECRETGROUP", bad`}}))
		var ev eventCounter
		cfg := cacheCfg
		cfg.Logger = slog.New(slog.NewTextHandler(&buf, nil))
		cfg.Observer = &ev
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for i := range 3 {
			serve(t, e, postReq("/p"+strconv.Itoa(i)), o)
		}
		log := buf.String()
		if n := strings.Count(log, "Cache-Group-Invalidation"); n != 1 || !strings.Contains(log, "https://example.com") || strings.Contains(log, "SECRETGROUP") {
			t.Fatalf("log has %d warnings, want 1 naming the origin and not the field:\n%s", n, log)
		}
		if n := ev.count(weir.EvPurge.String() + "/group-invalid"); n != 3 {
			t.Fatalf("EvPurge{group-invalid} = %d, want 3", n)
		}
	})
}

// sharedOnly answers the shared tags alone, so a test sees only what a group
// tag does under a URI flood (T-29). Its plain NewestEpoch is the memory
// store's, which false-positives on every tag once the plane is full.
type sharedOnly struct{ *memory.Store }

func (b sharedOnly) NewestEpochShared(ctx context.Context, _, shared []store.Tag, since time.Time) (store.Epoch, bool, error) {
	return b.Store.NewestEpochShared(ctx, nil, shared, since)
}

// T-29, 05 E-12: a flood of URI invalidations that fills the invalid plane
// must not make a group's entries read as invalidated. With the store's
// SharedTagEpochs capability the entries are hits; without it the group tag's
// false positive revalidates them, which is the old behavior.
func TestInvalidationFloodLeavesGroupsServable(t *testing.T) {
	for _, shared := range []bool{true, false} {
		t.Run("capability="+strconv.FormatBool(shared), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mem, err := memory.New(memory.Config{EpochSlots: 1 << 4})
				if err != nil {
					t.Fatal(err)
				}
				var st store.Store = struct{ store.Store }{mem} // hides the capability
				if shared {
					st = sharedOnly{mem}
				}
				o := testorigin.NewChecked(t, 64, 16)
				o.Default(grouped(`"g"`, nil))
				cfg := cacheCfg
				cfg.Store = st
				e := newEngine(t, cfg)
				defer closeEngine(t, e)

				serve(t, e, getReq("/a"), o)
				time.Sleep(time.Second)
				for i := range 200 {
					serve(t, e, postReq("/flood"+strconv.Itoa(i)), o)
				}
				time.Sleep(time.Second)
				resp, _ := serve(t, e, getReq("/a"), o)
				if resp.Cache.Hit != shared {
					t.Fatalf("hit = %v (%s), want %v", resp.Cache.Hit, resp.Header.Get("Cache-Status"), shared)
				}
			})
		})
	}
}

// T-29, 05 E-12: with the capability present, the global and URI tags are
// still read in the invalid plane, so a POST invalidates its own URI.
func TestSharedTagsKeepURIInvalidation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(grouped(`"g"`, nil))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(time.Second)
		serve(t, e, postReq("/a"), o)
		time.Sleep(time.Second)
		if resp, _ := serve(t, e, getReq("/a"), o); resp.Cache.Hit {
			t.Fatalf("hit after a POST to the same URI (%s), want a validation", resp.Header.Get("Cache-Status"))
		}
	})
}

// T-29, 05 E-12: with the invalid plane saturated by a flood, a POST to the
// entry's own URI still revalidates it. Pins the split of Entry.Tags: the
// URI tag stays plain while the group is shared.
func TestSharedTagsKeepURIInvalidationUnderFlood(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mem, err := memory.New(memory.Config{EpochSlots: 1 << 4})
		if err != nil {
			t.Fatal(err)
		}
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(grouped(`"g"`, nil))
		cfg := cacheCfg
		cfg.Store = mem
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		serve(t, e, getReq("/b"), o)
		time.Sleep(time.Second)
		for i := range 200 {
			serve(t, e, postReq("/flood"+strconv.Itoa(i)), o)
		}
		serve(t, e, postReq("/a"), o)
		time.Sleep(time.Second)
		if resp, _ := serve(t, e, getReq("/a"), o); resp.Cache.Hit {
			t.Fatalf("/a was a hit after a POST to it (%s), want a validation", resp.Header.Get("Cache-Status"))
		}
	})
}
