package keys

import (
	"net/http"
	"slices"
	"strings"
)

// normalizeHeader is the normalizer of a keyed header without one of its own
// (01 §5.2.3): lines combined with ", ", outer whitespace trimmed, and
// whitespace around commas collapsed to one space after each comma. Other
// bytes, case included, are kept. ok is false when the header is absent.
// T-13: a line with a byte outside visible ASCII, space and tab, or lines
// over limit bytes combined (FR-VAL-3), also reports absent, the one
// fallback. The normal form can be longer than the lines ("a,b" gains a
// space), so the limit applies to it as well; the result then normalizes to
// itself, and the forwarded request has the key of the client's (INV-1).
// The limit on the lines bounds the buffer at twice limit bytes (P5).
func normalizeHeader(lines []string, limit int) (v string, ok bool) {
	if len(lines) == 0 || combinedLen(lines) > limit {
		return "", false
	}
	var buf [128]byte
	b := buf[:0]
	comma := false // the last byte written is a comma
	for li, line := range lines {
		if li > 0 {
			b, comma = append(b, ','), true
		}
		ws := -1 // start of the whitespace run before line[i]
		for i := 0; i < len(line); i++ {
			switch c := line[i]; {
			case c == ' ' || c == '\t':
				if ws < 0 {
					ws = i
				}
				continue
			case c == ',':
				b, comma = append(b, ','), true
			case c < 0x21 || c > 0x7e:
				return "", false
			default:
				if comma {
					b = append(b, ' ')
				} else if ws >= 0 && len(b) > 0 {
					b = append(b, line[ws:i]...)
				}
				b, comma = append(b, c), false
			}
			ws = -1
		}
	}
	if len(b) > limit {
		return "", false
	}
	if len(lines) == 1 && string(b) == lines[0] {
		return lines[0], true // already normal: no copy
	}
	return string(b), true
}

// keyedHeaders returns the Key.Headers part of the key input, read from fh,
// the header forwardHeader built. INV-1: reading the forward itself, not the
// client's lines, keys exactly what the origin sees, also for a keyed name
// the forward rewrites (the Accept-Encoding bucket, keyed cookies), filters
// (trace fields) or drops (hop-by-hop fields, Range). forwardHeader leaves
// each of these names with one line or none.
func keyedHeaders(fh http.Header, c *Config) []Header {
	if len(c.Headers) == 0 {
		return nil
	}
	out := make([]Header, len(c.Headers))
	for i, name := range c.Headers {
		out[i].Name = name
		if v := fh[name]; len(v) > 0 {
			out[i].Value, out[i].Present = v[0], true
		}
	}
	return out
}

// bypassed reports a Bypass rule match (FR-BYP-1): any Bypass.Headers name
// present, or any Bypass.Cookies name in any Cookie line. A pair without
// "=" counts by its whole text, and whitespace before "=" is ignored: a
// malformed session cookie still means a client that must not get a shared
// response, and under ForwardAll a lenient origin would read it as one.
func bypassed(h http.Header, c *Config) bool {
	for _, name := range c.BypassHeaders {
		if len(h[name]) > 0 {
			return true
		}
	}
	if len(c.BypassCookies) == 0 {
		return false
	}
	for _, line := range h["Cookie"] {
		for pair := range strings.SplitSeq(line, ";") {
			name, _, _ := strings.Cut(pair, "=")
			if indexOf(c.BypassCookies, strings.Trim(name, " \t")) >= 0 {
				return true
			}
		}
	}
	return false
}

// IsHopByHop reports whether the canonical header name is one DropHopByHop
// always removes, so a Forward.Allow entry naming it can never take effect.
func IsHopByHop(name string) bool {
	return slices.Contains(hopByHop, name)
}
