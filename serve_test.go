package weir_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
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

// FR-PRG-3, 04 §6.2: a hard-purged entry behaves exactly like a miss.
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
		resp, _ := serve(t, e, getReq("/a"), o)
		if got, want := resp.Header.Get("Cache-Status"), "Weir; fwd=uri-miss; fwd-status=200; stored"; got != want {
			t.Fatalf("Cache-Status %q, want %q", got, want)
		}
	})
}
