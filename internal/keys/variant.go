package keys

import (
	"crypto/sha256"
	"encoding/binary"
	"net/http"
	"net/textproto"
	"slices"

	"github.com/AshwinSathian/weir/store"
)

// variantVersion prefixes every variant-key encoding (04 §3.3).
const variantVersion = "weir/variant/v1"

// maxPooledBuf is the largest encoding buffer VariantKey returns to bufPool.
const maxPooledBuf = 64 << 10

// Field tags of the variant-key encoding (04 §3.3).
const (
	tagVaryName  = 0x30
	tagVaryValue = 0x31
)

// VaryNames parses a response's Vary lines into canonical field names,
// sorted and deduplicated (04 §3.3). star reports a "*" member (FR-KEY-8);
// names is then nil. Empty members are skipped. The scan is bytewise:
// Vary comes from the origin, so no split on its value.
func VaryNames(lines []string) (names []string, star bool) {
	forEachMember(lines, func(m string) {
		switch m {
		case "":
		case "*":
			star = true
		default:
			names = append(names, textproto.CanonicalMIMEHeaderKey(m))
		}
	})
	if star {
		return nil, true
	}
	slices.Sort(names)
	return slices.Compact(names), false
}

// VariantKey returns the key of the variant of primary selected by the
// forwarded header h under names, as VaryNames returns them (FR-KEY-7). It
// is primary itself when names is empty. T-3: names and values are tagged
// and length-prefixed, and absence is its own byte, so no value list can
// pass for another, and absent never matches empty (FR-KEY-11).
func VariantKey(primary store.Key, names []string, h http.Header) store.Key {
	if len(names) == 0 {
		return primary
	}
	bp := bufPool.Get().(*[]byte)
	b := (*bp)[:0]
	b = append(b, variantVersion...)
	b = append(b, primary[:]...)
	for _, name := range names {
		b = appendField(b, tagVaryName, name)
		lines, ok := h[name]
		if !ok || len(lines) == 0 {
			b = append(b, 0)
			continue
		}
		b = append(b, 1, tagVaryValue)
		b = appendNormalized(b, lines)
	}
	k := store.Key(sha256.Sum256(b))
	// Vary values are forwarded fields that MaxKeyedHeaderBytes does not
	// bound, so a large one is not kept in the pool (bufPool's bound).
	if cap(b) <= maxPooledBuf {
		*bp = b
		bufPool.Put(bp)
	}
	return k
}

// appendNormalized appends the FR-KEY-11 normal form of a field's lines,
// length-prefixed: lines joined with ",", whitespace around every comma and
// at both ends removed. Accept-Encoding needs no normalizer of its own here:
// its forwarded value is already the bucket token (§5.2.3). The first pass
// only measures, so the prefix can precede the value without a scratch
// buffer.
func appendNormalized(b []byte, lines []string) []byte {
	var n uint64
	first := true
	forEachMember(lines, func(m string) {
		if !first {
			n++ // the comma
		}
		first = false
		n += uint64(len(m))
	})
	b = binary.AppendUvarint(b, n)
	first = true
	forEachMember(lines, func(m string) {
		if !first {
			b = append(b, ',')
		}
		first = false
		b = append(b, m...)
	})
	return b
}

// forEachMember calls fn with every comma-separated member of lines, OWS
// trimmed, empty members included.
func forEachMember(lines []string, fn func(string)) {
	for _, l := range lines {
		for i := 0; ; {
			j := i
			for j < len(l) && l[j] != ',' {
				j++
			}
			fn(trimOWS(l[i:j]))
			if j == len(l) {
				break
			}
			i = j + 1
		}
	}
}
