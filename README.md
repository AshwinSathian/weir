# Weir

Weir is a Go library for shared HTTP caching in front of an origin that has to stay up. It implements RFC 9111 caching and adds the defenses most caches leave out: TTL jitter, request coalescing with timeouts, an origin concurrency budget with per-path fairness, stale-while-revalidate and stale-if-error, a circuit breaker, short negative caching, lazy soft purges, and a cache key built as a security boundary.

A weir is a low dam that regulates flow without stopping it. That is the job: let traffic through to the origin at a rate it can survive, whatever the cache is doing.

## Status

Phase 1 (the engine, milestones M1 to M10) is implemented and tested; the release gate and the runbook are the last open items. The public API can still change before the first tag. The Caddy module, the Valkey store and the experiment dimensions come in later phases.

| Phase | Scope | State |
|---|---|---|
| 0 | skeleton, CI, test harness | done |
| 1 | the engine, milestone by milestone (M1 to M10) | implemented, in hardening |
| 1.x | single-range responses, targeted cache-control, snapshots, per-host fairness, eager purge (M11 to M16) | specified |
| 2 | Caddy module (single node) | draft spec |
| 2.5 | Valkey store, multi-node | planned |
| 3 | experiment-aware key dimensions | draft spec |

## Why another cache

Existing caches solve RFC correctness and same-key coalescing well. Weir exists for the cases that still take origins down: many keys expiring together, purges and cold starts that turn into cross-key stampedes, storage outages that fail open, and attackers who generate guaranteed misses or poison entries through inputs the cache does not key. The full argument, with prior art, is in [docs/00-design-doc.md](docs/00-design-doc.md).

## Quick start

Put Weir in front of an `http.Handler` or an upstream URL with the `weirhttp` adapter. The zero `weir.Config` is valid.

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
	e, err := weir.New(weir.Config{})
	if err != nil {
		log.Fatal(err)
	}
	origin := &weirhttp.TransportOrigin{Target: target}

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           weirhttp.Handler(e, origin),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
	// On shutdown: srv.Shutdown(ctx), then e.Close(ctx).
}
```

- `weirhttp.Handler(e, origin)` serves every request through the engine. `weirhttp.Middleware(e, origin)` returns a `func(http.Handler) http.Handler` that does the same but hands CONNECT and protocol upgrades to the next handler.
- An origin is anything with `Fetch(ctx, *weir.Request) (*weir.Response, error)`. `weirhttp.TransportOrigin` forwards to an upstream over an `http.RoundTripper`; `weirhttp.HandlerOrigin{Handler: h}` runs an in-process `http.Handler` as the origin.
- `examples/weirproxy` is a complete reverse proxy built this way: `weirproxy -listen :8080 -origin http://localhost:9000`.
- Responses carry an RFC 9211 `Cache-Status` header (member name `Weir`, change it with `Config.CacheStatus`, drop it with `NoCacheStatus`).
- Set `GOMEMLIMIT`. The default memory store takes 40% of it (256 MiB with a warning when it is unset).
- Read [docs/runbook.md](docs/runbook.md) before putting it in front of production traffic.

Configuration is the `weir.Config` struct; every field, default and range is in [docs/01-technical-spec.md §6](docs/01-technical-spec.md). For a first run, `Key.QueryDrop: weir.TrackingParams()` is the one setting most sites want: it keeps `utm_*`, `gclid`, `fbclid` and similar from minting a key each.

## Strict forwarding

For a cacheable request, the origin receives only the headers that are part of the cache key, are explicitly allowed, or HTTP requires (`Authorization`, `Cache-Control`, `Pragma`, a normalized `Accept-Encoding`), plus the trace headers `traceparent`, `tracestate` and `X-Request-Id`. The request Weir forwards is the request it keyed. That equality is what makes unkeyed-input cache poisoning structurally impossible: an attacker cannot change the response for other users with a header the key ignores, because the origin never sees such a header.

The consequence for you: if your origin reads `Accept-Language`, `X-Device`, a cookie or any other request header to build a page, say so. Put it in `Key.Headers` or `Key.Cookies` (one cache entry per distinct value) or, when the response must not vary on it, in `Forward.Allow` (the origin sees it, the key does not, so only do this for a header that cannot change the response). A cookie the origin needs but you do not list is dropped. For the first `Bypass.ReportStrippedCookies` (5 minutes by default) after `New`, Weir counts the dropped cookie names (never values) and logs the most common ones once, so you can find the ones you forgot.

Weir also serves stale content only when the origin allows it with `stale-while-revalidate` or `stale-if-error`, unless you set `Freshness.DefaultStaleWhileRevalidate` or `DefaultStaleIfError`.

## Settings that take a protection back

Each of these is safe to use when you know why. Each one returns a risk the defaults remove ([docs/06 §5](docs/06-threat-model.md), R-3 and R-8).

| Setting | What you take back | Use it when |
|---|---|---|
| `Forward.Mode: weir.ForwardAll` | strict forwarding: every request header reaches the origin, unkeyed, on cacheable requests too. `New` logs a warning. Cookie bypass rules then match names as sent, so an origin that rewrites cookie names (PHP turns `.` and a space into `_`) needs each spelling in `Bypass.Cookies` | the origin cannot be audited for header use and you accept the poisoning risk; a long `Forward.Allow` is usually better |
| `Forward.Allow` with many names | the same, one header at a time. `New` logs a warning for `Cookie`, `Authorization` and `Proxy-Authorization` | a header cannot change the response but the origin requires it |
| `Storable.StripSetCookie: true` | the rule that a response with `Set-Cookie` is not stored: Weir removes the field, stores the rest and still sends the cookie to the client whose request triggered the fetch (FR-STO-6) | the origin sets a cookie on every response (analytics) and the body does not depend on it |
| `Key.VaryAllow: ["Cookie"]` (or `Authorization`) | the rule that a response varying on `Cookie`, `Authorization` or `Proxy-Authorization` is not stored (FR-KEY-9) | a cookie really selects a small set of public variants |
| `Freshness.DefaultTTL`, `DefaultStaleWhileRevalidate`, `DefaultStaleIfError` | origin authority over freshness and staleness (D6): a response with no explicit lifetime or `Last-Modified` becomes cacheable, and stale content is served that the origin never allowed | the origin sends no freshness headers and cannot be changed |
| `Client.HonorRevalidation: true` | the default of ignoring client `Cache-Control: no-cache`, `max-age`, `min-fresh`, `max-stale` and `Pragma: no-cache` (D5): reloads reach the origin, and so can anyone who sends those headers | you want browser reloads to bypass the cache |
| `Breaker.Disable`, `Negative.Disable`, `MissRate.Disable`, `Freshness.NoJitter`, `Freshness.NoEarlyRefresh` | the matching defense: circuit breaker, short negative caching, miss-rate detection, TTL jitter, early refresh | a test, or a measured reason |
| `TransportOrigin.Rewrite` adding headers | the same, from the adapter side: a header it adds to a cacheable request is unkeyed input (T-4). Proxies in front of Weir that add `X-Forwarded-For` have the same effect (R-1) | the origin needs `Via` or origin auth and never varies on it |
| `Storable.Statuses` | the default status set; 206, 304, 500, 502, 503 and 504 are never storable whatever you list | you cache an unusual status on purpose |
| `MissRate.Throttle: true` | the busy path's own misses: a client that floods it with distinct queries can keep it capped at one origin fetch for as long as it sends (R-8). Off by default | after a run without it where you read the warnings, see the runbook |
| `CacheGroups.Ignore: true` | RFC 9875 group invalidation (`Cache-Group-Invalidation` is no longer acted on) | the origin's group headers should not purge other entries |

A change to a default that weakens security or stale serving needs a reason you can write down; the project treats this as a hard rule for itself (CLAUDE.md, rule 12).

## Design documents

Start with [docs/README.md](docs/README.md). The build plan is [PLAN-weir.md](PLAN-weir.md). Agent instructions are in [CLAUDE.md](CLAUDE.md).

## Requirements

Go 1.27 or later; Weir supports the two latest Go releases. The core module has no dependencies outside the standard library.

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
