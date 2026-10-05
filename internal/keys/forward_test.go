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
		Allow: []string{"X-Tenant", "Upgrade", "Http2-Settings"}, NoTraceHeaders: true, AcceptEncoding: []string{"br", "gzip"},
	},
	{
		MaxPathBytes: 256, MaxQueryBytes: 256, MaxQueryParams: 8, MaxKeyedHeaderBytes: 128,
		Cookies: []string{"a"}, AcceptEncoding: []string{"zstd", "gzip"}, HonorRevalidation: true,
	},
}

// fuzzKeyedCfg has Key.Headers, including names the forward rewrites (the
// bucket, keyed cookies), filters (trace) or drops (Range, hop-by-hop). Bit
// 0x40 of the selector picks it, so older corpus entries keep their config.
var fuzzKeyedCfg = Config{
	MaxPathBytes: 256, MaxQueryBytes: 256, MaxQueryParams: 8, MaxKeyedHeaderBytes: 128,
	Headers: []string{"X-Tenant", "Tracestate", "Accept-Encoding", "Cookie", "Range", "Connection", "X-Forwarded-Host"},
	Cookies: []string{"sid"}, AcceptEncoding: []string{"gzip"},
}

// fuzzAllCfg is ForwardAll with Key.Headers, picked by selector bit 0x20.
// Unkeyed fields reach the origin by design (G4), so only the keyed part of
// the property holds: the forward carries the normal form the key holds.
var fuzzAllCfg = Config{
	MaxPathBytes: 256, MaxQueryBytes: 256, MaxQueryParams: 8, MaxKeyedHeaderBytes: 128,
	ForwardAll: true, Headers: []string{"X-Tenant", "X-Forwarded-Host"}, Cookies: []string{"sid"}, AcceptEncoding: []string{"gzip"},
}

func FuzzForwardEqualsKey(f *testing.F) {
	// INV-1, FR-FWD-1, FR-FWD-5, FR-FWD-6, FR-UPG-1; T-1, T-2, T-5, T-44
	f.Add(byte(0), "GET", "/a", "a=1&utm_x=2", "lang=en; sid=1; x=2", "gzip;q=0.5, br", "evil", "X-Original-Url", "")
	f.Add(byte(1), "HEAD", "/%7e/%2f", "b2=1&a=0&c=3", "lang=en", "*", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "host", "")
	// T-40: two Tracestate lines whose combined length straddles the 512-byte
	// W3C limit filterTrace sums across lines.
	f.Add(byte(2), "GET", "/", "", "a=1; a=2", "zstd;q=0", "", "content-length", strings.Repeat("a", 510))
	// FR-UPG-1: a lone h2c upgrade is served; Allow names Upgrade and
	// HTTP2-Settings in config 1, and neither may reach the origin.
	f.Add(byte(0x82), "GET", "/a", "", "", "", "AAMAAABkAAQAAP__", "Http2-Settings", "")
	// FR-VAL-3: keyed header values that the generic normalizer rewrites
	// (two lines, whitespace around commas) or maps to absent.
	f.Add(byte(0x40), "GET", "/a", "", "sid=1; x=2", "gzip", " a ,b", "X-Tenant", " c ")
	f.Add(byte(0x40), "GET", "/a", "", "", "", "a\x00b", "Range", "")
	f.Add(byte(0x40), "GET", "/a", "", "", "", "", "X-Tenant", "")
	f.Add(byte(0x40), "GET", "/a", "", "", "", strings.Repeat("x", 127), "X-Tenant", "y")
	f.Add(byte(0x40), "GET", "/a", "", "", "", "\xff", "X-Tenant", "a")
	f.Add(byte(0x40), "GET", "/a", "", "", "", " , ,, ", "X-Tenant", ",")
	f.Add(byte(0x20), "GET", "/a", "", "sid=1", "br", " a ,b", "X-Tenant", "c\x00")
	// FR-KEY-6 under ForwardAll: Connection names Cookie, so no cookie is
	// forwarded and none may be keyed.
	f.Add(byte(0x20), "GET", "/a", "", "sid=1", "", "cookie", "X-Tenant", "")
	f.Fuzz(func(t *testing.T, sel byte, method, path, query, cookie, ae, extra, name, tracestate2 string) {
		cfg := &fuzzCfgs[int(sel)%len(fuzzCfgs)]
		switch {
		case sel&0x40 != 0:
			cfg = &fuzzKeyedCfg
		case sel&0x20 != 0:
			cfg = &fuzzAllCfg
		}
		r := &Request{Method: method, Scheme: "https", Host: "example.com", Path: path, RawQuery: query,
			Header: http.Header{
				"Cookie": {cookie}, "Accept-Encoding": {ae},
				"X-Forwarded-Host": {extra}, "X-Tenant": {extra}, "Traceparent": {extra},
				"Tracestate":   {extra, tracestate2},
				"X-Request-Id": {extra}, "If-None-Match": {extra}, "Range": {extra}, "Connection": {extra},
			}}
		if sel&0x60 != 0 {
			// A keyed header in several lines (FR-VAL-3).
			r.Header["X-Tenant"] = []string{extra, tracestate2}
		}
		if sel&0x80 != 0 {
			// The h2c upgrade shape: the high bit of sel adds it to any config.
			r.Header["Connection"] = []string{extra, "Upgrade"} // HTTP2-Settings not named
			r.Header["Upgrade"] = []string{"h2c"}
			r.Header["Http2-Settings"] = []string{extra}
		}
		if isToken(name) {
			// Any field net/http would accept, under its canonical name.
			r.Header[http.CanonicalHeaderKey(name)] = []string{extra}
		}
		before := r.Header.Clone()
		c, err := Classify(r, cfg)
		if !equalHeader(r.Header, before) {
			t.Fatalf("client header mutated: %v, was %v", r.Header, before)
		}
		if err != nil {
			return
		}
		if c.Class == ClassPass {
			for _, name := range append(hopByHop, "Host") {
				if _, ok := c.Forwarded.Header[name]; ok {
					t.Fatalf("pass request forwards %s", name)
				}
			}
			return
		}
		f := c.Forwarded
		if f.Method != http.MethodGet || f.Body != nil {
			t.Fatalf("forwarded method %q, body %v", f.Method, f.Body)
		}
		allowed := append([]string{"Accept-Encoding", "Cookie", "Authorization", "Cache-Control", "Pragma"}, cfg.Allow...)
		allowed = append(allowed, cfg.Headers...)
		for _, name := range cfg.Headers {
			if len(f.Header[name]) > 1 {
				t.Fatalf("keyed header %s forwarded as %d lines; the key holds one value", name, len(f.Header[name]))
			}
		}
		if !cfg.NoTraceHeaders {
			allowed = append(allowed, "Traceparent", "Tracestate", "X-Request-Id")
		}
		for name := range f.Header {
			if name == "Connection" || name == "Upgrade" || name == "Http2-Settings" {
				t.Fatalf("forwarded hop-by-hop header %q", name)
			}
			if indexOf(allowed, name) < 0 && !cfg.ForwardAll {
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
		// FR-VAL-3: the limit counts the keyed pair as forwarded, after OWS
		// trimming, not the received line.
		if visibleASCII(w) && len("lang=")+len(w) <= cfg.MaxKeyedHeaderBytes {
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
