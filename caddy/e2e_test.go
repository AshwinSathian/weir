package weircaddy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2/caddytest"
)

// These tests run a real Caddy in-process through caddytest, so they use the
// real clock (the synctest bubble cannot hold Caddy's listeners). Waits are
// polls on observable state, and the one fixed pause is named where it is
// needed. caddytest skips them under -short.

// e2eOrigin is an in-process origin behind reverse_proxy. It counts calls and
// tracks the highest number of requests in flight.
type e2eOrigin struct {
	*httptest.Server
	calls       atomic.Int32
	inflight    atomic.Int32
	maxInflight atomic.Int32
	down        atomic.Bool   // true: close the connection without answering
	gate        chan struct{} // when non-nil, GETs wait until it is closed
	mu          sync.Mutex
	handler     http.HandlerFunc
}

func newE2EOrigin(t *testing.T, h http.HandlerFunc) *e2eOrigin {
	t.Helper()
	o := &e2eOrigin{handler: h}
	o.Server = httptest.NewServer(http.HandlerFunc(o.serve))
	t.Cleanup(o.Close)
	return o
}

func (o *e2eOrigin) serve(w http.ResponseWriter, r *http.Request) {
	o.calls.Add(1)
	n := o.inflight.Add(1)
	defer o.inflight.Add(-1)
	for {
		m := o.maxInflight.Load()
		if n <= m || o.maxInflight.CompareAndSwap(m, n) {
			break
		}
	}
	if o.down.Load() {
		// A dropped connection is a gateway failure to reverse_proxy (502).
		if c, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = c.Close()
		}
		return
	}
	o.mu.Lock()
	gate := o.gate
	o.mu.Unlock()
	if gate != nil && r.Method == http.MethodGet {
		<-gate
	}
	o.handler(w, r)
}

func (o *e2eOrigin) setGate(g chan struct{}) {
	o.mu.Lock()
	o.gate = g
	o.mu.Unlock()
}

// cacheableBody answers with a body that names the path and is fresh for a minute.
func cacheableBody(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "max-age=60")
	_, _ = io.WriteString(w, "body "+r.URL.Path)
}

// e2eCaddyfile wraps a site body in the global options caddytest needs: the
// test admin port, plain HTTP on 9080 and no trust-store changes.
func e2eCaddyfile(upstream, site string) string {
	return fmt.Sprintf(`{
	admin localhost:2999
	http_port 9080
	https_port 9443
	grace_period 1ns
	auto_https off
	skip_install_trust
}
http://localhost:9080 {
%s
	reverse_proxy %s
}
`, site, strings.TrimPrefix(upstream, "http://"))
}

func (o *e2eOrigin) caddyfile(site string) string { return e2eCaddyfile(o.URL, site) }

// reply is what a test needs from a response; the body is already closed.
type reply struct {
	code   int
	header http.Header
	body   string
}

// getErr is get without the test failure, safe to call from other goroutines
// (t.Fatal there would only end that goroutine).
func getErr(t *testing.T, c *http.Client, path string, hdr ...string) (reply, error) {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost:9080"+path, nil)
	if err != nil {
		return reply{}, err
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		return reply{}, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return reply{resp.StatusCode, resp.Header, string(b)}, nil
}

func get(t *testing.T, c *http.Client, path string, hdr ...string) reply {
	t.Helper()
	r, err := getErr(t, c, path, hdr...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// 08 §3, OQ-C1 (no FR covers reloads): a reload that changes only the limiter keeps the
// store, so 100 warm keys stay hits. A forward.allow change alters what an
// unchanged key means, so the same keys miss once (hard epoch, R-3). Adding a
// host to a site that is already multi_host changes nothing.
func TestReloadKeepsWarmKeys(t *testing.T) {
	o := newE2EOrigin(t, cacheableBody)
	tester := caddytest.NewTester(t)
	c := tester.Client
	site := func(extra string) string {
		return "\tweir {\n\t\tname e2e-reload\n\t\tmulti_host\n" + extra + "\t}\n"
	}
	const keys = 100
	warm := func() {
		for i := range keys {
			get(t, c, "/k"+strconv.Itoa(i))
		}
	}
	// requests runs all keys again and returns the origin calls they caused.
	requests := func() int32 {
		before := o.calls.Load()
		warm()
		return o.calls.Load() - before
	}

	tester.InitServer(o.caddyfile(site("")), "caddyfile")
	warm()
	if got := o.calls.Load(); got != keys {
		t.Fatalf("warm-up made %d origin calls, want %d", got, keys)
	}
	if n := requests(); n != 0 {
		t.Fatalf("second pass made %d origin calls, want 0", n)
	}

	t.Run("limiter change keeps every key", func(t *testing.T) {
		tester.InitServer(o.caddyfile(site("\t\tlimiter {\n\t\t\tmax_concurrent 64\n\t\t}\n")), "caddyfile")
		if n := requests(); n != 0 {
			t.Fatalf("%d of %d keys missed after a limiter change", n, keys)
		}
	})
	t.Run("another host on a multi_host site changes nothing", func(t *testing.T) {
		cf := strings.Replace(o.caddyfile(site("\t\tlimiter {\n\t\t\tmax_concurrent 64\n\t\t}\n")),
			"http://localhost:9080 {", "http://localhost:9080, http://127.0.0.1:9080 {", 1)
		tester.InitServer(cf, "caddyfile")
		if n := requests(); n != 0 {
			t.Fatalf("%d of %d keys missed after adding a host", n, keys)
		}
	})
	t.Run("forward.allow change makes every key a miss", func(t *testing.T) {
		tester.InitServer(o.caddyfile(site("\t\tforward {\n\t\t\tallow X-Request-Id\n\t\t}\n")), "caddyfile")
		if n := requests(); n != keys {
			t.Fatalf("%d of %d keys missed after a forward.allow change, want all", n, keys)
		}
		if n := requests(); n != 0 {
			t.Fatalf("keys stored after the epoch should hit, got %d misses", n)
		}
	})
}

// FR-COA-1, T6.2: a cold hot key reaches the origin once however many clients
// ask while it is in flight, and every client gets the body. The pause lets
// the followers reach Caddy before the gate opens. A late one becomes a hit,
// so the origin count stays 1 either way; only that count and one collapsed
// response are asserted, because how many followers join depends on load.
func TestE2ECoalesceColdKey(t *testing.T) {
	o := newE2EOrigin(t, cacheableBody)
	gate := make(chan struct{})
	o.setGate(gate)
	tester := caddytest.NewTester(t)
	tester.InitServer(o.caddyfile("\tweir {\n\t\tname e2e-coalesce\n\t}\n"), "caddyfile")

	const clients = 50
	type result struct {
		code      int
		body, cst string
	}
	results := make(chan result, clients)
	for range clients {
		go func() {
			r, err := getErr(t, tester.Client, "/hot")
			if err != nil {
				t.Error(err)
			}
			results <- result{r.code, r.body, r.header.Get("Cache-Status")}
		}()
	}
	eventually(t, "the leader to reach the origin", func() bool { return o.calls.Load() == 1 })
	time.Sleep(300 * time.Millisecond)
	close(gate)

	collapsed := 0
	for range clients {
		r := <-results
		if r.code != http.StatusOK || r.body != "body /hot" {
			t.Fatalf("got %d %q", r.code, r.body)
		}
		if strings.Contains(r.cst, "collapsed") {
			collapsed++
		}
	}
	if n := o.calls.Load(); n != 1 {
		t.Fatalf("%d origin calls for one cold key, want 1", n)
	}
	if collapsed < 1 {
		t.Fatalf("%d of %d responses report collapsed, want at least one", collapsed, clients)
	}
}

// openBreaker sends misses to a failing origin until the breaker answers 503,
// and returns that response. The 20-request floor is the default (FR-CB-1).
func openBreaker(t *testing.T, c *http.Client) reply {
	t.Helper()
	for i := range 200 {
		resp := get(t, c, "/fail/"+strconv.Itoa(i))
		if resp.code == http.StatusServiceUnavailable {
			return resp
		}
		if resp.code != http.StatusBadGateway {
			t.Fatalf("failing origin gave %d, want 502 until the breaker opens", resp.code)
		}
	}
	t.Fatal("breaker never opened")
	return reply{}
}

// FR-STL-2, FR-STL-3, FR-CB-1, T6.6: with the origin down, a stale entry with
// stale-if-error is served, a must-revalidate one answers 504, and the
// breaker opens so later misses stop reaching the origin.
func TestE2EOriginOutage(t *testing.T) {
	o := newE2EOrigin(t, func(w http.ResponseWriter, r *http.Request) {
		cc := "max-age=0, stale-if-error=60"
		if r.URL.Path == "/mr" {
			cc = "max-age=0, must-revalidate"
		}
		w.Header().Set("Cache-Control", cc)
		_, _ = io.WriteString(w, "body "+r.URL.Path)
	})
	tester := caddytest.NewTester(t)
	tester.InitServer(o.caddyfile("\tweir {\n\t\tname e2e-outage\n\t}\n"), "caddyfile")
	c := tester.Client
	for _, p := range []string{"/sie", "/mr"} {
		if resp := get(t, c, p); resp.code != http.StatusOK {
			t.Fatalf("warm %s: %d", p, resp.code)
		}
	}

	o.down.Store(true)
	resp := get(t, c, "/sie")
	if resp.code != http.StatusOK || resp.body != "body /sie" || !strings.Contains(resp.header.Get("Cache-Status"), "detail=stale-if-error") {
		t.Fatalf("stale-if-error: %d %q %q", resp.code, resp.body, resp.header.Get("Cache-Status"))
	}
	if resp := get(t, c, "/mr"); resp.code != http.StatusGatewayTimeout {
		t.Fatalf("must-revalidate with the origin down: %d, want 504", resp.code)
	}

	openBreaker(t, c)
	before := o.calls.Load()
	for i := range 20 {
		if resp := get(t, c, "/after/"+strconv.Itoa(i)); resp.code != http.StatusServiceUnavailable {
			t.Fatalf("open breaker: %d, want 503", resp.code)
		}
	}
	if n := o.calls.Load() - before; n != 0 {
		t.Fatalf("%d origin calls while the breaker was open", n)
	}
	if resp := get(t, c, "/sie"); resp.code != http.StatusOK || resp.body != "body /sie" {
		t.Fatalf("stale entry with the breaker open: %d %q", resp.code, resp.body)
	}
}

// FR-CB-1, D34: Retry-After set before the error is returned survives the
// operator's handle_errors route, which writes its own body.
func TestRetryAfterSurvivesHandleErrors(t *testing.T) {
	o := newE2EOrigin(t, cacheableBody)
	o.down.Store(true)
	tester := caddytest.NewTester(t)
	site := "\tweir {\n\t\tname e2e-retry\n\t}\n" +
		"\thandle_errors {\n\t\theader X-Handled yes\n\t\trespond \"custom {http.error.status_code}\" {http.error.status_code}\n\t}\n"
	tester.InitServer(o.caddyfile(site), "caddyfile")

	resp := openBreaker(t, tester.Client)
	if resp.header.Get("X-Handled") != "yes" {
		t.Fatal("handle_errors did not run")
	}
	ra, err := strconv.Atoi(resp.header.Get("Retry-After"))
	if err != nil || ra < 1 {
		t.Fatalf("Retry-After = %q, want whole seconds >= 1", resp.header.Get("Retry-After"))
	}
	if body := get(t, tester.Client, "/fail/again").body; body != "custom 503" {
		t.Fatalf("handle_errors body = %q", body)
	}
}

// FR-INV-2, FR-PRG-2, FR-STL-1, FR-LIM-4, T6.12: a group invalidation makes 100 cached keys
// stale-but-servable. Every client is answered from the stale entry while the
// origin is gated, and the refreshes the herd triggers stay under the limiter.
func TestE2EPurgeHerd(t *testing.T) {
	const keys, maxConc = 100, 8
	o := newE2EOrigin(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Cache-Group-Invalidation", `"g"`)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.Header().Set("Cache-Control", "max-age=60, stale-while-revalidate=30")
		w.Header().Set("Cache-Groups", `"g"`)
		_, _ = io.WriteString(w, "body "+r.URL.Path)
	})
	tester := caddytest.NewTester(t)
	site := fmt.Sprintf("\tweir {\n\t\tname e2e-herd\n\t\tlimiter {\n\t\t\tmax_concurrent %d\n\t\t}\n\t}\n", maxConc)
	tester.InitServer(o.caddyfile(site), "caddyfile")
	c := tester.Client
	for i := range keys {
		get(t, c, "/k"+strconv.Itoa(i))
	}

	// Epochs have a one-second grain (05 E-7, rounded up): an entry stored in the same second as
	// the invalidation is not older than it.
	time.Sleep(1100 * time.Millisecond)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost:9080/post", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	pr, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = pr.Body.Close()
	if pr.StatusCode != http.StatusCreated {
		t.Fatalf("POST: %d", pr.StatusCode)
	}

	o.setGate(make(chan struct{}))
	var wg sync.WaitGroup
	var stale atomic.Int32
	start := time.Now()
	for i := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := getErr(t, c, "/k"+strconv.Itoa(i))
			if err != nil {
				t.Error(err)
				return
			}
			if resp.code == http.StatusOK && resp.body == "body /k"+strconv.Itoa(i) &&
				strings.Contains(resp.header.Get("Cache-Status"), "detail=stale-while-revalidate") {
				stale.Add(1)
			}
		}()
	}
	wg.Wait() // the origin is still gated, so these were all served stale
	if n := stale.Load(); n != keys {
		t.Fatalf("%d of %d requests served stale while revalidating", n, keys)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("herd took %v with the origin gated", d)
	}

	// Refreshes the herd started hold the gated origin: exactly the
	// MaxConcurrent - ReserveForeground background slots (default reserve is
	// MaxConcurrent/4), the rest are dropped (FR-LIM-4).
	const wantRefreshes = maxConc - maxConc/4
	eventually(t, "background refreshes to fill their slots", func() bool { return o.inflight.Load() == wantRefreshes })
	o.mu.Lock()
	close(o.gate)
	o.gate = nil
	o.mu.Unlock()
	eventually(t, "refreshes to finish", func() bool { return o.inflight.Load() == 0 })
	if m := o.maxInflight.Load(); m != wantRefreshes {
		t.Fatalf("origin saw at most %d concurrent requests, want exactly %d", m, wantRefreshes)
	}
}
