---
title: Benchmarks
description: One round of counting tests against Varnish, nginx and Souin with the scenarios Weir loses, then hit-path cost, five load scenarios and garbage-collection cost. One machine, one day, no speed comparison.
---

Every number on this page was measured on one machine with a synthetic origin, on the date its section names. The charts and tables are drawn at build time from [docs/benchmarks.md](https://github.com/AshwinSathian/weir/blob/main/docs/benchmarks.md) in the repository, where the raw output files are linked. They show what Weir costs, whether its limits hold under load, and how one round of counting tests came out against three other caches. They do not show what your deployment will do, and there is no speed comparison with any other cache here.

## Results at a glance

Where Weir is ahead of the other caches, or level with the best of them:

{{figure results-ahead}}

Where it is behind, and what was never measured:

{{figure results-behind}}

Each tile links to the chart it comes from. Tiles with an amber line name another project, and the figures in them are ours alone.

## The reference machine

Apple M4 Pro, 12 cores, 24 GiB, macOS. Go 1.27.1 unless a section says otherwise. Absolute figures belong to this machine. On a 4-vCPU Linux container the same hit costs roughly two and a half times as long, and that box drifts by 10 to 20% between runs.

## Against other caches

One round of black-box tests, run on 2026-10-13: Varnish 9.1.0, nginx 1.30.5 with `proxy_cache`, Caddy with the cache-handler v0.17.0 (Souin), and Caddy with Weir, each in a container behind the same origin. Every cache ran in two tiers. `default` is the smallest configuration that turns caching on, and `tuned` is that project's documented stampede and stale settings. The round counts origin calls and failed clients. It does not measure speed.

> [!LIMITS]
> Every figure about another project here comes from our own runs with configurations we wrote. None has been verified by that project's owners, so treat them as our measurements, not as statements those projects would sign, and [send corrections](https://github.com/AshwinSathian/weir/issues/new). "Weir v1.1" is the `v1.1.0` code, built from the working tree shortly before the tag. The configurations, the harness and the method are in [`bench/compare/`](https://github.com/AshwinSathian/weir/tree/main/bench/compare) and the [comparison specification](https://github.com/AshwinSathian/weir/blob/main/docs/11-comparison-spec.md).

The two buttons above a chart switch between the tiers. Both tiers are in the page, and the full table with its notes is under each chart.

### Clients that did not get a 2xx

How many clients did each cache fail?

{{figure results-failed}}

<details>
<summary>Full table and notes, with the no-cache control and Weir v1.0.1</summary>

{{include docs/benchmarks.md "### Comparison: clients that did not get a 2xx"}}

</details>

### Requests that reached the origin

How much of each burst did the origin see, and did anyone pay for a low number?

{{figure results-calls}}

<details>
<summary>Full table and notes, with the no-cache control and Weir v1.0.1</summary>

{{include docs/benchmarks.md "### Comparison: origin calls"}}

</details>

### An origin outage, a bad route and a flood

What happens to clients while the origin is down, and after it comes back? This is the chart where Weir's default tier does worst.

{{figure results-recovery}}

<details>
<summary>Full table and what each scenario does</summary>

{{include docs/benchmarks.md "### Comparison: failure and recovery scenarios"}}

</details>

### What the numbers say about Weir

{{include docs/benchmarks.md "### Comparison: what the numbers say about Weir"}}

### What the round changed in Weir

The round started with `v1.0.1`, the published release, and a hardening branch. Which of that release's failures are gone in the code released as `v1.1.0`, and which did the round itself find?

{{figure results-changed}}

<details>
<summary>Full table and what was changed</summary>

{{include docs/benchmarks.md "### Comparison: what the round changed in Weir"}}

</details>

### Not measured

- **Speed, memory and hit ratio against any other cache.** That part of the comparison has not been run. Nothing above is a throughput claim, and no latency from this round is published, because the runs were not made under the drift check the specification requires.
- Four scenarios the specification lists: a cache-busting flood, a brownout, a purge under load and a restart.
- Four of the probes, and differential fuzzing.
- Any review by a maintainer of Varnish, nginx or cache-handler of the configuration used for their project.
- A second machine. The tables are stitched from several invocations of the harness on one day on one Mac, three repetitions per scenario with the median reported.

```sh
cd bench/compare && ./run.sh -reps 3 && ./probes.sh && ./conformance.sh
```

## One cached hit

`BenchmarkServeHitSmall` measures `Engine.Serve` on a fresh 1 KiB entry in the memory store: classify the request, look it up, check freshness, build the response. No network, no TLS, no `net/http`.

{{figure results-hit}}

Three builds, run interleaved on the reference machine on 2026-10-13. `main` is `v1.0.1`, "after HRD-01 to 15" is the hardening branch, and "after HRD-19" is the code that became `v1.1.0`. The project's own budget is at most 1.25 µs and 16 allocations per hit on this machine, and a change that slows the hit path by more than 20% fails review.

```sh
make bench
```

<details>
<summary>Every benchmark and load scenario of the v1.1 hardening run</summary>

{{include docs/benchmarks.md "### v1.1 hardening (2026-10-13)"}}

</details>

<details>
<summary>Earlier builds on an idle machine (2026-10-11)</summary>

{{include docs/benchmarks.md "### On the reference machine"}}

{{figure hit-path}}

After the last hardening changes before v1.0.0 the same benchmark measured 852.2 ns with 12 allocations on Go 1.27.2, with the machine not idle.

</details>

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

- No speed comparison with any other cache. See [not measured](#not-measured).
- No network, TLS or HTTP parsing. A request served through `net/http` over TLS costs many times the 852 ns measured here, and most of that is outside Weir.
- No real origin. The synthetic origin answers instantly or fails on cue.
- One reference machine and one container. No ARM server, no NUMA box, no production trace.
- The Valkey store is covered by correctness tests, not by these load scenarios.
