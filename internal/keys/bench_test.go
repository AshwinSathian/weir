package keys

import (
	"net/http"
	"testing"
)

// BenchmarkKeyBuild measures validation, normalization and the primary key
// of a typical cacheable GET (07 §10, 04 §3.1).
func BenchmarkKeyBuild(b *testing.B) {
	c := classifyCfg()
	r := classifyReq("GET", http.Header{
		"Accept":          {"text/html"},
		"Accept-Encoding": {"gzip, deflate, br"},
		"Cookie":          {"session=abc; lang=en; theme=dark"},
		"User-Agent":      {"Mozilla/5.0"},
	})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Classify(r, c); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAcceptEncoding measures the Accept-Encoding bucket (§5.2.3) for a
// browser-style value with qvalues.
func BenchmarkAcceptEncoding(b *testing.B) {
	c := &Config{AcceptEncoding: []string{"br", "gzip", "zstd"}, MaxKeyedHeaderBytes: 1024}
	lines := []string{"gzip;q=0.8, deflate, br;q=1.0, zstd;q=0.9, *;q=0"}
	b.ReportAllocs()
	for b.Loop() {
		if aeBucket(lines, c) != "br" {
			b.Fatal("wrong bucket")
		}
	}
}
