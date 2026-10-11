package experiments

import (
	"math"
	"strconv"
	"testing"
)

// E3, T-35: Pick is a pure function of (salt, id) and follows the weights.
func TestPickDistribution(t *testing.T) {
	variants := []Variant{{"control", 50}, {"b", 30}, {"c", 20}}
	const n = 100000
	counts := map[string]int{}
	for i := range n {
		counts[Pick("2026-10-a", "id"+strconv.Itoa(i), variants)]++
	}
	for _, v := range variants {
		p := float64(v.Weight) / 100
		sigma := math.Sqrt(n * p * (1 - p))
		if d := math.Abs(float64(counts[v.Name]) - n*p); d > 3*sigma {
			t.Errorf("%s: %d of %d, expected %.0f, off by %.1f sigma", v.Name, counts[v.Name], n, n*p, d/sigma)
		}
	}
}

func TestPickIsDeterministicAndSaltDependent(t *testing.T) {
	variants := []Variant{{"a", 1}, {"b", 1}}
	first := Pick("s", "v", variants)
	if again := Pick("s", "v", variants); first != again {
		t.Fatal("same input, different variant")
	}
	differ := false
	for i := range 64 {
		id := strconv.Itoa(i)
		if Pick("s1", id, variants) != Pick("s2", id, variants) {
			differ = true
		}
	}
	if !differ {
		t.Error("two salts never disagreed over 64 ids")
	}
	if Pick("s", "v", nil) != "" || Pick("s", "v", []Variant{{"a", 0}}) != "" {
		t.Error("no usable variants should yield an empty pick")
	}
}

// E3: the hash input is salt || 0x00 || id. The golden values pin the bytes,
// so an analyst can reproduce an assignment offline and a refactor cannot
// reshuffle visitors.
func TestPickGolden(t *testing.T) {
	variants := []Variant{{"control", 50}, {"b", 50}}
	// Computed outside Go: slot = (first 8 bytes of SHA-256(salt 0x00 id), big endian) * 100 >> 64.
	golden := map[string]string{
		"visitor-0": "control",
		"visitor-1": "control",
		"visitor-2": "b",
		"visitor-3": "b",
		"visitor-4": "control",
		"visitor-5": "control",
	}
	for id, want := range golden {
		if got := Pick("2026-10-a", id, variants); got != want {
			t.Errorf("Pick(%q) = %q, want %q", id, got, want)
		}
	}
	// Salt/id boundary: these pairs concatenate identically.
	same := 0
	for i := range 64 {
		id := strconv.Itoa(i)
		if Pick("a", id+"x", variants) == Pick("a"+id, "x", variants) {
			same++
		}
	}
	if same == 64 {
		t.Error("salt and id are not separated")
	}
	if Pick("s", "v", []Variant{{"a", 1}, {"b", -1}}) != "" {
		t.Error("a negative weight should yield no pick")
	}
}
