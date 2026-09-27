package keys

import (
	"slices"
	"strings"
	"testing"
)

func TestAcceptEncodingBuckets(t *testing.T) {
	// §5.2.3, FR-VAL-3; T-13
	c := &Config{AcceptEncoding: []string{"br", "gzip"}, MaxKeyedHeaderBytes: 64}
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{"absent header gives identity", nil, "identity"},
		{"empty header gives identity", []string{""}, "identity"},
		{"single supported coding", []string{"gzip"}, "gzip"},
		{"unsupported coding gives identity", []string{"deflate"}, "identity"},
		{"tie breaks by config order", []string{"gzip, br"}, "br"},
		{"higher q wins", []string{"br;q=0.5, gzip"}, "gzip"},
		{"q=0 excludes", []string{"br;q=0, gzip;q=0.001"}, "gzip"},
		{"all q=0 gives identity", []string{"br;q=0, gzip;q=0.000"}, "identity"},
		{"star covers unlisted", []string{"*"}, "br"},
		{"star q=0 with explicit gzip", []string{"*;q=0, gzip"}, "gzip"},
		{"explicit beats star", []string{"br;q=0, *"}, "gzip"},
		{"coding is case-insensitive", []string{"GZip"}, "gzip"},
		{"Q is case-insensitive with OWS", []string{"gzip ; Q=0.5 , br;q=0.4"}, "gzip"},
		{"first occurrence wins", []string{"br;q=0, br"}, "identity"},
		{"combined across lines", []string{"deflate", "gzip;q=0.2"}, "gzip"},
		{"malformed member skipped", []string{"br;q=2, gzip"}, "gzip"},
		{"q with four decimals skipped", []string{"gzip;q=0.0001"}, "identity"},
		{"q=1.0001 skipped", []string{"gzip;q=1.0001"}, "identity"},
		{"q=1.000 accepted", []string{"gzip;q=1.000"}, "gzip"},
		{"empty q skipped", []string{"gzip;q="}, "identity"},
		{"unknown parameter skipped", []string{"gzip;level=9"}, "identity"},
		{"prefix of supported name is different", []string{"br_"}, "identity"},
		{"separators only", []string{",,, ,"}, "identity"},
		{"wholly malformed gives identity", []string{"@@@"}, "identity"},
		{"non-ASCII gives identity", []string{"gzip, \xff"}, "identity"},
		{"NUL gives identity", []string{"gzip\x00"}, "identity"},
		{"oversized gives identity", []string{"gzip," + strings.Repeat(" ", 64)}, "identity"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := aeBucket(tt.lines, c); got != tt.want {
				t.Fatalf("aeBucket(%q) = %q, want %q", tt.lines, got, tt.want)
			}
		})
	}
}

func FuzzAcceptEncoding(f *testing.F) {
	// §5.2.3, FR-VAL-3, NFR-2; T-13: no panic and never a value outside
	// supported ∪ {identity}, so malformed input cannot mint a key.
	f.Add("gzip;q=0.5, br", "*;q=0")
	f.Fuzz(func(t *testing.T, a, b string) {
		c := &Config{AcceptEncoding: []string{"br", "gzip", "zstd"}, MaxKeyedHeaderBytes: 1024}
		got := aeBucket([]string{a, b}, c)
		if got != "identity" && !slices.Contains(c.AcceptEncoding, got) {
			t.Fatalf("aeBucket(%q, %q) = %q", a, b, got)
		}
	})
}
