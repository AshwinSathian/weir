package experiments

import (
	"math"
	"strconv"
	"testing"
)

// FR-EXP: E3, T-35. Pick is a pure function of (salt, id).
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
	if Pick("s", "v", variants) != Pick("s", "v", variants) {
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
