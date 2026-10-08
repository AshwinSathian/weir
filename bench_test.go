package weir_test

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// BenchmarkServeHitSmall measures the fresh-hit path (07 §10, NFR-5) for a
// 1 KiB body: classify, store Get, freshness, response build.
func BenchmarkServeHitSmall(b *testing.B) {
	o := testorigin.NewChecked(b, 64, 16)
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

// benchEngine builds an engine for a benchmark and closes it when b ends.
func benchEngine(b *testing.B, cfg weir.Config) *weir.Engine {
	b.Helper()
	e, err := weir.New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := e.Close(context.Background()); err != nil {
			b.Error(err)
		}
	})
	return e
}

// BenchmarkServeHitVary measures a fresh hit whose stored entry varies on
// one forwarded header (07 §10): the second lookup (primary key, then the
// variant key) is the extra cost over BenchmarkServeHitSmall.
func BenchmarkServeHitVary(b *testing.B) {
	o := testorigin.NewChecked(b, 64, 16)
	o.Default(testorigin.Behavior{
		Header: http.Header{"Cache-Control": {"max-age=3600"}, "Vary": {"X-Custom"}},
		Body:   []byte(strings.Repeat("x", 1024)),
	})
	cfg := cacheCfg
	cfg.Forward.Allow = []string{"X-Custom"}
	e := benchEngine(b, cfg)
	req := getReq("/a")
	req.Header.Set("X-Custom", "v1")

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

// BenchmarkServeMissCoalesced measures a cold key requested by eight
// goroutines at once (07 §10, FR-COA-*): flight creation, joining, fan-out
// of one origin response. The origin answers after a short real delay so
// followers have time to join; "origin-calls/op" shows how many fetches the
// eight requests cost (1 is perfect coalescing).
func BenchmarkServeMissCoalesced(b *testing.B) {
	const followers = 8
	o := testorigin.NewChecked(b, 1<<20, 1<<20)
	o.Default(testorigin.Behavior{
		Header: http.Header{"Cache-Control": {"max-age=3600"}},
		Body:   []byte(strings.Repeat("x", 1024)),
		Delay:  100 * time.Microsecond,
	})
	e := benchEngine(b, cacheCfg)
	ctx := b.Context()

	b.ReportAllocs()
	var i int
	for b.Loop() {
		req := getReq("/m" + strconv.Itoa(i))
		i++
		var wg sync.WaitGroup
		for range followers {
			wg.Go(func() {
				resp, err := e.Serve(ctx, req, o)
				if err != nil {
					b.Error(err)
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			})
		}
		wg.Wait()
	}
	b.ReportMetric(float64(o.TotalCalls())/float64(max(i, 1)), "origin-calls/op")
}

// BenchmarkServeHitParallel measures fresh hits from GOMAXPROCS goroutines
// over 1024 resident keys, once with the miss-rate tracker on (the default)
// and once with it disabled. The difference is the cost of the tracker's one
// mutex per cacheable request (M8-02, card M10-05).
func BenchmarkServeHitParallel(b *testing.B) {
	for _, tc := range []struct {
		name    string
		disable bool
	}{{"missrate-on", false}, {"missrate-off", true}} {
		b.Run(tc.name, func(b *testing.B) {
			const n = 1024
			o := testorigin.NewChecked(b, 64, 16)
			o.Default(testorigin.Behavior{
				Header: http.Header{"Cache-Control": {"max-age=3600"}},
				Body:   []byte(strings.Repeat("x", 1024)),
			})
			cfg := cacheCfg
			cfg.MissRate.Disable = tc.disable
			e := benchEngine(b, cfg)
			reqs := make([]*weir.Request, n)
			for i := range reqs {
				reqs[i] = getReq("/p" + strconv.Itoa(i))
				resp, err := e.Serve(b.Context(), reqs[i], o)
				if err != nil {
					b.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			ctx := b.Context()
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				var i int
				for pb.Next() {
					resp, err := e.Serve(ctx, reqs[i%n], o)
					if err != nil {
						b.Error(err)
						return
					}
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					i++
				}
			})
		})
	}
}
