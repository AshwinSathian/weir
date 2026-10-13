---
title: Benchmarks
description: Two rounds of counting tests against Varnish, nginx and Souin, with the scenarios Weir loses and what the next release changes, then hit-path cost, five load scenarios and garbage-collection cost. One machine, no speed comparison.
---

Every number on this page was measured on one machine with a synthetic origin, on the date its section names. The charts and tables are drawn at build time from [docs/benchmarks.md](https://github.com/AshwinSathian/weir/blob/main/docs/benchmarks.md) in the repository, where the raw output files are linked. They show what Weir costs, whether its limits hold under load, and how two rounds of counting tests came out against three other caches. They do not show what your deployment will do, and there is no speed comparison with any other cache here.

## Results at a glance

Where Weir is ahead of the other caches, or level with the best of them:

{{figure results-ahead}}

Where it is behind, and what was never measured:

{{figure results-behind}}

Each tile links to the chart it comes from. A tile tagged "our runs" names another project, and the figures in it are ours alone. Every figure is the published `v1.1.0`. Four of the losses have fixes merged for the next release, which [a section below](#what-the-next-release-changes) describes without redrawing a chart. The cost of one cached hit, which is Weir measured against itself, is [further down](#one-cached-hit).

## The reference machine

Apple M4 Pro, 12 cores, 24 GiB, macOS. Go 1.27.1 unless a section says otherwise. Absolute figures belong to this machine. On a 4-vCPU Linux container the same hit costs roughly two and a half times as long, and that box drifts by 10 to 20% between runs.

## Against other caches

Black-box tests, run on 2026-10-13 in two rounds: Varnish 9.1.0, nginx 1.30.5 with `proxy_cache`, Caddy with the cache-handler v0.17.0 (Souin), and Caddy with Weir v1.1.0, each in a container behind the same origin. Every cache ran in two tiers. `default` is the smallest configuration that turns caching on, and `tuned` is that project's documented stampede and stale settings. The second round reran everything with a corrected harness, which now counts a success with the wrong body as a failure, and added a flood, a brownout, a purge and a restart. The rounds count origin calls and failed clients. They do not measure speed.

> [!LIMITS]
> Every figure about another project here comes from our own runs with configurations we wrote. None has been verified by that project's owners, so treat them as our measurements, not as statements those projects would sign, and [send corrections](https://github.com/AshwinSathian/weir/issues/new). Weir is the published `v1.1.0` module. The configurations, the harness and the method are in [`bench/compare/`](https://github.com/AshwinSathian/weir/tree/main/bench/compare) and the [comparison specification](https://github.com/AshwinSathian/weir/blob/main/docs/11-comparison-spec.md).

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

### What the next release changes

The four losses of the second round have fixes merged on `main`. None is in a release yet, so no chart above shows them, and the comparison will be run again when one is.

- A refresh that stale serving owes now waits for a slot. Before, a purge or a mass expiry left most pages stale until someone asked again.
- The breaker probes about once a second and no longer doubles its wait, and it lets traffic back in steps once a probe succeeds.
- A path's fetches may use origin slots the other paths leave idle. That alone moves the flood scenario little. An adaptive limit that finds what the origin can take moves it more, and is off by default because finding that limit means crossing it now and then.
- A snapshot can keep entries as fresh as they were, so a restart costs the origin nothing. It is off by default: a purge sent while the server was down is then not applied to what the snapshot brings back.

The measurements behind these, from builds of `main` and not from a release, and one figure that got worse, are in the [changelog](/changelog/) and in the card that tracked the work, [HRD-23](https://github.com/AshwinSathian/weir/blob/main/docs/cards/22-hardening.md).

### What the first round changed in Weir

The first round started with `v1.0.1`, which was the published release then, and a hardening branch. Which of that release's failures are gone in the code released as `v1.1.0`, and which did the round itself find?

{{figure results-changed}}

<details>
<summary>Full table and what was changed</summary>

{{include docs/benchmarks.md "### Comparison: what the round changed in Weir"}}

</details>

### Not measured

- **Speed, memory and hit ratio against any other cache.** That part of the comparison has not been run. Nothing above is a throughput claim, and no latency from these rounds is published, because the runs were not made under the drift check the specification requires.
- Differential fuzzing across the caches.
- Any review by a maintainer of Varnish, nginx or cache-handler of the configuration used for their project. We have been wrong three times so far, each time against the other project and each time found by our own review before publication: port exhaustion in our containers that looked like nginx failing, a rule from Varnish's grace guide that our tuned tier lacked, and a cache-handler storage left at a default size that evicted the keys the test had stored.
- The next release. Everything above is `v1.1.0`.
- A second machine. The tables are stitched from many invocations of the harness on one day on one Mac, three repetitions per scenario with the median reported, except `large-body-herd` and `post-flood`, which ran once.

```sh
cd bench/compare && ./run.sh -reps 3 && ./probes.sh && ./conformance.sh   # hours; one subject and tier at a time is in the README there
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

`make load` runs each failure mode against a real engine for about eight minutes in total and checks a threshold. This is the idle reference run of 2026-10-11. The same scenarios on the `v1.1.0` code, on a machine that was not idle, are in the collapsed table under [one cached hit](#one-cached-hit).

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
- No network, TLS or HTTP parsing. A request served through `net/http` over TLS costs many times the hit measured here, and most of that is outside Weir.
- No real origin. The synthetic origin answers instantly or fails on cue.
- One reference machine and one container. No ARM server, no NUMA box, no production trace.
- The Valkey store is covered by correctness tests, not by these load scenarios.
