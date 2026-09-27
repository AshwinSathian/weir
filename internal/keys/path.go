package keys

// normalizePath applies Key.NormalizePath (FR-KEY-4): percent-encoded
// unreserved characters are decoded and other escapes uppercased (RFC 3986
// §6.2.2.1-6.2.2.2). Dot-segments are never resolved. The caller keys and
// forwards the result (P2). A path with a malformed escape, which Validate
// rejects, comes back unchanged: decoding around a stray '%' could form a
// new escape and make the result depend on how often it ran.
func normalizePath(p string) string {
	changed := false
	for i := 0; i < len(p); i++ {
		if p[i] != '%' {
			continue
		}
		if i+2 >= len(p) || !isHex(p[i+1]) || !isHex(p[i+2]) {
			return p
		}
		if c := unhex(p[i+1])<<4 | unhex(p[i+2]); unreserved(c) || isLowerHex(p[i+1]) || isLowerHex(p[i+2]) {
			changed = true
		}
		i += 2
	}
	if !changed {
		return p
	}
	b := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		if p[i] != '%' {
			b = append(b, p[i])
			continue
		}
		c := unhex(p[i+1])<<4 | unhex(p[i+2])
		if unreserved(c) {
			b = append(b, c)
		} else {
			b = append(b, '%', upperHex(p[i+1]), upperHex(p[i+2]))
		}
		i += 2
	}
	return string(b)
}

// unreserved reports RFC 3986 §2.3 unreserved characters.
func unreserved(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
		c == '-' || c == '.' || c == '_' || c == '~'
}

func unhex(c byte) byte {
	switch {
	case c <= '9':
		return c - '0'
	case c <= 'F':
		return c - 'A' + 10
	default:
		return c - 'a' + 10
	}
}

func isLowerHex(c byte) bool { return 'a' <= c && c <= 'f' }

func upperHex(c byte) byte {
	if isLowerHex(c) {
		return c - 'a' + 'A'
	}
	return c
}
