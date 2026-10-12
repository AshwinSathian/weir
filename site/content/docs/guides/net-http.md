---
title: net/http guide
description: The weirhttp adapter. Handler or Middleware, the two origin types, shutdown order and metrics.
---

`weirhttp` adapts an engine to `net/http`. It is part of the root module, so there is nothing extra to install.

## Handler or Middleware

```go
h := weirhttp.Handler(e, origin)      // an http.Handler
mw := weirhttp.Middleware(e, origin)  // a func(http.Handler) http.Handler
```

Both serve every ordinary request through the engine. They differ on `CONNECT` and protocol upgrades such as WebSocket, which a cache has no business in:

- `Handler` answers them 501.
- `Middleware` passes them to the next handler, outside the engine and its limiter (FR-UPG-1). Use it when the same server also carries WebSocket traffic.

## Choosing an origin

An origin is anything with this method:

```go
Fetch(ctx context.Context, req *weir.Request) (*weir.Response, error)
```

`weir.OriginFunc` adapts a plain function. Two implementations ship.

### TransportOrigin: an upstream server

```go
origin := &weirhttp.TransportOrigin{Target: target} // target is a *url.URL; its path is ignored
```

It sends the forwarded request to `Target` over an `http.RoundTripper`. Leave `Transport` nil and you get a clone of `http.DefaultTransport` with compression disabled and no proxy. If you pass your own `*http.Transport`, set `DisableCompression: true` as well. Go's transport otherwise adds `Accept-Encoding: gzip` to requests that have none, and that is a header the origin sees and the key does not (INV-1).

> [!PROTECTION]
> `TransportOrigin.Rewrite` edits each outgoing request, for a `Via` header or origin authentication. Any header it adds to a cacheable request is unkeyed input (T-4). Add only headers that cannot change the response. A proxy in front of Weir that appends `X-Forwarded-For` has the same effect if the origin reads it (R-1).

### HandlerOrigin: a handler in the same process

```go
origin := &weirhttp.HandlerOrigin{Handler: app}
```

The handler runs on its own goroutine and writes into a pipe that becomes the response body. Nothing is buffered twice.

> [!CAUTION]
> The handler receives the context values of the request that created the flight (FR-COA-9). If authentication middleware in front of Weir puts a user in the context and `app` reads it, the response is built for that user and may then be stored and shared (T-45, R-6). The key cannot see context values. Put per-user middleware behind Weir, or bypass those routes with `Bypass.Cookies` or `Bypass.Headers`.

## A complete server

The README program is the minimum:

{{fenced README.md go}}

For production, add graceful shutdown. Order matters: stop accepting requests, then close the engine, which waits for origin fetches and writes the snapshot if one is configured.

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
go func() { errc <- srv.ListenAndServe() }()
<-ctx.Done()

shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
defer cancel()
_ = srv.Shutdown(shutdown) // first: no new requests
_ = e.Close(shutdown)      // then: finish fetches, close the store
```

`examples/weirproxy/main.go` in the repository is this, complete, in under 100 lines.

## Using the engine directly

You do not need `weirhttp`. `Engine.Serve` takes a `weir.Request` and returns a `weir.Response`; this is the tested example from the package documentation:

{{example example_test.go ExampleEngine_Serve}}

It prints:

```text
200 "hello /page" hit=false
200 "hello /page" hit=true
origin calls: 1
```

`weirhttp.RequestFrom`, `WriteResponse` and `WriteError` are the conversion helpers if you are writing an adapter for another router.

## Metrics

Prometheus support is a separate module, so the root module stays free of dependencies:

```sh
go get github.com/AshwinSathian/weir/observe/prom@{{version}}
```

```go
obs := prom.NewObserver()
e, err := weir.New(weir.Config{Observer: obs})
// ...
reg.MustRegister(obs, prom.NewCollector(e))
```

Register one of each per engine and per registry. The metric list is in the [observability guide](/docs/guides/observability/). Without Prometheus, implement `weir.Observer` yourself and poll `Engine.Stats()`.

## Limits

> [!LIMITS]
> One engine serves one logical origin. `Engine.Close` waits for origin goroutines, so an `Origin` that ignores its context can delay shutdown. An `Observer` runs on the request path and its panics are not recovered: keep `Observe` fast and safe.
