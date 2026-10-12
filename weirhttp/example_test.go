package weirhttp_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/weirhttp"
)

func ExampleHandler() {
	e, err := weir.New(weir.Config{})
	if err != nil {
		fmt.Println("new:", err)
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		e.Close(ctx)
	}()

	var calls atomic.Int64
	origin := weir.OriginFunc(func(_ context.Context, _ *weir.Request) (*weir.Response, error) {
		calls.Add(1)
		return &weir.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Cache-Control": {"max-age=60"}},
			Body:       io.NopCloser(strings.NewReader("hello")),
		}, nil
	})
	h := weirhttp.Handler(e, origin)

	for range 2 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/page", nil))
		fmt.Println(rec.Code, rec.Body.String())
	}
	fmt.Println("origin calls:", calls.Load())
	// Output:
	// 200 hello
	// 200 hello
	// origin calls: 1
}
