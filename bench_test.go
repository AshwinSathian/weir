package weir_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// BenchmarkServeHitSmall measures the fresh-hit path (07 §10, NFR-5) for a
// 1 KiB body: classify, store Get, freshness, response build.
func BenchmarkServeHitSmall(b *testing.B) {
	o := testorigin.New()
	o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=3600"}}, Body: []byte(strings.Repeat("x", 1024))})
	e, err := weir.New(cacheCfg)
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := e.Close(b.Context()); err != nil {
			b.Error(err)
		}
	}()
	req := getReq("/a")
	req.Header.Set("Accept-Encoding", "gzip, br")

	serveOnce := func() *weir.Response {
		resp, err := e.Serve(b.Context(), req, o)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}
	serveOnce()
	b.ReportAllocs()
	for b.Loop() {
		serveOnce()
	}
	if !serveOnce().Cache.Hit || o.TotalCalls() != 1 {
		b.Fatalf("not a hit: origin calls %d", o.TotalCalls())
	}
}
