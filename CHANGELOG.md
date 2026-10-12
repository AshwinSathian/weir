# Changelog

Format: one section per release, newest first. Versions follow [semantic versioning](https://semver.org). The root module and the `observe/prom`, `store/valkey` and `caddy` modules carry the same version number.

## v1.0.0 (2026-10-12)

First stable release. It supersedes the `v0.1.0` tag, which predates most of the engine and cannot be used with the sub-modules.

### Engine (root module)

- RFC 9111 shared-cache decisions per request, including conditional requests, `Vary`, `Age`, `Cache-Status` (RFC 9211), single-range responses from cache, and targeted cache-control (RFC 9213).
- Request coalescing with a follower wait bound and leader aging, TTL jitter, probabilistic early refresh, stale-while-revalidate and stale-if-error, a circuit breaker, negative caching and lazy purges (soft, hard and Cache Groups; optional eager hard purge).
- An origin concurrency limiter with per-partition fairness, an upload pool, a per-host cap and per-owner store quota for multi-tenant use, and a miss-rate anomaly detector.
- A cache key that is a security boundary: the forwarded request equals the keyed request, with strict `Vary` and cookie handling and fuzzed parsers.
- Memory store (S3-FIFO) with snapshots across restarts; the `store.Store` interface and its optional capability interfaces.
- Experiment-aware key dimensions (`Config.Experiments`): signed assignment cookie, consent gate, up to 4 signing keys of 32 to 256 bytes, `EvExposure`.
- `weirhttp` adapters for `net/http`.
- Hit path: one cached hit costs 852 ns and 12 allocations in process on an Apple M4 Pro with Go 1.27.2 (`BenchmarkServeHitSmall`), inside the budget of 1.25 µs and 16 allocations (NFR-5). Numbers and method are in docs/benchmarks.md.

### Modules

- `observe/prom`: Prometheus observer and collector.
- `store/valkey`: shared store for several nodes (tested against Valkey 8.1; `volatile-lfu` or `noeviction`).
- `caddy`: the `weir` handler, Caddyfile and JSON configuration, admin purge API, `experiments` block. Built against Caddy v2.11.7.

### Changed since the last pre-release work

- Experiment signing keys are limited to 4 keys and 256 bytes each.
- A stored response or negative entry whose status cannot be written by an HTTP server is treated as a miss.
- `TransportOrigin` no longer panics when `http.DefaultTransport` has been replaced, and returns an error for a missing `Target`.
- An origin body whose `Read` panics is closed instead of leaked.

### Changed after v1.0.0-rc.1

- `Key.QuerySort` sorts by parameter name only and keeps the order of a repeated name's values (`ids=3&ids=1` is no longer rewritten to `ids=1&ids=3`).
- An unkeyed `Cache-Control` or `Pragma` request field is forwarded only as a plain directive list (at most 8 lines and 256 bytes, letters, digits, `-`, `_`, `=`, `,` and space); anything else is dropped for that request.
- A `Range` request that also carries `If-Match` or `If-Unmodified-Since` gets the full 200 instead of a 206: Weir does not evaluate those preconditions.
- `store.Entry.Size` charges 48 bytes per header name and 16 per header value and vary name, so a memory store holds somewhat fewer small entries per byte of `MaxBytes` and can no longer be made to hold many times its limit.
- `store/valkey`: new `Config.MaxObjectBytes` (64 MiB). Larger values are never read whole and count as misses; `*Store` now reports its limit, so `weir.New` rejects a larger `Storable.MaxObjectBytes`.
- The root module builds for `js/wasm` and `wasip1/wasm`.

### Requirements

- Go 1.27.2 or later for anything you deploy. Earlier 1.27 releases carry standard library vulnerabilities that `govulncheck` reports against `weirhttp`.

### Known limits

- Pointer-light memory layout deferred (M16-02, D36): GC cost at 1M entries is 1.08 µs per request on the reference machine (Apple M4 Pro) and 1.76 µs on a 4-vCPU Linux container, inside the 2 µs gate.
- The `caddy` module imports the root module's `internal/keys`, so the four modules must be used at the same version (with xcaddy, pin the root module and `caddy` to it explicitly).
- `Engine.Close` waits for origin goroutines; an `Origin` that ignores its context can delay it.

Install: see the README. Documentation: https://weir.ashwinsathian.com
