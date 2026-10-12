package store

import (
	"net/http"
	"slices"
	"testing"
)

// 04 §2: Size = len(Body) + header bytes + 48 per header name + 16 per
// header value + vary name bytes + 16 per vary name + 32 per tag + 56 per
// variant ref + 256; byte-weighted stores account it (NFR-3, D2, T-21).
func TestEntrySize(t *testing.T) {
	tests := []struct {
		name string
		e    Entry
		want int64
	}{
		{"empty entry is the fixed overhead", Entry{}, 256},
		{"body bytes count", Entry{Body: make([]byte, 1000)}, 1256},
		{
			"header names and every value count",
			Entry{Header: http.Header{"Etag": {`"a"`}, "Vary": {"Accept", "Origin"}}},
			int64(256 + 2*48 + 3*16 + len("Etag") + len(`"a"`) + 2*len("Vary") + len("Accept") + len("Origin")),
		},
		{"each tag is 32 bytes", Entry{Tags: make([]Tag, 3)}, 256 + 96},
		{
			// T-21: 1000 one-byte values hold 16 000 bytes of string headers.
			"tiny values are charged their string headers",
			Entry{Header: http.Header{"X": slices.Repeat([]string{"a"}, 1000)}},
			256 + 48 + 1000*(1+1+16),
		},
		{
			"vary names count so vary specs are not under-accounted",
			Entry{Kind: KindVarySpec, VaryNames: []string{"accept-language", "origin"}},
			256 + 2*16 + int64(len("accept-language")+len("origin")),
		},
		{"each variant ref is charged", Entry{Kind: KindVarySpec, Variants: make([]VariantRef, 8)}, 256 + 8*variantRefSize},
		{
			"all parts add up",
			Entry{Body: []byte("hello"), Header: http.Header{"A": {"b"}}, Tags: make([]Tag, 1)},
			256 + 5 + 2 + 48 + 16 + 32,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.e.Size(); got != tt.want {
				t.Errorf("Size() = %d, want %d", got, tt.want)
			}
		})
	}
}
