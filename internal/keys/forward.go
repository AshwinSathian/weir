package keys

import (
	"net/http"
	"slices"
	"strings"
)

// hopByHop are the fields RFC 9110 §7.6.1 says a proxy never forwards,
// plus HTTP2-Settings, which RFC 9113 §3.1 ties to an h2c Upgrade on the
// client's connection (FR-UPG-1).
var hopByHop = []string{"Connection", "Keep-Alive", "Proxy-Connection", "Te", "Transfer-Encoding", "Upgrade", "Http2-Settings"}

// dropped are the fields a cacheable fetch never carries (FR-FWD-1): client
// preconditions and ranges, since the stored response must be the full one,
// and fields that describe a body, since the fetch has none (T-5).
var dropped = []string{
	"If-None-Match", "If-Modified-Since", "If-Match", "If-Unmodified-Since", "If-Range", "Range",
	"Content-Length", "Expect", "Trailer",
}

// forwardHeader builds the forwarded header of a cacheable request (04 §3.5).
// In strict mode (T-1, INV-1) it holds only keyed fields, the cache
// directives and Authorization (FR-FWD-1), and operator-allowed and trace
// fields. cookies is the keyedCookies result the key was built from.
// unkeyed reports that the origin sees client-chosen bytes the key ignores:
// always under ForwardAll, else when an Allow field, Cache-Control or Pragma
// is sent unkeyed. Authorization has its own flag, and trace fields have a
// validated shape (FR-FWD-6).
func forwardHeader(h http.Header, c *Config, cookies []Cookie, dims []Dim) (out http.Header, unkeyed bool) {
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
	//
	// Without Allow and Key.Headers, strict mode copied only defaultForward
	// names, so out holds none of the fixed hop-by-hop, Cookie or dropped
	// names; only a Connection option can still name a field in it. Skipping
	// those deletes is the same result with about thirty fewer map writes
	// per request (forwardHeaderRef in the tests is the unabridged version).
	fixedClean := !c.ForwardAll && len(c.Allow) == 0 && len(c.Headers) == 0
	if fixedClean {
		dropConnectionOptions(out, h["Connection"])
	} else {
		DropHopByHop(out, h["Connection"])
	}
	if !c.ForwardAll {
		if !fixedClean {
			delete(out, "Cookie") // only keyed cookies, even if Allow or Key.Headers names Cookie
		}
		if v := cookieHeader(cookies); v != "" {
			out["Cookie"] = []string{v}
		}
	}
	if !fixedClean {
		for _, name := range dropped {
			delete(out, name)
		}
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

// dropControlBytes removes every forwarded field with a CR, LF or NUL in a
// value. Keyed fields were checked when they were normalized; the unkeyed ones
// (Authorization, the cache directives, trace and Allow fields) travel as the
// client sent them, and an Origin that is not net/http would put the bytes
// on the wire, where a CR LF pair splits the request (T-5).
func dropControlBytes(h http.Header) {
	for name, vals := range h {
		for _, v := range vals {
			if strings.ContainsAny(v, "\r\n\x00") {
				delete(h, name)
				break
			}
		}
	}
}

// defaultForward are the unkeyed fields strict mode always sends. Only
// Authorization and a request no-store change behavior (FR-FWD-1); the rest
// are cache directives and trace fields.
var defaultForward = []string{"Authorization", "Cache-Control", "Pragma", "Traceparent", "Tracestate", "X-Request-Id"}

func copyField(dst, src http.Header, name string) {
	if v := src[name]; len(v) > 0 {
		dst[name] = slices.Clone(v) // the forwarded request can outlive the client's
	}
}

// dropConnectionOptions deletes from h the fields the Connection lines conn
// name (RFC 9110 §7.6.1).
func dropConnectionOptions(h http.Header, conn []string) {
	for _, line := range conn {
		for opt := range strings.SplitSeq(line, ",") {
			if opt = trimOWS(opt); opt != "" {
				delete(h, http.CanonicalHeaderKey(opt))
			}
		}
	}
}

// DropHopByHop deletes from h the hop-by-hop fields, Host, and the fields named
// in the Connection lines conn (RFC 9110 §7.6.1). It serves forwarded
// requests and stored responses (FR-FWD-2, FR-STO-11).
func DropHopByHop(h http.Header, conn []string) {
	dropConnectionOptions(h, conn)
	for _, name := range hopByHop {
		delete(h, name)
	}
	// P2: the origin sees the normalized Request.Host only. net/http never
	// leaves Host in the map, but other adapters and Origins may.
	delete(h, "Host")
}

// maxTracestateBytes is the W3C Trace Context limit on tracestate combined
// across its lines (FR-FWD-6).
const maxTracestateBytes = 512

// maxTracestateLines is the W3C limit on list members; a line holds at
// least one. T-31: without it 257 empty lines fit in 512 combined bytes and
// could trip an origin's field-count limit on a request that still plants
// markers.
const maxTracestateLines = 32

// filterTrace applies FR-FWD-6 (T-40): trace fields that reach the origin
// have a validated shape, and none do with NoTraceHeaders.
func filterTrace(h http.Header, none bool) {
	if tp := h["Traceparent"]; none || len(tp) != 1 || !validTraceparent(tp[0]) {
		delete(h, "Traceparent")
		delete(h, "Tracestate")
	}
	if ts := h["Tracestate"]; len(ts) > maxTracestateLines || len(ts) > 0 && (combinedLen(ts) > maxTracestateBytes || !allTraceBytes(ts)) {
		delete(h, "Tracestate")
	}
	if id := h["X-Request-Id"]; none || len(id) > 0 && (len(id) != 1 || len(id[0]) > 128 || !allTraceBytes(id)) {
		delete(h, "X-Request-Id")
	}
}

// Bounds on an unkeyed Cache-Control or Pragma field (FR-FWD-1). Every
// request directive RFC 9111 §5.2.1 defines fits many times over.
const (
	maxDirectiveBytes = 256
	maxDirectiveLines = 8
)

// filterDirectives drops an unkeyed Cache-Control or Pragma field that is
// not a plain directive list (FR-FWD-1, T-1). Both reach the origin outside
// the key, and the response is stored for everyone, so neither may carry a
// payload an origin could reflect or an amount a server would reject: at
// most 8 lines and 256 bytes together, no empty line, and only letters,
// digits, "-", "_", "=", "," and space. A quoted argument is legal HTTP but
// no request directive needs one; the cost is that one field for that one
// request. The engine has already read the directives from the client's
// own header, so a dropped no-store still keeps the response out of the cache.
func filterDirectives(h http.Header, keyed []string) {
	for _, name := range [...]string{"Cache-Control", "Pragma"} {
		if v := h[name]; len(v) > 0 && !slices.Contains(keyed, name) && !plainDirectives(v) {
			delete(h, name)
		}
	}
}

func plainDirectives(lines []string) bool {
	if len(lines) > maxDirectiveLines || combinedLen(lines) > maxDirectiveBytes {
		return false
	}
	for _, l := range lines {
		if l == "" {
			return false
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if ('a' > c || c > 'z') && ('A' > c || c > 'Z') && ('0' > c || c > '9') && strings.IndexByte("-_=, ", c) < 0 {
				return false
			}
		}
	}
	return true
}

// allTraceBytes reports whether every line is non-empty, holds only
// traceByte bytes and has no ".." (a path-traversal pattern to a WAF).
func allTraceBytes(lines []string) bool {
	for _, l := range lines {
		if l == "" || strings.Contains(l, "..") {
			return false
		}
		for i := 0; i < len(l); i++ {
			if !traceByte(l[i]) {
				return false
			}
		}
	}
	return true
}

// traceByte reports a byte tracestate and X-Request-Id may carry (FR-FWD-6).
// T-31: both go forward unkeyed and do not suppress markers, so the set
// leaves out what a WAF rule matches on (quotes, angle brackets, brackets,
// backslash, "%", "&", "#", "$", "?"). It covers W3C tracestate keys, the
// vendor values in use, UUIDs, base64 and hierarchical ids.
func traceByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
		strings.IndexByte("-_.:/=+@,;*~!|", c) >= 0
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

// CanonicalHeader returns h with every key canonical, merging duplicates
// canonical-first and then in sorted key order. Every decision reads
// canonical keys: a custom Origin's "set-cookie" or "cache-control: private"
// must not slip past storability (INV-4, T-8), and a caller's "x-custom"
// must not reach the origin while the key reads "X-Custom" (INV-1, T-15).
// h is returned as is when already canonical; otherwise a new map is built
// and no value array of h is written, since callers may reuse them.
func CanonicalHeader(h http.Header) http.Header {
	if canonicalKeys(h) {
		return h
	}
	out := make(http.Header, len(h))
	var odd []string
	for k, v := range h {
		if http.CanonicalHeaderKey(k) == k {
			out[k] = v
		} else {
			odd = append(odd, k)
		}
	}
	slices.Sort(odd)
	for _, k := range odd {
		ck := http.CanonicalHeaderKey(k)
		out[ck] = append(slices.Clip(out[ck]), h[k]...)
	}
	return out
}

func canonicalKeys(h http.Header) bool {
	for k := range h {
		if !isCanonicalKey(k) {
			return false
		}
	}
	return true
}

// isCanonicalKey reports http.CanonicalHeaderKey(k) == k. The common case, a
// key of token bytes in canonical case, is decided in one pass with no call;
// anything else (a lower-case letter after a hyphen, a byte that is not a
// token byte, which makes the standard library return the key unchanged)
// goes to the standard library, so the answer is the same on every input.
func isCanonicalKey(k string) bool {
	upper := true
	for i := 0; i < len(k); i++ {
		c := k[i]
		isLetter := 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
		switch {
		case isLetter:
			if upper != ('A' <= c && c <= 'Z') {
				return http.CanonicalHeaderKey(k) == k
			}
		case '0' <= c && c <= '9', c == '-':
		default:
			return http.CanonicalHeaderKey(k) == k
		}
		upper = c == '-'
	}
	return true
}

// sameHeaderName reports whether a map key names the variant header once
// '_' reads as '-' and case is folded: CGI-style origins give Weir_Variant
// and Weir-Variant one variable (T-35). name is canonical.
func sameHeaderName(k, name string) bool {
	if len(k) != len(name) {
		return false
	}
	for i := 0; i < len(k); i++ {
		a := k[i]
		if a == '_' {
			a = '-'
		}
		if lowerASCII(a) != lowerASCII(name[i]) {
			return false
		}
	}
	return true
}

func lowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// deleteVariantHeader removes every client-supplied spelling of the variant
// header (T-35).
func deleteVariantHeader(h http.Header, name string) {
	if name == "" {
		return
	}
	for k := range h {
		if sameHeaderName(k, name) {
			delete(h, k)
		}
	}
}

// setVariantHeader replaces the variant header with the assignment, in
// every forwarding mode (10 §4 step 4). No dimension, no header.
func setVariantHeader(h http.Header, name string, dims []Dim) {
	deleteVariantHeader(h, name)
	if name == "" || len(dims) == 0 {
		return
	}
	var b strings.Builder
	for i, d := range dims {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(d.Experiment)
		b.WriteByte('=')
		b.WriteString(d.Variant)
	}
	h[name] = []string{b.String()}
}

// stripCookie removes every pair named name from the Cookie lines, keeping
// the other pairs in order. A line left empty is dropped; h is untouched
// when nothing matches.
func stripCookie(h http.Header, name string) {
	lines := h["Cookie"]
	hit := false
	for _, line := range lines {
		for pair := range strings.SplitSeq(line, ";") {
			hit = hit || cookiePairNamed(pair, name)
		}
	}
	if !hit {
		return
	}
	var out []string
	for _, line := range lines {
		var kept []string
		for pair := range strings.SplitSeq(line, ";") {
			if p := strings.Trim(pair, " \t"); p != "" && !cookiePairNamed(pair, name) {
				kept = append(kept, p)
			}
		}
		if len(kept) > 0 {
			out = append(out, strings.Join(kept, "; "))
		}
	}
	if len(out) == 0 {
		delete(h, "Cookie")
		return
	}
	h["Cookie"] = out
}

// cookiePairNamed reports whether a Cookie pair carries the cookie name, with
// or without "=" and in any case: origins differ on both, and the visitor
// id must reach none of them.
func cookiePairNamed(pair, name string) bool {
	n, _, _ := strings.Cut(pair, "=")
	// net/http and most origins trim the name, so "weir_v =1" is the cookie.
	return strings.EqualFold(strings.Trim(n, " \t\r\n"), name)
}
