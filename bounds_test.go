package weir_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
)

// gaugeBody hands out size bytes in chunk-sized reads, one every tick, and
// counts the bytes it has delivered and not yet been closed over. The
// shared gauge is what an engine holds transiently while it buffers.
type gaugeBody struct {
	ctx         context.Context
	left, chunk int
	tick        time.Duration
	given       int
	live, peak  *atomic.Int64
}

func (b *gaugeBody) Read(p []byte) (int, error) {
	if b.left == 0 {
		return 0, io.EOF
	}
	select {
	case <-time.After(b.tick):
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	}
	n := min(b.left, b.chunk, len(p))
	clear(p[:n])
	b.left -= n
	b.given += n
	if v := b.live.Add(int64(n)); v > b.peak.Load() {
		b.peak.Store(v)
	}
	return n, nil
}

func (b *gaugeBody) Close() error {
	b.live.Add(-int64(b.given))
	b.given = 0
	return nil
}

// NFR-4, T-21, INV-5: with MaxConcurrent fetches allowed and twelve cold
// keys of exactly MaxObjectBytes arriving together, the bytes buffered for bodies at one moment never
// pass MaxConcurrent x (MaxObjectBytes + 1), the read-ahead that detects an
// oversized body. The peak must also exceed one fetch's worth, or the test
// proves nothing about overlap.
func TestTransientBodyMemoryBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const (
			maxConc = 4
			limit   = 4096
			clients = 12
		)
		var live, peak atomic.Int64
		origin := weir.OriginFunc(func(ctx context.Context, _ *weir.Request) (*weir.Response, error) {
			h := http.Header{"Cache-Control": {"max-age=60"}}
			return &weir.Response{StatusCode: http.StatusOK, Header: h,
				Body: &gaugeBody{ctx: ctx, left: limit, chunk: 1024, tick: 100 * time.Millisecond, live: &live, peak: &peak}}, nil
		})
		cfg := cacheCfg
		cfg.Storable.MaxObjectBytes = limit
		cfg.Limiter.MaxConcurrent = maxConc
		cfg.Limiter.MaxQueueWait = 30 * time.Second
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		var wg sync.WaitGroup
		for i := range clients {
			wg.Go(func() {
				resp, err := e.Serve(t.Context(), getReq(fmt.Sprintf("/k%d", i)), origin)
				if err != nil {
					t.Errorf("Serve /k%d: %v", i, err)
					return
				}
				defer resp.Body.Close()
				io.Copy(io.Discard, resp.Body)
			})
		}
		wg.Wait()

		if got, max := peak.Load(), int64(maxConc*(limit+1)); got > max {
			t.Fatalf("peak buffered body bytes = %d, want at most %d", got, max)
		}
		if got := peak.Load(); got <= limit {
			t.Fatalf("peak buffered body bytes = %d: fetches never overlapped", got)
		}
	})
}
