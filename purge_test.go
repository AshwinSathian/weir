package weir_test

import (
	"errors"
	"io"
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
		time.Sleep(2 * time.Second) // epochs round up to whole seconds (E-7)

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
// does). The purge names the keys by URL; M9-03 adds the shared-group form.
func TestPurge5000KeysBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const n = 5000
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(purgeable("v"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		urls := make([]string, n)
		for i := range n {
			p := "/k" + strconv.Itoa(i)
			urls[i] = "https://example.com" + p
			serve(t, e, getReq(p), o)
		}
		time.Sleep(time.Second)
		mustPurge(t, e, weir.Purge{URLs: urls})
		time.Sleep(2 * time.Second)
		b := purgeable("v")
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

// FR-PRG-1: a group purge with a valid origin is accepted (M9-03 tests that
// it reaches entries). FR-LCY-2: Purge on a closed engine is ErrClosed.
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
		mustPurge(t, e, weir.Purge{Origin: "HTTPS://Example.com:443", Groups: []string{"g", "h"}})
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
