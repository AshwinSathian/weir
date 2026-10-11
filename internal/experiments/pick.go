package experiments

import (
	"crypto/sha256"
	"encoding/binary"
	"math/bits"
)

// Pick chooses a variant by weight from SHA-256(salt || 0x00 || visitorID), so the
// same visitor always lands on the same arm of one experiment and different
// salts decorrelate experiments (10 §4 step 3). It returns "" for an empty
// list or any weight below 1. The separator keeps (salt "a", id "1x") apart
// from (salt "a1", id "x"); Validate keeps NUL out of salts.
func Pick(salt, visitorID string, variants []Variant) string {
	total := 0
	for _, v := range variants {
		if v.Weight < 1 {
			return ""
		}
		total += v.Weight
	}
	if total == 0 {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(salt))
	h.Write([]byte{0})
	h.Write([]byte(visitorID))
	var sum [sha256.Size]byte
	h.Sum(sum[:0])
	// The high word of u*total is uniform on [0,total) up to a bias below
	// 2^-50, without a modulo.
	slot, _ := bits.Mul64(binary.BigEndian.Uint64(sum[:8]), uint64(total))
	for _, v := range variants {
		w := uint64(v.Weight) //nolint:gosec // every weight is >= 1, checked above
		if slot < w {
			return v.Name
		}
		slot -= w
	}
	return variants[len(variants)-1].Name
}
