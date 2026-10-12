# Changelog

Format: one section per release, newest first. Changes land under "Unreleased" in the pull request that makes them. Versions follow [semantic versioning](https://semver.org). The root module and the `observe/prom`, `store/valkey` and `caddy` modules carry the same version number.

## Unreleased

Hardening after an adversarial review of `v1.0.1` (cards HRD-01 to HRD-18, [docs/cards/22-hardening.md](docs/cards/22-hardening.md)). Several fixes add exported fields, so the next release is `v1.1.0`. Each entry names the requirement it changes.

### Changed behavior

Read these before upgrading. Each has a way back where one makes sense.

- Coalescing: a follower whose `Coalesce.FollowerMaxWait` expires without a servable stale entry keeps waiting for its flight until `Timeouts.Origin + Limiter.MaxQueueWait` after it joined, and only then fetches for itself (FR-COA-4). Before, 100 requests for one key behind an origin that answered in 15 s made 16 fetches and 84 responses 503. `EvCoalesceTimeout{direct}` now means the later deadline.
- Breaker: it opens only when the failures in the window come from at least `Breaker.MinPartitions` (new, default 2) partitions, or when nothing in the window succeeded (FR-CB-2). A path whose half-open probe failed leaves the next probe to another path for one second (FR-CB-4). Before, 20 responses 504 from one path opened it for every path. `MinPartitions: 1` is the old rule.
- Markers: an object over `Storable.MaxObjectBytes` writes a hit-for-miss marker, and a full response that is not storable replaces the stale response the request found (FR-STO-12). Behind an over-size marker the response is streamed, and a response whose `Content-Length` exceeds the limit is streamed without the buffered read. Cost: a URI that turns `private` loses its stale-if-error copy at once.
- Streams: `Timeouts.StreamIdle` also ends a stream that nobody reads for that long, and `weirhttp.WriteResponse` gives each write of a live stream a deadline of the same length (FR-TMO-2). A consumer that pauses longer than `StreamIdle` between reads of a streamed response is cut.
- Invalidation: on an in-process store with no keyed header, keyed cookie or experiment, an unsafe request writes its invalidation epoch only when the URI has a record or a fetch in progress (FR-INV-1). Before, 500 000 unsafe requests to uncached URIs sent about half of all other entries back to the origin.
- `weirhttp.TransportOrigin` removes `Forwarded`, `X-Real-Ip` and `X-Forwarded-*` from every outgoing request by default (FR-FWD-8). `XForwarded: weirhttp.XForwardedKeep` is the old behavior; `XForwardedSet` sets them from the request on a pass-through. A site that lists such a field in `Forward.Allow` or `Key.Headers` needs `XForwardedKeep`.
- Caddy: every forwarded request has a replacer bound to itself, so `{http.request.header.X}`, `{http.request.cookie.X}` and `{http.request.uri}` behind `weir` read what the origin is sent (08 §6). Placeholders that a matcher or handler in front of `weir` defined (`{http.regexp.*}`, `{http.auth.*}`, `map` outputs) are empty behind it; `client_placeholders` restores the old binding.
- A `GET` or `HEAD` that declares a body is passed through with it and never cached (FR-FWD-10). Before, the body was dropped and the answer cached.
- An encoded response is stored as if `Vary` named `Accept-Encoding`, so a compressed body without `Vary` is no longer served to a client that asked for another coding (FR-STO-14).
- A 304 whose entity-tag does not weakly match the stored one is discarded and the request repeated without conditionals (FR-SRV-3).
- `Close` returns 5 s after it has cancelled the engine's goroutines even when an `Origin` ignores its context, with an error that says so (FR-LCY-2). Before, it did not return.
- A stalled or failed request body ends in `ErrRequestBodyTimeout` (408) or `ErrRequestBody` (400) and is not a breaker outcome (FR-CB-1). One path holds at most half the upload pool (FR-LIM-7).
- A 502, 503 or 504 marked `no-store` or `private` writes no negative entry (FR-NEG-1). The `Retry-After` of a shed request is one to one and a half queue waits (FR-LIM-5).
- Memory store: `MaxHardEpochs` defaults to 100 000 (was 10 000). The engine's own store and the Caddy adapter use shards of about 16 MiB, so a store above 256 MiB has more than 16 shards and its largest object is 1.6 MiB unless `Storable.MaxObjectBytes` asks for more (05 §5.1).
- Hosts may contain `_` (FR-VAL-1). `TRACE` and `OPTIONS` honor `Max-Forwards` (FR-FWD-9).

### Added

- `Request.Client` and `Limiter.MaxPerClient` (0: off): a cap on one client's requests at the origin, whatever URLs they name (FR-FAIR-4). `weirhttp.RequestFrom` fills `Client` from the peer address; the Caddy adapter uses `client_ip` and gains `limiter { max_per_client N }`.
- `Breaker.MinPartitions`.
- `ErrRequestBody`, `ErrRequestBodyTimeout`.
- `weirhttp.XForwardedMode` and `TransportOrigin.XForwarded`.
- Response trailers of a streamed response reach the client: a response body may implement `Trailer() http.Header`, and both `weirhttp` origins do (FR-FWD-8).
- `Config.KeyGeneration()`: a digest of the settings that change what an unchanged key means, for snapshot file names and remote key prefixes (FR-SNP-4).
- `memory.Shards(maxBytes, maxObject)`.
- With `Client.HonorRevalidation`, request `max-age` and `min-fresh` are applied (FR-SRV-8).
- Caddy key `client_placeholders`.

### Performance

- The miss-rate tracker no longer takes one mutex per cacheable request: a hit takes a read lock and never replaces a counter (FR-MR-1). The limiter's release walk stops once no slot is free. Numbers are in [docs/benchmarks.md](docs/benchmarks.md).

### Known limits, unchanged

- Coalescing is per process, and a remote store has no local tier in front of it (deferred card HRD-20).
- With `Limiter.MaxPerClient` off, which is the default, a flood of distinct paths can still take every origin slot (T-11, R-2).
- An unknown method still invalidates on 2xx or 3xx, as RFC 9111 §4.4 requires; `identity;q=0` still gets `identity`.

Earlier entries under this heading:

- The root module no longer ships the website: `site/` has its own `go.mod`, which keeps it out of the module zip (about 0.16 MB smaller than `v1.0.1`). No change to any Go package.
- Repository: `main` and `release/*` are protected by GitHub rulesets (pull request and a green `check` for `main`; no force-push or deletion for both).

## v1.0.1 (2026-10-12)

Documentation release. No change to the behavior or the API of any module; the only Go changes are two tests and one doc comment. It exists so that pkg.go.dev shows the current README.

- Documentation: a website at https://weir.ashwinsathian.com (source in `site/`, built from this repository), a shorter README that links to it, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md` and issue templates. The README's table of settings that take a protection back moved to the site's tuning page.
- Site: links inside text taken from repository documents now point at the right GitHub path, and the build fails on a GitHub link to a file that is not in the repository.
- Wording corrections in docs/01 (status line, the targeted cache-control non-goal), docs/03 (a note on what its flows do not cover), the runbook (8.9 step 1 agrees with 7.8 about `caddy validate`) and docs/benchmarks.md (a "Current results" section). No requirement, default or signature changed.
- Documents: threat model rows T-46 to T-51 for the experiment dimensions (signing keys, header aliases, `Set-Cookie` isolation, the assignment cookie on the forward, cookie tossing, exposure floods), with residual risks R-9 and R-10 and runbook advice to use `{env.NAME}` key placeholders and a `__Host-` cookie name. No behavior change.
- `store/valkey` is tested only against Valkey 8.1 (in CI). The v1.0.0 notes below said "Valkey 7 or later" without saying what was tested; they now do.
- README, runbook and the Caddy package comment name `v1.0.1` in their install lines. Tags: `v1.0.1`, `observe/prom/v1.0.1`, `store/valkey/v1.0.1` and `caddy/v1.0.1`.

## v1.0.0 (2026-10-12)

First stable release. It supersedes the `v0.1.0` tag, which predates most of the engine and cannot be used with the sub-modules.

The copy of this file inside the `v1.0.0` tag reads "v1.0.0 (unreleased)": the date was added on `main` after the tag. It lists the same changes; two headings and three lines were corrected here afterwards.

Tags: `v1.0.0`, `observe/prom/v1.0.0`, `store/valkey/v1.0.0` and `caddy/v1.0.0`. The release candidates `v1.0.0-rc.1` and `v1.0.0-rc.3` stay on the module proxy for all four modules; `caddy/v1.0.0-rc.2` is the same commit as `caddy/v1.0.0-rc.1` and has no counterpart in the other modules.

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
- `store/valkey`: shared store for several nodes (written for Valkey 7 or later; tested only against Valkey 8.1, in CI; `maxmemory-policy` must be `volatile-lfu`, another `volatile-*` policy or `noeviction`).
- `caddy`: the `weir` handler, Caddyfile and JSON configuration, admin purge API, `experiments` block. Built against Caddy v2.11.7.

### Fixed before the first release candidate

- Experiment signing keys are limited to 4 keys and 256 bytes each.
- A stored response or negative entry whose status cannot be written by an HTTP server is treated as a miss.
- `TransportOrigin` no longer panics when `http.DefaultTransport` has been replaced, and returns an error for a missing `Target`.
- An origin body whose `Read` panics is closed instead of leaked.

### Changed between v1.0.0-rc.1 and v1.0.0

- `Key.QuerySort` sorts by parameter name only and keeps the order of a repeated name's values (`ids=3&ids=1` is no longer rewritten to `ids=1&ids=3`).
- An unkeyed `Cache-Control` or `Pragma` request field is forwarded only as a plain directive list (at most 8 lines and 256 bytes, letters, digits, `-`, `_`, `=`, `,` and space); anything else is dropped for that request.
- A `Range` request that also carries `If-Match` or `If-Unmodified-Since` gets the full 200 instead of a 206: Weir does not evaluate those preconditions.
- `store.Entry.Size` charges 48 bytes per header name and 16 per header value and vary name, so a memory store holds somewhat fewer small entries per byte of `MaxBytes` and can no longer be made to hold many times its limit.
- `store/valkey`: new `Config.MaxObjectBytes` (64 MiB). Larger values are never read whole and count as misses; `*Store` now reports its limit, so `weir.New` rejects a larger `Storable.MaxObjectBytes`.
- The root module builds for `js/wasm` and `wasip1/wasm`.

### Requirements

- Go 1.27.2 or later for anything you deploy. Earlier 1.27 releases carry standard library vulnerabilities that `govulncheck` reports against `weirhttp`.

### Known limits

- Pointer-light memory layout deferred (M16-02, D36): GC cost at 1M entries is about 1 µs per request on the reference machine (Apple M4 Pro; 1.76 µs on a 4-core Xeon), inside the 2 µs gate.
- The `caddy` module imports the root module's `internal/keys`, so the four modules must be used at the same version (with xcaddy, pin the root module and `caddy` to it explicitly).
- `Engine.Close` waits for origin goroutines; an `Origin` that ignores its context can delay it.
- `store/valkey`: a new or flushed server stores nothing usable for `MaxClockSkew` + 1 s (2 s by default) after the first request; a value at an entry key that fits `MaxObjectBytes` but is not a Weir record counts as a store error. The Caddyfile has no key for `MaxObjectBytes`.

Install: see the README.
