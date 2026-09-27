package store

import (
	"net/http"
	"testing"
)

// 04 §2: Size = len(Body) + header bytes + vary name bytes + 32 per tag +
// 56 per variant ref + 256; byte-weighted stores account it (NFR-3, D2).
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
			int64(256 + len("Etag") + len(`"a"`) + 2*len("Vary") + len("Accept") + len("Origin")),
		},
		{"each tag is 32 bytes", Entry{Tags: make([]Tag, 3)}, 256 + 96},
		{
			"vary names count so vary specs are not under-accounted",
			Entry{Kind: KindVarySpec, VaryNames: []string{"accept-language", "origin"}},
			256 + int64(len("accept-language")+len("origin")),
		},
		{"each variant ref is charged", Entry{Kind: KindVarySpec, Variants: make([]VariantRef, 8)}, 256 + 8*variantRefSize},
		{
			"all parts add up",
			Entry{Body: []byte("hello"), Header: http.Header{"A": {"b"}}, Tags: make([]Tag, 1)},
			256 + 5 + 2 + 32,
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
