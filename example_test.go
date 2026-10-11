package weir_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/AshwinSathian/weir"
)

// newExampleOrigin counts calls and returns a response cacheable for a minute.
func newExampleOrigin(calls *int) weir.Origin {
	return weir.OriginFunc(func(_ context.Context, req *weir.Request) (*weir.Response, error) {
		*calls++
		return &weir.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Cache-Control": {"max-age=60"}},
			Body:       io.NopCloser(strings.NewReader("hello " + req.Path)),
		}, nil
	})
}

func exampleGet(path string) *weir.Request {
	return &weir.Request{Method: http.MethodGet, Scheme: "http", Host: "example.com", Path: path, Header: http.Header{}}
}

func exampleClose(e *weir.Engine) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Close(ctx); err != nil {
		fmt.Println("close:", err)
	}
}

func ExampleNew() {
	e, err := weir.New(weir.Config{})
	if err != nil {
		fmt.Println("new:", err)
		return
	}
	defer exampleClose(e)
	fmt.Println("engine ready")
	// Output: engine ready
}

func ExampleEngine_Serve() {
	e, err := weir.New(weir.Config{})
	if err != nil {
		fmt.Println("new:", err)
		return
	}
	defer exampleClose(e)

	calls := 0
	origin := newExampleOrigin(&calls)
	for range 2 {
		resp, err := e.Serve(context.Background(), exampleGet("/page"), origin)
		if err != nil {
			fmt.Println("serve:", err)
			return
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Printf("%d %q hit=%v\n", resp.StatusCode, body, resp.Cache.Hit)
	}
	fmt.Println("origin calls:", calls)
	// Output:
	// 200 "hello /page" hit=false
	// 200 "hello /page" hit=true
	// origin calls: 1
}

func ExampleEngine_Close() {
	e, err := weir.New(weir.Config{})
	if err != nil {
		fmt.Println("new:", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fmt.Println("close:", e.Close(ctx))

	calls := 0
	_, err = e.Serve(ctx, exampleGet("/"), newExampleOrigin(&calls))
	fmt.Println("serve after close:", errors.Is(err, weir.ErrClosed), "origin calls:", calls)
	// Output:
	// close: <nil>
	// serve after close: true origin calls: 0
}

func ExampleEngine_Purge() {
	e, err := weir.New(weir.Config{})
	if err != nil {
		fmt.Println("new:", err)
		return
	}
	defer exampleClose(e)

	calls := 0
	origin := newExampleOrigin(&calls)
	fetch := func() {
		resp, err := e.Serve(context.Background(), exampleGet("/page"), origin)
		if err != nil {
			fmt.Println("serve:", err)
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	fetch()
	fetch()
	fmt.Println("calls before purge:", calls)

	err = e.Purge(context.Background(), weir.Purge{
		Mode: weir.PurgeHard,
		URLs: []string{"http://example.com/page"},
	})
	if err != nil {
		fmt.Println("purge:", err)
		return
	}
	fetch()
	fmt.Println("calls after purge:", calls)
	// Output:
	// calls before purge: 1
	// calls after purge: 2
}

func ExampleEngine_Warm() {
	e, err := weir.New(weir.Config{})
	if err != nil {
		fmt.Println("new:", err)
		return
	}
	defer exampleClose(e)

	calls := 0
	origin := newExampleOrigin(&calls)
	reqs := slices.Values([]*weir.Request{exampleGet("/a"), exampleGet("/b")})

	st, err := e.Warm(context.Background(), reqs, origin)
	fmt.Printf("%+v %v\n", st, err)
	st, err = e.Warm(context.Background(), reqs, origin)
	fmt.Printf("%+v %v\n", st, err)
	// Output:
	// {Fetched:2 Skipped:0 NotStored:0 Failed:0} <nil>
	// {Fetched:0 Skipped:2 NotStored:0 Failed:0} <nil>
}

func ExampleTrackingParams() {
	e, err := weir.New(weir.Config{
		Key: weir.KeyConfig{QueryDrop: weir.TrackingParams()},
	})
	if err != nil {
		fmt.Println("new:", err)
		return
	}
	defer exampleClose(e)

	calls := 0
	origin := newExampleOrigin(&calls)
	for _, q := range []string{"utm_source=a", "utm_source=b", "fbclid=xyz"} {
		req := exampleGet("/page")
		req.RawQuery = q
		resp, err := e.Serve(context.Background(), req, origin)
		if err != nil {
			fmt.Println("serve:", err)
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	fmt.Println("origin calls:", calls)
	// Output: origin calls: 1
}

func ExampleStatusCode() {
	fmt.Println(weir.StatusCode(nil))
	fmt.Println(weir.StatusCode(errors.New("boom")))
	fmt.Println(weir.StatusCode(weir.ErrClosed))
	// The values below depend on the error mapping in 01 §4.
	// Output:
	// 502
	// 502
	// 503
}

func ExampleRetryAfter() {
	err := &weir.RetryError{Err: weir.ErrShed, After: 1500 * time.Millisecond}
	d, ok := weir.RetryAfter(err)
	fmt.Println(d, ok)

	_, ok = weir.RetryAfter(errors.New("plain"))
	fmt.Println(ok)
	// Output:
	// 2s true
	// false
}
