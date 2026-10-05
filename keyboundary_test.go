package weir_test

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

func bodyOf(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }

// noisyReq is a request carrying inputs of every kind the key boundary
// handles: keyed and unkeyed headers, cookies and query parameters.
func noisyReq(path, query string) *weir.Request {
	r := getReq(path)
	r.RawQuery = query
	r.Header = http.Header{
		"Accept": {"text/html"}, "User-Agent": {"evil"}, "X-Forwarded-Host": {"evil.example"},
		"X-Tenant": {" a ,b"}, "X-Debug": {"1"}, "Accept-Encoding": {"br, gzip;q=0.5"},
		"Traceparent": {"00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"},
		"Cookie":      {"lang=en; sid=1", "track=9"}, "If-Match": {`"x"`}, "Content-Length": {"3"},
	}
	return r
}

// INV-1, FR-KEY-2, FR-KEY-6, FR-FWD-1; T-1, T-2: under every key and
// forwarding setting the origin sees only fields the key covers or the
// operator allowed, and the request it saw has the key of the client's.
func TestForwardEqualsKey(t *testing.T) {
	always := []string{"Accept-Encoding", "Authorization", "Cache-Control", "Pragma", "Traceparent", "Tracestate", "X-Request-Id"}
	for _, tc := range []struct {
		name    string
		key     weir.KeyConfig
		allow   []string
		forward []string // fields the origin may see besides always
		query   string   // the forwarded query for "b=2&utm_source=x&a=1"
		tenant  []string // the forwarded X-Tenant lines
		fwd     weir.ForwardConfig
	}{
		{name: "defaults", query: "b=2&utm_source=x&a=1"},
		{name: "Key.Headers", key: weir.KeyConfig{Headers: []string{"x-tenant", "X-Absent"}}, forward: []string{"X-Tenant"},
			query: "b=2&utm_source=x&a=1", tenant: []string{"a, b"}},
		{name: "Key.Cookies", key: weir.KeyConfig{Cookies: []string{"sid", "lang"}}, forward: []string{"Cookie"}, query: "b=2&utm_source=x&a=1"},
		{name: "Forward.Allow", allow: []string{"X-Debug"}, forward: []string{"X-Debug"}, query: "b=2&utm_source=x&a=1"},
		{name: "query rules", key: weir.KeyConfig{QueryDrop: []string{"utm_*"}, QuerySort: true}, query: "a=1&b=2"},
		{name: "everything", key: weir.KeyConfig{Headers: []string{"X-Tenant"}, Cookies: []string{"lang"}, QueryKeep: []string{"a"}, NormalizePath: true},
			allow: []string{"X-Debug"}, forward: []string{"X-Tenant", "Cookie", "X-Debug"}, query: "a=1", tenant: []string{"a, b"}},
		{name: "NoTraceHeaders", fwd: weir.ForwardConfig{NoTraceHeaders: true}, query: "b=2&utm_source=x&a=1"},
		// G4: ForwardAll sends unkeyed fields by design, but a keyed header
		// still goes in the normal form the key holds.
		{name: "ForwardAll with Key.Headers", key: weir.KeyConfig{Headers: []string{"X-Tenant"}}, fwd: weir.ForwardConfig{Mode: weir.ForwardAll},
			query: "b=2&utm_source=x&a=1", tenant: []string{"a, b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.NewChecked(t, 64, 16)
				o.Default(cacheable("v"))
				cfg := cacheCfg
				cfg.Key, cfg.Forward = tc.key, tc.fwd
				cfg.Forward.Allow = tc.allow
				all := tc.fwd.Mode == weir.ForwardAll
				e := newEngine(t, cfg)
				defer closeEngine(t, e)

				if resp, _ := serve(t, e, noisyReq("/a", "b=2&utm_source=x&a=1"), o); resp.Cache.Hit {
					t.Fatal("first request was a hit")
				}
				seen := o.Requests()[0]
				for name := range seen.Header {
					if !all && !slices.Contains(always, name) && !slices.Contains(tc.forward, name) {
						t.Errorf("origin saw unkeyed %s: %q", name, seen.Header[name])
					}
				}
				if seen.RawQuery != tc.query || !slices.Equal(seen.Header["X-Tenant"], tc.tenant) {
					t.Errorf("origin saw query %q, X-Tenant %q; want %q, %q", seen.RawQuery, seen.Header["X-Tenant"], tc.query, tc.tenant)
				}
				if got := len(seen.Header["Traceparent"]); (got == 0) != tc.fwd.NoTraceHeaders {
					t.Errorf("origin saw %d Traceparent lines with NoTraceHeaders=%v", got, tc.fwd.NoTraceHeaders)
				}
				if got := seen.Header["Accept-Encoding"]; !slices.Equal(got, []string{"gzip"}) {
					t.Errorf("origin saw Accept-Encoding %q, want the bucket", got)
				}
				// INV-1: what the origin saw is a request for the same entry.
				seen.Body = nil
				if resp, _ := serve(t, e, seen, o); !resp.Cache.Hit || o.TotalCalls() != 1 {
					t.Fatalf("the forwarded request is not a hit for the stored entry (%+v, %d origin calls)", resp.Cache, o.TotalCalls())
				}
				// A request differing only in unkeyed inputs is the same entry.
				other := noisyReq("/a", "b=2&utm_source=x&a=1")
				other.Header.Set("User-Agent", "other")
				other.Header.Set("X-Forwarded-Host", "other.example")
				other.Header["Cookie"] = []string{"track=1; lang=en", "sid=1"}
				if len(tc.allow) == 0 {
					other.Header.Set("X-Debug", "2")
				}
				if resp, _ := serve(t, e, other, o); !resp.Cache.Hit {
					t.Fatalf("unkeyed inputs split the key (%+v)", resp.Cache)
				}
			})
		})
	}
}

// FR-KEY-2, FR-KEY-6, T-1: a keyed header or cookie value selects the
// entry; a value the origin can see never shares another value's response.
func TestKeyedInputsSplitEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			body := r.Header.Get("X-Tenant") + "|" + r.Header.Get("Cookie")
			return &weir.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: bodyOf(body)}, nil
		}})
		cfg := cacheCfg
		cfg.Key = weir.KeyConfig{Headers: []string{"X-Tenant"}, Cookies: []string{"lang"}}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for _, tc := range []struct {
			tenant, cookie, want string
			hit                  bool
		}{
			{"a", "", "a|", false},
			{"b", "", "b|", false},
			{"a", "lang=en", "a|lang=en", false},
			{"a", "lang=fr; x=1", "a|lang=fr", false},
			{" a ", "x=2; lang=en", "a|lang=en", true},
			{"a\x00", "lang=en; lang=fr", "|", false}, // FR-VAL-3: both fall back to absent
			{"", "", "|", false},                      // an empty header is present, and keyed apart from absent
		} {
			r := getReq("/a")
			r.Header["X-Tenant"] = []string{tc.tenant}
			if tc.cookie != "" {
				r.Header.Set("Cookie", tc.cookie)
			}
			resp, body := serve(t, e, r, o)
			if body != tc.want || resp.Cache.Hit != tc.hit {
				t.Fatalf("X-Tenant %q Cookie %q: body %q hit %v, want %q %v", tc.tenant, tc.cookie, body, resp.Cache.Hit, tc.want, tc.hit)
			}
		}
		if _, body := serve(t, e, getReq("/a"), o); body != "|" {
			t.Fatalf("no keyed input: %q", body)
		}
	})
}

// FR-FWD-1, T-1: the fields behind published poisoning attacks never reach
// the origin on a cacheable request in strict mode.
func TestUnkeyedHeaderNotForwarded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(cacheable("v"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		names := []string{"X-Forwarded-Host", "X-Forwarded-Port", "User-Agent", "Origin", "Accept", "Upgrade", "Max-Forwards"}
		for _, method := range []string{"GET", "HEAD"} {
			r := getReq("/" + method)
			r.Method = method
			for _, n := range names {
				r.Header.Set(n, "evil")
			}
			serve(t, e, r, o)
		}
		if len(o.Requests()) != 2 {
			t.Fatalf("origin calls = %d, want 2", len(o.Requests()))
		}
		for _, seen := range o.Requests() {
			for _, n := range names {
				if v, ok := seen.Header[n]; ok {
					t.Errorf("%s: origin saw %s: %q", seen.Path, n, v)
				}
			}
		}
	})
}

// FR-KEY-5, P2: a dropped parameter leaves the forwarded query and the key.
func TestQueryDropRemovesFromForward(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(cacheable("v"))
		cfg := cacheCfg
		cfg.Key.QueryDrop = []string{"utm_*", "fbclid"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		first := getReq("/a")
		first.RawQuery = "utm_source=evil&x=1&fbclid=abc"
		serve(t, e, first, o)
		if got := o.Requests()[0].RawQuery; got != "x=1" {
			t.Fatalf("forwarded query %q, want x=1", got)
		}
		second := getReq("/a")
		second.RawQuery = "x=1&utm_campaign=other"
		if resp, _ := serve(t, e, second, o); !resp.Cache.Hit || o.TotalCalls() != 1 {
			t.Fatalf("dropped parameter is in the key (%+v, %d calls)", resp.Cache, o.TotalCalls())
		}
	})
}

// FR-BYP-1, FR-FWD-3: a request matching a bypass rule goes to the origin
// as received, is never stored and is never answered from the cache.
func TestBypassNeverStored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			return &weir.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"max-age=60"}},
				Body: bodyOf("for " + r.Header.Get("Cookie"))}, nil
		}})
		cfg := cacheCfg
		cfg.Bypass = weir.BypassConfig{Cookies: []string{"session"}, Headers: []string{"x-preview"}}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		loggedIn := func() *weir.Request {
			r := getReq("/a")
			r.RawQuery = "q=1"
			r.Header.Set("Cookie", "session=alice")
			r.Header.Set("User-Agent", "ua")
			return r
		}
		resp, body := serve(t, e, loggedIn(), o)
		if body != "for session=alice" || resp.Cache.Hit || resp.Cache.Fwd != weir.FwdBypass || resp.Cache.Stored {
			t.Fatalf("bypassed: %q %+v; want the origin's answer, fwd=bypass, not stored", body, resp.Cache)
		}
		if seen := o.Requests()[0]; seen.Header.Get("User-Agent") != "ua" || seen.RawQuery != "q=1" {
			t.Fatalf("bypassed request not forwarded as received: %+v", seen)
		}
		anon := getReq("/a")
		anon.RawQuery = "q=1"
		if resp, body := serve(t, e, anon, o); resp.Cache.Hit || body != "for " {
			t.Fatalf("anonymous request after a bypass: %q hit=%v; the bypassed response was stored", body, resp.Cache.Hit)
		}
		// The anonymous response is stored now; a bypassed request never gets it.
		if resp, body := serve(t, e, loggedIn(), o); resp.Cache.Hit || body != "for session=alice" {
			t.Fatalf("bypassed request served from cache: %q %+v", body, resp.Cache)
		}
		preview := getReq("/a")
		preview.RawQuery = "q=1"
		preview.Method = "HEAD"
		preview.Header.Set("X-Preview", "1")
		if resp, _ := serve(t, e, preview, o); resp.Cache.Hit || resp.Cache.Fwd != weir.FwdBypass {
			t.Fatalf("bypass header: %+v", resp.Cache)
		}
		if got := o.Requests()[3].Method; got != "HEAD" {
			t.Fatalf("bypassed HEAD forwarded as %s", got)
		}
		calls := o.TotalCalls()
		// FR-SRV-6: the client forbids the origin and bypass forbids the cache.
		oic := loggedIn()
		oic.Header.Set("Cache-Control", "only-if-cached")
		if _, _, err := serveResult(t, e, oic, o); !errors.Is(err, weir.ErrOnlyIfCached) || o.TotalCalls() != calls {
			t.Fatalf("only-if-cached under a bypass rule = %v (%d new origin calls), want ErrOnlyIfCached and none", err, o.TotalCalls()-calls)
		}
	})
}

// FR-BYP-1, FR-COA-8: concurrent bypassed requests for one URL each get
// their own origin fetch.
func TestBypassNotCoalesced(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		gate := make(chan struct{})
		o.Default(testorigin.Behavior{Gate: gate, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: []byte("v")})
		cfg := cacheCfg
		cfg.Bypass.Cookies = []string{"session"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		var done []<-chan timedServe
		for range 3 {
			done = append(done, serveTimed(t, e, withHeader(getReq("/a"), "Cookie", "session=1"), o))
		}
		synctest.Wait()
		if got := o.MaxInflight(); got != 3 {
			t.Fatalf("in-flight origin fetches = %d, want 3 (one per bypassed request)", got)
		}
		close(gate)
		for _, ch := range done {
			if r := <-ch; r.err != nil {
				t.Fatal(r.err)
			}
		}
	})
}

// FR-LIM-7, T-39: bypassed GETs from logged-in users carry no body and take
// main-pool slots, even while uploads hold the whole upload pool.
func TestBodylessBypassUsesMainPool(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 5, 4)
		origin := &uploadOrigin{o: o}
		cfg := cacheCfg
		cfg.Limiter = weir.LimiterConfig{MaxConcurrent: 4, MaxUpload: 1}
		cfg.Bypass.Cookies = []string{"session"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		stop := make(chan struct{})
		held := serveTimed(t, e, uploadReq(t.Context(), "/upload", stop), origin)
		queued := serveTimed(t, e, uploadReq(t.Context(), "/upload", stop), origin)
		synctest.Wait()

		for _, body := range []func(*weir.Request){
			func(*weir.Request) {},
			func(r *weir.Request) { r.Body = http.NoBody },
		} {
			r := withHeader(getReq("/a"), "Cookie", "session=alice")
			body(r)
			if res := <-serveTimed(t, e, r, origin); res.err != nil || res.elapsed != 0 {
				t.Fatalf("bypassed GET: err %v after %v; want served at once from the main pool", res.err, res.elapsed)
			}
		}
		if n := o.Calls("/a"); n != 2 {
			t.Fatalf("bypassed GETs reaching the origin = %d, want 2", n)
		}
		close(stop)
		for _, ch := range []<-chan timedServe{held, queued} {
			if r := <-ch; r.err != nil {
				t.Fatalf("upload: %v", r.err)
			}
		}
	})
}

// FR-BYP-1, T-8: under ForwardAll the origin sees the Cookie lines as sent,
// so a session cookie in a shape only a lenient parser reads must still
// bypass, or the sender's personalized response is stored for everyone.
func TestBypassCookieEvasion(t *testing.T) {
	for _, cookie := range []string{"session =alice", "SESSION=alice", "lang=en, session=alice", `"session"=alice`, "session", "sess%69on=alice", "%20session=alice", "+session=alice"} {
		t.Run(cookie, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.NewChecked(t, 64, 16)
				o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
					body := "anonymous"
					if c, _ := url.QueryUnescape(r.Header.Get("Cookie")); strings.Contains(strings.ToLower(c), "session") { // a lenient origin
						body = "alice's page"
					}
					return &weir.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: bodyOf(body)}, nil
				}})
				cfg := cacheCfg
				cfg.Forward.Mode = weir.ForwardAll
				cfg.Bypass.Cookies = []string{"session"}
				e := newEngine(t, cfg)
				defer closeEngine(t, e)

				resp, body := serve(t, e, withHeader(getReq("/a"), "Cookie", cookie), o)
				if resp.Cache.Fwd != weir.FwdBypass || resp.Cache.Stored || body != "alice's page" {
					t.Fatalf("%q: %q %+v; want fwd=bypass, not stored", cookie, body, resp.Cache)
				}
				if _, body := serve(t, e, getReq("/a"), o); body != "anonymous" {
					t.Fatalf("anonymous client got %q", body)
				}
			})
		})
	}
}

// FR-WRM-1, FR-BYP-1: Warm does not send a request a bypass rule matches;
// its response could never be stored.
func TestWarmSkipsBypassed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(cacheable("v"))
		cfg := cacheCfg
		cfg.Bypass = weir.BypassConfig{Cookies: []string{"session"}, Headers: []string{"X-Preview"}}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		reqs := []*weir.Request{getReq("/a"), withHeader(getReq("/b"), "Cookie", "session=1"), withHeader(getReq("/c"), "X-Preview", "1")}
		st, err := e.Warm(t.Context(), slices.Values(reqs), o)
		if err != nil {
			t.Fatal(err)
		}
		if want := (weir.WarmStats{Fetched: 1, NotStored: 2}); st != want || o.TotalCalls() != 1 {
			t.Fatalf("stats %+v with %d origin calls, want %+v and 1", st, o.TotalCalls(), want)
		}
	})
}

// 01 §5.2.3, T-13, T-31: the Accept-Encoding bucket is keyed only through
// Vary by default, so a marker written for one bucket covers the others
// until a storable response arrives. Key.Headers can name the field; the
// key then holds the bucket and each bucket has its own entry and marker.
func TestKeyedAcceptEncodingSeparatesBuckets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		keyed  bool
		marker bool // the identity client meets the gzip client's marker
	}{
		{"default: buckets share the primary key", false, true},
		{"Key.Headers names Accept-Encoding", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.NewChecked(t, 64, 16)
				o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
					cc := "max-age=60"
					if r.Header.Get("Accept-Encoding") == "gzip" {
						cc = "private" // storability differs by coding
					}
					return &weir.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {cc}}, Body: bodyOf("v")}, nil
				}})
				cfg := cacheCfg
				if tc.keyed {
					cfg.Key.Headers = []string{"accept-encoding"}
				}
				e := newEngine(t, cfg)
				defer closeEngine(t, e)

				serve(t, e, withHeader(getReq("/a"), "Accept-Encoding", "gzip, br"), o)
				resp, _ := serve(t, e, getReq("/a"), o)
				if got := resp.Cache.Detail == "hit-for-miss"; got != tc.marker || !resp.Cache.Stored {
					t.Fatalf("identity client: marker %v, want %v, and its response stored (%+v)", got, tc.marker, resp.Cache)
				}
				for _, r := range o.Requests() {
					if ae := r.Header["Accept-Encoding"]; len(ae) != 1 || ae[0] != "gzip" && ae[0] != "identity" {
						t.Fatalf("origin saw Accept-Encoding %q, want one bucket", ae)
					}
				}
			})
		})
	}
}

// FR-FWD-1, T-1; 01 §6: New refuses a Forward.Allow or Key.Headers entry
// that could never take effect instead of silently ignoring it.
func TestNewRejectsDeadForwardNames(t *testing.T) {
	allow := func(names ...string) weir.Config { return weir.Config{Forward: weir.ForwardConfig{Allow: names}} }
	keyed := func(names ...string) weir.Config { return weir.Config{Key: weir.KeyConfig{Headers: names}} }
	both := keyed("X-Tenant")
	both.Forward.Allow = []string{"x-tenant"}
	ok := keyed("X-Tenant", "accept-encoding", "Authorization", "traceparent")
	ok.Forward.Allow = []string{"X-Debug", "proxy-authorization"}
	for _, tc := range []struct {
		name string
		cfg  weir.Config
		ok   bool
	}{
		{"Allow names a keyed header", both, false},
		{"Allow names Cookie", allow("cookie"), false},
		{"Allow names a hop-by-hop field", allow("te"), false},
		{"Allow names Connection after a valid name", allow("X-A", "Connection"), false},
		{"Allow names Host", allow("host"), false},
		{"Allow names a client precondition", allow("If-None-Match"), false},
		{"Allow names Range", allow("range"), false},
		{"Allow names a body field", allow("Content-Length"), false},
		{"Key.Headers names Cookie", keyed("Cookie"), false},
		{"Key.Headers names a hop-by-hop field", keyed("X-Tenant", "upgrade"), false},
		{"Key.Headers names Range", keyed("Range"), false},
		{"Key.Headers names Host", keyed("Host"), false},
		{"names that are forwarded", ok, true},
		{"Allow names Accept-Encoding, sent as the bucket anyway", allow("Accept-Encoding"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := weir.New(tc.cfg)
			if err == nil {
				closeEngine(t, e)
			}
			if tc.ok != (err == nil) || !tc.ok && !errors.Is(err, weir.ErrInvalidConfig) {
				t.Fatalf("New = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}
