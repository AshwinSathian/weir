package experiments

import (
	"crypto/sha256"
	"encoding/binary"
	"math/bits"
)

// Pick chooses a variant by weight from SHA-256(salt || visitorID), so the
// same visitor always lands on the same arm of one experiment and different
// salts decorrelate experiments (10 §4 step 3). It returns "" for an empty
// list. Weights must be positive; Config.Validate guarantees that.
func Pick(salt, visitorID string, variants []Variant) string {
	total := 0
	for _, v := range variants {
		total += v.Weight
	}
	if total <= 0 {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(salt))
	h.Write([]byte(visitorID))
	var sum [sha256.Size]byte
	h.Sum(sum[:0])
	// The high word of u*total is uniform on [0,total) up to a bias below
	// 2^-50, without a modulo.
	slot, _ := bits.Mul64(binary.BigEndian.Uint64(sum[:8]), uint64(total))
	for _, v := range variants {
		if slot < uint64(v.Weight) {
			return v.Name
		}
		slot -= uint64(v.Weight)
	}
	return variants[len(variants)-1].Name
}
