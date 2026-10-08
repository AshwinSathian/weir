# Runbook

Updated: 2026-10-08

For the person running Weir in front of an origin. It covers first deployment, the signals to watch and what to do when each one fires. Setting names are in [01 §6](01-technical-spec.md); the reasoning behind the defaults is in [06](06-threat-model.md). This is not a design document: when it disagrees with 01 to 07, they win.

## 1. Before the first deployment

1. Set `GOMEMLIMIT`. The default memory store takes 40% of it, clamped to 16 MiB to 8 GiB. Without it the store takes 256 MiB and `New` logs a warning. Weir does not look at cgroup limits for you.
2. List what the origin reads from the request. Strict forwarding sends the origin only keyed, allowed and required headers (README, "Strict forwarding"). An `Accept-Language`, device header or cookie that is not in `Key.Headers`, `Key.Cookies` or `Forward.Allow` is invisible to the origin, so a page built from it comes out wrong, and the same wrong page is cached.
3. Run with the defaults for a day and read the warnings. The log line `weir: cookies stripped from cacheable requests` (Info, once, after the first 5 minutes of `Bypass.ReportStrippedCookies`) lists cookie names the origin never saw. A name you recognize as session or login state belongs in `Bypass.Cookies` (those requests skip the cache) or in `Key.Cookies` (each value is its own entry).
4. Decide what happens to requests with `Authorization` or a session cookie. A response to a request with `Authorization` is stored only when it carries `public`, a valid `s-maxage` or `must-revalidate` (FR-STO-5), and those requests are never coalesced with other users' requests.
5. Put a rate limiter in front for distinct-path floods (06 R-2). Weir bounds origin concurrency; it does not rate-limit clients.
6. Decide the stale policy. Defaults serve stale only inside the origin's `stale-while-revalidate` and `stale-if-error` windows. Setting `Freshness.DefaultStaleIfError` is the usual first change; it trades freshness for survival of an outage (D6).

## 2. What to watch

Each response carries `Cache-Status: Weir; ...` (RFC 9211). `hit; ttl=N` is a fresh hit. `fwd=uri-miss` (no entry), `fwd=vary-miss` (entry exists, not for these `Vary` values), `fwd=stale`, `fwd=request` (the client asked), `fwd=method` and `fwd=bypass` say why the origin was asked. `; collapsed` marks a request that joined another's fetch. `detail=negative` marks a negative-cache entry. A stale hit shows a negative `ttl=`. `detail=` names the stale reason: `stale-while-revalidate`, `stale-if-error`, `shed`, `circuit-open`, `coalesce-timeout`. The metric label `reason` uses short names for the first two: `swr` and `sie`.

With `observe/prom` (a separate module):

| Metric | Read it as |
|---|---|
| `weir_requests_total{outcome}` | the hit ratio and the mix of outcomes |
| `weir_origin_fetches_total{class,result}`, `weir_origin_fetch_seconds` | origin load and latency, split foreground, background and warm |
| `weir_origin_inflight`, `weir_limiter_queue_depth` | the limiter: sustained `inflight` at `MaxConcurrent` with a growing queue means the origin is the limit |
| `weir_shed_total{reason}` | requests refused (clients get 503 with `Retry-After`) |
| `weir_breaker_state` (0 closed, 1 half-open, 2 open), `weir_breaker_transitions_total{to}` | origin health as Weir sees it |
| `weir_stale_served_total{reason}` | how often staleness is hiding a problem |
| `weir_coalesce_timeouts_total{fallback}`, `weir_coalesced_total` | flights that ran long, and how much coalescing saves |
| `weir_store_errors_total{op}`, `weir_store_bytes`, `weir_evictions_total{queue}` | the store: errors mean it is unavailable, evictions are normal when full |
| `weir_not_stored_total{reason}`, `weir_key_rejections_total{reason}` | responses refused by storability rules, and requests refused by validation |
| `weir_miss_rate_anomalies_total{action}` | see 4.4 |
| `weir_purges_total{mode}`, `weir_negative_served_total` | purge activity and negative-cache use |

Labelled series appear on their first event, so alert on `absent()` as well as `rate()`. Without Prometheus, implement `weir.Observer` (it runs on the request path: keep `Observe` fast) and poll `Engine.Stats()` for in-flight, queued, breaker state and store bytes. Request-derived strings such as `Event.Partition` are never metric labels.

## 3. Alerts worth having

- The breaker is open for more than a few minutes (`weir_breaker_state == 2`).
- `weir_shed_total` is nonzero for more than a minute: the origin cannot keep up, or `MaxConcurrent` is set too low for it.
- `weir_stale_served_total{reason="sie"}` or `reason="circuit-open"` is rising: you are serving through an outage.
- `weir_store_errors_total` is rising (remote stores only).
- Hit ratio falls by half against the same hour last week.

## 4. Incidents

### 4.1 The origin is down or slow

The breaker counts origin-health failures over a 10 s window (500 and 4xx are not failures unless `Breaker.CountStatus500`), opens at a 50% failure ratio of at least 20 outcomes, and stays open 5 s (±20%), doubling to 60 s on each consecutive reopen. Meanwhile entries inside their stale-if-error window are served with `detail=stale-if-error` or `circuit-open`; everything else gets 503.

If entries have no stale-if-error window and you want to ride out the outage: `Engine.SetMode(weir.ModeStaleOnError, ttl)` serves any stored entry stale (up to 24 h past expiry, but see the retention note below), except hard-purged, invalidated and `must-revalidate`, `proxy-revalidate`, `no-cache` or `s-maxage` ones. `ttl` is required, at most 24 h, and the mode reverts by itself. Entries are kept only for their stale windows plus `Freshness.Keep` (5 min) after expiry, and only when they carry `ETag` or `Last-Modified`, so the practical reach is minutes unless you raised `Freshness.Keep` or `DefaultStaleIfError` before the incident. Modes are not persisted across restarts. Set it from an admin handler, not from request data.

### 4.2 The cache itself is suspect

Wrong content is being served, or you changed the key configuration. `SetMode(weir.ModeBypass, ttl)` makes every request a pass-through (still through the limiter and breaker, nothing stored, existing entries untouched; an unsafe request that succeeds still invalidates its target as usual). Then purge (4.3) and leave bypass when it is clean. Check `weir_not_stored_total` and the origin's `Vary` and `Cache-Control` first: most wrong-content reports are an origin that varies on something Weir does not key (R-1, R-5, R-6).

### 4.3 Purging

`Engine.Purge(ctx, weir.Purge{...})` takes `All`, `URLs` (absolute, rewritten like requests) and `Groups` (with `Origin`), in `PurgeSoft` (default) or `PurgeHard` mode.

- Soft: matching entries become stale at the purge time (with the memory store it can land up to 1 s late) and are revalidated with conditional requests. Inside the entry's `stale-while-revalidate` window clients keep getting the stale copy while a background refresh runs; with no such window (the default) the next request revalidates in the foreground and waits. Either way a purge of thousands of keys is bounded by coalescing per key and the limiter, and background refreshes never use the foreground reserve. Use it for content changes.
- Hard: matching entries are unusable at once, and clients wait for the origin. Use it for content that must not be served again (a leak, a takedown).

A same-origin 2xx or 3xx response to an unsafe method (`POST`, `PUT`, `DELETE`) also invalidates its target URI and `Location` and `Content-Location` URIs. `Cache-Group-Invalidation` from the origin soft-purges groups unless `CacheGroups.Ignore` is set. Purge errors reach you; a store that refuses a hard purge at its cap is not an outage.

### 4.4 Miss-rate anomaly

The log line `weir: miss-rate anomaly` and `weir_miss_rate_anomalies_total` mean that one partition (origin plus path) had at least 500 misses in a 10 s window at a miss ratio of 0.9 or more. Causes, in order of how often they happen:

1. A busy path whose responses are uncacheable (`private`, `no-store`) or always revalidated (`max-age=0`, `no-cache`). Every request is a miss by design. Route that path around Weir in your server (mount it on the mux beside the Weir handler); `Bypass` handles cookies and headers, not paths. It is not an attack and the warning will not go away until you do.
2. A cold path: a fresh deploy, a purge, a crawler walking many distinct URLs.
3. Cache busting with unique query strings (T-11). The per-path cap (`Limiter.MaxPerPartition`, 16) already bounds the damage; the flood cannot fill the shared queue.

`MissRate.Throttle` is off by default. Turn it on only after running without it and finding the warnings are cases 2 or 3. With it, an anomalous partition is capped at one origin fetch for the next window, for as long as it stays over both thresholds. Read the cost before you do (06 R-8):

- A cold path with far more distinct keys than a single fetch at a time can fill in a window stays capped. Measured: 300 000 distinct keys at 1 000 requests a second were capped in all 30 windows of the run.
- A client that sends max(500, 9 × the path's hits) distinct-query requests per window keeps that path capped for as long as it sends. In a review experiment, 60 requests a second let 390 of 1 200 legitimate misses through, against all 1 200 without `Throttle` (not a repo test).
- Sheds from a full global limiter count as misses, so a slow origin can get a busy, miss-heavy path capped in the next window.
- An attacker who floods every other window is never throttled.

`Throttle` trades the path's own misses for origin protection. If case 1 applies, fix it with a route, not with `Throttle`. `MissRate.MinMisses` and `MinRatio` tune detection, and `TopK` (64) bounds memory; a partition with under 1/`TopK` of a window's requests may go untracked.

### 4.5 Memory

`weir_store_bytes` should sit near the configured size once the cache is warm. If the process nears `GOMEMLIMIT`, look at `Storable.MaxObjectBytes` (1 MiB, headers included) and at large streamed bodies before you blame the cache: buffering for storage is bounded by `MaxConcurrent × MaxObjectBytes` (NFR-4, 64 MiB at defaults). A stream that exceeds `MaxObjectBytes` is not stored, but it holds up to `MaxObjectBytes + 1` bytes of read-ahead after its fetch slot is released, until the client reads it. Many slow clients on large responses therefore cost about one `MaxObjectBytes` each (1 MiB at defaults), beyond the bound above. The hit path's GC cost at 1M entries is in [benchmarks.md](benchmarks.md).

### 4.6 A remote store fails (Phase 2.5)

After 5 consecutive `ErrUnavailable` results the store breaker opens for 1 s, doubling to 30 s. While it is open, lookups are treated as misses and nothing is stored, so origin load rises to the uncached rate. The limiter and breaker still protect the origin. Epoch (purge) state may be lost with a store restart; run a hard purge of everything (`All: true`) after one if stale content matters (06 R-4).

## 5. Planned operations

- Warm start: `Engine.Warm(ctx, requests, origin)` fetches a list of requests at background priority (4 at a time, never using the foreground reserve) and skips keys that are already fresh. It returns counts of fetched, skipped, not-stored and failed. Run it after a deploy that empties the cache, from a goroutine that you own.
- Shutdown: stop the server first (`srv.Shutdown`), then `Engine.Close(ctx)`. `Close` cancels background work after the grace period in `ctx`, and requests that arrive later get `weir.ErrClosed` (503).
- Config changes: `weir.New` rejects an invalid `Config` with `ErrInvalidConfig`, naming the field. A running engine's config is fixed; build a new engine and swap it at your handler.

## 6. Errors clients see

| Status | Cause |
|---|---|
| 400 | request failed validation (bad host, path, escape, oversized target); no store or origin cost |
| 501 | CONNECT or a protocol upgrade reached `weirhttp.Handler` (`Middleware` passes them on) |
| 502 | origin error with no usable stale entry |
| 503 | shed (with `Retry-After`), breaker open with no usable stale entry, or the engine closed |
| 499 | the client went away; a log hint only, adapters write nothing |
| 504 | origin timeout, `must-revalidate` entry that could not be validated, or `only-if-cached` with nothing stored |
