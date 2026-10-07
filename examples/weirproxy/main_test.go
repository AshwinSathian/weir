package main

import (
	"slices"
	"testing"
)

func TestSplitList(t *testing.T) {
	// D4: the allow list is the only way an unkeyed header reaches the origin.
	for _, tc := range []struct {
		name string
		in   string
		want []string
	}{
		{"empty flag gives no headers", "", nil},
		{"items are trimmed", " Foo , Bar", []string{"Foo", "Bar"}},
		{"empty items are dropped", "Foo,,Bar,", []string{"Foo", "Bar"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := splitList(tc.in); !slices.Equal(got, tc.want) {
				t.Fatalf("splitList(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
