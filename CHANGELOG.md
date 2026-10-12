# Changelog

Format: one section per release, newest first. Versions follow [semantic versioning](https://semver.org). The root module and the `observe/prom`, `store/valkey` and `caddy` modules carry the same version number.

## v1.0.0 (unreleased)

First stable release. It supersedes the `v0.1.0` tag, which predates most of the engine and cannot be used with the sub-modules.

### Engine (root module)

- RFC 9111 shared-cache decisions per request, including conditional requests, `Vary`, `Age`, `Cache-Status` (RFC 9211), single-range responses from cache, and targeted cache-control (RFC 9213).
- Request coalescing with a follower wait bound and leader aging, TTL jitter, probabilistic early refresh, stale-while-revalidate and stale-if-error, a circuit breaker, negative caching and lazy purges (soft, hard and Cache Groups; optional eager hard purge).
- An origin concurrency limiter with per-partition fairness, an upload pool, a per-host cap and per-owner store quota for multi-tenant use, and a miss-rate anomaly detector.
- A cache key that is a security boundary: the forwarded request equals the keyed request, with strict `Vary` and cookie handling and fuzzed parsers.
- Memory store (S3-FIFO) with snapshots across restarts; the `store.Store` interface and its optional capability interfaces.
- Experiment-aware key dimensions (`Config.Experiments`): signed assignment cookie, consent gate, up to 4 signing keys of 32 to 256 bytes, `EvExposure`.
- `weirhttp` adapters for `net/http`.

### Modules

- `observe/prom`: Prometheus observer and collector.
- `store/valkey`: shared store for several nodes (Valkey 7 or later, `volatile-lfu` or `noeviction`).
- `caddy`: the `weir` handler, Caddyfile and JSON configuration, admin purge API, `experiments` block. Built against Caddy v2.11.7.

### Changed since the last pre-release work

- Experiment signing keys are limited to 4 keys and 256 bytes each.
- A stored response or negative entry whose status cannot be written by an HTTP server is treated as a miss.
- `TransportOrigin` no longer panics when `http.DefaultTransport` has been replaced, and returns an error for a missing `Target`.
- An origin body whose `Read` panics is closed instead of leaked.

### Requirements

- Go 1.27.2 or later for anything you deploy. Earlier 1.27 releases carry standard library vulnerabilities that `govulncheck` reports against `weirhttp`.

### Known limits

- Pointer-light memory layout deferred (M16-02, D36): GC cost at 1M entries is 1.76 µs per request on the measured machine, inside the 2 µs gate.
- The `caddy` module imports the root module's `internal/keys`, so the four modules must be used at the same version (with xcaddy, pin the root module and `caddy` to it explicitly).
- `Engine.Close` waits for origin goroutines; an `Origin` that ignores its context can delay it.
- The root module does not build for `js/wasm` or `wasip1/wasm` (the snapshot loader needs `O_NONBLOCK`).

Install: see the README.
