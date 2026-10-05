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
// present, or any Bypass.Cookies name in any Cookie line. T-8: the cookie
// match is as wide as the most lenient origin parser, because under
// ForwardAll the origin reads the lines as sent. A miss here stores the
// sender's personalized response for everyone, while a false match costs
// one uncached response. So a name matches in any letter case wherever it
// stands as a whole token: before "=" with or without whitespace, without
// "=", after a comma, in quotes, or as a value, and also when some of its
// bytes are percent-encoded or it has an encoded space or a "+" before or
// after it, which some origins decode and trim in cookie names.
// The scan costs line bytes times len(Bypass.Cookies), and the server's
// header limit bounds the lines (P5).
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
		for i := 0; i < len(line); {
			if !isTchar(line[i]) {
				i++
				continue
			}
			start := i
			for i < len(line) && isTchar(line[i]) {
				i++
			}
			for _, name := range c.BypassCookies {
				if tokenIs(line[start:i], name) {
					return true
				}
			}
		}
	}
	return false
}

// tokenIs reports whether tok spells name, ignoring ASCII case. Each valid
// %XX in tok is read as the byte it encodes ("%" is a token character, so
// an encoded name is still one token). Decoded bytes that are no token
// character, and "+", a space to a form decoder, are skipped before and
// after the name: an origin that decodes names also trims them, and reads
// "%20session" and "+session" as session (T-8). Inside the token they end
// the match, so an encoded value that mentions the name ("q=a+session+b",
// URL-encoded JSON) does not bypass. A name that itself holds "%" or "+"
// matches its own spelling. It allocates nothing.
func tokenIs(tok, name string) bool {
	if len(tok) < len(name) {
		return false
	}
	if strings.EqualFold(tok, name) {
		return true
	}
	n, done := 0, false // bytes of name matched; done once a separator follows them
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if c == '%' && i+2 < len(tok) && isHex(tok[i+1]) && isHex(tok[i+2]) {
			c = unhex(tok[i+1])<<4 | unhex(tok[i+2])
			i += 2
		}
		switch sep := c == '+' || !isTchar(c); {
		case sep && n == 0:
		case sep && n == len(name):
			done = true
		case sep || done || n == len(name) || lower(c) != lower(name[n]):
			return false
		default:
			n++
		}
	}
	return n == len(name)
}

func lower(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// Unforwardable reports a canonical header name that no Forward.Allow or
// Key.Headers entry can put into a cacheable fetch: forwardHeader always
// deletes hop-by-hop fields, Host and the dropped fields, and writes Cookie
// from the keyed cookies only (FR-FWD-1, FR-KEY-6).
func Unforwardable(name string) bool {
	return name == "Cookie" || name == "Host" || slices.Contains(hopByHop, name) || slices.Contains(dropped, name)
}
