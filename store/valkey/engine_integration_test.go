//go:build integration

package valkey

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store"
)

// The engine suite against a real server (P25-04, docs/07 §6). These run on
// the real clock: the server expires keys and orders epochs on its own clock
// (CLAUDE.md hard rule 6, 05 §8), so they cannot sit in a synctest bubble.
// Lifetimes are 1 to 2 s and every fixed pause is named. The scenarios that
// read the memory store's internals (TestBatchWriteExpirySpread's counters,
// flight-table sizes, store-outage wrappers, limiter cap at 5 000 keys) are
// not repeated here; the LOG entry of P25-04 lists them.

// engineStore returns a store on a fresh prefix; keys from an earlier run live
// on the server for up to an hour.
//
// A fresh prefix has no epoch state, which the store reads as a loss and
// repairs with a global hard epoch at the next whole server second (05 §7). An
// entry fetched inside that second would be purged, so the helper makes the
// repair now and waits it out. NoClockSkew: client and server share a clock
// here, and a skew would stretch the wait.
func engineStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(Config{Addrs: []string{serverAddr(t)}, NoClockSkew: true, Prefix: "e" + strconv.FormatInt(time.Now().UnixNano(), 36)})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.NewestEpoch(t.Context(), []store.Tag{store.TagGlobal()}, time.Now()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond) // the epoch is ceil(now); an entry fetched in that second is still purged (E-7)
	return s
}

func engineWith(t *testing.T, cfg weir.Config) *weir.Engine {
	t.Helper()
	cfg.Store = engineStore(t)
	cfg.Freshness.NoJitter = true
	e, err := weir.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Errorf("engine Close: %v", err)
		}
		_ = cfg.Store.Close() // the engine does not close a store it was given
	})
	return e
}

func engineReq(method, path string) *weir.Request {
	return &weir.Request{Method: method, Scheme: "https", Host: "example.com", Path: path, Header: http.Header{"Accept": {"*/*"}}}
}

func engineServe(t *testing.T, e *weir.Engine, path string, o weir.Origin) (*weir.Response, string) {
	t.Helper()
	resp, body, err := engineServeErr(t, e, path, o)
	if err != nil {
		t.Fatalf("Serve %s: %v", path, err)
	}
	return resp, body
}

func engineServeErr(t *testing.T, e *weir.Engine, path string, o weir.Origin) (*weir.Response, string, error) {
	t.Helper()
	resp, err := e.Serve(t.Context(), engineReq(http.MethodGet, path), o)
	if err != nil {
		return nil, "", err
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(b), nil
}

func cc(status int, value, body string) testorigin.Behavior {
	h := http.Header{"Etag": {`"` + body + `"`}}
	if value != "" {
		h.Set("Cache-Control", value)
	}
	return testorigin.Behavior{Status: status, Header: h, Body: []byte(body)}
}

// eventLog records event kinds and reasons.
type eventLog struct {
	mu sync.Mutex
	n  map[string]int
}

func (l *eventLog) Observe(ev weir.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n == nil {
		l.n = map[string]int{}
	}
	l.n[ev.Kind.String()+"/"+ev.Reason]++
}

func (l *eventLog) count(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n[key]
}

// waitFor polls cond; a refresh in the background has no other signal on the
// real clock.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// FR-COA-1, FR-COA-4, T6.2: concurrent cold requests for one key make one
// origin call, and the entry reaches the server for the next request.
func TestEngineCoalesceColdKey(t *testing.T) {
	t.Parallel()
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: []byte("x"), Delay: 200 * time.Millisecond})
	obs := &eventLog{}
	e := engineWith(t, weir.Config{Observer: obs})

	const n = 200
	var wg sync.WaitGroup
	var bad atomic.Int32
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := e.Serve(context.Background(), engineReq(http.MethodGet, "/hot"), o)
			if err != nil {
				bad.Add(1)
				return
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 || string(b) != "x" {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("%d of %d requests failed", bad.Load(), n)
	}
	if c := o.Calls("/hot"); c != 1 {
		t.Fatalf("origin calls = %d, want 1", c)
	}
	if resp, _ := engineServe(t, e, "/hot", o); !resp.Cache.Hit || o.Calls("/hot") != 1 {
		t.Fatalf("next request: hit=%v calls=%d, want a hit from the server", resp.Cache.Hit, o.Calls("/hot"))
	}
}

// FR-STL-1, FR-STL-6, T6.1: inside the stale-while-revalidate window the
// stale entry is served and one background refresh replaces it.
func TestEngineStaleWhileRevalidate(t *testing.T) {
	t.Parallel()
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(cc(200, "max-age=1, stale-while-revalidate=60", "a"))
	e := engineWith(t, weir.Config{})

	engineServe(t, e, "/a", o)
	time.Sleep(2100 * time.Millisecond) // past max-age=1 on the server clock
	resp, body := engineServe(t, e, "/a", o)
	if body != "a" || !resp.Cache.Hit || resp.Cache.Stale != weir.StaleWhileRevalidate {
		t.Fatalf("in the window: %q hit=%v stale=%v, want a stale-while-revalidate hit", body, resp.Cache.Hit, resp.Cache.Stale)
	}
	waitFor(t, "the background refresh", func() bool { return o.Calls("/a") == 2 })
	waitFor(t, "the refreshed entry", func() bool {
		r, _ := engineServe(t, e, "/a", o)
		return r.Cache.Hit && r.Cache.Stale == weir.StaleNone
	})
	if c := o.Calls("/a"); c != 2 {
		t.Fatalf("origin calls = %d, want 2 (one refresh)", c)
	}
}

// FR-STL-2, 01 §7.2, T6.6: within stale-if-error a down origin still gets a
// stale 200; past it, an error.
func TestEngineStaleIfErrorOnOriginDown(t *testing.T) {
	t.Parallel()
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(cc(200, "max-age=1, stale-if-error=2", "a"))
	e := engineWith(t, weir.Config{})

	engineServe(t, e, "/a", o)
	time.Sleep(1500 * time.Millisecond) // stale, 0.5 s into the 2 s window
	o.SetDown(true)
	resp, body := engineServe(t, e, "/a", o)
	if body != "a" || resp.Cache.Stale != weir.StaleIfError {
		t.Fatalf("inside the window: %q stale=%v, want stale-if-error", body, resp.Cache.Stale)
	}
	time.Sleep(2500 * time.Millisecond) // 4 s old: past max-age plus window
	if _, _, err := engineServeErr(t, e, "/a", o); !errors.Is(err, weir.ErrOrigin) {
		t.Fatalf("past the window: err = %v, want ErrOrigin", err)
	}
}

func purgeable(body string) testorigin.Behavior {
	return cc(200, "max-age=60, stale-while-revalidate=30, stale-if-error=600", body)
}

// FR-PRG-2, 05 E-7, T6.1: a soft purge turns entries stale (served while they
// revalidate); an untouched entry stays fresh. The epoch lives on the server.
func TestEngineSoftPurge(t *testing.T) {
	t.Parallel()
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(purgeable("v"))
	e := engineWith(t, weir.Config{})

	engineServe(t, e, "/a", o)
	engineServe(t, e, "/keep", o)
	time.Sleep(time.Second) // the epoch is a whole second; entries must be older
	if err := e.Purge(t.Context(), weir.Purge{URLs: []string{"https://example.com/a"}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond) // epochs round up to the next second (E-7)

	resp, body := engineServe(t, e, "/a", o)
	if body != "v" || !resp.Cache.Hit || resp.Cache.Stale != weir.StaleWhileRevalidate {
		t.Fatalf("after soft purge: %q hit=%v stale=%v, want a stale-while-revalidate hit", body, resp.Cache.Hit, resp.Cache.Stale)
	}
	waitFor(t, "the refresh of /a", func() bool { return o.Calls("/a") == 2 })
	if resp, _ = engineServe(t, e, "/keep", o); !resp.Cache.Hit || resp.Cache.Stale != weir.StaleNone || o.Calls("/keep") != 1 {
		t.Fatalf("unpurged entry: hit=%v stale=%v calls=%d, want an untouched fresh hit", resp.Cache.Hit, resp.Cache.Stale, o.Calls("/keep"))
	}
}

// FR-PRG-2, FR-PRG-3, T6.12: a hard purge makes the entry a miss that is
// never revalidated or served stale, even with the origin down.
func TestEngineHardPurge(t *testing.T) {
	t.Parallel()
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(purgeable("v"))
	e := engineWith(t, weir.Config{})

	engineServe(t, e, "/a", o)
	engineServe(t, e, "/keep", o)
	time.Sleep(time.Second)
	if err := e.Purge(t.Context(), weir.Purge{Mode: weir.PurgeHard, URLs: []string{"https://example.com/a"}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)

	resp, _ := engineServe(t, e, "/a", o)
	if resp.Cache.Hit || o.Calls("/a") != 2 {
		t.Fatalf("after hard purge: hit=%v calls=%d, want a miss", resp.Cache.Hit, o.Calls("/a"))
	}
	for _, r := range o.Requests() {
		if r.Path == "/a" && r.Header.Get("If-None-Match") != "" {
			t.Fatalf("a hard-purged entry was revalidated with If-None-Match %q", r.Header.Get("If-None-Match"))
		}
	}
	if resp, _ = engineServe(t, e, "/keep", o); !resp.Cache.Hit || o.Calls("/keep") != 1 {
		t.Fatalf("unpurged entry: hit=%v calls=%d, want a hit", resp.Cache.Hit, o.Calls("/keep"))
	}
	if err := e.Purge(t.Context(), weir.Purge{Mode: weir.PurgeHard, All: true}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)
	o.SetDown(true)
	if _, body, err := engineServeErr(t, e, "/keep", o); err == nil {
		t.Fatalf("origin down after a global hard purge: served %q, want an error", body)
	}
}

// FR-PRG-6, FR-STO-10, T-25: a group purge reaches the entries whose
// Cache-Groups list names the group, and no other entry.
func TestEngineGroupPurge(t *testing.T) {
	t.Parallel()
	o := testorigin.NewChecked(t, 64, 16)
	grouped := func(group string) testorigin.Behavior {
		b := cc(200, "max-age=60, stale-while-revalidate=30", "v")
		b.Header.Set("Cache-Groups", `"`+group+`"`)
		return b
	}
	o.Route("/a", grouped("g"))
	o.Route("/b", grouped("g"))
	o.Route("/c", grouped("other"))
	e := engineWith(t, weir.Config{})

	for _, p := range []string{"/a", "/b", "/c"} {
		engineServe(t, e, p, o)
	}
	time.Sleep(time.Second)
	if err := e.Purge(t.Context(), weir.Purge{Mode: weir.PurgeHard, Origin: "https://example.com", Groups: []string{"g"}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)

	for _, p := range []string{"/a", "/b"} {
		if resp, _ := engineServe(t, e, p, o); resp.Cache.Hit || o.Calls(p) != 2 {
			t.Fatalf("%s after group purge: hit=%v calls=%d, want a miss", p, resp.Cache.Hit, o.Calls(p))
		}
	}
	if resp, _ := engineServe(t, e, "/c", o); !resp.Cache.Hit || o.Calls("/c") != 1 {
		t.Fatalf("/c: hit=%v calls=%d, want a hit", resp.Cache.Hit, o.Calls("/c"))
	}
}

// FR-NEG-1, FR-NEG-2, T6.6: an origin 503 is cached briefly as a negative
// entry that carries the status and Retry-After only; it expires on its own.
func TestEngineNegativeCache(t *testing.T) {
	t.Parallel()
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(testorigin.Behavior{Status: 503, Header: http.Header{"Retry-After": {"7"}, "X-Origin": {"secret"}}, Body: []byte("down")})
	obs := &eventLog{}
	e := engineWith(t, weir.Config{Observer: obs, Negative: weir.NegativeConfig{TTL: time.Second}})

	engineServe(t, e, "/a", o)
	for range 20 {
		resp, body := engineServe(t, e, "/a", o)
		if resp.StatusCode != 503 || resp.Cache.Detail != "negative" || body != "" || resp.Header.Get("X-Origin") != "" || resp.Header.Get("Retry-After") != "7" {
			t.Fatalf("negative hit: %d detail=%q body=%q header=%v", resp.StatusCode, resp.Cache.Detail, body, resp.Header)
		}
	}
	if c := o.Calls("/a"); c != 1 {
		t.Fatalf("origin calls = %d, want 1", c)
	}
	time.Sleep(2100 * time.Millisecond) // past Negative.TTL on the server clock
	engineServe(t, e, "/a", o)
	if c := o.Calls("/a"); c != 2 {
		t.Fatalf("after Negative.TTL origin calls = %d, want 2", c)
	}
}

// FR-CB-1, FR-CB-5, T6.6: gateway failures open the breaker, an open breaker
// makes no origin calls and still serves stale from the server, and a
// successful probe closes it.
func TestEngineBreaker(t *testing.T) {
	t.Parallel()
	o := testorigin.NewChecked(t, 64, 16)
	o.Route("/stale", cc(200, "max-age=1, stale-if-error=600", "s"))
	cfg := weir.Config{Breaker: weir.BreakerConfig{MinRequests: 10, OpenFor: time.Second, MaxOpenFor: time.Second}, Rand: func() float64 { return 0.5 }}
	e := engineWith(t, cfg)

	engineServe(t, e, "/stale", o)
	time.Sleep(1500 * time.Millisecond) // /stale is stale now
	o.Default(cc(http.StatusServiceUnavailable, "", "down"))
	for i := range 30 {
		if _, _, err := engineServeErr(t, e, fmt.Sprintf("/f%d", i), o); err != nil && !errors.Is(err, weir.ErrCircuitOpen) {
			t.Fatalf("failure %d: %v", i, err)
		}
	}
	calls := o.TotalCalls()
	if _, _, err := engineServeErr(t, e, "/x", o); !errors.Is(err, weir.ErrCircuitOpen) {
		t.Fatalf("while open: err = %v, want ErrCircuitOpen", err)
	}
	resp, body := engineServe(t, e, "/stale", o)
	if body != "s" || resp.Cache.Stale != weir.StaleCircuitOpen {
		t.Fatalf("stale while open: %q stale=%v, want circuit-open", body, resp.Cache.Stale)
	}
	if o.TotalCalls() != calls {
		t.Fatalf("origin calls while open = %d, want none", o.TotalCalls()-calls)
	}

	time.Sleep(1200 * time.Millisecond) // past OpenFor
	o.Default(cc(200, "max-age=60", "ok"))
	if _, body := engineServe(t, e, "/probe", o); body != "ok" {
		t.Fatalf("probe body = %q", body)
	}
	if _, body := engineServe(t, e, "/after", o); body != "ok" {
		t.Fatalf("after close: body = %q", body)
	}
}
