package keys

import (
	"net/http"
	"strings"
	"testing"
)

// fuzzCfgs are the strict-mode config variants FuzzForwardEqualsKey picks
// from. ForwardAll is excluded: it forwards unkeyed headers by design (G4).
var fuzzCfgs = []Config{
	{
		MaxPathBytes: 256, MaxQueryBytes: 256, MaxQueryParams: 8, MaxKeyedHeaderBytes: 128,
		QueryDrop: []string{"utm_*"}, Cookies: []string{"lang", "sid"}, AcceptEncoding: []string{"gzip"},
	},
	{
		MaxPathBytes: 256, MaxQueryBytes: 256, MaxQueryParams: 8, MaxKeyedHeaderBytes: 128,
		NormalizePath: true, QuerySort: true, QueryKeep: []string{"a", "b*"},
		Allow: []string{"X-Tenant"}, NoTraceHeaders: true, AcceptEncoding: []string{"br", "gzip"},
	},
	{
		MaxPathBytes: 256, MaxQueryBytes: 256, MaxQueryParams: 8, MaxKeyedHeaderBytes: 128,
		Cookies: []string{"a"}, AcceptEncoding: []string{"zstd", "gzip"}, HonorRevalidation: true,
	},
}

func FuzzForwardEqualsKey(f *testing.F) {
	// INV-1, FR-FWD-1, FR-FWD-5, FR-FWD-6; T-1, T-2, T-5
	f.Add(byte(0), "GET", "/a", "a=1&utm_x=2", "lang=en; sid=1; x=2", "gzip;q=0.5, br", "evil")
	f.Add(byte(1), "HEAD", "/%7e/%2f", "b2=1&a=0&c=3", "lang=en", "*", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	f.Add(byte(2), "GET", "/", "", "a=1; a=2", "zstd;q=0", "")
	f.Fuzz(func(t *testing.T, sel byte, method, path, query, cookie, ae, extra string) {
		cfg := &fuzzCfgs[int(sel)%len(fuzzCfgs)]
		r := &Request{Method: method, Scheme: "https", Host: "example.com", Path: path, RawQuery: query,
			Header: http.Header{
				"Cookie": {cookie}, "Accept-Encoding": {ae},
				"X-Forwarded-Host": {extra}, "X-Tenant": {extra}, "Traceparent": {extra}, "Tracestate": {extra},
				"X-Request-Id": {extra}, "If-None-Match": {extra}, "Range": {extra}, "Connection": {extra},
			}}
		c, err := Classify(r, cfg)
		if err != nil || c.Class != ClassCacheable {
			return
		}
		f := c.Forwarded
		if f.Method != http.MethodGet || f.Body != nil {
			t.Fatalf("forwarded method %q, body %v", f.Method, f.Body)
		}
		allowed := append([]string{"Accept-Encoding", "Cookie", "Authorization", "Cache-Control", "Pragma"}, cfg.Allow...)
		if !cfg.NoTraceHeaders {
			allowed = append(allowed, "Traceparent", "Tracestate", "X-Request-Id")
		}
		for name := range f.Header {
			if indexOf(allowed, name) < 0 {
				t.Fatalf("forwarded unkeyed header %q", name)
			}
		}
		// Rebuild the key from the forwarded request alone.
		again, err := Classify(&f, cfg)
		if err != nil {
			t.Fatalf("forwarded request fails validation: %v", err)
		}
		if again.Primary != c.Primary {
			t.Fatalf("key of forwarded request differs: %+v", f)
		}
		if !equalHeader(again.Forwarded.Header, f.Header) {
			t.Fatalf("forwarding is not idempotent: %v then %v", f.Header, again.Forwarded.Header)
		}
	})
}

func FuzzMalformedHeaderAbsent(f *testing.F) {
	// INV-3, FR-KEY-6, FR-VAL-3; T-13
	f.Add("en", "gzip")
	f.Add("e\x00n", "gzip;q=x")
	f.Add("en \t", "\xff")
	f.Add(strings.Repeat("x", 200), "br")
	f.Fuzz(func(t *testing.T, v, ae string) {
		if strings.ContainsRune(v, ';') {
			return
		}
		cfg := &fuzzCfgs[0]
		line := "lang=" + v
		r := &Request{Method: "GET", Scheme: "https", Host: "example.com", Path: "/",
			Header: http.Header{"Cookie": {line}, "Accept-Encoding": {ae}}}
		c, err := Classify(r, cfg)
		if err != nil {
			t.Fatal(err)
		}
		w := strings.TrimRight(v, " \t")
		var want []Cookie
		if visibleASCII(w) && len(line) <= cfg.MaxKeyedHeaderBytes {
			want = []Cookie{{"lang", w}}
		}
		if got := c.Forwarded.Header.Get("Cookie"); got != cookieHeader(want) {
			t.Fatalf("forwarded Cookie %q, want %q", got, cookieHeader(want))
		}
		in := &KeyInput{Method: "GET", Scheme: "https", Host: "example.com", Path: "/",
			CookieNames: cfg.Cookies, Cookies: want}
		if c.Primary != PrimaryKey(in) {
			t.Fatal("key disagrees with the forwarded cookies")
		}
		if got := c.Forwarded.Header.Get("Accept-Encoding"); got != "identity" && indexOf(cfg.AcceptEncoding, got) < 0 {
			t.Fatalf("Accept-Encoding bucket %q outside the fixed set", got)
		}
	})
}
