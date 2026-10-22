# weirproxy

A caching reverse proxy built from `weir` and `weirhttp` with default settings. It is the program the [quickstart](https://weir.ashwinsathian.com/docs/quickstart/) runs, and the one the cache-tests conformance job tests.

```sh
go run github.com/AshwinSathian/weir/examples/weirproxy@v1.2.0 -listen :8080 -origin http://localhost:9000
```

| Flag | Default | Meaning |
|---|---|---|
| `-listen` | `:8080` | address to listen on |
| `-origin` | none, required | origin scheme and host, for example `http://localhost:9000`. A path is ignored |
| `-forward-allow` | empty | comma-separated request headers the origin may see although they are not keyed (`Forward.Allow`) |

Ask for a URL twice and read the `Cache-Status` header: the first response shows `fwd=uri-miss; stored`, the second `hit; ttl=N`.

It is an example, not a product. It has no TLS, no metrics endpoint and no purge API, it answers WebSocket and other upgrade requests with 501, and it uses the memory store, which takes 256 MiB for entries (plus about 4 MiB of fixed tables) when `GOMEMLIMIT` is unset. It sets read, header and idle timeouts but no write timeout, which would also end event streams; see the comment in `main.go`. For production, use the [Caddy module](https://weir.ashwinsathian.com/docs/guides/caddy/) or build your own server from [`main.go`](main.go), which is under 100 lines.
