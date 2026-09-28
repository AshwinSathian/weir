package keys

import (
	"net/http"
	"strings"
)

// Validate applies FR-VAL-1 and returns the normalized host (04 §3.7).
// Upgrade and CONNECT detection runs first (FR-UPG-1), because those
// requests carry paths that would otherwise fail as ReasonPath.
func Validate(r *Request, c *Config) (string, error) {
	if IsUpgrade(r.Method, r.Header) {
		return "", ErrUpgrade
	}
	if r.Scheme != "http" && r.Scheme != "https" {
		return "", &RequestError{ReasonScheme}
	}
	host, ok := normalizeHost(r.Scheme, r.Host)
	if !ok {
		return "", &RequestError{ReasonHost}
	}
	if !isToken(r.Method) {
		return "", &RequestError{ReasonMethod}
	}
	if reason := checkPath(r.Method, r.Path, c.MaxPathBytes); reason != "" {
		return "", &RequestError{reason}
	}
	if reason := checkQuery(r.RawQuery, c); reason != "" {
		return "", &RequestError{reason}
	}
	return host, nil
}

// IsUpgrade reports CONNECT in any form, including HTTP/2 and HTTP/3
// extended CONNECT, which carries no Upgrade field (T-44), and requests
// with an "upgrade" Connection option plus an Upgrade field other than a
// lone h2c. Adapters use it to route these around the engine (FR-UPG-1).
func IsUpgrade(method string, h http.Header) bool {
	// Methods are case-sensitive (RFC 9110 §9.1): "connect" is an unknown
	// method and goes forward as ClassPass, never through a tunnel.
	if method == http.MethodConnect {
		return true
	}
	// RFC 9110 §7.8 lets a server ignore Upgrade. A lone h2c is ignored so
	// "curl --http2 http://..." is served; Upgrade and HTTP2-Settings are
	// hop-by-hop, so key and forward stay equal.
	if up := h.Values("Upgrade"); len(up) == 0 || onlyH2C(up) {
		return false
	}
	for _, line := range h.Values("Connection") {
		for opt := range strings.SplitSeq(line, ",") {
			if strings.EqualFold(strings.Trim(opt, " \t"), "upgrade") {
				return true
			}
		}
	}
	return false
}

// onlyH2C reports whether the Upgrade lines hold exactly one token, h2c.
func onlyH2C(lines []string) bool {
	n := 0
	for _, line := range lines {
		for tok := range strings.SplitSeq(line, ",") {
			if tok = trimOWS(tok); tok == "" {
				continue
			}
			if n++; n > 1 || !strings.EqualFold(tok, "h2c") {
				return false
			}
		}
	}
	return n == 1
}

func checkPath(method, p string, limit int) string {
	if p == "*" && method == http.MethodOptions {
		return ""
	}
	// '#' and '?' end the path in origin-form (RFC 9112 §3.2); raw, they
	// would reach the origin as a fragment or a query the key never saw.
	if p == "" || p[0] != '/' || len(p) > limit || !visibleASCII(p) || strings.ContainsAny(p, "#?") {
		return ReasonPath
	}
	for i := 0; i < len(p); i++ {
		if p[i] == '%' {
			if i+2 >= len(p) || !isHex(p[i+1]) || !isHex(p[i+2]) {
				return ReasonPathEscape
			}
			i += 2
		}
	}
	return ""
}

func checkQuery(q string, c *Config) string {
	if len(q) > c.MaxQueryBytes || !visibleASCII(q) || strings.IndexByte(q, '#') >= 0 {
		return ReasonQuery
	}
	if q != "" && strings.Count(q, "&")+1 > c.MaxQueryParams {
		return ReasonQueryParams
	}
	return ""
}

// visibleASCII reports whether every byte is in 0x21-0x7E (FR-VAL-1).
func visibleASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// isToken reports whether s is an RFC 9110 §5.6.2 token.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isTchar(s[i]) {
			return false
		}
	}
	return true
}

func isTchar(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
		strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}
