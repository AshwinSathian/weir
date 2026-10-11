package weir_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/experiments"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// headerOrigin answers every request with 200, body "ok" and the given headers.
func headerOrigin(t *testing.T, h http.Header) *testorigin.Origin {
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(testorigin.Behavior{Func: func(*weir.Request) (*weir.Response, error) {
		return &weir.Response{StatusCode: 200, Header: h.Clone(), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	}})
	return o
}

// expCookieOf returns the weir_v Set-Cookie lines of a response.
func expCookieOf(resp *weir.Response) []string {
	var out []string
	for _, sc := range resp.Header["Set-Cookie"] {
		if strings.HasPrefix(sc, "weir_v=") {
			out = append(out, sc)
		}
	}
	return out
}

// cookiePair is the name=value part of a Set-Cookie line.
func cookiePair(sc string) string { return strings.SplitN(sc, ";", 2)[0] }

func ccDirectives(resp *weir.Response) map[string]bool {
	m := map[string]bool{}
	for _, l := range resp.Header["Cache-Control"] {
		for d := range strings.SplitSeq(l, ",") {
			m[strings.ToLower(strings.TrimSpace(d))] = true
		}
	}
	return m
}

// E1, E7, T-36: a created assignment adds Set-Cookie to that client's
// response only. A request carrying the cookie gets none, and a hit for a new
// visitor gets that visitor's own cookie, never the first one's.
func TestSetCookiePerClientOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := headerOrigin(t, http.Header{"Cache-Control": {"max-age=60"}})
		e := newEngine(t, expConfig())
		defer closeEngine(t, e)

		first, _ := serve(t, e, expReq("/checkout", nil), o)
		cs := expCookieOf(first)
		if len(cs) != 1 {
			t.Fatalf("first visit Set-Cookie = %q, want one weir_v", first.Header["Set-Cookie"])
		}
		for _, want := range []string{"Path=/", "HttpOnly", "SameSite=Lax", "Secure", "Max-Age=7776000"} {
			if !strings.Contains(cs[0], want) {
				t.Fatalf("Set-Cookie %q lacks %q", cs[0], want)
			}
		}

		// Same visitor again: the assignment is kept, so no rewrite.
		again, _ := serve(t, e, expReq("/checkout", http.Header{"Cookie": {cookiePair(cs[0])}}), o)
		if got := expCookieOf(again); len(got) != 0 {
			t.Fatalf("returning visitor got Set-Cookie %q", got)
		}

		// A different new visitor is served from the entry the first visit
		// stored (hit path, when they land in the same variant) and gets its own cookie.
		seen := map[string]bool{cookiePair(cs[0]): true}
		for range 8 {
			r, _ := serve(t, e, expReq("/checkout", nil), o)
			c := expCookieOf(r)
			if len(c) != 1 || seen[cookiePair(c[0])] {
				t.Fatalf("new visitor Set-Cookie = %q, want a fresh weir_v", c)
			}
			seen[cookiePair(c[0])] = true
		}
		if n := o.Calls("/checkout"); n > 2 {
			t.Fatalf("origin calls = %d, want at most one per variant", n)
		}
	})
}

// E1: Secure only on https.
func TestSetCookieSecureOnlyOnHTTPS(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := headerOrigin(t, http.Header{"Cache-Control": {"max-age=60"}})
		e := newEngine(t, expConfig())
		defer closeEngine(t, e)

		r := expReq("/checkout", nil)
		r.Scheme = "http"
		resp, _ := serve(t, e, r, o)
		cs := expCookieOf(resp)
		if len(cs) != 1 || strings.Contains(cs[0], "Secure") {
			t.Fatalf("http Set-Cookie = %q, want one without Secure", cs)
		}
	})
}

// E7, 10 §4 step 5, T-36: the assignment Set-Cookie is added after the entry
// is built, so no stored entry ever carries it. A 304 for a new visitor
// still carries their cookie.
func TestSetCookieNeverStored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := headerOrigin(t, http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"v1"`}})
		e := newEngine(t, expConfig())
		defer closeEngine(t, e)

		serve(t, e, expReq("/checkout", nil), o) // fills one variant; its cookie must not be stored
		// A visitor carrying a stored assignment gets neither a stored cookie nor a new one.
		for _, v := range []string{"control", "b"} {
			r, _ := serve(t, e, expReq("/checkout", http.Header{"Cookie": {visitor(t, "known", v)}}), o)
			if got := r.Header["Set-Cookie"]; len(got) != 0 {
				t.Fatalf("variant %s: Set-Cookie %q leaked from the stored entry", v, got)
			}
		}
		// 304 to a new visitor's conditional request: the cookie rides on it.
		for range 8 {
			r := expReq("/checkout", http.Header{"If-None-Match": {`"v1"`}})
			resp, _ := serve(t, e, r, o)
			if resp.StatusCode != http.StatusNotModified {
				t.Fatalf("status = %d, want 304", resp.StatusCode)
			}
			if len(expCookieOf(resp)) != 1 {
				t.Fatalf("304 Set-Cookie = %q, want one weir_v", resp.Header["Set-Cookie"])
			}
		}
	})
}

// E7, T-36: matched responses lose public, s-maxage and CDN-Cache-Control and
// gain private, on a miss and on a hit; unmatched paths are left alone.
func TestDownstreamRewrittenPrivate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := headerOrigin(t, http.Header{
			"Cache-Control":     {"Public, max-age=60", `S-MaxAge=120, stale-while-revalidate=5`},
			"Cdn-Cache-Control": {"max-age=300"},
		})
		e := newEngine(t, expConfig())
		defer closeEngine(t, e)

		for i, name := range []string{"miss", "hit", "hit again"} {
			resp, _ := serve(t, e, expReq("/checkout", http.Header{"Cookie": {visitor(t, "v", "b")}}), o)
			d := ccDirectives(resp)
			if !d["private"] || d["public"] || d["s-maxage=120"] || !d["max-age=60"] || !d["stale-while-revalidate=5"] {
				t.Fatalf("%s: Cache-Control = %q", name, resp.Header["Cache-Control"])
			}
			if _, ok := resp.Header["Cdn-Cache-Control"]; ok {
				t.Fatalf("%s: CDN-Cache-Control survived", name)
			}
			if i == 0 && resp.Cache.Hit {
				t.Fatal("first request was a hit")
			}
		}
		other, _ := serve(t, e, expReq("/other", nil), o)
		if d := ccDirectives(other); !d["public"] || d["private"] || other.Header.Get("Cdn-Cache-Control") == "" {
			t.Fatalf("unmatched path was rewritten: %q", other.Header)
		}
	})
}

// E7: a response without Cache-Control still becomes private.
func TestDownstreamPrivateWithoutOriginCacheControl(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := headerOrigin(t, http.Header{"Expires": {time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)}})
		e := newEngine(t, expConfig())
		defer closeEngine(t, e)

		resp, _ := serve(t, e, expReq("/checkout", http.Header{"Cookie": {visitor(t, "v", "b")}}), o)
		if got := resp.Header["Cache-Control"]; len(got) != 1 || got[0] != "private" {
			t.Fatalf("Cache-Control = %q, want private", got)
		}
	})
}

// E7: with DownstreamKeysOnVariant the origin's directives pass through for a
// returning visitor. A response that carries the new assignment cookie is
// still private, because a cache that keys on the variant but not on the
// cookie would hand one visitor id to everyone (T-36).
func TestDownstreamVariantAwareKeepsDirectives(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := headerOrigin(t, http.Header{
			"Cache-Control":     {"public, max-age=60, s-maxage=120"},
			"Cdn-Cache-Control": {"max-age=300"},
		})
		cfg := expConfig()
		cfg.Experiments.DownstreamKeysOnVariant = true
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		first, _ := serve(t, e, expReq("/checkout", nil), o)
		if d := ccDirectives(first); !d["private"] || d["public"] || d["s-maxage=120"] || len(expCookieOf(first)) != 1 {
			t.Fatalf("cookie-bearing response: %q", first.Header)
		}
		h := http.Header{"Cookie": {visitor(t, "v", "b")}}
		for range 2 { // miss, then hit
			resp, _ := serve(t, e, expReq("/checkout", h), o)
			d := ccDirectives(resp)
			if !d["public"] || !d["s-maxage=120"] || d["private"] || resp.Header.Get("Cdn-Cache-Control") != "max-age=300" {
				t.Fatalf("headers were rewritten: %q", resp.Header)
			}
		}
	})
}

// P4, E7: Weir's own copy keeps the origin's directives. Freshness follows
// s-maxage=120, so the entry is still a hit at 60s although the client was
// told max-age=10, and the rewrite never edits the stored header map.
func TestStoredCopyKeepsOriginDirectives(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := headerOrigin(t, http.Header{"Cache-Control": {"public, max-age=10, s-maxage=120"}})
		e := newEngine(t, expConfig())
		defer closeEngine(t, e)

		h := http.Header{"Cookie": {visitor(t, "v", "b")}}
		serve(t, e, expReq("/checkout", h), o)
		time.Sleep(60 * time.Second)
		for range 3 {
			resp, _ := serve(t, e, expReq("/checkout", h), o)
			if !resp.Cache.Hit || resp.Cache.Stale != weir.StaleNone {
				t.Fatalf("at 60s: cache = %+v, want a fresh hit", resp.Cache)
			}
			resp.Header["Cache-Control"][0] = "mutated" // callers own their clone
		}
		if n := o.Calls("/checkout"); n != 1 {
			t.Fatalf("origin calls = %d, want 1", n)
		}
	})
}

// E7: an Origin that returns non-canonical map keys cannot keep a second
// Cache-Control or a CDN-Cache-Control next to the rewrite.
func TestDownstreamRewriteCoversNonCanonicalKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Func: func(*weir.Request) (*weir.Response, error) {
			return &weir.Response{StatusCode: 200, Header: http.Header{"cache-control": {"public, max-age=60"}, "cdn-cache-control": {"max-age=9"}},
				Body: io.NopCloser(strings.NewReader("ok"))}, nil
		}})
		e := newEngine(t, expConfig())
		defer closeEngine(t, e)

		resp, _ := serve(t, e, expReq("/checkout", http.Header{"Cookie": {visitor(t, "v", "b")}}), o)
		for k := range resp.Header {
			if strings.EqualFold(k, "cdn-cache-control") {
				t.Fatalf("%s survived", k)
			}
		}
		if d := ccDirectives(resp); d["public"] || !d["private"] {
			t.Fatalf("Cache-Control = %q", resp.Header)
		}
	})
}

var expOldKey = []byte("fedcba9876543210fedcba9876543210-old-key")

// T-35, E2: a cookie signed under a rotated-out key is re-signed with the
// first key (same visitor, same assignment), so the old key can be dropped
// later without re-bucketing anyone. A cookie already under the first key is
// left alone, and without consent nothing is written.
func TestSetCookieResignsOldKeyCookie(t *testing.T) {
	old := func() string {
		c := experimentsCookieFor(t, "keep-me", "b", expOldKey)
		return "weir_v=" + c
	}
	rotated := func(consent bool) weir.Config {
		cfg := expConfig()
		cfg.Experiments.SigningKeys = [][]byte{expKey, expOldKey}
		if consent {
			cfg.Experiments.Consent = weir.ExperimentConsent{Cookie: "cmp", Value: "analytics=yes"}
		}
		return cfg
	}
	t.Run("old key is re-signed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := headerOrigin(t, http.Header{"Cache-Control": {"max-age=60"}})
			e := newEngine(t, rotated(false))
			defer closeEngine(t, e)
			resp, _ := serve(t, e, expReq("/checkout", http.Header{"Cookie": {old()}}), o)
			cs := expCookieOf(resp)
			if len(cs) != 1 || !strings.Contains(cs[0], ".keep-me.checkout-copy:b.") {
				t.Fatalf("Set-Cookie = %q, want the same visitor and variant", cs)
			}
			if cookiePair(cs[0]) == old() {
				t.Fatal("cookie was not re-signed")
			}
			again, _ := serve(t, e, expReq("/checkout", http.Header{"Cookie": {cookiePair(cs[0])}}), o)
			if got := expCookieOf(again); len(got) != 0 {
				t.Fatalf("first-key cookie was rewritten: %q", got)
			}
		})
	})
	t.Run("no consent writes nothing", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := headerOrigin(t, http.Header{"Cache-Control": {"max-age=60"}})
			e := newEngine(t, rotated(true))
			defer closeEngine(t, e)
			resp, _ := serve(t, e, expReq("/checkout", http.Header{"Cookie": {old()}}), o)
			if got := expCookieOf(resp); len(got) != 0 {
				t.Fatalf("Set-Cookie without consent: %q", got)
			}
		})
	})
}

// E7, 10 §4 step 6: the rewrite covers every response class: a Range pass-through
// (206), an unsatisfiable range (416), a bypassed engine, and concurrent followers
// of one fetch, each of which gets its own cookie.
func TestExperimentResponseClasses(t *testing.T) {
	origin := func(t *testing.T) *testorigin.Origin {
		return headerOrigin(t, http.Header{"Cache-Control": {"public, max-age=60, s-maxage=9"}, "Cdn-Cache-Control": {"max-age=1"}})
	}
	check := func(t *testing.T, name string, resp *weir.Response, cookie bool) {
		t.Helper()
		if d := ccDirectives(resp); d["public"] || d["s-maxage=9"] || !d["private"] || resp.Header.Get("Cdn-Cache-Control") != "" {
			t.Fatalf("%s: Cache-Control = %q", name, resp.Header)
		}
		if got := len(expCookieOf(resp)); cookie && got != 1 || !cookie && got != 0 {
			t.Fatalf("%s: weir_v cookies = %d", name, got)
		}
	}
	t.Run("range request", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := origin(t)
			e := newEngine(t, expConfig())
			defer closeEngine(t, e)
			ck := http.Header{"Cookie": {visitor(t, "v", "b")}}
			serve(t, e, expReq("/checkout", ck), o)
			r := expReq("/checkout", http.Header{"Cookie": ck["Cookie"], "Range": {"bytes=0-0"}})
			resp, _ := serve(t, e, r, o)
			if resp.StatusCode != http.StatusPartialContent {
				t.Fatalf("status = %d, want 206", resp.StatusCode)
			}
			check(t, "206", resp, false)
			r = expReq("/checkout", http.Header{"Cookie": ck["Cookie"], "Range": {"bytes=900-"}})
			resp, _ = serve(t, e, r, o)
			if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
				t.Fatalf("status = %d, want 416", resp.StatusCode)
			}
			check(t, "416", resp, false)
		})
	})
	t.Run("bypass mode", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := origin(t)
			e := newEngine(t, expConfig())
			defer closeEngine(t, e)
			if err := e.SetMode(weir.ModeBypass, time.Minute); err != nil {
				t.Fatal(err)
			}
			resp, _ := serve(t, e, expReq("/checkout", http.Header{"Cookie": {visitor(t, "v", "b")}}), o)
			check(t, "bypass", resp, false)
		})
	})
	t.Run("followers each get their own cookie", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := origin(t)
			e := newEngine(t, expConfig())
			defer closeEngine(t, e)
			cookies := make(chan string, 8)
			for range 8 {
				go func() {
					resp, err := e.Serve(t.Context(), expReq("/checkout", nil), o)
					if err != nil {
						cookies <- "error"
						return
					}
					defer resp.Body.Close()
					c := expCookieOf(resp)
					if len(c) != 1 || ccDirectives(resp)["public"] {
						cookies <- "bad"
						return
					}
					cookies <- cookiePair(c[0])
				}()
			}
			seen := map[string]bool{}
			for range 8 {
				c := <-cookies
				if c == "error" || c == "bad" || seen[c] {
					t.Fatalf("follower cookie %q (seen %v)", c, seen)
				}
				seen[c] = true
			}
		})
	})
}

// experimentsCookieFor signs a cookie for visitor id holding variant under key.
func experimentsCookieFor(t *testing.T, id, variant string, key []byte) string {
	t.Helper()
	c := experiments.Cookie{VisitorID: id}
	c.Set("checkout-copy", variant)
	v, ok := c.Encode(key)
	if !ok {
		t.Fatal("encode refused")
	}
	return v
}
