package prom_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/observe/prom"
)

// FR-OBS-4: wired to a real engine, the exporter sees the reason strings the
// engine emits, so a renamed Reason fails here rather than silently changing
// dashboards.
func TestExporterWithRealEngine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		obs := prom.NewObserver()
		eng, err := weir.New(weir.Config{Observer: obs})
		if err != nil {
			t.Fatal(err)
		}
		origin := weir.OriginFunc(func(context.Context, *weir.Request) (*weir.Response, error) {
			return &weir.Response{
				StatusCode: 200,
				Header:     http.Header{"Cache-Control": {"max-age=60"}},
				Body:       io.NopCloser(strings.NewReader("ok")),
			}, nil
		})
		for range 2 {
			resp, err := eng.Serve(context.Background(), &weir.Request{
				Method: "GET", Scheme: "https", Host: "example.com", Path: "/a", Header: http.Header{},
			}, origin)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		want := `# HELP weir_requests_total Requests served, by outcome.
# TYPE weir_requests_total counter
weir_requests_total{outcome="hit"} 1
weir_requests_total{outcome="miss"} 1
`
		if err := testutil.CollectAndCompare(obs, strings.NewReader(want), "weir_requests_total"); err != nil {
			t.Error(err)
		}
		fetches := `# HELP weir_origin_fetches_total Origin fetches, by class and result.
# TYPE weir_origin_fetches_total counter
weir_origin_fetches_total{class="foreground",result="ok"} 1
`
		if err := testutil.CollectAndCompare(obs, strings.NewReader(fetches), "weir_origin_fetches_total"); err != nil {
			t.Error(err)
		}
		if got := testutil.CollectAndCount(prom.NewCollector(eng), "weir_origin_inflight", "weir_limiter_queue_depth", "weir_breaker_state", "weir_store_bytes"); got != 4 {
			t.Errorf("gauge series = %d, want 4", got)
		}
		if err := eng.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}
