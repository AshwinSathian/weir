package keys

import (
	"net/netip"
	"strconv"
	"strings"
)

const maxHostBytes = 255

// normalizeHost validates an authority and returns its single spelling
// (04 §3.7): lower case, default port and one trailing dot removed. The
// caller keys and forwards the result, so both agree (P2).
func normalizeHost(scheme, host string) (string, bool) {
	if host == "" || len(host) > maxHostBytes {
		return "", false
	}
	for i := 0; i < len(host); i++ {
		if !hostByte(host[i]) {
			return "", false // T-6: '%', '@', '/' and non-ASCII never reach the key
		}
	}
	host = strings.ToLower(host) // returns host itself when already lower case

	name, port := host, ""
	if host[0] == '[' {
		end := strings.IndexByte(host, ']')
		if end < 0 {
			return "", false
		}
		name, port = host[:end+1], host[end+1:]
		if port != "" && port[0] != ':' {
			return "", false
		}
		a, err := netip.ParseAddr(name[1:end])
		if err != nil || !a.Is6() {
			return "", false
		}
		// One spelling per address ([0::1] and [::1] share a key); zones
		// never get here because hostByte rejects '%'.
		name = "[" + a.String() + "]"
	} else {
		if i := strings.LastIndexByte(host, ':'); i >= 0 {
			name, port = host[:i], host[i:]
		}
		if strings.ContainsAny(name, ":[]") {
			return "", false
		}
		name = strings.TrimSuffix(name, ".")
		// "a.." would become "a." and then "a" on a second pass; one spelling only.
		if name == "" || name[len(name)-1] == '.' {
			return "", false
		}
	}

	if port == "" {
		return name, true
	}
	n, ok := parsePort(port[1:])
	if !ok {
		return "", false
	}
	if (scheme == "http" && n == 80) || (scheme == "https" && n == 443) {
		return name, true
	}
	return name + ":" + strconv.Itoa(n), true // drops leading zeros
}

func hostByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
		c == '.' || c == '-' || c == ':' || c == '[' || c == ']'
}

// parsePort accepts 1-65535 in decimal, leading zeros allowed.
func parsePort(s string) (int, bool) {
	if s == "" || len(s) > 5 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, n >= 1 && n <= 65535
}
