---
title: Origin protection
description: The concurrency budget, per-path fairness, the circuit breaker, negative caching and the miss-rate detector.
---

Coalescing handles many requests for one key. This page is about everything it cannot handle: many requests for many keys, and an origin that is already failing.

## The budget

Every origin fetch holds a slot in a limiter from before the request is sent until the response body is buffered (FR-LIM-1). Every fetch means every fetch: foreground misses, background refreshes, `Warm`, pass-through `POST`s, bypassed requests. There is one path to the origin, so there is one budget.

{{figure budget}}

With defaults:

- `Limiter.MaxConcurrent` is 64. That many fetches run at once.
- A request that finds no free slot queues. The queue holds `MaxQueue` (1,024) waiters and each waits at most `MaxQueueWait` (2 seconds) (FR-LIM-2).
- A request that cannot queue, or waits too long, is shed. It gets a stale entry if stale-if-error allows one, and otherwise a 503 with `Retry-After` (FR-LIM-5).
- Background refreshes never queue and never use the last 25% of slots, so they cannot starve the requests people are waiting on (FR-LIM-4).
- Requests with a body draw from a separate pool of `MaxUpload` slots (16). A slow uploader can exhaust that pool and nothing else (FR-LIM-7).

So the origin sees at most `MaxConcurrent + MaxUpload` fetches at once, 80 by default, whatever happens in front: a full purge, a restart with a cold cache, a storage outage that turns every lookup into a miss. Set `MaxConcurrent` to what your origin can serve concurrently and still answer in reasonable time.

In the repository's load test, 10,000 keys with `max-age=60` were stored within 1.7 seconds and left to expire together under a limit of 32. All 10,000 were refetched, the origin never had more than 32 in flight, and no client saw an error. [Benchmarks](/benchmarks/) has the machine and the command.

## Fairness between paths

A budget alone has a weakness: one noisy path can spend all of it. An attacker sends `/search?q=<random>` as fast as possible; every request is a distinct key, so every one is a miss, and 64 slots fill with `/search` while the home page queues behind them.

Weir caps each partition (scheme, host and path, without the query) at `Limiter.MaxPerPartition` slots, 16 by default (FR-LIM-3), and limits how much of the queue one partition may occupy. The flood on `/search` gets 16 slots and sheds the rest. In the load test, a flood of 99,976 requests against one path was held to 8 in flight (the test's cap) while requests to other paths kept a p99 of 41.0 µs, against 36.9 µs with no flood.

For sites serving many hostnames, `Limiter.MaxPerHost` adds the same cap per host. The Caddy module sets it to 25% when you declare `multi_host`.

## The circuit breaker

A limiter protects a healthy origin from too much work. A breaker protects a failing origin from any work, and protects your clients from waiting on it.

One breaker per engine watches all origin fetches over a 10-second window (FR-CB-1). When at least 20 have completed and half or more failed, it opens (FR-CB-2). A failure here means the origin's health is in doubt: a timeout, a connection error, a 502, 503 or 504. A 500 or a 4xx is the origin answering, and does not count unless you set `Breaker.CountStatus500`.

While open, nothing reaches the origin (FR-CB-5). Requests get a stale entry where allowed (`detail=circuit-open`) and a fast 503 otherwise. After 5 seconds, give or take 20%, one probe request is let through. Success closes the breaker; failure reopens it for twice as long, up to 60 seconds (FR-CB-3, FR-CB-4).

The random 20% is there for the same reason as TTL jitter: several Weir instances that opened together should not all probe in the same instant.

## Negative caching

Between "healthy" and "breaker open" is an uncomfortable middle: a few URLs fail while the rest work. Each request for a failing URL costs an origin attempt.

When a fetch fails on origin health and nothing is stored for that key, Weir records a negative entry for `Negative.TTL`, 2 seconds (FR-NEG-1). Requests in that window get the same error status with `Cache-Status: Weir; hit; ttl=1; detail=negative` and no origin contact. A negative entry holds a status and an optional `Retry-After`, never a body (FR-NEG-2), and is never created for a 500, a 4xx, or a request that carried credentials (FR-NEG-4).

Two seconds is short on purpose. It is long enough to stop a tight retry loop, and short enough that a recovered URL is noticed almost at once.

## The miss-rate detector

Weir counts requests and misses per partition, in a fixed 64 counters (FR-MR-1). A partition with at least 500 misses in 10 seconds and a miss ratio of 90% or more raises a `miss_rate_anomaly` event and a warning in the log (FR-MR-2). Usually that is a cache-busting query flood. Sometimes it is a tracking parameter you forgot to put in `Key.QueryDrop`.

By default the detector only reports.

> [!PROTECTION]
> `MissRate.Throttle` caps an anomalous path at one origin fetch for the next window. It also lets a client who floods a path with distinct queries keep that path's own legitimate misses throttled for as long as the flood lasts (R-8). Run without it first and read the warnings.

## When the store fails

A remote store can time out. Each call to one is bounded by `Timeouts.Store`, 50 ms (FR-STF-1). After 5 consecutive failures a store breaker opens for a second, doubling up to 30, and lookups are treated as misses without calling the store (FR-STF-2). Those misses still go through coalescing, the limiter and the origin breaker (FR-STF-3). A cache outage becomes a slower site with a bounded origin load, not an origin outage. With the store failing half its calls, the load test finished 10.8 million requests with no client errors.

## Limits

> [!LIMITS]
> The limiter bounds concurrency, not request rate, and it does not tell clients apart. A flood of distinct paths (not distinct queries on one path) spreads across partitions and can use the whole budget; put a rate limiter in front (T-12, R-2). The breaker is per engine, not per path or per upstream host, so one broken endpoint with enough traffic can open it for all. `Limiter.MaxConcurrent` is yours to size: 64 is a default, not a measurement of your origin.
