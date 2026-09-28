package weir_test

import (
	"context"
	"errors"
	"io"
	"net/http"
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

// cacheCfg disables jitter so lifetimes and ttl values are exact.
var cacheCfg = weir.Config{Freshness: weir.FreshnessConfig{NoJitter: true}}

// serve runs one request and returns the response with its body read.
func serve(t *testing.T, e *weir.Engine, req *weir.Request, o weir.Origin) (*weir.Response, string) {
	t.Helper()
	resp, err := e.Serve(t.Context(), req, o)
	if err != nil {
		t.Fatalf("Serve %s %s: %v", req.Method, req.Path, err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(b)
}

func cacheable(body string) testorigin.Behavior {
	return testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: []byte(body)}
}

// FR-SRV-1: a fresh entry is served without contacting the origin.
func TestFreshHitNoOrigin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(cacheable("hello"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(59 * time.Second)
		resp, body := serve(t, e, getReq("/a"), o)
		if body != "hello" || resp.StatusCode != http.StatusOK || !resp.Cache.Hit {
			t.Fatalf("got %d %q hit=%v", resp.StatusCode, body, resp.Cache.Hit)
		}
		if n := o.Calls("/a"); n != 1 {
			t.Fatalf("origin calls = %d, want 1", n)
		}
	})
}

// FR-SRV-1, FR-SRV-9, FR-FRS-7: a storable miss is stored and the next
// request hits, each with its Cache-Status member; a stale entry goes
// forward again.
func TestMissStoresThenHits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		v1 := cacheable("v1")
		v1.Header.Set("ETag", `"1"`) // a validator keeps the entry past its lifetime
		o.Default(v1)
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		resp, body := serve(t, e, getReq("/a"), o)
		if got, want := resp.Header.Get("Cache-Status"), "Weir; fwd=uri-miss; fwd-status=200; stored"; got != want || body != "v1" {
			t.Fatalf("miss: Cache-Status %q body %q, want %q", got, body, want)
		}
		if resp.Cache.Hit || !resp.Cache.Stored || resp.Cache.Fwd != weir.FwdURIMiss || resp.Cache.FwdStatus != 200 {
			t.Fatalf("miss CacheInfo %+v", resp.Cache)
		}

		time.Sleep(10 * time.Second)
		resp, body = serve(t, e, getReq("/a"), o)
		if got, want := resp.Header.Get("Cache-Status"), "Weir; hit; ttl=50"; got != want || body != "v1" {
			t.Fatalf("hit: Cache-Status %q body %q, want %q", got, body, want)
		}

		o.Default(cacheable("v2"))
		time.Sleep(time.Minute)
		resp, body = serve(t, e, getReq("/a"), o)
		if got, want := resp.Header.Get("Cache-Status"), "Weir; fwd=stale; fwd-status=200; stored"; got != want || body != "v2" {
			t.Fatalf("stale: Cache-Status %q body %q, want %q", got, body, want)
		}
		if n := o.Calls("/a"); n != 2 {
			t.Fatalf("origin calls = %d, want 2", n)
		}
	})
}

// FR-FRS-7, FR-FRS-4: a response served from an entry carries its current
// age in whole seconds, including the Age the origin sent.
func TestAgeHeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Age": {"7"}}, Delay: 2 * time.Second})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		resp, _ := serve(t, e, getReq("/a"), o)
		if got := resp.Header.Get("Age"); got != "7" {
			t.Fatalf("miss Age = %q, want the origin's 7", got)
		}
		time.Sleep(3*time.Second + 500*time.Millisecond)
		resp, _ = serve(t, e, getReq("/a"), o)
		// 7 (Age) + 2 (response delay) + 3.5 (resident), floored.
		if got := resp.Header.Values("Age"); len(got) != 1 || got[0] != "12" {
			t.Fatalf("hit Age = %q, want [12]", got)
		}
	})
}

// FR-SRV-9, T-27: no Cache-Status member carries the key parameter, and an
// origin's own Cache-Status members stay in front of ours.
func TestCacheStatusNoKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Route("/stored", cacheable("x"))
		o.Route("/private", testorigin.Behavior{Header: http.Header{"Cache-Control": {"private"}}})
		o.Route("/upstream", testorigin.Behavior{Header: http.Header{"Cache-Status": {"CDN; hit"}, "Cache-Control": {"max-age=5"}}})
		o.Route("/error", testorigin.Behavior{Status: 503})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		var all []string
		for _, p := range []string{"/stored", "/stored", "/private", "/private", "/upstream", "/upstream", "/error"} {
			resp, _ := serve(t, e, getReq(p), o)
			cs := resp.Header.Values("Cache-Status")
			if len(cs) == 0 {
				t.Fatalf("%s: no Cache-Status", p)
			}
			all = append(all, cs...)
		}
		for _, v := range all {
			if strings.Contains(v, "key=") {
				t.Fatalf("Cache-Status %q carries key", v)
			}
		}
		resp, _ := serve(t, e, getReq("/upstream"), o)
		if got := resp.Header.Values("Cache-Status"); len(got) != 2 || got[0] != "CDN; hit" || got[1] != "Weir; hit; ttl=5" {
			t.Fatalf("upstream Cache-Status = %q", got)
		}
		resp, _ = serve(t, e, getReq("/private"), o)
		if got, want := resp.Header.Get("Cache-Status"), "Weir; fwd=uri-miss; fwd-status=200; detail=hit-for-miss"; got != want {
			t.Fatalf("marker Cache-Status %q, want %q", got, want)
		}
		off := newEngine(t, weir.Config{NoCacheStatus: true})
		defer closeEngine(t, off)
		if resp, _ := serve(t, off, getReq("/stored"), o); len(resp.Header["Cache-Status"]) != 0 {
			t.Fatalf("NoCacheStatus: got %q", resp.Header["Cache-Status"])
		}
	})
}

// P4, 04 §6.10: every way a caller can change a served response's headers
// leaves the stored entry, and so the next hit, unchanged.
func TestServedHeaderMutationDoesNotLeak(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "X-A": {"1"}, "X-B": {"2"}, "X-C": {"3", "4"}}})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		for range 2 { // the miss response, then a hit
			resp, _ := serve(t, e, getReq("/a"), o)
			h := resp.Header
			h.Add("X-A", "evil")
			h.Set("X-B", "evil")
			h.Del("X-C")
			h.Add("X-New", "evil")
			_ = append(h.Values("Cache-Control"), "evil")
			h["Age"] = append(h["Age"], "evil")
		}
		resp, _ := serve(t, e, getReq("/a"), o)
		h := resp.Header
		if h.Get("X-A") != "1" || len(h["X-A"]) != 1 || h.Get("X-B") != "2" || len(h["X-C"]) != 2 ||
			h.Get("X-New") != "" || h.Get("Cache-Control") != "max-age=60" || len(h["Age"]) != 1 {
			t.Fatalf("mutation leaked into the stored entry: %v", h)
		}
	})
}

// countingStore counts every store call.
type countingStore struct {
	store.Store
	calls atomic.Int64
}

func (s *countingStore) Get(ctx context.Context, k store.Key) (*store.Entry, error) {
	s.calls.Add(1)
	return s.Store.Get(ctx, k)
}

func (s *countingStore) Set(ctx context.Context, k store.Key, e *store.Entry) error {
	s.calls.Add(1)
	return s.Store.Set(ctx, k, e)
}

func (s *countingStore) NewestEpoch(ctx context.Context, tags []store.Tag, since time.Time) (store.Epoch, bool, error) {
	s.calls.Add(1)
	return s.Store.NewestEpoch(ctx, tags, since)
}

// FR-VAL-1: a rejected request costs no store or origin call.
func TestInvalidRequestsCostNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, err := memory.New(memory.Config{})
		if err != nil {
			t.Fatal(err)
		}
		st := &countingStore{Store: m}
		defer m.Close()
		o := testorigin.New()
		e := newEngine(t, weir.Config{Store: st})
		defer closeEngine(t, e)

		bad := map[string]func(r *weir.Request){
			"bad scheme":     func(r *weir.Request) { r.Scheme = "ftp" },
			"empty host":     func(r *weir.Request) { r.Host = "" },
			"host with path": func(r *weir.Request) { r.Host = "a.example/x" },
			"relative path":  func(r *weir.Request) { r.Path = "a" },
			"control byte":   func(r *weir.Request) { r.Path = "/a\x01" },
			"long path":      func(r *weir.Request) { r.Path = "/" + strings.Repeat("a", 9000) },
			"long query":     func(r *weir.Request) { r.RawQuery = strings.Repeat("a", 9000) },
			"many params":    func(r *weir.Request) { r.RawQuery = strings.Repeat("a=1&", 300) },
			"bad escape":     func(r *weir.Request) { r.Path = "/a%zz" },
			"query space":    func(r *weir.Request) { r.RawQuery = "a=b c" },
			"method token":   func(r *weir.Request) { r.Method = "GE T" },
			"long host":      func(r *weir.Request) { r.Host = strings.Repeat("a", 256) },
			"port zero":      func(r *weir.Request) { r.Host = "a.example:0" },
			"port too big":   func(r *weir.Request) { r.Host = "a.example:65536" },
		}
		for name, mutate := range bad {
			r := getReq("/a")
			mutate(r)
			resp, err := e.Serve(t.Context(), r, o)
			if resp != nil || !errors.Is(err, weir.ErrInvalidRequest) || weir.StatusCode(err) != http.StatusBadRequest {
				t.Fatalf("%s: got %v, %v; want a 400 error", name, resp, err)
			}
		}
		if n := st.calls.Load(); n != 0 {
			t.Fatalf("store calls = %d, want 0", n)
		}
		if n := o.TotalCalls(); n != 0 {
			t.Fatalf("origin calls = %d, want 0", n)
		}
	})
}

// T-1, INV-1, FR-FWD-1: an unkeyed User-Agent never reaches the origin, so
// a response varying on it without Vary cannot poison the key.
func TestKettleUserAgent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			body := "ua=" + r.Header.Get("User-Agent")
			return &weir.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"max-age=60"}},
				Body: io.NopCloser(strings.NewReader(body))}, nil
		}})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		evil := getReq("/a")
		evil.Header.Set("User-Agent", "evil")
		serve(t, e, evil, o)
		normal := getReq("/a")
		normal.Header.Set("User-Agent", "Mozilla/5.0")
		if _, body := serve(t, e, normal, o); body != "ua=" {
			t.Fatalf("normal client got %q, want the no-User-Agent response", body)
		}
		for _, r := range o.Requests() {
			if len(r.Header["User-Agent"]) != 0 {
				t.Fatalf("origin saw User-Agent %q", r.Header["User-Agent"])
			}
		}
	})
}

// T-5, FR-FWD-1: the body of a GET never reaches the origin.
func TestFatGETBodyDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(cacheable("x"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		r := getReq("/a")
		r.Body = io.NopCloser(strings.NewReader("poison"))
		r.Header.Set("Content-Length", "6")
		serve(t, e, r, o)
		reqs := o.Requests()
		if len(reqs) != 1 || reqs[0].Body != nil || len(reqs[0].Header["Content-Length"]) != 0 {
			t.Fatalf("forwarded %+v", reqs)
		}
	})
}

// FR-STO-9, 04 §6.7: a body over MaxObjectBytes streams to the client
// whole and is not stored; the Origin's header map is left untouched.
func TestOversizedBodyStreamed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		big := strings.Repeat("x", 3000)
		shared := http.Header{"Cache-Control": {"max-age=60"}}
		origin := weir.OriginFunc(func(context.Context, *weir.Request) (*weir.Response, error) {
			return &weir.Response{StatusCode: 200, Header: shared, Body: io.NopCloser(strings.NewReader(big))}, nil
		})
		e := newEngine(t, weir.Config{Storable: weir.StorableConfig{MaxObjectBytes: 1024}})
		defer closeEngine(t, e)
		for range 2 {
			resp, body := serve(t, e, getReq("/a"), origin)
			if body != big || resp.Cache.Hit || resp.Cache.Stored {
				t.Fatalf("got %d bytes, CacheInfo %+v", len(body), resp.Cache)
			}
		}
		if len(shared) != 1 {
			t.Fatalf("Origin header map written: %v", shared)
		}
		head := getReq("/a")
		head.Method = http.MethodHead
		if resp, body := serve(t, e, head, origin); body != "" || resp.Body != http.NoBody {
			t.Fatalf("HEAD got body %q", body)
		}
	})
}

// FR-TMO-1, NFR-2: a cacheable body that times out or ends early is neither
// served nor stored.
func TestBufferedBodyFailures(t *testing.T) {
	for name, b := range map[string]testorigin.Behavior{
		"timeout":   {Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: []byte("x"), BodyDelay: time.Hour},
		"truncated": {Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: []byte("hello"), Truncate: 2},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.New()
				o.Default(b)
				e := newEngine(t, weir.Config{Timeouts: weir.TimeoutsConfig{Origin: time.Second}})
				defer closeEngine(t, e)
				want := weir.ErrOrigin
				if name == "timeout" {
					want = weir.ErrOriginTimeout
				}
				for range 2 {
					if resp, err := e.Serve(t.Context(), getReq("/a"), o); resp != nil || !errors.Is(err, want) {
						t.Fatalf("got %v, %v; want %v", resp, err, want)
					}
				}
				if n := o.Calls("/a"); n != 2 {
					t.Fatalf("origin calls = %d, want 2 (nothing stored)", n)
				}
			})
		})
	}
}

// FR-PRG-3, FR-STO-12, 04 §6.2: a hard-purged entry behaves exactly like a
// miss, marker included.
func TestHardPurgedEntryIsMiss(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, err := memory.New(memory.Config{})
		if err != nil {
			t.Fatal(err)
		}
		defer m.Close()
		o := testorigin.New()
		o.Default(cacheable("v1"))
		e := newEngine(t, weir.Config{Store: m, Freshness: weir.FreshnessConfig{NoJitter: true}})
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(time.Second)
		if err := m.SetEpoch(t.Context(), store.TagGlobal(), store.Epoch{At: time.Now(), Mode: store.EpochHard}); err != nil {
			t.Fatal(err)
		}
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"private"}}})
		resp, _ := serve(t, e, getReq("/a"), o)
		if got, want := resp.Header.Get("Cache-Status"), "Weir; fwd=uri-miss; fwd-status=200"; got != want {
			t.Fatalf("Cache-Status %q, want %q", got, want)
		}
		// FR-STO-12: the unusable response does not block the marker.
		resp, _ = serve(t, e, getReq("/a"), o)
		if got, want := resp.Header.Get("Cache-Status"), "Weir; fwd=uri-miss; fwd-status=200; detail=hit-for-miss"; got != want {
			t.Fatalf("Cache-Status %q, want %q", got, want)
		}
	})
}

// withHeader returns req with one more request field.
func withHeader(req *weir.Request, name, value string) *weir.Request {
	req.Header.Set(name, value)
	return req
}

func headReq(path string) *weir.Request {
	r := getReq(path)
	r.Method = http.MethodHead
	return r
}

// FR-SRV-4, FR-FWD-4: a HEAD miss is fetched as GET and stored, the client
// gets headers only, and a later HEAD or GET is answered from that entry.
func TestHeadFromGetEntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(cacheable("hello"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		resp, body := serve(t, e, headReq("/a"), o)
		if body != "" || resp.StatusCode != http.StatusOK || !resp.Cache.Stored {
			t.Fatalf("HEAD miss: %d %q stored=%v", resp.StatusCode, body, resp.Cache.Stored)
		}
		if m := o.Requests()[0].Method; m != http.MethodGet {
			t.Fatalf("HEAD miss forwarded as %s, want GET", m)
		}
		resp, body = serve(t, e, getReq("/a"), o)
		if body != "hello" || !resp.Cache.Hit {
			t.Fatalf("GET after HEAD miss: %q hit=%v", body, resp.Cache.Hit)
		}
		resp, body = serve(t, e, headReq("/a"), o)
		if body != "" || !resp.Cache.Hit || resp.Header.Get("Cache-Control") != "max-age=60" {
			t.Fatalf("HEAD hit: %q hit=%v header %v", body, resp.Cache.Hit, resp.Header)
		}
		if n := o.Calls("/a"); n != 1 {
			t.Fatalf("origin calls = %d, want 1", n)
		}
	})
}

// FR-SRV-5, T-7: a Range request with no usable entry passes through with
// its Range field; the origin's error is neither stored nor turned into a
// marker, and a Range request on a fresh entry gets the full 200.
func TestRangeGarbageNotPoisoning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			if r.Header.Get("Range") != "" { // an origin that chokes on bad ranges
				return respond(http.StatusBadRequest, http.Header{"Cache-Control": {"max-age=60"}}, "bad range"), nil
			}
			// The ETag keeps the entry stored once stale.
			return respond(http.StatusOK, http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"v1"`}}, "full"), nil
		}})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		resp, body := serve(t, e, withHeader(getReq("/a"), "Range", "bytes=cow"), o)
		if resp.StatusCode != http.StatusBadRequest || body != "bad range" || resp.Cache.Stored {
			t.Fatalf("range miss: %d %q stored=%v", resp.StatusCode, body, resp.Cache.Stored)
		}
		if got := o.Requests()[0].Header.Get("Range"); got != "bytes=cow" {
			t.Fatalf("forwarded Range = %q, want bytes=cow", got)
		}
		resp, body = serve(t, e, getReq("/a"), o)
		if body != "full" || resp.Cache.Hit || !resp.Cache.Stored || resp.Cache.Detail != "" {
			t.Fatalf("plain request after range: %q %+v, want a normal miss", body, resp.Cache)
		}
		resp, body = serve(t, e, withHeader(getReq("/a"), "Range", "bytes=0-1"), o)
		if resp.StatusCode != http.StatusOK || body != "full" || !resp.Cache.Hit {
			t.Fatalf("range on fresh entry: %d %q hit=%v", resp.StatusCode, body, resp.Cache.Hit)
		}
		if n := o.Calls("/a"); n != 2 {
			t.Fatalf("origin calls = %d, want 2", n)
		}

		// FR-FWD-4: a HEAD Range miss goes forward as GET; the client gets
		// no body.
		_, body = serve(t, e, withHeader(headReq("/b"), "Range", "bytes=0-1"), o)
		if r := o.Requests()[2]; r.Method != http.MethodGet || r.Header.Get("Range") != "bytes=0-1" || body != "" {
			t.Fatalf("HEAD range miss forwarded as %s Range=%q, body %q", r.Method, r.Header.Get("Range"), body)
		}

		// T-37: a stale entry does not turn a Range request into a stored
		// full fetch; the pass-through carries If-Range too (RFC 9110
		// §13.1.5), and the next plain fetch carries neither (T-7).
		time.Sleep(61 * time.Second)
		rr := withHeader(withHeader(getReq("/a"), "Range", "bytes=0-1"), "If-Range", `"v1"`)
		resp, _ = serve(t, e, rr, o)
		r := o.Requests()[3]
		if r.Header.Get("Range") != "bytes=0-1" || r.Header.Get("If-Range") != `"v1"` || resp.Cache.Stored || resp.Cache.Fwd != weir.FwdStale {
			t.Fatalf("range on stale entry: forwarded %v, CacheInfo %+v", r.Header, resp.Cache)
		}
		serve(t, e, withHeader(getReq("/a"), "If-Range", `"v1"`), o)
		if r := o.Requests()[4]; r.Header.Get("Range") != "" || r.Header.Get("If-Range") != "" {
			t.Fatalf("cacheable fetch carried %v", r.Header)
		}
	})
}

// FR-SRV-6: only-if-cached is answered from a usable entry or fails with
// ErrOnlyIfCached (504) without contacting the origin.
func TestOnlyIfCached(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(cacheable("hello"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		oic := func() *weir.Request { return withHeader(getReq("/a"), "Cache-Control", "only-if-cached") }

		_, err := e.Serve(t.Context(), oic(), o)
		if !errors.Is(err, weir.ErrOnlyIfCached) || weir.StatusCode(err) != http.StatusGatewayTimeout {
			t.Fatalf("miss: err %v, want ErrOnlyIfCached with 504", err)
		}
		if n := o.Calls("/a"); n != 0 {
			t.Fatalf("origin calls on miss = %d, want 0", n)
		}
		serve(t, e, getReq("/a"), o)
		resp, body := serve(t, e, oic(), o)
		if body != "hello" || !resp.Cache.Hit {
			t.Fatalf("fresh: %q hit=%v", body, resp.Cache.Hit)
		}
		time.Sleep(61 * time.Second)
		if _, err := e.Serve(t.Context(), oic(), o); !errors.Is(err, weir.ErrOnlyIfCached) {
			t.Fatalf("stale: err %v, want ErrOnlyIfCached", err)
		}
		if n := o.Calls("/a"); n != 1 {
			t.Fatalf("origin calls = %d, want 1", n)
		}
	})
}

// FR-SRV-6, FR-SRV-8: with HonorRevalidation, only-if-cached still gets a
// fresh entry even when no-cache would otherwise force validation.
func TestOnlyIfCachedBeatsNoCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(cacheable("hello"))
		cfg := cacheCfg
		cfg.Client.HonorRevalidation = true
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		resp, body := serve(t, e, withHeader(getReq("/a"), "Cache-Control", "only-if-cached, no-cache"), o)
		if body != "hello" || !resp.Cache.Hit || o.Calls("/a") != 1 {
			t.Fatalf("%q hit=%v calls=%d, want the fresh entry", body, resp.Cache.Hit, o.Calls("/a"))
		}
	})
}

// FR-SRV-7: request no-store is honored under the default config: the
// response is served but not stored, and no marker is left behind (T-31).
func TestRequestNoStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(cacheable("hello"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		resp, body := serve(t, e, withHeader(getReq("/a"), "Cache-Control", "no-store"), o)
		if body != "hello" || resp.Cache.Stored {
			t.Fatalf("no-store: %q stored=%v", body, resp.Cache.Stored)
		}
		resp, _ = serve(t, e, getReq("/a"), o)
		if resp.Cache.Hit || !resp.Cache.Stored || resp.Cache.Detail != "" {
			t.Fatalf("next request %+v, want a normal miss that stores", resp.Cache)
		}
	})
}

// FR-SRV-8, D5, T-14: client revalidation directives do not change lookup
// by default; with HonorRevalidation they force validation of a fresh entry.
func TestClientNoCacheIgnored(t *testing.T) {
	directives := []struct{ name, value string }{
		{"Cache-Control", "no-cache"},
		{"Pragma", "no-cache"},
		{"Cache-Control", "max-age=0"},
		{"Cache-Control", "min-fresh=3600"},
		{"Cache-Control", "max-stale=0"},
	}
	for _, d := range directives {
		t.Run(d.name+" "+d.value+" is a hit by default", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.New()
				o.Default(cacheable("hello"))
				e := newEngine(t, cacheCfg)
				defer closeEngine(t, e)

				serve(t, e, getReq("/a"), o)
				resp, body := serve(t, e, withHeader(getReq("/a"), d.name, d.value), o)
				if body != "hello" || !resp.Cache.Hit || o.Calls("/a") != 1 {
					t.Fatalf("%q hit=%v calls=%d, want a hit", body, resp.Cache.Hit, o.Calls("/a"))
				}
			})
		})
		if strings.HasPrefix(d.value, "min-fresh") || strings.HasPrefix(d.value, "max-stale") {
			continue // FR-SRV-8 names only no-cache and max-age=0 as forcing validation
		}
		t.Run(d.name+" "+d.value+" validates with HonorRevalidation", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.New()
				b := cacheable("hello")
				b.Header.Set("ETag", `"1"`)
				o.Default(b)
				cfg := cacheCfg
				cfg.Client.HonorRevalidation = true
				e := newEngine(t, cfg)
				defer closeEngine(t, e)

				serve(t, e, getReq("/a"), o)
				resp, _ := serve(t, e, withHeader(getReq("/a"), d.name, d.value), o)
				// RFC 9211 §2.2: the request's directives caused the forward.
				if resp.Cache.Hit || resp.Cache.Fwd != weir.FwdRequest || o.Calls("/a") != 2 {
					t.Fatalf("CacheInfo %+v calls=%d, want a validation with fwd=request", resp.Cache, o.Calls("/a"))
				}
				if got := o.Requests()[1].Header.Get("If-None-Match"); got != `"1"` {
					t.Fatalf("If-None-Match = %q, want the stored ETag", got)
				}
			})
		})
	}
}

// FR-FRS-4, T-30: an origin Date 10 minutes behind does not age the
// response on arrival.
func TestOriginClockSkewDoesNotStale(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(testorigin.Behavior{Header: http.Header{
			"Cache-Control": {"max-age=300"},
			"Date":          {time.Now().Add(-10 * time.Minute).UTC().Format(http.TimeFormat)},
		}, Body: []byte("v")})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(time.Second)
		resp, _ := serve(t, e, getReq("/a"), o)
		if !resp.Cache.Hit {
			t.Fatalf("second request not a hit: %s", resp.Header.Get("Cache-Status"))
		}
	})
}

// RFC 9111 §4, 04 §6.7: a slow fetch whose response is older (by Date)
// than the stored one does not replace it.
func TestNewerResponseWins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		var mu sync.Mutex
		n := 0
		o := testorigin.New()
		o.Default(testorigin.Behavior{Func: func(*weir.Request) (*weir.Response, error) {
			date := time.Now().UTC().Format(http.TimeFormat)
			mu.Lock()
			n++
			first := n == 1
			mu.Unlock()
			body := "new"
			if first {
				<-gate
				body = "old"
			}
			return &weir.Response{StatusCode: http.StatusOK,
				Header: http.Header{"Cache-Control": {"max-age=60"}, "Date": {date}},
				Body:   io.NopCloser(strings.NewReader(body))}, nil
		}})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		done := make(chan struct{})
		go func() {
			defer close(done)
			serve(t, e, getReq("/a"), o)
		}()
		synctest.Wait() // the slow fetch is at the origin with its Date taken
		time.Sleep(2 * time.Second)
		serve(t, e, getReq("/a"), o)
		close(gate)
		<-done

		resp, body := serve(t, e, getReq("/a"), o)
		if !resp.Cache.Hit || body != "new" {
			t.Fatalf("got %q hit=%v, want the newer response", body, resp.Cache.Hit)
		}
	})
}

// FR-PRG-3, 04 §6.7: newest-wins never protects the entry the request
// found unusable. An origin clock that once ran ahead must not pin a
// hard-purged entry until it expires.
func TestNewerResponseWinsSkipsPurgedEntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, err := memory.New(memory.Config{})
		if err != nil {
			t.Fatal(err)
		}
		defer m.Close()
		o := testorigin.New()
		ahead := cacheable("v1")
		ahead.Header.Set("Date", time.Now().Add(5*time.Minute).UTC().Format(http.TimeFormat))
		o.Default(ahead)
		e := newEngine(t, weir.Config{Store: m, Freshness: weir.FreshnessConfig{NoJitter: true}})
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(time.Second)
		if err := m.SetEpoch(t.Context(), store.TagGlobal(), store.Epoch{At: time.Now(), Mode: store.EpochHard}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		o.Default(cacheable("v2"))
		if resp, _ := serve(t, e, getReq("/a"), o); !resp.Cache.Stored {
			t.Fatalf("refetch not stored: %s", resp.Header.Get("Cache-Status"))
		}
		resp, body := serve(t, e, getReq("/a"), o)
		if !resp.Cache.Hit || body != "v2" {
			t.Fatalf("got %q hit=%v, want a v2 hit", body, resp.Cache.Hit)
		}
	})
}
