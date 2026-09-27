package keys

import (
	"net/http"
	"slices"
	"strings"
)

// hopByHop are the fields RFC 9110 §7.6.1 says a proxy never forwards.
var hopByHop = []string{"Connection", "Keep-Alive", "Proxy-Connection", "Te", "Transfer-Encoding", "Upgrade"}

// dropped are the client preconditions and ranges a cacheable fetch never
// carries (FR-FWD-1): the stored response must be the full one.
var dropped = []string{"If-None-Match", "If-Modified-Since", "If-Match", "If-Unmodified-Since", "If-Range", "Range"}

// forwardHeader builds the forwarded header of a cacheable request (04 §3.5).
// In strict mode (T-1, INV-1) it holds only keyed fields, the cache
// directives and Authorization (FR-FWD-1), and operator-allowed and trace
// fields. cookies is the keyedCookies result the key was built from.
func forwardHeader(h http.Header, c *Config, cookies []Cookie) http.Header {
	var out http.Header
	if c.ForwardAll {
		out = h.Clone()
		if out == nil {
			out = http.Header{}
		}
		dropHopByHop(out, h["Connection"])
	} else {
		out = http.Header{}
		for _, name := range []string{"Authorization", "Cache-Control", "Pragma", "Traceparent", "Tracestate", "X-Request-Id"} {
			copyField(out, h, name)
		}
		for _, name := range c.Allow {
			copyField(out, h, name)
		}
		// An Allow entry or a Connection option can name any copied field;
		// hop-by-hop fields still never go.
		dropHopByHop(out, h["Connection"])
		delete(out, "Cookie") // only keyed cookies, even if Allow names Cookie
		if v := cookieHeader(cookies); v != "" {
			out["Cookie"] = []string{v}
		}
	}
	for _, name := range dropped {
		delete(out, name)
	}
	filterTrace(out, c.NoTraceHeaders)
	// Set last so no Connection option or Allow entry can remove or replace it.
	out["Accept-Encoding"] = []string{aeBucket(h["Accept-Encoding"], c)}
	return out
}

func copyField(dst, src http.Header, name string) {
	if v := src[name]; len(v) > 0 {
		dst[name] = slices.Clone(v) // the forwarded request can outlive the client's
	}
}

// dropHopByHop deletes from h the hop-by-hop fields and the fields named
// in the client's Connection lines conn (RFC 9110 §7.6.1).
func dropHopByHop(h http.Header, conn []string) {
	for _, line := range conn {
		for opt := range strings.SplitSeq(line, ",") {
			if opt = trimOWS(opt); opt != "" {
				delete(h, http.CanonicalHeaderKey(opt))
			}
		}
	}
	for _, name := range hopByHop {
		delete(h, name)
	}
}

// filterTrace applies FR-FWD-6 (T-40): trace fields that reach the origin
// have a validated shape, and none do with NoTraceHeaders.
func filterTrace(h http.Header, none bool) {
	if tp := h["Traceparent"]; none || len(tp) != 1 || !validTraceparent(tp[0]) {
		delete(h, "Traceparent")
		delete(h, "Tracestate")
	}
	if id := h["X-Request-Id"]; none || len(id) > 0 && (len(id) != 1 || len(id[0]) > 128 || !visibleASCII(id[0])) {
		delete(h, "X-Request-Id")
	}
}

// validTraceparent accepts W3C Trace Context version 00 only:
// 00-<32 lowercase hex>-<16 lowercase hex>-<2 lowercase hex>, ids not all zero.
func validTraceparent(s string) bool {
	const zeroTrace, zeroParent = "00000000000000000000000000000000", "0000000000000000"
	if len(s) != 55 || s[:3] != "00-" || s[35] != '-' || s[52] != '-' {
		return false
	}
	return lowerHex(s[3:35]) && lowerHex(s[36:52]) && lowerHex(s[53:]) &&
		s[3:35] != zeroTrace && s[36:52] != zeroParent
}

func lowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && !isLowerHex(c) {
			return false
		}
	}
	return true
}
