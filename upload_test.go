package weir_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// transportOrigin reads each request body the way net/http's transport does:
// to the end, giving up when the fetch context ends. Then it answers from o.
type transportOrigin struct {
	o      *testorigin.Origin
	answer func(context.Context) error // after the body; nil: answer at once
}

func (u transportOrigin) Fetch(ctx context.Context, req *weir.Request) (*weir.Response, error) {
	if req.Body != nil {
		done := make(chan error, 1)
		go func() {
			_, err := io.Copy(io.Discard, req.Body)
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				return nil, err
			}
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	if u.answer != nil {
		if err := u.answer(ctx); err != nil {
			return nil, err
		}
	}
	return u.o.Fetch(ctx, req)
}

// failBody fails every Read, as a connection the client reset does.
type failBody struct{}

func (failBody) Read([]byte) (int, error) { return 0, errors.New("connection reset by peer") }
func (failBody) Close() error             { return nil }

func bodyReq(path string, body io.ReadCloser) *weir.Request {
	r := postReq(path)
	r.Body = body
	return r
}

// FR-CB-1, FR-LIM-7, T-39: a client that stays connected and never sends its
// body is the client's failure. Before v1.1 each such request counted as an
// origin timeout, and 16 of them beside 4 ordinary misses opened the breaker.
func TestStalledUploadIsNotOriginFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(cacheable("ok"))
		origin := transportOrigin{o: o}
		cfg := cacheCfg
		cfg.Breaker.MinPartitions = 1 // the v1.0 rule: every counted failure matters
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		stop := make(chan struct{})
		defer close(stop)
		uploads := make([]<-chan timedServe, 16)
		for i := range uploads {
			path := "/upload-a"
			if i%2 == 1 {
				path = "/upload-b"
			}
			uploads[i] = serveTimed(t, e, uploadReq(t.Context(), path, stop), origin)
		}
		time.Sleep(29 * time.Second)
		for i := range 4 {
			serve(t, e, getReq("/page"+string(rune('a'+i))), origin)
		}
		for i, ch := range uploads {
			r := <-ch
			if !errors.Is(r.err, weir.ErrRequestBodyTimeout) || weir.StatusCode(r.err) != http.StatusRequestTimeout {
				t.Fatalf("upload %d: %v (status %d), want ErrRequestBodyTimeout and 408", i, r.err, weir.StatusCode(r.err))
			}
			if r.elapsed != 30*time.Second {
				t.Fatalf("upload %d ended after %v, want Timeouts.Origin", i, r.elapsed)
			}
		}
		if s := e.Stats().BreakerState; s != weir.BreakerClosed {
			t.Fatalf("breaker = %v after 16 stalled uploads, want closed", s)
		}
		if _, body := serve(t, e, getReq("/innocent"), origin); body != "ok" {
			t.Fatalf("/innocent = %q, want ok", body)
		}
	})
}

// FR-CB-1: a body read that fails while the origin is receiving it is the
// client's failure too, however often it happens.
func TestFailedUploadReadIsRequestError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(cacheable("ok"))
		origin := transportOrigin{o: o}
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		for i := range 40 {
			_, err := e.Serve(t.Context(), bodyReq("/upload", failBody{}), origin)
			if !errors.Is(err, weir.ErrRequestBody) || weir.StatusCode(err) != http.StatusBadRequest {
				t.Fatalf("request %d: %v (status %d), want ErrRequestBody and 400", i, err, weir.StatusCode(err))
			}
		}
		if s := e.Stats().BreakerState; s != weir.BreakerClosed {
			t.Fatalf("breaker = %v after 40 failed body reads and no success, want closed", s)
		}
	})
}

// FR-CB-1: an origin that took the whole body and then does not answer is
// still an origin failure.
func TestSlowOriginWithBodyStillCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		origin := transportOrigin{o: o, answer: func(ctx context.Context) error {
			<-ctx.Done()
			return context.Cause(ctx)
		}}
		cfg := cacheCfg
		cfg.Timeouts.Origin = time.Second
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		var wg sync.WaitGroup
		for i := range 20 {
			wg.Go(func() {
				body := io.NopCloser(strings.NewReader("payload"))
				_, err := e.Serve(t.Context(), bodyReq("/upload"+string(rune('a'+i)), body), origin)
				if !errors.Is(err, weir.ErrOriginTimeout) {
					t.Errorf("request %d: %v, want ErrOriginTimeout", i, err)
				}
			})
			if i%8 == 7 { // the upload pool holds 16
				wg.Wait()
			}
		}
		wg.Wait()
		if s := e.Stats().BreakerState; s != weir.BreakerOpen {
			t.Fatalf("breaker = %v after 20 origin timeouts, want open", s)
		}
	})
}

// FR-LIM-7, T-39: one path holds at most half the upload pool, so stalled
// uploads to one URL leave room for uploads to another.
func TestUploadPoolPartitionCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		origin := &uploadOrigin{o: o}
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		stop := make(chan struct{})
		var chs []<-chan timedServe
		for range 100 {
			chs = append(chs, serveTimed(t, e, uploadReq(t.Context(), "/one", stop), origin))
		}
		synctest.Wait()
		if n := origin.max; n != 8 {
			t.Fatalf("uploads to one path in flight = %d, want half the pool of 16", n)
		}
		other := serveTimed(t, e, bodyReq("/two", io.NopCloser(strings.NewReader("x"))), origin)
		if r := <-other; r.err != nil || r.elapsed != 0 {
			t.Fatalf("upload to another path: %v after %v, want served at once", r.err, r.elapsed)
		}
		close(stop)
		for _, ch := range chs {
			<-ch
		}
	})
}
