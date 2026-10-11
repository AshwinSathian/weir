package weir_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// FR-PRG-3, FR-PRG-7, T-10: a hard purge that lands while a stale request
// waits for an origin slot must not be undone by the origin's 304. The
// validation is sent after the purge, so the new entry's request time
// postdates the epoch; the 304 may not bring the purged body back.
func TestHardPurgeDuringQueuedRevalidation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		gate := make(chan struct{})
		o.Route("/b", testorigin.Behavior{Gate: gate, Body: []byte("b")})
		o.Route("/a", testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=10"}, "Etag": {`"v1"`}}, Body: []byte("purged")})
		cfg := cacheCfg
		cfg.Limiter = weir.LimiterConfig{MaxConcurrent: 1, MaxQueue: 10, MaxQueueWait: time.Minute}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(20 * time.Second) // stale, with a validator
		o.Route("/a", testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			h := http.Header{"Cache-Control": {"max-age=3600"}, "Etag": {`"v1"`}}
			if r.Header.Get("If-None-Match") != "" {
				return &weir.Response{StatusCode: http.StatusNotModified, Header: h, Body: http.NoBody}, nil
			}
			return &weir.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(strings.NewReader("fresh"))}, nil
		}})
		go func() { serve(t, e, getReq("/b"), o) }() // holds the only slot
		synctest.Wait()
		done := make(chan string)
		go func() { _, b := serve(t, e, getReq("/a"), o); done <- b }() // waits for the slot
		synctest.Wait()
		if err := e.Purge(t.Context(), weir.Purge{Mode: weir.PurgeHard, URLs: []string{"https://example.com/a"}}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Second) // the validation will be sent after the epoch
		close(gate)
		if b := <-done; b != "fresh" {
			t.Fatalf("request after a hard purge got %q, want the origin's fresh body", b)
		}
		synctest.Wait()
		if _, b := serve(t, e, getReq("/a"), o); b != "fresh" {
			t.Fatalf("later GET got %q, want fresh", b)
		}
	})
}
