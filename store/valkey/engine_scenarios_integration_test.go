//go:build integration

package valkey

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// The scenarios P25-04 left out (P25-04b, docs/07 §6), on the same footing:
// real server, real clock, fresh key prefix per test, every fixed pause named.
// Epochs are whole server seconds (E-7), so an entry must be at least a second
// older than the purge and a check waits 2.1 s after it.

func enginePost(t *testing.T, e *weir.Engine, path string, o weir.Origin) *weir.Response {
	t.Helper()
	req := engineReq(http.MethodPost, path)
	resp, err := e.Serve(t.Context(), req, o)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	resp.Body.Close()
	return resp
}

// unsafeAnswer answers unsafe methods with status and header and GET with a
// cacheable body, as the root package's helper of the same name does.
func unsafeAnswer(status int, h http.Header, body string) testorigin.Behavior {
	return testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
		if r.Method != http.MethodGet {
			return &weir.Response{StatusCode: status, Header: h.Clone(), Body: http.NoBody}, nil
		}
		return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"` + body + `"`}},
			Body: io.NopCloser(strings.NewReader(body))}, nil
	}}
}

// grouped answers GET with a group list and a stale-while-revalidate window,
// and an unsafe method with extra headers.
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

// FR-INV-1, FR-INV-3, T-29: a 2xx answer to an unsafe method invalidates the
// target URI and a same-origin Location through the invalid-mode epochs on the
// server; a 4xx answer and a cross-origin Location invalidate nothing. An
// invalidated entry with a validator is revalidated, not refetched in full.
func TestEngineUnsafeMethodInvalidates(t *testing.T) {
	t.Parallel()
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(unsafeAnswer(http.StatusOK, nil, "v"))
	o.Route("/a", unsafeAnswer(http.StatusCreated, http.Header{"Location": {"b"}}, "a"))
	o.Route("/x", unsafeAnswer(http.StatusCreated, http.Header{"Location": {"https://other.example/y"}}, "x"))
	o.Route("/d", unsafeAnswer(http.StatusNotFound, http.Header{"Location": {"/keep"}}, "d"))
	e := engineWith(t, weir.Config{})

	for _, p := range []string{"/a", "/b", "/x", "/d", "/keep"} {
		engineServe(t, e, p, o)
	}
	time.Sleep(time.Second) // entries must be older than the epoch second
	enginePost(t, e, "/a", o)
	enginePost(t, e, "/x", o)
	enginePost(t, e, "/d", o)
	time.Sleep(2100 * time.Millisecond) // epochs round up to the next second (E-7)

	for p, want := range map[string]bool{"/a": false, "/b": false, "/x": false, "/d": true, "/keep": true} {
		if resp, _ := engineServe(t, e, p, o); resp.Cache.Hit != want {
			t.Errorf("%s: hit = %v, want %v (%s)", p, resp.Cache.Hit, want, resp.Header.Get("Cache-Status"))
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
}

// FR-INV-2, T-28: Cache-Group-Invalidation on a 2xx answer to an unsafe method
// soft-purges the group, so its entries are served stale inside the window;
// the same field on a GET answer does nothing.
func TestEngineGroupInvalidationIsSoft(t *testing.T) {
	t.Parallel()
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(grouped(`"g"`, nil))
	o.Route("/h", grouped(`"h"`, nil))
	o.Route("/gm", grouped(`"gm"`, http.Header{"Cache-Group-Invalidation": {`"gm"`}}))
	o.Route("/post", grouped("", http.Header{"Cache-Group-Invalidation": {`"g", "nobody"`}}))
	e := engineWith(t, weir.Config{})

	for _, p := range []string{"/a", "/b", "/h", "/gm"} {
		engineServe(t, e, p, o)
	}
	time.Sleep(time.Second)
	enginePost(t, e, "/post", o)
	time.Sleep(2100 * time.Millisecond)

	for _, p := range []string{"/a", "/b"} {
		resp, body := engineServe(t, e, p, o)
		if body != "v" || !resp.Cache.Hit || resp.Cache.Stale != weir.StaleWhileRevalidate {
			t.Errorf("%s: hit=%v stale=%v, want a stale-while-revalidate hit", p, resp.Cache.Hit, resp.Cache.Stale)
		}
	}
	for _, p := range []string{"/h", "/gm"} {
		if resp, _ := engineServe(t, e, p, o); !resp.Cache.Hit || resp.Cache.Stale != weir.StaleNone {
			t.Errorf("%s: hit=%v stale=%v, want a fresh hit (other group, or a GET answer)", p, resp.Cache.Hit, resp.Cache.Stale)
		}
	}
	waitFor(t, "the refreshes of /a and /b", func() bool { return o.Calls("/a") == 2 && o.Calls("/b") == 2 })
}

// T-29, 05 E-12: after a flood of POSTs to other URIs a group's entries are
// still fresh hits, and a POST to an entry's own URI still revalidates it. A
// sketch collision with the group tag is unlikely at this flood size, so the
// shared-tag rule itself is pinned by the store conformance suite, not here.
// The lookup cost under the flood is logged: every lookup is GET newest plus a script (P25-03b), and a newest
// cache is the upgrade if this number turns out to matter.
func TestEngineInvalidationFlood(t *testing.T) {
	t.Parallel()
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(grouped(`"g"`, nil))
	e := engineWith(t, weir.Config{})

	engineServe(t, e, "/a", o)
	engineServe(t, e, "/b", o)
	time.Sleep(time.Second) // entries must be older than the epoch second

	lookups := func() time.Duration {
		const n = 200
		start := time.Now()
		for range n {
			if resp, _ := engineServe(t, e, "/b", o); !resp.Cache.Hit || resp.Cache.Stale != weir.StaleNone {
				t.Fatalf("/b: hit=%v stale=%v, want a fresh hit", resp.Cache.Hit, resp.Cache.Stale)
			}
		}
		return time.Since(start) / n
	}
	before := lookups()
	for i := range 300 {
		enginePost(t, e, "/flood"+strconv.Itoa(i), o)
	}
	time.Sleep(2100 * time.Millisecond)
	after := lookups()
	t.Logf("hit path per request: %v before the flood, %v after 300 invalidations", before, after)

	// The group tag is shared, so /b stays fresh. The URI tag of /a is plain.
	enginePost(t, e, "/a", o)
	time.Sleep(2100 * time.Millisecond)
	if resp, _ := engineServe(t, e, "/a", o); resp.Cache.Hit {
		t.Errorf("/a was a hit after a POST to it (%s), want a validation", resp.Header.Get("Cache-Status"))
	}
}

// FR-PRG-5, T6.13: a global soft purge makes every entry stale; inside the
// window it is served stale, past the window measured from the purge it is
// revalidated, and every entry revalidates exactly once.
func TestEngineGlobalEpochSoft(t *testing.T) {
	t.Parallel()
	const n = 10
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(cc(200, "max-age=60, stale-while-revalidate=5", "v"))
	e := engineWith(t, weir.Config{})

	path := func(i int) string { return "/k" + strconv.Itoa(i) }
	for i := range n {
		engineServe(t, e, path(i), o)
	}
	time.Sleep(time.Second) // entries must be older than the epoch second
	if err := e.Purge(t.Context(), weir.Purge{All: true}); err != nil {
		t.Fatal(err)
	}
	// The epoch is ceil(server now), and a response fetched before it is purged
	// again (E-7), so the refreshes below must start after it. The 5 s window
	// is measured from the epoch and is still open then.
	time.Sleep(2100 * time.Millisecond)

	for i := range n / 2 {
		if resp, _ := engineServe(t, e, path(i), o); resp.Cache.Stale != weir.StaleWhileRevalidate {
			t.Fatalf("%s: stale=%v, want stale-while-revalidate", path(i), resp.Cache.Stale)
		}
	}
	for i := range n / 2 {
		waitFor(t, "the refresh of "+path(i), func() bool { return o.Calls(path(i)) == 2 })
	}
	time.Sleep(4500 * time.Millisecond) // 6.6 s after the purge: past the window even if the epoch was a second later
	for i := n / 2; i < n; i++ {
		if resp, _ := engineServe(t, e, path(i), o); resp.Cache.Stale != weir.StaleNone {
			t.Fatalf("%s: served stale (%v) past the window", path(i), resp.Cache.Stale)
		}
	}
	for i := range n {
		resp, _ := engineServe(t, e, path(i), o)
		if !resp.Cache.Hit || resp.Cache.Stale != weir.StaleNone || o.Calls(path(i)) != 2 {
			t.Fatalf("%s: hit=%v stale=%v calls=%d, want a fresh hit after one revalidation", path(i), resp.Cache.Hit, resp.Cache.Stale, o.Calls(path(i)))
		}
	}
}

// FR-PRG-3, 04 §7: a soft purge after a hard purge does not bring the entry
// back for stale serving or conditional revalidation.
func TestEngineSoftAfterHardStaysHard(t *testing.T) {
	t.Parallel()
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(purgeable("v"))
	e := engineWith(t, weir.Config{})

	engineServe(t, e, "/a", o)
	engineServe(t, e, "/down", o)
	time.Sleep(time.Second) // entries must be older than the epoch second
	urls := []string{"https://example.com/a", "https://example.com/down"}
	if err := e.Purge(t.Context(), weir.Purge{Mode: weir.PurgeHard, URLs: urls}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)
	for _, p := range []weir.Purge{{URLs: urls}, {All: true}} {
		if err := e.Purge(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(2100 * time.Millisecond)

	resp, _ := engineServe(t, e, "/a", o)
	if resp.Cache.Hit || resp.Cache.Stale != weir.StaleNone {
		t.Fatalf("hit=%v stale=%v, want a miss", resp.Cache.Hit, resp.Cache.Stale)
	}
	for _, r := range o.Requests() {
		if r.Path == "/a" && r.Header.Get("If-None-Match") != "" {
			t.Errorf("hard-purged entry was revalidated with If-None-Match %q", r.Header.Get("If-None-Match"))
		}
	}
	o.SetDown(true)
	if _, body, err := engineServeErr(t, e, "/down", o); err == nil {
		t.Fatalf("origin down: served %q, want an error", body)
	}
}

// FR-PRG-7, T-10: a fetch sent before an invalidation and stored after it does
// not survive the invalidation, because the store orders the epoch against the
// entry on the server clock.
func TestEnginePurgeDuringInflightFetch(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	var gets atomic.Int32
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
		if r.Method == http.MethodPost {
			return &weir.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
		}
		if gets.Add(1) == 1 {
			<-gate
		}
		return &weir.Response{StatusCode: http.StatusOK, Header: http.Header{"Cache-Control": {"max-age=60"}},
			Body: io.NopCloser(strings.NewReader("old"))}, nil
	}})
	e := engineWith(t, weir.Config{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		engineServe(t, e, "/a", o)
	}()
	waitFor(t, "the GET at the origin", func() bool { return gets.Load() == 1 })
	time.Sleep(time.Second)
	enginePost(t, e, "/a", o)
	time.Sleep(time.Second)
	close(gate)
	<-done
	time.Sleep(2100 * time.Millisecond)

	if resp, _ := engineServe(t, e, "/a", o); resp.Cache.Hit {
		t.Fatalf("pre-invalidation fetch served as a hit: %s", resp.Header.Get("Cache-Status"))
	}
	if n := gets.Load(); n != 2 {
		t.Fatalf("origin GETs = %d, want 2", n)
	}
}

func varyReqLang(path, lang string) *weir.Request {
	r := engineReq(http.MethodGet, path)
	r.Header.Set("Accept-Language", lang)
	return r
}

// FR-COA-5, FR-KEY-7, T-15: followers of a flight whose response varies on a
// field they differ in re-enter and coalesce on their variant key, and the
// vary spec and variants round-trip through the codec: 90 requests over 3
// languages cost 1 + 2 origin calls, then every language is a hit with its
// own body.
func TestEngineVaryFollowersRecoalesce(t *testing.T) {
	t.Parallel()
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(testorigin.Behavior{Delay: 300 * time.Millisecond, Func: func(r *weir.Request) (*weir.Response, error) {
		return &weir.Response{StatusCode: http.StatusOK,
			Header: http.Header{"Cache-Control": {"max-age=60"}, "Vary": {"Accept-Language"}},
			Body:   io.NopCloser(strings.NewReader(r.Header.Get("Accept-Language")))}, nil
	}})
	cfg := weir.Config{}
	cfg.Forward.Allow = []string{"Accept-Language"}
	e := engineWith(t, cfg)

	langs := []string{"en", "fr", "de"}
	var wg sync.WaitGroup
	var bad atomic.Int32
	for i := range 90 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := e.Serve(context.Background(), varyReqLang("/l", langs[i%3]), o)
			if err != nil {
				bad.Add(1)
				return
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(b) != langs[i%3] {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("%d of 90 requests got the wrong body or an error", bad.Load())
	}
	if n := o.Calls("/l"); n != 3 {
		t.Fatalf("origin calls = %d, want 1 + 2", n)
	}
	for _, l := range langs {
		resp, err := e.Serve(t.Context(), varyReqLang("/l", l), o)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !resp.Cache.Hit || string(b) != l {
			t.Errorf("%s: hit=%v body=%q, want its own variant from the server", l, resp.Cache.Hit, b)
		}
	}
	if n := o.Calls("/l"); n != 3 {
		t.Errorf("origin calls after the hits = %d, want 3", n)
	}
}

// FR-STL-4, 01 §7.2, T6.6: a must-revalidate entry that cannot be validated is
// ErrMustRevalidate (504), even with stale-if-error; an origin 5xx answer is
// passed through instead.
func TestEngineMustRevalidate504(t *testing.T) {
	for _, tc := range []struct{ name, cc string }{
		{"must-revalidate", "max-age=1, must-revalidate"},
		{"with stale-if-error", "max-age=1, must-revalidate, stale-if-error=60"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := testorigin.NewChecked(t, 64, 16)
			o.Default(cc(200, tc.cc, "a"))
			e := engineWith(t, weir.Config{})

			engineServe(t, e, "/a", o)
			time.Sleep(2100 * time.Millisecond) // past max-age=1 on the server clock
			o.SetDown(true)
			_, _, err := engineServeErr(t, e, "/a", o)
			if !errors.Is(err, weir.ErrMustRevalidate) || weir.StatusCode(err) != http.StatusGatewayTimeout {
				t.Fatalf("origin down: err = %v, want ErrMustRevalidate (504)", err)
			}
			o.SetDown(false)
			o.Default(cc(http.StatusServiceUnavailable, "", "busy"))
			resp, body := engineServe(t, e, "/a", o)
			if resp.StatusCode != http.StatusServiceUnavailable || body != "busy" {
				t.Fatalf("origin 503: got %d %q, want the 503 passed through", resp.StatusCode, body)
			}
		})
	}
}

type served struct {
	body string
	err  error
}

func serveAsync(ctx context.Context, e *weir.Engine, req *weir.Request, o weir.Origin) <-chan served {
	ch := make(chan served, 1)
	go func() {
		resp, err := e.Serve(ctx, req, o)
		if err != nil {
			ch <- served{err: err}
			return
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		ch <- served{body: string(b), err: err}
	}()
	return ch
}

// FR-COA-2, FR-COA-9: canceling the creator does not cancel the shared fetch,
// the fetch still carries the creator's context values, and the followers
// read the stored entry from the server path as usual.
func TestEngineCoalesceCreatorCancel(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	o := testorigin.NewChecked(t, 64, 16)
	b := cc(200, "max-age=60", "shared")
	b.Gate = gate
	o.Default(b)
	type ctxKey struct{}
	var seen atomic.Value
	via := weir.OriginFunc(func(ctx context.Context, req *weir.Request) (*weir.Response, error) {
		seen.Store(ctx.Value(ctxKey{}))
		return o.Fetch(ctx, req)
	})
	e := engineWith(t, weir.Config{})

	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), ctxKey{}, "creator"))
	creator := serveAsync(ctx, e, engineReq(http.MethodGet, "/a"), via)
	waitFor(t, "the creator at the origin", func() bool { return seen.Load() != nil })
	followers := make([]<-chan served, 5)
	for i := range followers {
		followers[i] = serveAsync(t.Context(), e, engineReq(http.MethodGet, "/a"), via)
	}
	time.Sleep(300 * time.Millisecond) // the followers each do a store lookup before they join the flight
	cancel()
	if s := <-creator; !errors.Is(s.err, context.Canceled) {
		t.Fatalf("creator: %v, want context.Canceled", s.err)
	}
	close(gate)
	for _, ch := range followers {
		if s := <-ch; s.err != nil || s.body != "shared" {
			t.Fatalf("follower: %v, %q", s.err, s.body)
		}
	}
	if n := o.TotalCalls(); n != 1 {
		t.Fatalf("origin calls = %d, want 1", n)
	}
	if v := seen.Load(); v != "creator" {
		t.Fatalf("origin saw context value %v, want the creator's", v)
	}
}

func warmReqs(paths ...string) iter.Seq[*weir.Request] {
	return func(yield func(*weir.Request) bool) {
		for _, p := range paths {
			if !yield(engineReq(http.MethodGet, p)) {
				return
			}
		}
	}
}

// FR-WRM-1, FR-WRM-2, T6.4: Warm fetches at most Warm.Concurrency at once,
// stores what is storable in the server, counts each outcome, and skips the
// entries the server already holds fresh on a second call.
func TestEngineWarm(t *testing.T) {
	t.Parallel()
	o := testorigin.NewChecked(t, 64, 16)
	b := cc(200, "max-age=60", "v")
	b.Delay = 20 * time.Millisecond
	o.Default(b)
	o.Route("/no-store", testorigin.Behavior{Header: http.Header{"Cache-Control": {"no-store"}}})
	o.Route("/down", testorigin.Behavior{Err: errors.New("refused")})
	cfg := weir.Config{}
	cfg.Warm.Concurrency = 4
	e := engineWith(t, cfg)

	var paths []string
	for i := range 40 {
		paths = append(paths, fmt.Sprintf("/p%d", i))
	}
	st, err := e.Warm(t.Context(), warmReqs(append(append([]string(nil), paths...), "/no-store", "/down")...), o)
	if err != nil {
		t.Fatalf("Warm: %v", err)
	}
	if want := (weir.WarmStats{Fetched: 40, NotStored: 1, Failed: 1}); st != want {
		t.Fatalf("stats = %+v, want %+v", st, want)
	}
	if n := o.MaxInflight(); n < 2 || n > 4 {
		t.Fatalf("origin max in-flight = %d, want 2 to 4 (Warm.Concurrency is 4)", n)
	}
	for _, p := range paths {
		if resp, body := engineServe(t, e, p, o); !resp.Cache.Hit || body != "v" {
			t.Fatalf("%s: hit=%v body=%q, want the warmed entry", p, resp.Cache.Hit, body)
		}
	}
	calls := o.TotalCalls()
	st, err = e.Warm(t.Context(), warmReqs(paths...), o)
	if err != nil || st != (weir.WarmStats{Skipped: 40}) || o.TotalCalls() != calls {
		t.Fatalf("second Warm = %+v, %v with %d new origin calls; want 40 skipped, none sent", st, err, o.TotalCalls()-calls)
	}
}
