package keys

import "strings"

// aeBucket maps Accept-Encoding lines to the most preferred coding in
// Key.AcceptEncoding with a non-zero qvalue, or "identity" (§5.2.3,
// 04 §3.6). It is applied whether or not the header is keyed, and the
// result is also what the origin receives.
//
// T-13 (CVE-2024-35296): every malformed, oversized or non-ASCII value lands
// in a bucket from the fixed set, never a bypass or an error.
func aeBucket(lines []string, c *Config) string {
	// No header: RFC 9110 §12.5.3 allows any coding, but clients that omit
	// it often cannot decode one.
	if len(lines) == 0 || combinedLen(lines) > c.MaxKeyedHeaderBytes {
		return "identity"
	}
	// q holds thousandths per supported coding, -1 when not listed.
	var small [8]int
	q := small[:0]
	for range c.AcceptEncoding {
		q = append(q, -1)
	}
	star := -1
	for _, line := range lines {
		if !printableASCII(line) {
			return "identity"
		}
		for member := range strings.SplitSeq(line, ",") {
			coding, params, hasParams := strings.Cut(member, ";")
			coding = strings.Trim(coding, " \t")
			w := 1000
			if hasParams {
				var ok bool
				if w, ok = parseWeight(params); !ok {
					continue
				}
			}
			if coding == "*" {
				star = w
				continue
			}
			if !isToken(coding) {
				continue
			}
			for i, s := range c.AcceptEncoding {
				if q[i] < 0 && strings.EqualFold(coding, s) {
					q[i] = w // first occurrence wins
				}
			}
		}
	}
	best, bestQ := "identity", 0
	for i, s := range c.AcceptEncoding {
		w := q[i]
		if w < 0 {
			w = star
		}
		if w > bestQ {
			best, bestQ = s, w
		}
	}
	return best
}

// parseWeight parses the text after ';' as OWS "q=" qvalue OWS (RFC 9110
// §12.4.2) and returns the weight in thousandths. Any other parameter is
// malformed.
func parseWeight(p string) (int, bool) {
	p = strings.Trim(p, " \t")
	if len(p) < 3 || p[0] != 'q' && p[0] != 'Q' || p[1] != '=' {
		return 0, false
	}
	v := p[2:]
	if v[0] != '0' && v[0] != '1' {
		return 0, false
	}
	w := int(v[0]-'0') * 1000
	if len(v) == 1 {
		return w, true
	}
	if v[1] != '.' || len(v) > 5 {
		return 0, false
	}
	scale := 100
	for i := 2; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, false
		}
		w += int(v[i]-'0') * scale
		scale /= 10
	}
	if w > 1000 {
		return 0, false
	}
	return w, true
}

// printableASCII reports whether s holds only HTAB and 0x20-0x7E.
func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if (s[i] < 0x20 || s[i] > 0x7e) && s[i] != '\t' {
			return false
		}
	}
	return true
}
