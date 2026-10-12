---
title: Go API
description: Links to the package documentation on pkg.go.dev. The site does not copy godoc.
---

Every exported identifier has a doc comment, enforced by a test (`doccomments_test.go`). The package documentation is the reference; this page is a map.

| Package | Import path | What it holds |
|---|---|---|
| [`weir`](https://pkg.go.dev/github.com/AshwinSathian/weir) | `github.com/AshwinSathian/weir` | `Engine`, `Config`, `Request`, `Response`, `Origin`, `Purge`, events, errors |
| [`weirhttp`](https://pkg.go.dev/github.com/AshwinSathian/weir/weirhttp) | `.../weir/weirhttp` | `Handler`, `Middleware`, `TransportOrigin`, `HandlerOrigin` |
| [`store`](https://pkg.go.dev/github.com/AshwinSathian/weir/store) | `.../weir/store` | the `Store` interface, `Entry`, optional capability interfaces, the codec |
| [`store/memory`](https://pkg.go.dev/github.com/AshwinSathian/weir/store/memory) | `.../weir/store/memory` | the in-process S3-FIFO store |
| [`store/storetest`](https://pkg.go.dev/github.com/AshwinSathian/weir/store/storetest) | `.../weir/store/storetest` | the conformance suite for store implementations |
| [`prom`](https://pkg.go.dev/github.com/AshwinSathian/weir/observe/prom) | `.../weir/observe/prom` (own module) | Prometheus observer and collector |
| [`valkey`](https://pkg.go.dev/github.com/AshwinSathian/weir/store/valkey) | `.../weir/store/valkey` (own module) | the shared Valkey store |
| [`weircaddy`](https://pkg.go.dev/github.com/AshwinSathian/weir/caddy) | `.../weir/caddy` (own module) | the Caddy handler and admin API |

## The methods you will call

```go
e, err := weir.New(cfg)                       // build an engine
resp, err := e.Serve(ctx, req, origin)        // one request
err = e.Purge(ctx, weir.Purge{...})           // invalidate
stats, err := e.Warm(ctx, reqs, origin)       // prefill at background priority
err = e.SetMode(weir.ModeStaleOnError, ttl)   // incident mode with a time limit
s := e.Stats()                                // gauges
err = e.Close(ctx)                            // stop, wait for fetches, close the store
```

## Stability

From `v1.0.0` the exported API of the root module, `store`, `store/memory`, `store/storetest`, `weirhttp` and `observe/prom` follows semantic versioning. Construct structs with field names, because fields can be added in a minor release, and match on event kinds and free-text values with a default case.
