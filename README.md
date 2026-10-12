# Weir

Weir is a Go library for shared HTTP caching in front of an origin that has to stay up. The request it forwards is the request it keyed, so a header the cache ignores cannot poison an entry, and a stampede cannot reach the origin faster than a budget you set.

A weir is a low dam that regulates flow without stopping it. Adapters for `net/http` and Caddy handle the wire; Weir makes the decision for each request.

[![ci](https://github.com/AshwinSathian/weir/actions/workflows/ci.yml/badge.svg)](https://github.com/AshwinSathian/weir/actions/workflows/ci.yml) [![Go reference](https://pkg.go.dev/badge/github.com/AshwinSathian/weir.svg)](https://pkg.go.dev/github.com/AshwinSathian/weir) [![release](https://img.shields.io/github/v/release/AshwinSathian/weir)](https://github.com/AshwinSathian/weir/releases) [![license](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

Documentation: [weir.ashwinsathian.com](https://weir.ashwinsathian.com)

## See it work

```go
package main

import (
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/weirhttp"
)

func main() {
	target, _ := url.Parse("http://localhost:9000") // scheme and host; a path is ignored
	e, err := weir.New(weir.Config{})               // the zero Config is valid
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{
		Addr:              ":8080",
		Handler:           weirhttp.Handler(e, &weirhttp.TransportOrigin{Target: target}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(srv.ListenAndServe()) // on shutdown: srv.Shutdown(ctx), then e.Close(ctx)
}
```

`examples/weirproxy` is this program with flags. Run it in front of an origin that answers `Cache-Control: max-age=60` and ask twice:

```console
$ go run github.com/AshwinSathian/weir/examples/weirproxy@v1.0.1 -listen :8080 -origin http://localhost:9000
$ curl -si localhost:8080/ | grep -i -e '^age' -e cache-status
Cache-Status: Weir; fwd=uri-miss; fwd-status=200; stored
$ curl -si localhost:8080/ | grep -i -e '^age' -e cache-status
Age: 13
Cache-Status: Weir; hit; ttl=43
```

The first request went to the origin and was stored. The second, 13 seconds later, never left the process. Its `ttl` is 43 and not 47 because jitter took four seconds off this entry's lifetime, so entries stored together do not expire together. On Caddy the same thing is a `weir { name site-a }` block in front of `reverse_proxy`; see the [quickstart](https://weir.ashwinsathian.com/docs/quickstart/).

## What you get

- Coalescing. Concurrent requests for one key share one origin fetch, with a bound on how long followers wait and on how long a slow leader keeps the flight (FR-COA-1). [Coalescing](https://weir.ashwinsathian.com/docs/concepts/coalescing/)
- Jitter and early refresh. Each lifetime is shortened by up to 10%, and a hot entry is refreshed in the background shortly before it expires (FR-FRS-5, FR-FRS-6). [Freshness and staleness](https://weir.ashwinsathian.com/docs/concepts/freshness-and-staleness/)
- An origin budget. Every fetch holds a slot from a limiter (64 by default) with a per-path share, so 10,000 keys expiring at once reach the origin 64 at a time (FR-LIM-1). [Origin protection](https://weir.ashwinsathian.com/docs/concepts/origin-protection/)
- Stale serving and a breaker. Stale-while-revalidate, stale-if-error and a circuit breaker turn an origin outage into stale content or a fast 503 (FR-STL-1, FR-CB-1). Same page.
- Purges that do not stampede. A soft purge marks entries stale and lets them revalidate through the limiter; a hard purge removes them. By URL, by RFC 9875 group, or everything (FR-PRG-1). [Purging](https://weir.ashwinsathian.com/docs/concepts/purging/)
- A cache key that is a security boundary. On a cacheable request the origin receives only keyed, allowed and protocol headers (FR-FWD-1, INV-1). [The cache key](https://weir.ashwinsathian.com/docs/concepts/cache-key/)

## What Weir is not

A CDN, a WAF, a per-client rate limiter, a private (browser) cache, or a cache for application data. It does not store 206 responses (it answers single ranges from complete objects), has no prefix purge, and the memory store does not share a cache between nodes: several nodes need the Valkey store. It bounds origin load, so put a rate limiter in front of it for floods of distinct paths.

## Install

```sh
go get github.com/AshwinSathian/weir@v1.0.1                    # the engine, weirhttp, the memory store
go get github.com/AshwinSathian/weir/observe/prom@v1.0.1       # Prometheus metrics
go get github.com/AshwinSathian/weir/store/valkey@v1.0.1       # shared store for several nodes
xcaddy build --with github.com/AshwinSathian/weir@v1.0.1 --with github.com/AshwinSathian/weir/caddy@v1.0.1   # Caddy with the weir handler
```

The four modules are tagged together and share a version number, so use the same one for all of them. The Caddy module has the import path `github.com/AshwinSathian/weir/caddy` and the Go package name `weircaddy`; it is tested only against the root module at its own version, which is why xcaddy gets both pinned.

The minimum is Go 1.27.0, and CI tests the two latest Go releases. Deploy with 1.27.2 or later: earlier 1.27 releases carry standard library vulnerabilities (HTTP/2, `net/textproto`, `crypto/tls`) that `govulncheck` reports against the paths `weirhttp` uses. The root module builds for every target Go supports, `js/wasm` and `wasip1/wasm` included.

## Two things to know before production

Strict forwarding. If your origin reads `Accept-Language`, a device header or a cookie to build a page, it will not see it until you say so. Key it (`Key.Headers`, `Key.Cookies`: one entry per value), allow it (`Forward.Allow`: the origin sees it, the key does not, so only for a header that cannot change the response), or lose it. For the first five minutes Weir counts the cookie names it dropped and logs the common ones once.

Settings that take a protection back. `Forward.Mode: weir.ForwardAll` returns every header to the origin unkeyed. `Storable.StripSetCookie` stores responses that set cookies. `Freshness.DefaultStaleIfError` serves stale content the origin never allowed. Each is right for some site, and each returns a risk the defaults remove. The [tuning page](https://weir.ashwinsathian.com/docs/tuning/) lists all of them with what you give up and the threat ID.

Set `GOMEMLIMIT` (the memory store takes 40% of it) and read the [production guide](https://weir.ashwinsathian.com/docs/guides/production/) first.

## Status and stability

Everything planned for v1 is implemented: the engine, single-range responses, targeted cache-control (RFC 9213), snapshots across restarts, the Caddy module, the Valkey store and experiment-aware keys. A pointer-light memory layout (M16-02) is deferred until a deployment exceeds the GC budget at 1M entries.

From `v1.0.0` the exported Go API of the root module, `store`, `store/memory`, `store/storetest`, `weirhttp` and `observe/prom` follows semantic versioning. So do the metric families and labels, the `Cache-Status` parameter names, the Caddyfile directives and the Caddy JSON field names. Free-text values (`detail` and `fwd` strings, `Event.Reason`) and event kinds may gain values in a minor release, so match on them with a default case. Construct structs with field names: fields can be added in a minor release. Defaults that protect (strict forwarding, `Vary` handling, stale serving) change only in a major release.

## Security

Weir decides what a shared cache stores and serves, so a bug can hand one user's response to another. The [threat model](docs/06-threat-model.md) lists 51 threats with the answer and the test for each, 7 invariants and 10 residual risks; the [security page](https://weir.ashwinsathian.com/security/) puts the residual risks in plain language. Report a vulnerability privately through the repository's Security tab ([SECURITY.md](SECURITY.md)).

## Performance

Measured on an Apple M4 Pro (12 cores), in-process, with no network or TLS. Other hardware gives other numbers.

| What | Result | Command |
|---|---|---|
| One cached hit (`BenchmarkServeHitSmall`, Go 1.27.1) | 669 ns, 8 allocations (`v1.0.1` in the same run: 964 ns, 12) | `make bench` |
| Hits from 12 goroutines (`BenchmarkServeHitParallel`) | 225 ns per hit (`v1.0.1`: 1,005 ns) | `make bench` |
| 10,000 keys expiring together, limit 32 (Go 1.27.1) | origin peak in flight 32, 0 client errors | `make load` |
| Origin brownout (Go 1.27.1) | every request 2xx, 54,155 served stale | `make load` |

Method, raw output and what these do not show: [benchmarks](https://weir.ashwinsathian.com/benchmarks/).

## Compared with

| | Choose it when | Choose Weir when |
|---|---|---|
| Varnish | you want a standalone cache with VCL and years of production use | the cache lives inside a Go server or Caddy and you want a concurrency budget across keys |
| nginx `proxy_cache` | nginx is already your proxy and `proxy_cache_lock` covers your herds | you need jitter, a breaker and strict forwarding |
| Souin, Caddy cache-handler | you need its storage backends or surrogate keys today | key hardening and origin protection matter more than backend choice |
| `x/sync/singleflight`, sturdyc | you are deduplicating function calls, not HTTP responses | you need HTTP semantics: `Vary`, validation, `Cache-Control` |
| CDN request collapsing | you want a managed edge | you need the behavior at your own origin tier, in code you control |

Details and sources: [compare](https://weir.ashwinsathian.com/compare/).

## Documentation

The site has the [quickstart](https://weir.ashwinsathian.com/docs/quickstart/), concept pages, guides for `net/http` and Caddy, and the [configuration reference](https://weir.ashwinsathian.com/docs/reference/config/). In this repository: the [runbook](docs/runbook.md) for operators, the [design documents](docs/README.md), the [changelog](CHANGELOG.md), and [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
