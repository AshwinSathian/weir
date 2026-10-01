package weir_test

import (
	"cmp"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// rfcEpoch is the synctest bubble's start time, so rows can write dates
// relative to the first request.
var rfcEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

func rfcDate(d time.Duration) string { return rfcEpoch.Add(d).Format(http.TimeFormat) }

// rfcStep is one request of a row. Expectations are checked after it.
type rfcStep struct {
	after  time.Duration        // fake time to pass before the request
	origin *testorigin.Behavior // replaces the origin's behavior from this step on
	method string               // default GET
	path   string               // default /r
	host   string               // default example.com
	hdr    []string             // request header name/value pairs

	status      int      // expected status; 0 means 200
	err         error    // expected Serve error (errors.Is); status is then ignored
	calls       int      // expected total origin calls after this step
	body        string   // expected body when non-empty
	cacheStatus string   // expected Cache-Status when non-empty
	resp        []string // response header name/value pairs; "" means absent
	sent        []string // header name/value pairs on the last forwarded request; "" means absent
}

// rfcRow is one normative statement of RFC 9111 (or 5861, 9211) that Weir
// implements. Rows tagged with a later milestone are skipped until it lands.
type rfcRow struct {
	sec   string // RFC section
	name  string
	tag   string // milestone that makes the row pass; "" for M1
	cfg   func(*weir.Config)
	steps []rfcStep
}

func bh(status int, body string, kv ...string) *testorigin.Behavior {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return &testorigin.Behavior{Status: status, Header: h, Body: []byte(body)}
}

var rfcRows = []rfcRow{
	// §3 storage conditions: FR-STO-1..9.
	{sec: "9111 §3", name: "200 with max-age is stored", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60"), calls: 1, cacheStatus: "Weir; fwd=uri-miss; fwd-status=200; stored"},
		{calls: 1, body: "a"},
	}},
	{sec: "9111 §3", name: "POST response is not stored", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60"), method: "POST", calls: 1, cacheStatus: "Weir; fwd=method; fwd-status=200"},
		{calls: 2},
	}},
	{sec: "9111 §3", name: "response no-store is not stored", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60, no-store"), calls: 1},
		{calls: 2},
	}},
	{sec: "9111 §3", name: "request no-store is not stored", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60"), hdr: []string{"Cache-Control", "no-store"}, calls: 1},
		{calls: 2},
	}},
	{sec: "9111 §3", name: "private is not stored", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60, private"), calls: 1},
		{calls: 2},
	}},
	{sec: "9111 §5.2.2.7", name: "qualified private is treated as unqualified", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", `max-age=60, private="X-User"`), calls: 1},
		{calls: 2},
	}},
	{sec: "9111 §3", name: "status outside the storable set is not stored", steps: []rfcStep{
		{origin: bh(403, "a", "Cache-Control", "max-age=60"), status: 403, calls: 1},
		{status: 403, calls: 2},
	}},
	{sec: "9111 §3.3", name: "206 is never stored as an entry", steps: []rfcStep{
		{origin: bh(206, "a", "Cache-Control", "max-age=60", "Content-Range", "bytes 0-0/10"), status: 206, calls: 1},
		{status: 206, calls: 2},
	}},
	{sec: "9111 §3", name: "no freshness and no validator is not stored", steps: []rfcStep{
		{origin: bh(200, "a"), calls: 1, cacheStatus: "Weir; fwd=uri-miss; fwd-status=200"},
		{calls: 2},
	}},
	{sec: "9111 §3", name: "public alone permits storing", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "public"), calls: 1, cacheStatus: "Weir; fwd=uri-miss; fwd-status=200; stored"},
	}},
	{sec: "9111 §3", name: "validator with no-cache is stored", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "no-cache", "ETag", `"1"`), calls: 1, cacheStatus: "Weir; fwd=uri-miss; fwd-status=200; stored"},
	}},
	{sec: "9111 §3", name: "Set-Cookie blocks storing", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60", "Set-Cookie", "s=1"), calls: 1, resp: []string{"Set-Cookie", "s=1"}},
		{calls: 2},
	}},
	{sec: "9111 §3", name: "302 is stored only with explicit freshness", steps: []rfcStep{
		{origin: bh(302, "", "Location", "/x", "Last-Modified", rfcDate(-10*time.Hour)), status: 302, calls: 1,
			cacheStatus: "Weir; fwd=uri-miss; fwd-status=302"},
		{status: 302, calls: 2},
		{origin: bh(302, "", "Location", "/x", "Cache-Control", "max-age=60"), path: "/r2", status: 302, calls: 3},
		{path: "/r2", status: 302, calls: 3},
	}},

	// §3.1 stored header exclusions: FR-STO-11.
	{sec: "9111 §3.1", name: "hop-by-hop and Connection-named fields are not stored", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60", "Connection", "X-Hop", "X-Hop", "1", "Keep-Alive", "timeout=5",
			"Proxy-Authenticate", "Basic", "X-Kept", "1"), calls: 1},
		{calls: 1, resp: []string{"X-Hop", "", "Keep-Alive", "", "Connection", "", "Proxy-Authenticate", "", "X-Kept", "1"}},
	}},

	// §3.5 Authorization: FR-STO-5; T-8.
	{sec: "9111 §3.5", name: "authorized response without permission is not stored", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60"), hdr: []string{"Authorization", "Bearer x"}, calls: 1},
		{calls: 2},
	}},
	{sec: "9111 §3.5", name: "public permits an authorized response", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60, public"), hdr: []string{"Authorization", "Bearer x"}, calls: 1},
		{calls: 1},
	}},
	{sec: "9111 §3.5", name: "valid s-maxage permits an authorized response", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "s-maxage=60"), hdr: []string{"Authorization", "Bearer x"}, calls: 1},
		{calls: 1},
	}},
	{sec: "9111 §3.5", name: "must-revalidate permits an authorized response", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60, must-revalidate"), hdr: []string{"Authorization", "Bearer x"}, calls: 1},
		{calls: 1},
	}},
	{sec: "9111 §3.5", name: "invalid s-maxage grants no permission", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "s-maxage=x, max-age=60"), hdr: []string{"Authorization", "Bearer x"}, calls: 1,
			cacheStatus: "Weir; fwd=uri-miss; fwd-status=200"},
		{calls: 2},
	}},

	// §4 Age on reuse: FR-FRS-7; §4.2.3 age calculation: FR-FRS-4; T-30.
	{sec: "9111 §4", name: "a reused response carries Age", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60"), calls: 1},
		{after: 5 * time.Second, calls: 1, resp: []string{"Age", "5"}},
	}},
	{sec: "9111 §4.2.3", name: "corrected age adds age_value, response delay and resident time", steps: []rfcStep{
		{origin: &testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Age": {"7"}}, Delay: 2 * time.Second}, calls: 1, resp: []string{"Age", "7"}},
		{after: 3 * time.Second, calls: 1, resp: []string{"Age", "12"}},
	}},
	{sec: "9111 §4.2.3", name: "origin Age shortens the remaining lifetime", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60", "Age", "50"), calls: 1},
		{after: 9 * time.Second, calls: 1},
		{after: 2 * time.Second, calls: 2},
	}},
	{sec: "9111 §4.2.3", name: "Date from a slow origin clock does not age the entry", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=300", "Date", rfcDate(-10*time.Minute)), calls: 1},
		{after: 4 * time.Minute, calls: 1},
	}},

	// §4.1 Vary and normalization: FR-KEY-7..11, §5.2.3; variant keying in M7.
	{sec: "9111 §4.1", name: "Vary selects the stored variant",
		cfg: func(c *weir.Config) { c.Forward.Allow = []string{"Accept-Language"} }, steps: []rfcStep{
			{origin: bh(200, "en", "Cache-Control", "max-age=60", "Vary", "Accept-Language"), hdr: []string{"Accept-Language", "en"}, calls: 1},
			{hdr: []string{"Accept-Language", "en"}, calls: 1, cacheStatus: "Weir; hit; ttl=60"},
			{hdr: []string{"Accept-Language", "fr"}, calls: 2, cacheStatus: "Weir; fwd=vary-miss; fwd-status=200; stored"},
		}},
	{sec: "9111 §4.1", name: "a field the cache does not forward matches as absent",
		steps: []rfcStep{
			{origin: bh(200, "en", "Cache-Control", "max-age=60", "Vary", "Accept-Language"), hdr: []string{"Accept-Language", "en"}, calls: 1},
			{hdr: []string{"Accept-Language", "fr"}, calls: 1},
		}},
	{sec: "9111 §4.1", name: "Vary: * never matches", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60", "Vary", "*"), calls: 1, cacheStatus: "Weir; fwd=uri-miss; fwd-status=200"},
		{calls: 2},
	}},

	{sec: "9110 §12.5.3", name: "Accept-Encoding is normalized to one bucket before forwarding", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60"), hdr: []string{"Accept-Encoding", "deflate, gzip;q=0.5, br;q=0.9"},
			calls: 1, sent: []string{"Accept-Encoding", "gzip"}},
	}},

	// §4.2.1 lifetime precedence and invalid directives: FR-FRS-1, FR-FRS-2.
	{sec: "9111 §4.2.1", name: "s-maxage beats max-age", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60, s-maxage=10"), calls: 1},
		{after: 9 * time.Second, calls: 1},
		{after: 2 * time.Second, calls: 2},
	}},
	{sec: "9111 §4.2.1", name: "max-age beats Expires", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=10", "Expires", rfcDate(time.Hour)), calls: 1},
		{after: 11 * time.Second, calls: 2},
	}},
	{sec: "9111 §4.2.1", name: "Expires minus Date", steps: []rfcStep{
		{origin: bh(200, "a", "Date", rfcDate(0), "Expires", rfcDate(30*time.Second)), calls: 1},
		{after: 29 * time.Second, calls: 1},
		{after: 2 * time.Second, calls: 2},
	}},
	{sec: "9111 §4.2.1", name: "invalid Expires is in the past", steps: []rfcStep{
		{origin: bh(200, "a", "Expires", "0"), calls: 1},
		{calls: 2},
	}},
	{sec: "9111 §4.2.1", name: "conflicting max-age values make the lifetime zero", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60, max-age=120"), calls: 1},
		{calls: 2},
	}},
	{sec: "9111 §1.2.2", name: "huge delta-seconds is clamped, not rejected", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=99999999999999999999"), calls: 1},
		{after: time.Hour, calls: 1},
	}},

	// §4.2.2 heuristic: FR-FRS-3.
	{sec: "9111 §4.2.2", name: "heuristic is 10% of Date minus Last-Modified", steps: []rfcStep{
		{origin: bh(200, "a", "Date", rfcDate(0), "Last-Modified", rfcDate(-5*time.Hour)), calls: 1},
		{after: 29 * time.Minute, calls: 1},
		{after: 2 * time.Minute, calls: 2},
	}},
	{sec: "9111 §4.2.2", name: "heuristic is capped at one hour", steps: []rfcStep{
		{origin: bh(200, "a", "Date", rfcDate(0), "Last-Modified", rfcDate(-100*time.Hour)), calls: 1},
		{after: 59 * time.Minute, calls: 1},
		{after: 2 * time.Minute, calls: 2},
	}},

	// §4.3.1 validation headers: FR-SRV-3; §5.2.2.4 response no-cache: FR-SRV-1.
	{sec: "9111 §4.3.1", name: "validation sends If-None-Match and If-Modified-Since", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=1", "ETag", `"v1"`, "Last-Modified", rfcDate(-time.Hour)), calls: 1},
		{after: 2 * time.Second, calls: 2, sent: []string{"If-None-Match", `"v1"`, "If-Modified-Since", rfcDate(-time.Hour)}},
	}},
	{sec: "9111 §5.2.2.4", name: "response no-cache validates every reuse", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60, no-cache", "ETag", `"v1"`), calls: 1},
		{calls: 2, sent: []string{"If-None-Match", `"v1"`}},
	}},
	{sec: "9111 §5.2.2.4", name: "qualified no-cache is treated as unqualified", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", `max-age=60, no-cache="X-A"`, "ETag", `"v1"`), calls: 1},
		{calls: 2, sent: []string{"If-None-Match", `"v1"`}},
	}},

	// §4.3.2 client conditionals: FR-SRV-2.
	{sec: "9111 §4.3.2", name: "matching If-None-Match gets 304 from the entry", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60", "ETag", `"v1"`, "Content-Type", "text/plain"), calls: 1},
		{hdr: []string{"If-None-Match", `"v1"`}, status: 304, calls: 1, resp: []string{"ETag", `"v1"`, "Cache-Control", "max-age=60", "Content-Type", ""}},
	}},
	{sec: "9111 §4.3.2", name: "If-None-Match uses weak comparison", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60", "ETag", `W/"v1"`), calls: 1},
		{hdr: []string{"If-None-Match", `"x", "v1"`}, status: 304, calls: 1},
	}},
	{sec: "9111 §4.3.2", name: "non-matching If-None-Match gets the full response", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60", "ETag", `"v1"`), calls: 1},
		{hdr: []string{"If-None-Match", `"v2"`}, calls: 1, body: "a"},
	}},
	{sec: "9111 §4.3.2", name: "a stored non-200 answers a matching conditional in full", steps: []rfcStep{
		{origin: bh(404, "gone", "Cache-Control", "max-age=60", "ETag", `"v1"`), status: 404, calls: 1},
		{hdr: []string{"If-None-Match", `"v1"`}, status: 404, calls: 1, body: "gone"},
	}},
	{sec: "9110 §13.2.2", name: "If-None-Match takes precedence over If-Modified-Since", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60", "ETag", `"v1"`, "Last-Modified", rfcDate(-time.Hour)), calls: 1},
		{hdr: []string{"If-None-Match", `"v2"`, "If-Modified-Since", rfcDate(0)}, calls: 1, body: "a"},
	}},
	{sec: "9111 §4.3.2", name: "If-Modified-Since at or after Last-Modified gets 304", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60", "Last-Modified", rfcDate(-time.Hour)), calls: 1},
		{hdr: []string{"If-Modified-Since", rfcDate(-time.Hour)}, status: 304, calls: 1},
		{hdr: []string{"If-Modified-Since", rfcDate(-2 * time.Hour)}, calls: 1, body: "a"},
	}},

	// §4.3.4 freshening with 304: FR-SRV-3.
	{sec: "9111 §4.3.4", name: "304 freshens the stored response", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=1", "ETag", `"v1"`, "X-Ver", "1", "Content-Type", "text/plain"), calls: 1},
		{after: 2 * time.Second, origin: bh(304, "", "Cache-Control", "max-age=60", "ETag", `"v1"`, "X-Ver", "2", "Content-Type", "text/html"),
			calls: 2, body: "a", resp: []string{"X-Ver", "2", "Content-Type", "text/plain"}},
		// a 304 without Date dates the freshened entry at receipt (FR-STO-13)
		{after: 30 * time.Second, calls: 2, body: "a", resp: []string{"Date", rfcDate(2 * time.Second)}},
	}},
	{sec: "9111 §4.3.4", name: "304 with a different strong ETag is retried unconditionally", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=1", "ETag", `"v1"`), calls: 1},
		{after: 2 * time.Second, origin: &testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			if r.Header.Get("If-None-Match") != "" {
				return &weir.Response{StatusCode: 304, Header: http.Header{"Etag": {`"v2"`}}, Body: http.NoBody}, nil
			}
			return &weir.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"v2"`}},
				Body: io.NopCloser(strings.NewReader("b"))}, nil
		}}, calls: 3, body: "b", sent: []string{"If-None-Match", ""}},
	}},

	// §4.4 invalidation: FR-INV-1, FR-INV-3.
	{sec: "9111 §4.4", name: "2xx to an unsafe method marks the target URI invalid", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60", "ETag", `"v1"`), calls: 1},
		{method: "POST", calls: 2},
		{calls: 3, sent: []string{"If-None-Match", `"v1"`}},
	}},
	{sec: "9111 §4.4", name: "3xx to an unsafe method invalidates", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60"), calls: 1},
		{method: "POST", origin: bh(303, "", "Location", "/done"), status: 303, calls: 2},
		{origin: bh(200, "a", "Cache-Control", "max-age=60"), calls: 3},
	}},
	{sec: "9111 §4.4", name: "same-origin Content-Location is invalidated", steps: []rfcStep{
		{path: "/other", origin: bh(200, "a", "Cache-Control", "max-age=60"), calls: 1},
		{method: "PUT", origin: bh(200, "", "Content-Location", "https://example.com/other"), calls: 2},
		{path: "/other", origin: bh(200, "a", "Cache-Control", "max-age=60"), calls: 3},
	}},
	{sec: "9111 §4.4", name: "unknown method invalidates too", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60"), calls: 1},
		{method: "FROB", calls: 2},
		{calls: 3},
	}},
	{sec: "9111 §4.4", name: "an error response to an unsafe method does not invalidate", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60"), calls: 1},
		{method: "DELETE", origin: bh(500, ""), status: 500, calls: 2},
		{calls: 2},
	}},
	{sec: "9111 §4.4", name: "same-origin Location is invalidated", steps: []rfcStep{
		{path: "/other", origin: bh(200, "a", "Cache-Control", "max-age=60"), calls: 1},
		{method: "POST", origin: bh(201, "", "Location", "/other"), status: 201, calls: 2},
		{path: "/other", origin: bh(200, "a", "Cache-Control", "max-age=60"), calls: 3},
	}},
	{sec: "9111 §4.4", name: "cross-origin Location is not invalidated", steps: []rfcStep{
		{host: "other.example", path: "/other", origin: bh(200, "a", "Cache-Control", "max-age=60"), calls: 1},
		{method: "POST", origin: bh(201, "", "Location", "https://other.example/other"), status: 201, calls: 2},
		{host: "other.example", path: "/other", calls: 2},
	}},
	{sec: "9875 §3", name: "Cache-Group-Invalidation invalidates the group", tag: "M9", steps: []rfcStep{
		{path: "/other", origin: bh(200, "a", "Cache-Control", "max-age=60", "Cache-Groups", `"g"`), calls: 1},
		{method: "POST", origin: bh(200, "", "Cache-Group-Invalidation", `"g"`), calls: 2},
		{path: "/other", origin: bh(200, "a", "Cache-Control", "max-age=60"), calls: 3},
	}},

	// §5.2.1 request directives: FR-SRV-6, FR-SRV-8 (D5).
	{sec: "9111 §5.2.1.7", name: "only-if-cached without an entry is 504", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60"), hdr: []string{"Cache-Control", "only-if-cached"}, err: weir.ErrOnlyIfCached, calls: 0},
		{calls: 1},
		{hdr: []string{"Cache-Control", "only-if-cached"}, calls: 1, body: "a"},
		{after: 61 * time.Second, hdr: []string{"Cache-Control", "only-if-cached"}, err: weir.ErrOnlyIfCached, calls: 1},
	}},
	{sec: "9111 §5.2.1.7", name: "only-if-cached wins over a forced validation",
		cfg: func(c *weir.Config) { c.Client.HonorRevalidation = true }, steps: []rfcStep{
			{origin: bh(200, "a", "Cache-Control", "max-age=60", "ETag", `"v1"`), calls: 1},
			{hdr: []string{"Cache-Control", "only-if-cached, no-cache"}, calls: 1, body: "a"},
		}},
	{sec: "9111 §5.2.1.4", name: "request no-cache is advisory by default", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60", "ETag", `"v1"`), calls: 1},
		{hdr: []string{"Cache-Control", "no-cache"}, calls: 1},
	}},
	{sec: "9111 §5.2.1.4", name: "request no-cache validates with HonorRevalidation",
		cfg: func(c *weir.Config) { c.Client.HonorRevalidation = true }, steps: []rfcStep{
			{origin: bh(200, "a", "Cache-Control", "max-age=60", "ETag", `"v1"`), calls: 1},
			{hdr: []string{"Cache-Control", "no-cache"}, calls: 2, sent: []string{"If-None-Match", `"v1"`}},
		}},

	// §5.2.2 response directives.
	{sec: "9111 §5.2.2.2", name: "must-revalidate entry that cannot be validated is 504", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=1, must-revalidate", "ETag", `"v1"`), calls: 1},
		{after: 2 * time.Second, origin: &testorigin.Behavior{Err: errors.New("down")}, err: weir.ErrMustRevalidate, calls: 2},
	}},
	{sec: "9111 §5.2.2.3", name: "must-understand with a known status overrides no-store", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60, must-understand, no-store"), calls: 1},
		{calls: 1},
	}},
	{sec: "9111 §5.2.2.3", name: "must-understand with a status outside the storable set keeps no-store", steps: []rfcStep{
		{origin: bh(299, "a", "Cache-Control", "max-age=60, must-understand, no-store"), status: 299, calls: 1},
		{status: 299, calls: 2},
	}},

	// RFC 5861: FR-STL-1, FR-STL-2, M5.
	{sec: "5861 §3", name: "stale-while-revalidate serves stale and refreshes", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=600, stale-while-revalidate=30"), calls: 1},
		{after: 610 * time.Second, origin: bh(200, "b", "Cache-Control", "max-age=600"), body: "a", calls: 2,
			cacheStatus: "Weir; hit; ttl=-10; detail=stale-while-revalidate"},
		{after: time.Second, body: "b", calls: 2},
	}},
	{sec: "5861 §3", name: "stale-while-revalidate refresh validates and freshens on 304", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=600, stale-while-revalidate=30", "ETag", `"v1"`), calls: 1},
		{after: 610 * time.Second, origin: bh(304, "", "Cache-Control", "max-age=600", "ETag", `"v1"`), body: "a", calls: 2,
			sent: []string{"If-None-Match", `"v1"`}},
		{after: time.Second, body: "a", calls: 2, cacheStatus: "Weir; hit; ttl=599"},
	}},
	{sec: "5861 §4", name: "stale-if-error serves stale on a 500", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=600, stale-if-error=1200"), calls: 1},
		{after: 900 * time.Second, origin: bh(500, ""), body: "a", calls: 2,
			cacheStatus: "Weir; hit; ttl=-300; detail=stale-if-error"},
	}},
	{sec: "5861 §4", name: "stale-if-error serves stale on a 502, 503 or 504", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=600, stale-if-error=1200"), calls: 1},
		{after: 900 * time.Second, origin: bh(502, ""), body: "a", calls: 2},
		{origin: bh(503, ""), body: "a", calls: 3},
		{origin: bh(504, ""), body: "a", calls: 4},
	}},
	{sec: "5861 §4", name: "stale-if-error serves stale when the origin cannot be reached", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=600, stale-if-error=1200"), calls: 1},
		{after: 900 * time.Second, origin: &testorigin.Behavior{Err: errors.New("down")}, body: "a", calls: 2},
	}},
	{sec: "5861 §4", name: "stale-if-error past its window passes the 500 through", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=600, stale-if-error=1200"), calls: 1},
		{after: 1801 * time.Second, origin: bh(500, "e"), status: 500, body: "e", calls: 2},
	}},
	{sec: "5861 §4", name: "stale-if-error does not cover a 404", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=600, stale-if-error=1200"), calls: 1},
		{after: 900 * time.Second, origin: bh(404, "nf"), status: 404, body: "nf", calls: 2},
	}},

	// RFC 9211 Cache-Status: FR-SRV-9.
	{sec: "9211 §2", name: "hit carries ttl, revalidation carries fwd-status 304", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60", "ETag", `"v1"`), calls: 1, cacheStatus: "Weir; fwd=uri-miss; fwd-status=200; stored"},
		{after: 10 * time.Second, calls: 1, cacheStatus: "Weir; hit; ttl=50"},
		{after: 51 * time.Second, origin: bh(304, "", "Cache-Control", "max-age=60", "ETag", `"v1"`), calls: 2,
			cacheStatus: "Weir; fwd=stale; fwd-status=304; stored"},
	}},
	{sec: "9211 §2", name: "a member is appended after the origin's", steps: []rfcStep{
		{origin: bh(200, "a", "Cache-Control", "max-age=60", "Cache-Status", "CDN; hit"), calls: 1,
			cacheStatus: "CDN; hit, Weir; fwd=uri-miss; fwd-status=200; stored"},
	}},
}

// TestRFC9111 runs the RFC behavior table (07 §7). Each row cites the RFC
// section; the FR IDs are on the row groups above.
func TestRFC9111(t *testing.T) {
	for _, row := range rfcRows {
		t.Run(row.sec+" "+row.name, func(t *testing.T) {
			if row.tag != "" {
				t.Skipf("needs %s", row.tag)
			}
			synctest.Test(t, func(t *testing.T) { runRFCRow(t, row) })
		})
	}
}

func runRFCRow(t *testing.T, row rfcRow) {
	cfg := cacheCfg
	if row.cfg != nil {
		row.cfg(&cfg)
	}
	e := newEngine(t, cfg)
	defer closeEngine(t, e)
	o := testorigin.NewChecked(t, 64, 16)

	for i, s := range row.steps {
		time.Sleep(s.after)
		if s.origin != nil {
			o.Default(*s.origin)
		}
		req := getReq(cmp.Or(s.path, "/r"))
		req.Method = cmp.Or(s.method, "GET")
		req.Host = cmp.Or(s.host, req.Host)
		for j := 0; j+1 < len(s.hdr); j += 2 {
			req.Header.Add(s.hdr[j], s.hdr[j+1])
		}

		resp, err := e.Serve(t.Context(), req, o)
		if s.err != nil || err != nil {
			if !errors.Is(err, s.err) {
				t.Fatalf("step %d: err %v, want %v", i, err, s.err)
			}
		} else {
			b, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if rerr != nil {
				t.Fatalf("step %d: read body: %v", i, rerr)
			}
			if want := cmp.Or(s.status, 200); resp.StatusCode != want {
				t.Errorf("step %d: status %d, want %d", i, resp.StatusCode, want)
			}
			if s.body != "" && string(b) != s.body {
				t.Errorf("step %d: body %q, want %q", i, b, s.body)
			}
			if s.cacheStatus != "" {
				if got := strings.Join(resp.Header.Values("Cache-Status"), ", "); got != s.cacheStatus {
					t.Errorf("step %d: Cache-Status %q, want %q", i, got, s.cacheStatus)
				}
			}
			checkHeader(t, i, "response", resp.Header, s.resp)
		}
		synctest.Wait() // count a background refresh in the step that started it
		if n := o.TotalCalls(); n != s.calls {
			t.Fatalf("step %d: origin calls %d, want %d", i, n, s.calls)
		}
		if s.sent != nil {
			reqs := o.Requests()
			checkHeader(t, i, "forwarded", reqs[len(reqs)-1].Header, s.sent)
		}
	}
}

func checkHeader(t *testing.T, step int, what string, h http.Header, kv []string) {
	t.Helper()
	for j := 0; j+1 < len(kv); j += 2 {
		if got := h.Get(kv[j]); got != kv[j+1] {
			t.Errorf("step %d: %s %s = %q, want %q", step, what, kv[j], got, kv[j+1])
		}
	}
}
