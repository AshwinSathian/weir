package keys

import (
	"strings"
	"testing"
)

// aeBucketRef is the straightforward implementation aeBucket replaced
// (SplitSeq, no early exit). FuzzAcceptEncodingMatchesReference holds the two
// equal on every input, so the faster one cannot change a key (T-13, INV-1).
func aeBucketRef(lines []string, c *Config) string {
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

func FuzzAcceptEncodingMatchesReference(f *testing.F) {
	f.Add("gzip;q=0.5, br", "*;q=0", uint8(0))
	f.Add("gzip, br, deflate", "", uint8(1))
	f.Add("br;q=0, *", "gzip", uint8(2))
	f.Add("gzip,,br;q=1.000 ,", "\x01", uint8(3))
	f.Add("*", "gzip;q=0.1", uint8(4))
	cfgs := [][]string{nil, {"gzip"}, {"br", "gzip"}, {"br", "gzip", "zstd"}, {"a", "b", "c", "d", "e", "f", "g", "h", "i"}}
	f.Fuzz(func(t *testing.T, a, b string, sel uint8) {
		c := &Config{AcceptEncoding: cfgs[int(sel)%len(cfgs)], MaxKeyedHeaderBytes: 256}
		for _, lines := range [][]string{nil, {a}, {a, b}, {b, a}} {
			if got, want := aeBucket(lines, c), aeBucketRef(lines, c); got != want {
				t.Fatalf("aeBucket(%q, %v) = %q, reference %q", lines, c.AcceptEncoding, got, want)
			}
		}
	})
}
