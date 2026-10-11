package prom_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/observe/prom"
)

func ExampleNewObserver() {
	obs := prom.NewObserver()
	reg := prometheus.NewRegistry()

	e, err := weir.New(weir.Config{Observer: obs})
	if err != nil {
		fmt.Println("new:", err)
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		e.Close(ctx)
	}()
	reg.MustRegister(obs, prom.NewCollector(e))

	origin := weir.OriginFunc(func(ctx context.Context, req *weir.Request) (*weir.Response, error) {
		return &weir.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Cache-Control": {"max-age=60"}},
			Body:       io.NopCloser(strings.NewReader("hi")),
		}, nil
	})
	req := &weir.Request{Method: http.MethodGet, Scheme: "http", Host: "example.com", Path: "/", Header: http.Header{}}
	resp, err := e.Serve(context.Background(), req, origin)
	if err != nil {
		fmt.Println("serve:", err)
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	n, err := testutil.GatherAndCount(reg, "weir_requests_total")
	fmt.Println(n, err)
	// Output: 1 <nil>
}
