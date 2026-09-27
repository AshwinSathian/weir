package weir

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/store"
)

func storableConfig(t *testing.T, mod func(*Config)) *Config {
	t.Helper()
	var c Config
	if mod != nil {
		mod(&c)
	}
	p, err := prepareConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	return &p
}

func classifiedGET(path string, authorized bool) *keys.Classified {
	const origin = "https://example.com"
	return &keys.Classified{
		Forwarded:  keys.Request{Method: http.MethodGet, Scheme: "https", Host: "example.com", Path: path},
		Authorized: authorized,
		URITag:     keys.TagURI(origin, path, ""),
		OriginTag:  keys.TagOrigin(origin),
		Origin:     origin,
	}
}

// originResp builds a response from name/value pairs.
func originResp(status int, kv ...string) *Response {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return &Response{StatusCode: status, Header: h}
}

var testRespTime = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func TestErrorStatusesNotStored(t *testing.T) {
	// FR-STO-2, T-7: error pages are outside the default set even with explicit freshness.
	cfg := storableConfig(t, nil)
	for _, status := range []int{400, 401, 403, 500, 502, 503} {
		d := storability(cfg, classifiedGET("/", false), originResp(status, "Cache-Control", "max-age=60"), nil, testRespTime)
		if d.ok || d.reason != "status" || !d.responseDriven {
			t.Errorf("status %d: got %+v, want not stored for status", status, d)
		}
	}
	if d := storability(cfg, classifiedGET("/", false), originResp(200, "Cache-Control", "max-age=60"), nil, testRespTime); !d.ok {
		t.Errorf("200 max-age=60 not stored: %+v", d)
	}
}

func TestSetCookieNotStored(t *testing.T) {
	// FR-STO-6, T-8
	resp := originResp(200, "Cache-Control", "max-age=60", "Set-Cookie", "sid=abc")
	t.Run("set-cookie blocks storage by default", func(t *testing.T) {
		d := storability(storableConfig(t, nil), classifiedGET("/", false), resp, nil, testRespTime)
		if d.ok || d.reason != "set-cookie" || !d.responseDriven {
			t.Fatalf("got %+v, want not stored for set-cookie", d)
		}
	})
	t.Run("StripSetCookie stores without it and leaves the response intact", func(t *testing.T) {
		cfg := storableConfig(t, func(c *Config) { c.Storable.StripSetCookie = true })
		c := classifiedGET("/", false)
		d := storability(cfg, c, resp, nil, testRespTime)
		if !d.ok {
			t.Fatalf("not stored: %+v", d)
		}
		e := buildEntry(cfg, c, resp, nil, testRespTime, testRespTime, d)
		if _, ok := e.Header["Set-Cookie"]; ok {
			t.Error("stored entry carries Set-Cookie")
		}
		if resp.Header.Get("Set-Cookie") != "sid=abc" {
			t.Error("Set-Cookie removed from the response the triggering client receives")
		}
	})
}

func TestAuthorizationRules(t *testing.T) {
	// FR-STO-5, T-8: RFC 9111 §3.5.
	cfg := storableConfig(t, nil)
	for _, tc := range []struct {
		name, cc   string
		authorized bool
		ok         bool
	}{
		{"max-age alone is not enough", "max-age=60", true, false},
		{"public allows storing", "public, max-age=60", true, true},
		{"s-maxage allows storing", "s-maxage=60", true, true},
		{"must-revalidate allows storing", "must-revalidate, max-age=60", true, true},
		{"unauthorized max-age is stored", "max-age=60", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := classifiedGET("/", tc.authorized)
			resp := originResp(200, "Cache-Control", tc.cc)
			d := storability(cfg, c, resp, nil, testRespTime)
			if d.ok != tc.ok {
				t.Fatalf("ok = %v, want %v (%+v)", d.ok, tc.ok, d)
			}
			if !tc.ok && (d.reason != "authorization" || d.responseDriven) {
				t.Errorf("got %+v, want request-driven authorization", d)
			}
			if tc.ok && tc.authorized {
				if e := buildEntry(cfg, c, resp, nil, testRespTime, testRespTime, d); e.Flags&store.FlagFromAuthorized == 0 {
					t.Error("entry lacks FlagFromAuthorized")
				}
			}
		})
	}
}

func TestNoExtensionBasedCaching(t *testing.T) {
	// FR-STO-8, T-6: storability comes from origin headers, never the path.
	cfg := storableConfig(t, nil)
	for _, path := range []string{"/static/app.css", "/account;x.css", "/a.js", "/img.png"} {
		resp := originResp(200, "Content-Type", "text/css")
		d := storability(cfg, classifiedGET(path, false), resp, []byte("body{}"), testRespTime)
		if d.ok || d.reason != "no-freshness" || !d.responseDriven {
			t.Errorf("%s: got %+v, want not stored for no-freshness", path, d)
		}
	}
}

func TestRedirect302NeedsExplicitFreshness(t *testing.T) {
	// FR-STO-2, FR-STO-8, D39
	cfg := storableConfig(t, nil)
	lm := testRespTime.Add(-24 * time.Hour).Format(http.TimeFormat)
	for _, tc := range []struct {
		name   string
		status int
		kv     []string
		ok     bool
	}{
		{"302 with max-age is stored", 302, []string{"Cache-Control", "max-age=60"}, true},
		{"307 with Expires is stored", 307, []string{"Expires", testRespTime.Add(time.Minute).Format(http.TimeFormat)}, true},
		{"302 with only Last-Modified is not stored", 302, []string{"Last-Modified", lm}, false},
		{"307 with only public is not stored", 307, []string{"Cache-Control", "public"}, false},
		{"301 with only Last-Modified is stored heuristically", 301, []string{"Last-Modified", lm}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := storability(cfg, classifiedGET("/", false), originResp(tc.status, tc.kv...), nil, testRespTime)
			if d.ok != tc.ok {
				t.Fatalf("ok = %v, want %v (%+v)", d.ok, tc.ok, d)
			}
			if !tc.ok && d.reason != "no-freshness" {
				t.Errorf("reason = %q, want no-freshness", d.reason)
			}
		})
	}
}

func TestStorabilityReasons(t *testing.T) {
	// FR-STO-1, FR-STO-3, FR-STO-4, FR-STO-7, FR-STO-8, FR-STO-9, FR-STO-12, T-31
	etag := `"v1"`
	for _, tc := range []struct {
		name           string
		method         string
		reqNoStore     bool
		kv             []string
		body           int
		reason         string // "" means stored
		responseDriven bool
	}{
		{name: "HEAD forward is not stored", method: http.MethodHead, kv: []string{"Cache-Control", "max-age=60"}, reason: "method"},
		{name: "request no-store is request-driven", reqNoStore: true, kv: []string{"Cache-Control", "max-age=60"}, reason: "no-store"},
		{name: "response no-store", kv: []string{"Cache-Control", "no-store, max-age=60"}, reason: "no-store", responseDriven: true},
		{name: "must-understand overrides response no-store", kv: []string{"Cache-Control", "no-store, must-understand, max-age=60"}},
		{name: "qualified private counts", kv: []string{"Cache-Control", `private="x", max-age=60`}, reason: "private", responseDriven: true},
		{name: "Vary is unsupported until M7", kv: []string{"Cache-Control", "max-age=60", "Vary", "Accept-Language"}, reason: "vary-unsupported", responseDriven: true},
		{name: "public alone is stored", kv: []string{"Cache-Control", "public"}},
		{name: "ETag with no-cache is stored", kv: []string{"Cache-Control", "no-cache", "ETag", etag}},
		{name: "ETag with max-age=0 is stored", kv: []string{"Cache-Control", "max-age=0", "ETag", etag}},
		{name: "body plus headers over the limit", kv: []string{"Cache-Control", "max-age=60"}, body: 1024 - len("Cache-Control") - len("max-age=60") + 1, reason: "too-large", responseDriven: true},
		{name: "body plus headers at the limit", kv: []string{"Cache-Control", "max-age=60"}, body: 1024 - len("Cache-Control") - len("max-age=60")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := storableConfig(t, func(c *Config) { c.Storable.MaxObjectBytes = 1024 })
			c := classifiedGET("/", false)
			if tc.method != "" {
				c.Forwarded.Method = tc.method
			}
			c.ReqCC.NoStore = tc.reqNoStore
			d := storability(cfg, c, originResp(200, tc.kv...), make([]byte, tc.body), testRespTime)
			if tc.reason == "" {
				if !d.ok {
					t.Fatalf("not stored: %+v", d)
				}
				return
			}
			if d.ok || d.reason != tc.reason || d.responseDriven != tc.responseDriven {
				t.Fatalf("got %+v, want reason %q responseDriven %v", d, tc.reason, tc.responseDriven)
			}
		})
	}
}

func TestEntryHeadersClipped(t *testing.T) {
	// FR-STO-11, FR-STO-13, P4, 04 §6.10: every stored value slice has
	// len == cap, so a served clone's Header.Add never writes into it.
	cfg := storableConfig(t, nil)
	c := classifiedGET("/", false)
	h := http.Header{}
	for _, kv := range [][2]string{
		{"Cache-Control", "max-age=60"}, {"Content-Type", "text/plain"}, {"X-Multi", "a"}, {"X-Multi", "b"},
		{"Connection", "X-Hop"}, {"X-Hop", "1"}, {"Keep-Alive", "timeout=5"}, {"Transfer-Encoding", "chunked"},
		{"Proxy-Authenticate", "Basic"}, {"Proxy-Authentication-Info", "x"}, {"Age", "10"},
	} {
		h.Add(kv[0], kv[1])
	}
	h["X-Spare"] = append(make([]string, 0, 8), "v") // spare capacity to clip
	resp := &Response{StatusCode: 200, Header: h}
	before := h.Clone()
	d := storability(cfg, c, resp, nil, testRespTime)
	if !d.ok {
		t.Fatalf("not stored: %+v", d)
	}
	e := buildEntry(cfg, c, resp, []byte("hi"), testRespTime.Add(-time.Second), testRespTime, d)

	for name, vals := range e.Header {
		if len(vals) != cap(vals) {
			t.Errorf("%s: len %d cap %d", name, len(vals), cap(vals))
		}
	}
	for _, name := range []string{"Connection", "X-Hop", "Keep-Alive", "Transfer-Encoding", "Proxy-Authenticate", "Proxy-Authentication-Info", "Age"} {
		if _, ok := e.Header[name]; ok {
			t.Errorf("stored header keeps %s", name)
		}
	}
	if got := e.Header["X-Multi"]; !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("X-Multi = %q", got)
	}
	if got := e.Header.Get("Date"); got != testRespTime.Format(http.TimeFormat) {
		t.Errorf("Date = %q, want the response time", got)
	}
	if !e.Date.Equal(testRespTime) {
		t.Errorf("entry Date = %v", e.Date)
	}

	if len(h) != len(before) || h.Get("Age") != "10" || h.Get("Date") != "" {
		t.Error("buildEntry modified the origin response header")
	}
}

func TestBuildEntry(t *testing.T) {
	// FR-FRS-5, FR-STO-13, 04 §4.2 retention, 05 §4 tags.
	t0 := testRespTime.Add(-2 * time.Second)
	date := testRespTime.Add(-time.Hour).Format(http.TimeFormat)
	lm := testRespTime.Add(-48 * time.Hour).Format(http.TimeFormat)
	for _, tc := range []struct {
		name     string
		kv       []string
		u        float64
		lifetime time.Duration
		flags    store.Flags
		expires  time.Duration // after StoredAt
	}{
		{
			name: "jitter shortens, retention adds the stale window",
			kv:   []string{"Cache-Control", "max-age=100, stale-while-revalidate=30, stale-if-error=50", "Date", date},
			u:    0.5, lifetime: 95 * time.Second,
			expires: 95*time.Second - 2*time.Second + 50*time.Second,
		},
		{
			name: "Keep applies with a validator",
			kv:   []string{"Cache-Control", "max-age=100", "ETag", `"x"`},
			u:    0, lifetime: 100 * time.Second,
			expires: 98*time.Second + 5*time.Minute,
		},
		{
			name: "short lifetimes are not jittered",
			kv:   []string{"Cache-Control", "max-age=5"},
			u:    0.99, lifetime: 5 * time.Second,
			expires: 3 * time.Second,
		},
		{
			name: "no-cache heuristic entry has no stale windows",
			kv:   []string{"Cache-Control", "no-cache", "Last-Modified", lm, "Age", "600"},
			u:    0, lifetime: time.Hour, // 10% of 48h, capped at HeuristicMax
			flags:   store.FlagNoCache | store.FlagHeuristic,
			expires: time.Hour - 602*time.Second + 5*time.Minute,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := storableConfig(t, func(c *Config) {
				u := tc.u
				c.Rand = func() float64 { return u }
			})
			c := classifiedGET("/p", false)
			resp := originResp(200, tc.kv...)
			d := storability(cfg, c, resp, []byte("body"), testRespTime)
			if !d.ok {
				t.Fatalf("not stored: %+v", d)
			}
			e := buildEntry(cfg, c, resp, []byte("body"), t0, testRespTime, d)
			if e.Kind != store.KindResponse || e.Status != 200 || string(e.Body) != "body" {
				t.Fatalf("entry = %+v", e)
			}
			if !e.StoredAt.Equal(testRespTime) || !e.RequestTime.Equal(t0) || e.FetchDuration != 2*time.Second {
				t.Errorf("times: stored %v request %v fetch %v", e.StoredAt, e.RequestTime, e.FetchDuration)
			}
			if !slices.Equal(e.Tags, []store.Tag{store.TagGlobal(), c.OriginTag, c.URITag}) || e.Owner != c.OriginTag {
				t.Error("tags or owner wrong")
			}
			if e.Flags != tc.flags {
				t.Errorf("flags = %b, want %b", e.Flags, tc.flags)
			}
			if e.Lifetime != tc.lifetime {
				t.Errorf("lifetime = %v, want %v", e.Lifetime, tc.lifetime)
			}
			if got := e.Expires.Sub(e.StoredAt); got != tc.expires {
				t.Errorf("retention = %v, want %v", got, tc.expires)
			}
		})
	}
}

func TestRetentionFloorAndSaturation(t *testing.T) {
	// FR-STL-2, FR-FRS-4, 04 §4.2: retention is at least StoredAt + 1s, and a
	// huge Keep never wraps it into the past.
	cfg := storableConfig(t, func(c *Config) { c.Freshness.Keep = 1<<63 - 1 })
	c := classifiedGET("/", false)
	resp := originResp(200, "Cache-Control", "max-age=0", "ETag", `"x"`)
	d := storability(cfg, c, resp, nil, testRespTime)
	e := buildEntry(cfg, c, resp, nil, testRespTime, testRespTime, d)
	if !e.Expires.After(e.StoredAt.Add(time.Hour)) {
		t.Errorf("huge Keep: expires %v", e.Expires)
	}
	cfg = storableConfig(t, nil)
	resp = originResp(200, "Cache-Control", "public, max-age=10", "Age", "100")
	d = storability(cfg, c, resp, nil, testRespTime)
	e = buildEntry(cfg, c, resp, nil, testRespTime, testRespTime, d)
	if got := e.Expires.Sub(e.StoredAt); got != time.Second {
		t.Errorf("retention = %v, want the 1s floor", got)
	}
}

func TestOriginHeaderKeysCanonicalized(t *testing.T) {
	// INV-4, FR-STO-4, FR-STO-6, T-8: storability reads canonical keys, so a
	// non-canonical key from a custom Origin must not skip a check.
	origin := OriginFunc(func(context.Context, *Request) (*Response, error) {
		return &Response{StatusCode: 200, Header: http.Header{
			"Cache-Control": {"max-age=60"},
			"cache-control": {"private"},
			"set-cookie":    {"sid=victim"},
		}}, nil
	})
	resp, err := safeFetch(t.Context(), origin, &Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header["Cache-Control"]; !slices.Equal(got, []string{"max-age=60", "private"}) {
		t.Errorf("Cache-Control = %q, want both lines merged", got)
	}
	if len(resp.Header) != 2 || resp.Header.Get("Set-Cookie") != "sid=victim" {
		t.Errorf("header = %v, want Set-Cookie canonical", resp.Header)
	}
	d := storability(storableConfig(t, nil), classifiedGET("/", false), resp, nil, testRespTime)
	if d.ok || d.reason != "private" {
		t.Errorf("got %+v, want not stored for private", d)
	}
}

func TestUnkeyedRequestNeverResponseDriven(t *testing.T) {
	// FR-STO-12, T-31: Authorization and a request no-store reach the origin
	// unkeyed, so no refusal under them may license a hit-for-miss marker,
	// whatever reason wins.
	cfg := storableConfig(t, nil)
	for _, tc := range []struct {
		name string
		mod  func(*keys.Classified)
		resp *Response
	}{
		{"authorized error status", func(c *keys.Classified) { c.Authorized = true }, originResp(500, "Cache-Control", "public, max-age=60")},
		{"authorized set-cookie", func(c *keys.Classified) { c.Authorized = true }, originResp(200, "Cache-Control", "public, max-age=60", "Set-Cookie", "a=b")},
		{"authorized private", func(c *keys.Classified) { c.Authorized = true }, originResp(200, "Cache-Control", "private, s-maxage=60")},
		{"no-store request, error status", func(c *keys.Classified) { c.ReqCC.NoStore = true }, originResp(403, "Cache-Control", "max-age=60")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := classifiedGET("/", false)
			tc.mod(c)
			if d := storability(cfg, c, tc.resp, nil, testRespTime); d.ok || d.responseDriven {
				t.Errorf("got %+v, want refused and not response-driven", d)
			}
		})
	}
}

func TestNormalizeResponseLeavesOriginHeaderAlone(t *testing.T) {
	// INV-4, P4: canonicalizing must not write into a header map or value
	// array the Origin may reuse across responses, and duplicate keys merge
	// canonical-first in a fixed order.
	shared := http.Header{
		"Cache-Control": append(make([]string, 0, 4), "max-age=60"),
		"cache-control": {"private"},
		"CACHE-CONTROL": {"no-cache"},
	}
	snapshot := map[string][]string{}
	for k, v := range shared {
		snapshot[k] = slices.Clone(v[:cap(v)])
	}
	for range 20 {
		r := &Response{StatusCode: 200, Header: shared}
		normalizeResponse(r)
		if got := r.Header["Cache-Control"]; !slices.Equal(got, []string{"max-age=60", "no-cache", "private"}) {
			t.Fatalf("Cache-Control = %q", got)
		}
	}
	for k, v := range shared {
		if !slices.Equal(v[:cap(v)], snapshot[k]) || len(shared) != 3 {
			t.Fatalf("origin header changed: %s = %q", k, v[:cap(v)])
		}
	}
}
