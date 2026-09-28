package keys

import "strings"

// Cookie is one keyed cookie as it enters the key and the forwarded Cookie
// header.
type Cookie struct {
	Name, Value string
}

// keyedCookies returns the Key.Cookies present in the Cookie lines, in
// config order (FR-KEY-6). A name with conflicting values, or with any
// malformed occurrence, is absent. Keyed pairs whose forwarded Cookie
// header would exceed MaxKeyedHeaderBytes make every cookie absent
// (FR-VAL-3); unkeyed cookies do not count. Absent is the fallback both in
// the key and in the forwarded request, so malformed input never mints a
// key. The scan costs header bytes times len(Key.Cookies), and the
// server's header limit bounds the header (P5, 04 §3.5).
func keyedCookies(lines []string, c *Config) []Cookie {
	if len(lines) == 0 || len(c.Cookies) == 0 {
		return nil
	}
	const (
		unseen = iota
		seen
		conflict
	)
	state := make([]uint8, len(c.Cookies))
	vals := make([]string, len(c.Cookies))
	for _, line := range lines {
		for pair := range strings.SplitSeq(line, ";") {
			name, val, ok := strings.Cut(strings.Trim(pair, " \t"), "=")
			if !ok {
				continue
			}
			i := indexOf(c.Cookies, name)
			if i < 0 || state[i] == conflict {
				continue
			}
			// RFC 6265 cookie-octet is narrower. Visible ASCII admits ',',
			// '"' and '\', which is safe because the whole value is keyed and
			// forwarded byte-for-byte (T-2, INV-1); narrowing it changes the
			// key space.
			if !visibleASCII(val) || state[i] == seen && vals[i] != val {
				state[i] = conflict
				continue
			}
			state[i], vals[i] = seen, val
		}
	}
	var out []Cookie
	size := 0
	for i, s := range state {
		if s == seen {
			if len(out) > 0 {
				size += len("; ")
			}
			size += len(c.Cookies[i]) + 1 + len(vals[i])
			out = append(out, Cookie{c.Cookies[i], vals[i]})
		}
	}
	if size > c.MaxKeyedHeaderBytes {
		return nil
	}
	return out
}

// cookieHeader builds the forwarded Cookie value from keyedCookies output;
// "" means the header is omitted.
func cookieHeader(cs []Cookie) string {
	var b strings.Builder
	for i, ck := range cs {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(ck.Name)
		b.WriteByte('=')
		b.WriteString(ck.Value)
	}
	return b.String()
}

// combinedLen is the length of the lines joined with ", " (FR-VAL-3).
func combinedLen(lines []string) int {
	n := 2 * (len(lines) - 1)
	for _, l := range lines {
		n += len(l)
	}
	return n
}

func indexOf(names []string, name string) int {
	for i, n := range names {
		if n == name {
			return i
		}
	}
	return -1
}
