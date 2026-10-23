---
title: Benchmarks
description: Three rounds of counting tests against Varnish, nginx and the Caddy cache-handler (Souin), with the scenarios Weir loses, memory per object and hit ratio, then hit-path cost, load scenarios and garbage-collection cost. One machine, no latency comparison.
---

Every number on this page was measured on one machine with a synthetic origin, on the date its section names. The charts and tables are drawn at build time from [docs/benchmarks.md](https://github.com/AshwinSathian/weir/blob/main/docs/benchmarks.md) in the repository, where the raw output files are linked. They show what Weir costs, whether its limits hold under load, and how three rounds of counting tests came out against three other caches. They do not show what your deployment will do, and there is no latency or throughput comparison with any other cache here.

## Results at a glance

Where Weir is ahead of the other caches, or level with the best of them:

{{figure results-ahead}}

Where it is behind, and what was never measured:

{{figure results-behind}}

Each tile links to the chart it comes from. A tile tagged "our runs" names another project, and the figures in it are ours alone. Every figure is the published `v1.2.0`, and [a section below](#what-the-third-round-changed-in-weir) compares it with `v1.1.0`, which the second round measured. The cost of one cached hit, which is Weir measured against itself, is [further down](#one-cached-hit).

## The reference machine

Apple M4 Pro, 12 cores, 24 GiB, macOS. Go 1.27.1 unless a section says otherwise. Absolute figures belong to this machine. On a 4-vCPU Linux container the same hit costs roughly two and a half times as long, and that box drifts by 10 to 20% between runs.

## Against other caches

Black-box tests, the third round run on 2026-10-22 on a quiet machine: Varnish 9.1.0, nginx 1.30.5 with `proxy_cache`, Caddy with cache-handler v0.17.0 (Caddy's cache module, built on Souin), and Caddy with Weir v1.2.0, each in a container behind the same origin. Every cache ran in two tiers. `default` is the smallest configuration that turns caching on, and `tuned` is that project's documented stampede and stale settings. The first two rounds ran on 2026-10-13 against earlier Weir releases; the third repeats the second's scenarios unchanged, each script in one invocation, and adds memory and hit-ratio counts. The rounds count origin calls, failed clients, memory and cache hits. They publish no speed figures.

> [!LIMITS]
> Every figure about another project here comes from our own runs with configurations we wrote. None has been verified by that project's owners, so treat them as our measurements, not as statements those projects would sign, and [send corrections](https://github.com/AshwinSathian/weir/issues/new). Weir is the published `v1.2.0` module. The configurations, the harness and the method are in [`bench/compare/`](https://github.com/AshwinSathian/weir/tree/main/bench/compare) and the [comparison specification](https://github.com/AshwinSathian/weir/blob/main/docs/11-comparison-spec.md).

The two buttons above a chart switch between the tiers. Both tiers are in the page, and the full table with its notes is under each chart.

### Clients that did not get a 2xx

How many clients did each cache fail?

{{figure results-failed}}

<details>
<summary>Full table and notes, with the no-cache control</summary>

{{include docs/benchmarks.md "### Comparison: clients that did not get a 2xx"}}

</details>

### Requests that reached the origin

How much of each burst did the origin see, and did anyone pay for a low number?

{{figure results-calls}}

<details>
<summary>Full table and notes, with the no-cache control</summary>

{{include docs/benchmarks.md "### Comparison: origin calls"}}

</details>

### An origin outage, a bad route and a flood of writes

What happens to clients while the origin is down, and after it comes back?

{{figure results-recovery}}

<details>
<summary>Full table and what each scenario does</summary>

{{include docs/benchmarks.md "### Comparison: failure and recovery scenarios"}}

</details>

### A flood, a brownout, a purge and a restart

Four scenarios the first round left out. What does one flooded path do to everyone else, what does an origin that fails in bursts cost, what reaches the origin after a purge, and what is left after a restart? Weir loses a row in each.

{{figure results-flood}}

<details>
<summary>Full table and what each scenario does</summary>

{{include docs/benchmarks.md "### Comparison: flood, brownout, purge and restart"}}

</details>

### What the numbers say about Weir

{{include docs/benchmarks.md "### Comparison: what the numbers say about Weir"}}

### Speed and memory counts

How much memory does each cache need per stored object, and how many requests does it answer from a full store? These are the counts of the speed and memory runs. Their latency and CPU figures are not published (see [not measured](#not-measured)).

{{include docs/benchmarks.md "### Comparison: speed and memory counts"}}

### What the third round changed in Weir

The second round measured `v1.1.0` and the third `v1.2.0`, with the same scenarios and the same configurations for the other caches. Which Weir figures moved, and why?

{{figure results-changed}}

<details>
<summary>Full table and what was changed</summary>

{{include docs/benchmarks.md "### Comparison: what the round changed in Weir"}}

</details>

### Not measured

- **Latency, CPU time and throughput against any other cache.** The speed runs measured them, and every timing repetition failed the drift check the specification requires: the control's median moved at most 9%, under the check's 10%, but its 99th percentile moved 14% or more in every repetition. Only the runs' counts are published. Nothing above is a throughput or latency claim.
- Differential fuzzing across the caches.
- Any review by a maintainer of Varnish, nginx or cache-handler of the configuration used for their project. We have been wrong three times so far, each time against the other project and each time found by our own review before publication: port exhaustion in our containers that looked like nginx failing, a rule from Varnish's grace guide that our tuned tier lacked, and a cache-handler storage left at a default size that evicted the keys the test had stored.
- cache-handler's memory and hit ratio: its default storage has no size setting we could verify.
- A second machine. Each round ran on one Mac, three repetitions per scenario with the median reported, except `large-body-herd` and `post-flood`, which ran once.

```sh
cd bench/compare && ./run.sh -reps 3 && ./probes.sh && ./conformance.sh && ./speed.sh   # hours; one subject and tier at a time is in the README there
```

## One cached hit

`BenchmarkServeHitSmall` measures `Engine.Serve` on a fresh 1 KiB entry in the memory store: classify the request, look it up, check freshness, build the response. No network, no TLS, no `net/http`.

{{figure results-hit}}

Three builds, run interleaved on the reference machine on 2026-10-13. `main` is `v1.0.1`, "hardening branch" is the branch before the hit-path work, and "v1.1 hit path" is the code that became `v1.1.0`. The `v1.2.0` release measured the same hit within noise of `v1.1.0`, with the same allocations, in the first collapsed section below. The project's own budget is at most 1.25 µs and 16 allocations per hit on this machine, and a change that slows the hit path by more than 20% fails review.

```sh
make bench
```

<details>
<summary>Every benchmark and load scenario of the v1.2.0 release run</summary>

{{include docs/benchmarks.md "### v1.2.0 release (2026-10-23)"}}

</details>

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

`make load` runs each failure mode against a real engine for about eight minutes in total and checks a threshold. This is the idle reference run of 2026-10-11. The same scenarios on the `v1.2.0` release, on a quiet machine, and on the `v1.1.0` code, on a machine that was not idle, are in the collapsed tables under [one cached hit](#one-cached-hit). In the `v1.2.0` run the flood borrows idle origin slots and nothing is shed, and the normal traffic's tail under the flood grows more than in this run, still inside the threshold.

{{include docs/benchmarks.md "### Load scenarios (`make load`, scale 1, 473 s)"}}

What each one shows:

- **Synchronized expiry.** 10,000 entries with the same `max-age` expire together. The limiter holds origin concurrency at its configured 32 and every entry is refetched.
- Busting flood. One path is flooded with distinct queries while other paths carry normal traffic. The flood is held to its per-path cap of 8, and normal requests are 11% slower at p99.
- Origin brownout. The origin fails in bursts. The breaker opens and closes, stale content covers the gaps, and no client sees an error.
- Store flap. The store fails half its calls. Requests fall through to the origin under the limiter.

## Garbage collection at one million entries

The memory store keeps entries as ordinary Go objects, so the garbage collector has to walk them. This measures what that costs per request as the store grows.

{{include docs/benchmarks.md "### GC cost at 1M entries (D36)"}}

At 1 million entries the collector costs 1.08 µs per request here, and 1.76 µs on the 4-vCPU Linux container, against a project gate of 2 µs. A layout that stores entries without pointers brought that to 0.014 µs in a prototype, but it added 12 allocations per hit and broke the hit-path budget, so it is deferred until a deployment needs it. `GOGC=200` roughly halves the cost for more memory headroom.

## What this does not show

- No speed comparison with any other cache. See [not measured](#not-measured).
- No network, TLS or HTTP parsing. A request served through `net/http` over TLS costs many times the hit measured here, and most of that is outside Weir.
- No real origin. The synthetic origin answers instantly or fails on cue.
- One reference machine and one container. No ARM server, no NUMA box, no production trace.
- The Valkey store is covered by correctness tests, not by these load scenarios.
