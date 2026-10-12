package keys

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// forwardHeaderRef is forwardHeader before the strict-mode shortcut that
// skips deletes of names the header cannot hold. FuzzForwardHeaderMatchesReference
// holds the two equal on every header and config, so the shortcut cannot
// change what the origin sees (INV-1, T-1).
func forwardHeaderRef(h http.Header, c *Config, cookies []Cookie, dims []Dim) (out http.Header, unkeyed bool) {
	if c.ForwardAll {
		unkeyed = true
		out = h.Clone()
		if out == nil {
			out = http.Header{}
		}
	} else {
		out = http.Header{}
		for _, name := range defaultForward {
			copyField(out, h, name)
		}
		for _, name := range c.Allow {
			copyField(out, h, name)
		}
	}
	// After the Allow copy, so a name in both lists (New rejects that) goes
	// in its keyed form; unkeyed below still counts it, which only costs
	// markers. FR-VAL-3, T-13: a value with no normal form is
	// absent in the forward, and so in the key (keyedHeaders).
	for _, name := range c.Headers {
		if v, ok := normalizeHeader(h[name], c.MaxKeyedHeaderBytes); ok {
			out[name] = []string{v}
		} else {
			delete(out, name)
		}
	}
	// An Allow entry, a keyed name or a Connection option can name any
	// field; hop-by-hop fields still never go.
	DropHopByHop(out, h["Connection"])
	if !c.ForwardAll {
		delete(out, "Cookie") // only keyed cookies, even if Allow or Key.Headers names Cookie
		if v := cookieHeader(cookies); v != "" {
			out["Cookie"] = []string{v}
		}
	}
	for _, name := range dropped {
		delete(out, name)
	}
	if c.ExpCookie != "" {
		stripCookie(out, c.ExpCookie) // T-35, INV-1: ForwardAll would send the visitor id unkeyed
	}
	setVariantHeader(out, c.ExpHeader, dims)
	filterTrace(out, c.NoTraceHeaders)
	if !c.ForwardAll {
		filterDirectives(out, c.Headers)
	}
	for _, name := range c.Allow {
		// T-31: an Allow field reaches the origin unkeyed. Cookie goes
		// keyed-only, and Accept-Encoding goes as the bucket every request
		// carries whether or not Allow names it.
		keyed := name == "Cookie" || name == "Accept-Encoding" || slices.Contains(defaultForward, name)
		unkeyed = unkeyed || !keyed && len(out[name]) > 0
	}
	// T-31: the cache directives go as the client sent them once
	// filterDirectives has bounded their bytes and length, so an origin or
	// WAF that rejects one must not fail the key for everyone. Keyed, they
	// went in normal form and split the key.
	for _, name := range [...]string{"Cache-Control", "Pragma"} {
		unkeyed = unkeyed || len(out[name]) > 0 && !slices.Contains(c.Headers, name)
	}
	dropControlBytes(out)
	// Set last so no Connection option or Allow entry can remove or replace it.
	out["Accept-Encoding"] = []string{aeBucket(h["Accept-Encoding"], c)}
	return out, unkeyed
}

var refNames = []string{
	"Authorization", "Cache-Control", "Pragma", "Traceparent", "Tracestate", "X-Request-Id",
	"Cookie", "Connection", "Upgrade", "Keep-Alive", "Te", "Transfer-Encoding", "Host", "Http2-Settings",
	"If-None-Match", "If-Modified-Since", "If-Match", "If-Unmodified-Since", "If-Range", "Range",
	"Content-Length", "Expect", "Trailer", "X-Tenant", "Accept-Encoding", "X-Other",
}

var refConfigs = []Config{
	{MaxKeyedHeaderBytes: 128, AcceptEncoding: []string{"gzip"}},
	{MaxKeyedHeaderBytes: 128, AcceptEncoding: []string{"gzip"}, NoTraceHeaders: true},
	{MaxKeyedHeaderBytes: 128, AcceptEncoding: []string{"gzip"}, Allow: []string{"X-Tenant", "Range", "Te", "Cookie"}},
	{MaxKeyedHeaderBytes: 128, AcceptEncoding: []string{"gzip"}, Headers: []string{"X-Tenant", "Content-Length", "Cookie", "Connection"}},
	{MaxKeyedHeaderBytes: 128, AcceptEncoding: []string{"gzip"}, ForwardAll: true},
	{MaxKeyedHeaderBytes: 128, AcceptEncoding: []string{"gzip"}, ExpCookie: "weir_v", ExpHeader: "Weir-Variant"},
}

func FuzzForwardHeaderMatchesReference(f *testing.F) {
	f.Add([]byte{0, 1, 2, 7, 3}, "x", "y, Authorization", uint8(0))
	f.Add([]byte{7, 14, 15, 19, 20, 21}, "keep-alive, Te", "z", uint8(2))
	f.Add([]byte{6, 7, 23}, "Cookie", "a=b", uint8(3))
	// FR-FWD-1: Cache-Control (1) and Pragma (2) values the directive filter
	// must judge: empty, 10 KiB, non-ASCII, separators only, markup, and
	// lines that pass one by one but not together.
	f.Add([]byte{1, 2}, "", "", uint8(0))
	f.Add([]byte{1, 1, 2}, "", strings.Repeat("a", 10<<10), uint8(0))
	f.Add([]byte{1, 2}, "", "no-cach\xe9", uint8(0))
	f.Add([]byte{1, 2, 1}, "", ",, ,=", uint8(1))
	f.Add([]byte{1, 2}, "", `x="<script>"`, uint8(3))
	f.Add([]byte{1, 1, 1, 2, 2, 2}, "", strings.Repeat("a", 100), uint8(2))
	f.Fuzz(func(t *testing.T, pick []byte, conn, val string, sel uint8) {
		h := http.Header{}
		for i, b := range pick {
			if i > 24 {
				break
			}
			name := refNames[int(b)%len(refNames)]
			v := val
			if name == "Connection" {
				v = conn
			}
			h[name] = append(h[name], v)
		}
		c := &refConfigs[int(sel)%len(refConfigs)]
		cookies := keyedCookies(h["Cookie"], c)
		got, gu := forwardHeader(h, c, cookies, nil)
		want, wu := forwardHeaderRef(h, c, cookies, nil)
		if gu != wu || !reflect.DeepEqual(got, want) {
			t.Fatalf("forwardHeader = %v (%v), reference %v (%v), header %v config %d", got, gu, want, wu, h, int(sel)%len(refConfigs))
		}
	})
}
