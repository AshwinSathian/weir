---
title: Benchmarks
description: Hit-path cost, five load scenarios and garbage-collection cost, each with its machine, its command and what it does not show.
---

Every number on this page was measured on one machine, in process, with a synthetic origin. They tell you what Weir costs and whether its limits hold under load. They do not tell you what your deployment will do. The tables are rendered from [docs/benchmarks.md](https://github.com/AshwinSathian/weir/blob/main/docs/benchmarks.md) in the repository, where the raw output files are linked.

## The reference machine

Apple M4 Pro, 12 cores, 24 GiB, macOS. Go 1.27.1 unless a section says otherwise. Absolute figures belong to this machine. On a 4-vCPU Linux container the same hit costs roughly two and a half times as long, and that box drifts by 10 to 20% between runs.

## One cached hit

`BenchmarkServeHitSmall` measures `Engine.Serve` on a fresh 1 KiB entry in the memory store: classify the request, look it up, check freshness, build the response. No network, no TLS, no `net/http`.

{{include docs/benchmarks.md "### On the reference machine"}}

{{figure hit-path}}

After the last hardening changes before v1.0.0 the same benchmark measured 852.2 ns with 12 allocations on Go 1.27.2, with the machine not idle. The project's own budget is at most 1.25 µs and 16 allocations per hit on this machine, and a change that slows the hit path by more than 20% fails review.

```sh
make bench
```

## After the v1.1 hardening

The same benchmarks on `v1.0.1` and on the v1.1 branch, run interleaved on the reference machine, and `make load` against the record above.

{{include docs/benchmarks.md "### v1.1 hardening (2026-10-13)"}}

## Five load scenarios

`make load` runs each failure mode against a real engine for about eight minutes in total and checks a threshold. This is the idle reference run of 2026-10-11.

{{include docs/benchmarks.md "### Load scenarios (`make load`, scale 1, 473 s)"}}

What each one shows:

- **Synchronized expiry.** 10,000 entries with the same `max-age` expire together. The limiter holds origin concurrency at its configured 32 and every entry is refetched.
- Busting flood. One path is flooded with distinct queries while other paths carry normal traffic. The flood is held to its per-path cap of 8, and normal requests are 11% slower at p99.
- Origin brownout. The origin fails in bursts. The breaker opens and closes, stale content covers the gaps, and no client sees an error.
- Store flap. The store fails half its calls. Requests fall through to the origin under the limiter.

## Garbage collection at one million entries

The memory store keeps entries as ordinary Go objects, so the garbage collector has to walk them. This measures what that costs per request as the store grows.

{{include docs/benchmarks.md "### GC cost at 1M entries (D36)"}}

At 1 million entries the collector costs 1.08 µs per request here, and 1.76 µs on the 4-vCPU Linux container, against a project gate of 2 µs. A layout that stores entries without pointers brought that to 0.014 µs in a prototype, but it added 12 allocations per hit and broke the hit-path budget, so it is deferred (M16-02) until a deployment needs it. `GOGC=200` roughly halves the cost for more memory headroom.

## What this does not show

- **No comparison with any other cache.** We have not run one, so the [comparison page](/compare/) is qualitative.
- No network, TLS or HTTP parsing. A request served through `net/http` over TLS costs many times the 852 ns measured here, and most of that is outside Weir.
- No real origin. The synthetic origin answers instantly or fails on cue.
- One reference machine and one container. No ARM server, no NUMA box, no production trace.
- The Valkey store is covered by correctness tests, not by these load scenarios.
