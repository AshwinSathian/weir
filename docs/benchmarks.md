# Weir benchmarks

Updated: 2026-10-23

Benchmark and load-test results. The first section is the current state. Everything from "Earlier runs" on is the record of how the numbers got there, kept so each run can be compared with the one before it using `benchstat` ([07 §10](07-testing-strategy.md)). Every absolute number names its machine. The rendered version, with what the numbers do not show, is at [weir.ashwinsathian.com/benchmarks](https://weir.ashwinsathian.com/benchmarks/).

## Current results

Reference machine: Apple M4 Pro, 12 cores, 24 GiB, macOS. The budgets are NFR-5 (hit path) and D36 (GC) in [01](01-technical-spec.md).

| What | Result | Budget | Run |
|---|---|---|---|
| `BenchmarkServeHitSmall`, `v1.2.0`, Go 1.27.3, quiet machine | 614.8 ns/op, 8 allocs/op, 608 B/op (`v1.1.0` measured 636.8 ns in the same run; no significant change) | 1.25 µs, 16 allocs | "v1.2.0 release" below |
| Six load scenarios, `v1.2.0`, quiet machine | all pass; GC 0.44 µs per request at 1M entries | [07 §9](07-testing-strategy.md), 2 µs | "v1.2.0 release" below |
| `BenchmarkServeHitSmall`, v1.1 branch, Go 1.27.1, machine not idle | 669.1 ns/op, 8 allocs/op, 608 B/op (`main` measured 963.9 ns, 12 allocs in the same run) | 1.25 µs, 16 allocs | "v1.1 hardening" below |
| `BenchmarkServeHitSmall`, v1.0, Go 1.27.2 | 852.2 ns/op, 12 allocs/op | 1.25 µs, 16 allocs | "Hardening after v1.0.0-rc.1" below |
| `BenchmarkServeHitSmall`, Go 1.27.1, idle machine | 833.5 and 821.4 ns/op, 12 allocs/op | same | "Hit path optimization" below |
| Five load scenarios, idle machine | all pass | [07 §9](07-testing-strategy.md) | "Reference machine run" below |
| GC per request at 1M entries | 1.08 µs (1.76 µs on the 4-vCPU Linux container) | 2 µs | "Reference machine run" below |

Reproduce with:

```sh
go test -run '^$' -bench . -count 6 ./ ./internal/keys/ ./store/memory/ ./internal/limiter/ ./internal/missrate/
make load
```

Store the raw output next to the table when a run adds a row, so `benchstat old.txt new.txt` works without rerunning the old commit.

### v1.2.0 release (2026-10-23)

The reference machine (Apple M4 Pro, 12 cores), Go 1.27.3, quiet. Test binaries built at the tags `v1.1.0` and `v1.2.0`, eight interleaved runs each, `benchstat`. Raw output: [benchmarks/v1.2.0-release-2026-10-23-v1.1.0.txt](benchmarks/v1.2.0-release-2026-10-23-v1.1.0.txt) and [benchmarks/v1.2.0-release-2026-10-23-v1.2.0.txt](benchmarks/v1.2.0-release-2026-10-23-v1.2.0.txt).

| Benchmark | `v1.1.0` | `v1.2.0` | change | B/op | allocs/op |
|---|---:|---:|---|---:|---:|
| `BenchmarkServeHitSmall` | 636.8 ns ± 7% | 614.8 ns ± 4% | no significant change (p=0.065) | 608 | 8 |
| `BenchmarkServeHitVary` | 1.107 µs ± 4% | 1.113 µs ± 3% | no significant change (p=0.702) | 1 040 | 12 |
| `BenchmarkServeMissCoalesced` | 29.17 µs ± 5% | 28.82 µs ± 6% | no significant change (p=0.645) | 22.19 to 22.32 KiB (+0.59%, p<0.001) | 192 |
| the same, origin calls per key | 1.000 | 1.000 | | | |
| `BenchmarkServeHitParallel/missrate-on` | 191.8 ns ± 1% | 195.0 ns ± 1% | +1.67% (p=0.022) | 608 | 8 |
| `BenchmarkServeHitParallel/missrate-off` | 159.8 ns ± 2% | 161.3 ns ± 2% | +1.00% (p=0.038) | 608 | 8 |
| `BenchmarkServeHitExperiments/unmatched` | 888.7 ns ± 5% | 899.7 ns ± 4% | no significant change (p=0.152) | 1.008 KiB | 11 |
| `BenchmarkServeHitExperiments/matched-no-cookie` | 2.204 µs ± 6% | 2.224 µs ± 5% | no significant change (p=0.279) | 2.962 KiB | 45 |
| `BenchmarkServeHitExperiments/matched-valid-cookie` | 1.794 µs ± 5% | 1.795 µs ± 3% | no significant change (p=0.721) | 2.243 KiB | 32 |

The hit path did not change: 615 ns, 608 B and 8 allocations a hit, inside the hit-path budget (NFR-5). The two parallel-hit rows are 1 to 2% slower and the coalesced miss allocates 0.6% more bytes; the changes in v1.2.0 are on the miss, refresh and breaker paths, which a hit does not take. Bytes and allocations are equal on every other row.

`make load`, scale 1, at `0147a80` (the `v1.2.0` tag's code), 474.8 s, all six pass. Raw output: [benchmarks/v1.2.0-release-2026-10-23-load.txt](benchmarks/v1.2.0-release-2026-10-23-load.txt).

| Scenario | Measured | Threshold | Result |
|---|---|---|---|
| steady hits | 110.1 M requests, 1 835 k/s achieved (target 1 976 k/s, 50% of saturated), hit ratio 1.0000, p50 1.0 µs, p99 4.6 to 5.1 µs in each of six windows, 0 origin calls after warm | ratio > 99%, median-window p99 < 200 µs | pass |
| synchronized expiry | 10 000 keys stored in 1.7 s, all 10 000 refetched, origin peak in-flight 32, busiest second 1 488 origin calls, 0 client errors | refetch ≥ 90%, in-flight ≤ 32, ≤ 2 000 calls/s | pass |
| busting flood plus normal | 99 976 flood requests, 0 shed, flood in-flight peak 40 (`MaxPerPartition` 8, 40 with idle slots borrowed), normal offered 499/s with 0 drops, normal p99 45.1 µs alone and 73.7 µs under flood | in-flight ≤ 40, p99 within 20% or 50 µs | pass |
| origin brownout (×20) | 15 breaker open events, ended closed, 55 834 stale responses, every request 2xx before, during and after, peak 41 goroutines | breaker per config, stale served, no goroutine growth | pass |
| store flap (50%) | 17.7 M requests, 0 client errors, slowest request 49.6 ms (bound 211.5 ms), store breaker 14 open / 8 closed | bounded latency, breaker cycles | pass |
| GC at 1M entries | 808 B allocated and 0.44 µs of GC per request (0.52 µs at 10 000 entries), live heap 2 128 MiB after the fill | gate 2 µs | pass |

The busting flood is the scenario v1.2.0 changed. A flooded path may now borrow origin slots the rest of the traffic leaves idle (the limiter change in the CHANGELOG), so the flood reached 40 requests in flight instead of 8 and none of its 99 976 requests was shed, where earlier runs shed about 71 000. The normal traffic paid for it at the tail: p99 under the flood rose by 28.7 µs (64%), inside the 50 µs floor of the threshold but more than the 20% the earlier runs kept to. Its hit ratio stayed 1.0000.

### v1.1 hardening (2026-10-13)

The reference machine (Apple M4 Pro, 12 cores), Go 1.27.1, not idle (an editor and a second interactive workload), so `main` measured 964 ns here where the record above says 852. Read each row against the other columns of the same row. Three builds, eight interleaved runs each, `benchstat`: `main` at `c070976` (v1.0.1), the branch after the hardening fixes, and the branch after the hit-path performance change. Raw output: [benchmarks/v1.1-hardening-2026-10-13.txt](benchmarks/v1.1-hardening-2026-10-13.txt).

| Benchmark | `main` (v1.0.1) | hardening branch | v1.1 hit path | change from `main` |
|---|---:|---:|---:|---|
| `BenchmarkServeHitSmall` | 963.9 ns ± 6% | 939.5 ns ± 4% | 669.1 ns ± 2% | -30.6% (p<0.001) |
| `BenchmarkServeHitVary` | 1.304 µs ± 5% | 1.284 µs ± 4% | 1.204 µs ± 2% | -7.7% (p<0.001) |
| `BenchmarkServeHitParallel/missrate-on` | 1 004.5 ns ± 7% | 386.7 ns ± 3% | 224.5 ns ± 11% | -77.7% (p<0.001) |
| `BenchmarkServeHitParallel/missrate-off` | 391.8 ns ± 14% | 386.1 ns ± 9% | 209.0 ns ± 17% | -46.6% (p<0.001) |
| `BenchmarkServeMissCoalesced` | 37.90 µs ± 11% | 37.58 µs ± 8% | 40.21 µs ± 9% | no significant change (p=0.08) |
| the same, origin calls per key | 1.003 | 1.003 | 1.000 | |
| hit, bytes and allocations | 1 536 B, 12 | 1 536 B, 12 | 608 B, 8 | -60%, -33% |
| vary hit, bytes and allocations | 1 552 B, 13 | 1 552 B, 13 | 1 040 B, 12 | |

What moved the numbers:

- The hardening took the miss-rate tracker's mutex off the hit path: a hit takes a read lock and adds to a counter only when its partition already has one. That is the first drop in the parallel row. A single-threaded hit did not change. (An earlier note here claimed it had, from a run that was not interleaved with its baseline; that was noise.)
- The performance change keeps the request's classification on the stack for a hit and does not build the forwarded header until a miss, a pass-through or a vary lookup needs it. That is the 928 B and the 4 allocations, and with less to collect the parallel rows fall again.
- A request that creates a flight looks at the store once more first, so the window in which a follower's lookup misses just before the leader stores no longer costs a second fetch (1.003 to 1.000 calls per key).

In round figures, as the README quotes them: hits from 12 goroutines take 225 ns each on the branch and 1,005 ns on `v1.0.1`.

Smaller measurements on the same machine: `BenchmarkLimiterReleaseFullQueue` (one release with 1 024 waiters of distinct partitions) 1 750 to 225 ns; `BenchmarkObserveRequest` in `observe/prom` (one request event, 12 goroutines) 95 to 30 ns.

`make load`, scale 1, on the same machine. The first column is the record of 2026-10-12 further down; the other two are this branch before and after the performance change.

| Scenario | v1.0.0-rc (2026-10-12) | after the hardening fixes | after the performance change | Result |
|---|---|---|---|---|
| steady hits, paced at half the saturated rate | 578 k/s, p50 1.5 µs, p99 7.7 to 11.3 µs per window | 1 308 k/s, p50 1.4 µs, p99 5.6 to 7.7 µs | 1 331 k/s, p50 1.3 µs, median-window p99 9.2 µs | pass |
| synchronized expiry | all 10 000 refetched, peak 32 in flight, 1 455 calls in the busiest second, 0 errors | the same, 1 410 calls | the same, 1 464 calls | pass |
| busting flood plus normal | 70 784 of 99 976 shed, in-flight peak 8, normal p99 53.2 µs alone and 36.9 µs under flood | 70 833 shed, 41.0 and 28.7 µs | 71 150 shed, 45.1 and 32.8 µs | pass |
| origin brownout (×20) | 16 breaker opens, 55 054 stale, every request 2xx | 17 opens, 53 843 stale, every request 2xx | 17 opens, 54 155 stale, every request 2xx | pass |
| store flap (50%) | 11.2 M requests, 0 errors, slowest 49.2 ms | 11.0 M, 0 errors, 50.5 ms | 15.4 M, 0 errors, 66.0 ms (bound 208 ms) | pass |
| GC at 1M entries | 1 728 B allocated and 0.93 µs of GC per request | 1 736 B, 0.94 µs | 808 B, 0.46 to 0.71 µs (two runs) | pass (gate 2 µs) |

The paced target of the steady-hits scenario is half of what the engine does saturated, so its rate more than doubling is the tracker's lock again. The saturated stage of the GC test is noisy on a machine that is not idle: two runs after the performance change made 88 M and 206 M requests in 60 s at 1M entries (138 M before it), with GC at 0.71 and 0.46 µs per request. Allocation per request is the stable figure, and it halved.

`make cache-tests`: no regression against the baseline, and `UPDATE=1` leaves the baseline file unchanged, before and after the performance change.

Eviction pause by shard size (same machine): the longest single `Set` on a full shard of hot 64-byte records, `BenchmarkEvictionPauseLargeShard`, three runs each.

| Shard | Longest `Set` |
|---|---|
| 16 MiB | 0.48 ms |
| 64 MiB | 2.56 ms |
| 256 MiB | 9.76 ms |

The walk is linear in the shard, so the 512 MiB shards of an 8 GiB default store before v1.1 come to about 20 ms. `memory.Shards` now keeps shards near 16 MiB ([05 §5.1](05-storage-interface-spec.md)).

## What each benchmark measures

| Benchmark | Package | Path |
|---|---|---|
| `BenchmarkServeHitSmall` | `weir` | `Serve` on a fresh 1 KiB entry in the memory store, body read to `io.Discard`: classify, store `Get`, freshness, response build |
| `BenchmarkKeyBuild` | `internal/keys` | `Classify` of a GET with a query, four request headers and a keyed cookie: validation, normalization, forwarded request, primary key |
| `BenchmarkAcceptEncoding` | `internal/keys` | the Accept-Encoding bucket for a five-member value with qvalues |
| `BenchmarkServeHitVary` | `weir` | a hit whose entry varies on one forwarded header: the variant lookup on top of `ServeHitSmall` |
| `BenchmarkServeMissCoalesced` | `weir` | a cold key requested by 8 goroutines at once; the origin yields the processor 50 times so followers can join, without a timer; reports `origin-calls/op` |
| `BenchmarkServeHitParallel` | `weir` | `ServeHitSmall` from GOMAXPROCS goroutines over 1024 keys, miss-rate tracker on and off |
| `BenchmarkLimiterAcquireRelease` | `internal/limiter` | uncontended Acquire/Release, and with a full queue |
| `BenchmarkMemoryStoreGetParallel` | `store/memory` | `Get` from 12 goroutines over 1024 resident 1 KiB entries (16 shards) |

## Earlier runs

The sections from here to "Reference machine run" are superseded by it. Numbers from the 4-vCPU Linux container cannot be compared with the reference machine, and the 4 µs budget mentioned under M10 was provisional: NFR-5 now says 1.25 µs.

## M1 (2026-10-01)

Machine: Apple M4 Pro, 12 cores, 24 GiB, macOS (Darwin 27.0.0). Go 1.27.1 darwin/arm64. Commit: branch `card/M1-18-rfc-bench` on base `c878031`. Medians of 6 runs.

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkServeHitSmall` | 847 | 1480 | 14 |
| `BenchmarkKeyBuild` | 846 | 536 | 9 |
| `BenchmarkAcceptEncoding` | 171 | 0 | 0 |
| `BenchmarkMemoryStoreGetParallel` | 18.9 | 0 | 0 |

Raw output: [benchmarks/m1.txt](benchmarks/m1.txt).

`BenchmarkServeHitVary`, `BenchmarkServeMissCoalesced` and the parallel hit benchmark did not exist at M1; they are first measured in M10 below.

## M10 load tests (2026-10-07)

Machine: 4 vCPU Linux container (not the reference machine), Go 1.27.0 linux/amd64. `make load`, scale 1, one run, 294 s (second run of the final harness). Branch `claude/gifted-meitner-19l6l6` on base `44a69ae`. Scenarios and thresholds: [07 §9](07-testing-strategy.md).

| Scenario | Measured | Threshold | Result |
|---|---|---|---|
| steady hits | 6.29 M requests, 105 k/s achieved (target 160 k/s, 50% of the saturated rate; about a third of saturation), hit ratio 1.0000, p50 7.7 µs, p99 164 µs in every one of six 10 s windows | ratio > 99%, median-window p99 < 200 µs | pass |
| synchronized expiry | 10 000 keys stored in 1.9 s, `max-age=60`, all 10 000 refetched, origin peak in-flight 32 (`MaxConcurrent` 32), busiest second 1 470 origin calls, 0 client errors | refetch ≥ 90%, in-flight ≤ 32, ≤ 2 000 calls/s | pass |
| busting flood plus normal | 99 976 flood requests (73 284 shed), flood in-flight peak 8 (`MaxPerPartition` 8), normal offered 499/s with 0 drops, normal p99 131 µs alone, 90 µs under flood | in-flight ≤ 8, p99 within 20% (50 µs floor) | pass |
| origin brownout (×20) | 17 breaker open events, ended closed, 53 972 stale responses, every request 2xx during the brownout, peak 39 goroutines | breaker per config, stale served, no goroutine growth | pass |
| store flap (50%) | 3.59 M requests, 0 client errors, slowest request 54 ms (bound 207 ms), store breaker 15 open / 10 closed | bounded latency, breaker cycles | pass |

Every scenario also met the goroutine check (baseline ±10 within 5 s after load), before and after `Close`. Steady-hit p99 sits one histogram bucket (12.5%) under the 200 µs bound on this box, and moves with the achieved rate (an earlier lighter run measured 74 µs); compare runs only at a similar rate. The 4-core run is a floor; the reference machine run of 2026-10-11 below supersedes this table.

## M10 benchmarks and benchstat against M1 (2026-10-08)

Machine: 4 vCPU Intel Xeon 2.1 GHz Linux container (not the reference machine), Go 1.27.0 linux/amd64. M1 is the merge of PR 29 (`73e4d2e`), rerun here because the M1 row above was measured on an M4 Pro and cannot be compared with this box; M10 is `b909550` plus the benchmarks of this change. Medians; M1 pools two runs of 6 (12 samples) taken at different times, M10 is one run of 6. Raw output: [benchmarks/m1-linux4.txt](benchmarks/m1-linux4.txt), [benchmarks/m10.txt](benchmarks/m10.txt); full `benchstat`: [benchmarks/m1-vs-m10.benchstat.txt](benchmarks/m1-vs-m10.benchstat.txt). The box is shared and drifts: the first M10 run (not kept) gave `ServeHitSmall` +8% and `KeyBuild` +14%, the second +18% and +13%, and an interleaved `ServeHitSmall` rerun +22%. Read the change as roughly 10 to 20%.

| Benchmark | M1 ns/op | M10 ns/op | change | allocs/op (M1 → M10) |
|---|---:|---:|---|---:|
| `BenchmarkServeHitSmall` | 2 243 | 2 656 | +18% (p<0.001) | 14 → 14 |
| `BenchmarkKeyBuild` | 2 066 | 2 330 | +13% (p=0.004) | 9 → 9 |
| `BenchmarkAcceptEncoding` | 311 | 330 | no significant change | 0 → 0 |
| `BenchmarkMemoryStoreGetParallel` | 59.5 | 58.5 | no significant change | 0 → 0 |

Milestones 2 to 9 were not recorded; M1 to M10 is the only series. Bytes and allocations per operation are unchanged on every row, so the added time is CPU work in the hit path (the miss-rate tracker, breaker and event plumbing arrived after M1); it was not profiled.

Benchmarks added after M1 (M10 only, same box):

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkServeHitVary` | 3 195 | 1 496 | 15 |
| `BenchmarkServeMissCoalesced` (8 callers, origin yields instead of sleeping) | 121 100 | 22 610 | 209 (1.003 to 1.004 origin calls/op) |
| `BenchmarkServeHitParallel/missrate-on` (4 procs) | 2 599 | 1 480 | 14 |
| `BenchmarkServeHitParallel/missrate-off` (4 procs) | 1 159 | 1 480 | 14 |
| `BenchmarkLimiterAcquireRelease/uncontended` | 176 | 24 | 1 |
| `BenchmarkLimiterAcquireRelease/full_queue` | 11 200 | 24 | 1 |
| `BenchmarkObserveFlood` (`internal/missrate`) | 249 | 0 | 0 |

`ServeMissCoalesced` includes spawning 8 goroutines per operation, so it is a relative number. An earlier version slept 100 µs in the origin and measured the Linux timer floor (about 1 ms) instead of Weir. Origin calls per operation is slightly above 1 because a request can miss the store just before the leader stores, then join after the flight has been removed and start a second flight (lookup in `serve.go`, `Join` in `flight.go`). The cost is a second origin fetch for 0.3 to 0.4% of cold keys under this burst, never a wrong response. FR-COA-1 speaks of concurrent requests sharing a flight and does not close this window; it is recorded here and not fixed (STATUS notes).

### NFR-5

`BenchmarkServeHitSmall` is 2.7 µs/op and 14 allocs/op here, inside the budget of 4 µs and 16 allocs, but this box is not the reference machine and the M1 baseline on the M4 Pro is 0.85 µs, so the absolute budget has 4.7x slack there and cannot gate anything. The 20% regression rule is the gate that works, and the +18% above sits at its edge on this box. NFR-5 was left as written ("provisional") at the time; the ceiling was set from the reference run of 2026-10-11, below; finalizing it needs the reference-machine rerun and Ashwin's approval of any wording change. Pending that rerun, treat the M1 to M10 growth as a finding to explain, not a pass. Decided 2026-10-08: the 16 allocs/op bound and the 20% rule apply now; the 4 µs ceiling ends its provisional state when six or more runs on an Apple M-series machine at the M10 head are recorded here with `benchstat` against `benchmarks/m1.txt`, the growth is explained, and the ceiling is set to 1.5 times the median. If that growth is over 20%, a perf card comes first.

### Parallel hits and the miss-rate tracker

`Tracker.Observe` takes one mutex per cacheable request, hits included. With 4 procs a hit costs 2.60 µs with the tracker and 1.16 µs without, so the mutex adds about 1.4 µs, 2.2 times the time per hit under contention (the M8-02 run on an M4 Pro with 12 procs gave 600 against 343 ns, 1.75 times). Each goroutine starts at its own key offset, so the contention is on the tracker, not on every goroutine hitting the same shard in lockstep. Allocations are identical. Decision (Ashwin, in this session, 2026-10-08): record only. NFR-5 gets no parallel budget and the mutex stays. The two options for later are `TryLock` (367 ns on the M4 Pro, but it drops half the samples at 12 procs and so doubles the effective `MinMisses`) and per-shard counters; neither has a card.

## M10 GC cost at 1M entries (2026-10-08)

D36 keeps the store on the Go heap until GC cost is measured. `TestGCAt1MEntries` (`make load`, `loadtest/gc_test.go`) fills a memory store with 10 000 and then 1 000 000 entries with a 1 KiB body each (2.0 GiB live heap at 1M, 2.5 GiB in use), then runs two stages. Same box (4 vCPU Xeon, Go 1.27.0), one run, default `GOGC`. Raw output: [benchmarks/m10-gc.txt](benchmarks/m10-gc.txt).

1. Paced: 64 goroutines at 20 000 requests/s for 30 s, with a forced GC every 5 s (5 cycles). This gives GC CPU per cycle and a p99 with cycles in it. The runtime updates its CPU metrics only at the end of a cycle, and a 2 GiB heap triggers one rarely at this rate, hence the forcing.
2. Saturated: 64 goroutines without pacing for 60 s, natural cycles only. This gives CPU and allocation per request. The paced stage's CPU per request (about 26 µs) is mostly the load generator, so a share computed on it is far too low; the first version of this section did that and reported 6.6%, which was wrong.

| | 10 000 entries | 1 000 000 entries |
|---|---:|---:|
| GC CPU per forced cycle | 13.0 ms | 2 510 ms |
| paced p50 / p99 (forced cycle every 5 s) | 6.7 µs / 53 µs | 8.2 µs / 57 µs |
| paced slowest request | 5.4 ms | 9.4 ms |
| saturated requests in 60 s | 25.2 M | 18.6 M |
| allocation per request | 1 656 B | 1 664 B |
| non-GC CPU per request (generator included) | 8.7 µs | 10.6 µs |
| natural cycles in the window | 1 575 | 12 |
| GC share of busy CPU, runtime-reported | 7.3% | 13.6% |
| GC share of busy CPU, projected | 8.7% | 15.4% |

Result: at 1M entries GC takes 13.6% (reported over 12 cycles, so roughly 12.5% to 14.7% with a cycle of lag at the window edges) to 15.4% (projected from the cost per cycle) of busy CPU under saturated hits, above the 10% line in PLAN M10.5b. A pointer-light layout card pair is therefore added (M16-01 prototype and decision, M16-02 implementation, in `docs/cards/11-phase1x.md`). D36 does not change by this PR: the card starts with the decision.

The true share is higher than these figures. The non-GC CPU per request here is 8.7 to 10.6 µs, against 2.7 µs for a request in `BenchmarkServeHitSmall`; the rest is the harness (goroutine scheduling on 4 cores, random keys, `Request` construction), and a smaller denominator means a larger share. The figures are a lower bound for a server doing nothing but hits and a rough measure for one that also does other work. They also depend on entry size: about 2.1 KiB of live heap per entry here; smaller bodies carry more pointers per live byte. The GC CPU per cycle includes idle-priority workers on otherwise idle cores, which overstates the paced cost somewhat; the runtime-reported figure has no such correction and is 13.6%. Latency: the p99 with a forced cycle every 5 s is 57 µs against 53 µs for the small heap, so the cost shows up as CPU, not as a visible tail at this rate. Re-run on the reference machine and replace this table.

The share depends on the denominator. GC cost per request is about cycle CPU × allocation per request ÷ (live heap × GOGC/100), here roughly 1.9 µs at 1M entries, and it does not depend on offered load. M16-01 reports this figure beside the share. A load generator, `net/http` or TLS in the denominator lowers the share without changing the cost.

## M16-01 pointer-light layout prototype (2026-10-09)

D36 asks whether a pointer-light layout earns its cost. The prototype (`benchmarks/m16-prototype.patch`, not merged) keeps one `store.Encode` record per entry in 1 MiB pointer-free arena chunks and a pointer-free index (`map[Key]{chunk, off, len, expires}`); `Get` decodes on every hit, so the engine and `store.Entry` are unchanged. It has no eviction or reclamation, so it measures the GC and hit-path cost of the layout and nothing about churn. Same box as M10 (4 vCPU Xeon, Go 1.27.0), `TestGCAt1MEntries` with 1 KiB bodies, 1M entries, one run per column; the heap column is a rerun of the M10 baseline, because the box drifts (12.3% now against 15.4% then). Raw output: [benchmarks/m16-gc.txt](benchmarks/m16-gc.txt), [benchmarks/m16-bench.txt](benchmarks/m16-bench.txt).

| 1M entries, saturated hits | heap, GOGC=100 | heap, GOGC=200 | prototype, GOGC=100 | prototype, GOGC=200 |
|---|---:|---:|---:|---:|
| live heap after fill | 2 099 MiB | 2 098 MiB | 1 394 MiB | 1 395 MiB |
| GC CPU per forced cycle | 2 247 ms | 2 362 ms | 5.3 ms | 5.7 ms |
| allocation per request | 1 728 B | 1 728 B | 3 776 B | 3 776 B |
| non-GC CPU per request | 12.6 µs | 12.5 µs | 12.2 µs | 13.0 µs |
| natural cycles in the window | 11 | 5 | 50 | 23 |
| **GC µs per request** (cycle CPU × alloc ÷ live ÷ GOGC/100) | 1.76 | 0.93 | 0.014 | 0.007 |
| GC share, projected (denominator: the harness's non-GC CPU) | 12.3% | 6.9% | 0.11% | 0.06% |
| GC share, runtime-reported (few cycles, noisy) | 11.5% | 6.2% | 0.50% | 0.25% |

The two projected shares in the `GOGC=200` columns use the corrected formula (the test's `satProjected` at the time of the run assumed `GOGC=100` and printed 12.89% and 0.11% there (it now reads the `GOGC` in effect)). An allocation cut on the heap layout was not implemented; by the same formula GC µs per request is linear in allocation per request, so halving 1 728 B gives about 0.88 µs (6.5%), and `GOGC=200` with the cut about 0.44 µs. These two are projections.

Hit path, `benchstat` of 12 samples each (two interleaved batches of 6), same box, which is slower than at M10 (`ServeHitSmall` 4.6 µs here against 2.7 µs then, so compare only within this table):

| Benchmark | heap | prototype | change | allocs/op |
|---|---:|---:|---:|---:|
| `BenchmarkServeHitSmall` | 4.59 µs | 6.50 µs | +41.5% (p<0.001) | 14 → 26 |
| `BenchmarkMemoryStoreGetParallel` | 54.6 ns | 349.9 ns | +541% | 0 → 2 (1.4 KiB) |

What it says:

- The layout removes the GC problem: 2.2 s per cycle becomes 5 ms, and the live heap is 33% smaller (no map, node, header or slice objects). The cost per request goes from 1.76 µs to 0.014 µs.
- It breaks the hit-path budget as built. Decoding on every `Get` copies the body and rebuilds the header map: +12 allocs/op (26, over the 16 bound of NFR-5), +41% ns/op (over the 20% rule), and 2.2x the bytes allocated per request. A layout that passes needs a hit path that serves headers and body from the encoded bytes without building an `Entry`. That changes how the engine reads entries (P4, the `store.Entry` contract in 05 §1), which is a design decision and a public-interface question for the store, not a layout detail.
- Cheaper levers get most of the way for the cost of memory. `GOGC=200` halves GC µs per request (1.76 to 0.93) for about one more live-heap size (2.1 GiB here) of headroom, and needs no code. Allocation cuts help linearly. Neither reaches 0.
- The share depends on its denominator. At 1.76 µs of GC per request, a server whose hit costs 12.6 µs (this harness) sees 12.3%, one whose hit costs 30 µs (net/http, TLS) about 5.5%. GC µs per request does not move with that, so it is the better gate.

Recommendation, adopted 2026-10-09 (Ashwin delegated the decision; D36 in 01 now says this, M16-02 moved to the deferred cards in 20-later.md, `TestGCAt1MEntries` reports GC µs per request against the 2 µs gate): keep the heap layout and do not start M16-02 now. Replace the 10% share line with a gate on GC µs per request (a cost per harness hit, about 1.7 KiB allocated per request, with `GOMEMLIMIT` unset: a server that allocates more per request pays proportionally more) at 1M entries and 1 KiB bodies, at most 2 µs with default `GOGC` (now 1.76; the margin is thin and the baseline drifted 15.4% to 12.3% between two runs on this box, so re-base the number on the reference-machine rerun), and document `GOGC=200` as the operator lever. Revisit M16-02 if a deployment measures above the gate or needs more than 1M entries, and if so scope it as a design change first (a serve-from-encoded-bytes path), since the layout alone fails NFR-5. Reference-machine rerun still pending for both this and the M10 table.

## Reference machine run (2026-10-11)

First run on the reference machine, at `138d4ff` (the merge of PR 105, the v1.0.0 release candidate). Apple M4 Pro, 12 cores, macOS, Go 1.27.1 darwin/arm64. `make check` passed in all four modules (0 lint issues, trace 146/146). Raw output: [benchmarks/reference-2026-10-11.txt](benchmarks/reference-2026-10-11.txt).

### Load scenarios (`make load`, scale 1, 473 s)

| Scenario | Measured | Threshold | Result |
|---|---|---|---|
| steady hits | 26.3 M requests, 438 k/s achieved (target 442 k/s, 50% of saturated), hit ratio 1.0000, p50 2.0 µs, p99 18.4 to 22.5 µs in each of six windows, 0 origin calls after warm | ratio > 99%, median-window p99 < 200 µs | pass |
| synchronized expiry | 10 000 keys stored in 1.7 s, `max-age=60`, all 10 000 refetched, origin peak in-flight 32, busiest second 1 485 origin calls, 0 client errors | refetch ≥ 90%, in-flight ≤ 32, ≤ 2 000 calls/s | pass |
| busting flood plus normal | 99 976 flood requests (71 151 shed), flood in-flight peak 8 (`MaxPerPartition` 8), normal offered 499/s with 0 drops, normal p99 36.9 µs alone and 41.0 µs under flood (+11%) | in-flight ≤ 8, p99 within 20% (50 µs floor) | pass |
| origin brownout (×20) | 18 breaker open events, ended closed, 52 975 stale responses, every request 2xx before, during and after, peak 48 goroutines | breaker per config, stale served, no goroutine growth | pass |
| store flap (50%) | 10.8 M requests, 0 client errors, slowest request 47.1 ms (bound 208.8 ms), store breaker 14 open / 8 closed | bounded latency, breaker cycles | pass |

All five passed, and `TestGCAt1MEntries` passed as well.

### GC cost at 1M entries (D36)

| | 10 000 entries | 1 000 000 entries |
|---|---:|---:|
| GC CPU per forced cycle | 9.6 ms | 1 299 ms |
| paced p50 / p99 (forced cycle every 5 s) | 4.6 µs / 36.9 µs | 3.8 µs / 36.9 µs |
| paced slowest request | 2.0 ms | 11.9 ms |
| saturated requests in 60 s | 61.4 M | 43.4 M |
| allocation per request | 1 816 B | 1 824 B |
| non-GC CPU per request (generator included) | 11.0 µs | 14.7 µs |
| natural cycles in the window | 4 127 | 36 |
| GC share of busy CPU, runtime-reported | 3.1% | 6.2% |
| GC share of busy CPU, projected | 5.7% | 6.8% |
| **GC µs per request** | 0.66 | **1.08** |

The GC gate (at most 2 µs of GC per request at 1M entries, D36) holds with 1.08 µs, against 1.76 µs on the 4-core Xeon. The share of busy CPU is 6.2% to 6.8%, under the project's 10% line. The pointer-light layout stays deferred.

### `BenchmarkServeHitSmall` (NFR-5)

| Run | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| 1 | 1 856 | 1 640 | 14 |
| 2 | 1 247 | 1 640 | 14 |
| 3 | 1 304 | 1 640 | 14 |
| 4 | 1 268 | 1 640 | 14 |
| 5 | 1 267 | 1 640 | 14 |
| 6 | 1 545 | 1 640 | 14 |
| median of 6 | 1 286 | 1 640 | 14 |

The median is 1.29 µs and the allocation count is 14, inside the bound of 16. NFR-5 first set the ns/op ceiling at 1.5 times this median, 1.9 µs; the optimized hit path below supersedes it with 1.25 µs. Runs 1 and 6 are noisy; the four quiet runs sit within 1 247 to 1 304.

**Growth since M1, measured back to back.** The M1 row above (847 ns/op) was taken on another day and is not comparable with this run: `73e4d2e`, `b909550` (M10) and `138d4ff` rerun on the same machine in one session with `-count 10` and compared with `benchstat` (raw: [benchmarks/reference-2026-10-11-benchstat.txt](benchmarks/reference-2026-10-11-benchstat.txt)):

| Commit | ns/op | vs M1 | B/op | allocs/op |
|---|---:|---:|---:|---:|
| M1 `73e4d2e` | 926 ± 1% | | 1 480 | 14 |
| M10 `b909550` | 1 150 ± 1% | +24% (p<0.001) | 1 480 | 14 |
| release candidate `138d4ff` | 1 223 ± 3% | +32% (p<0.001) | 1 640 | 14 |

So the +52% in the first table was a comparison with a stale number; the real growth since M1 is +32%, still over the 20% line in NFR-5. It has two parts:

- M1 to M10, +24%: the miss-rate tracker, the breaker and the event plumbing, which arrived after M1. The M10 review already measured +18% on the Linux box, called it the edge of the rule and accepted it (section above). Allocations and bytes are unchanged.
- M10 to the release candidate, +6% (the same +6% as on the Linux box): `keys.Classified`, which `Serve` allocates once per request, grew from 408 to 528 bytes as range, experiment and conditional fields were added. That moves it up an allocator size class, which accounts for all of the +160 B/op (an allocation profile shows the whole difference in `(*Engine).Serve`, and the count of 14 allocations did not change); the extra bytes to zero and copy account for the time.

Resolved by the hit path optimization at the end of this file: on the M4 Pro the optimized branch is 4% faster than M1, so the growth is gone. The decision to accept it, proposed here first, is superseded.

## Hit path optimization (2026-10-11)

After the reference run showed +32% against M1, the hit path was profiled and trimmed. Measured on the 4-core Linux box (not the reference machine), `-count 10`, `benchstat`, base `138d4ff`; raw output: [benchmarks/hit-path-opt-linux4.txt](benchmarks/hit-path-opt-linux4.txt).

| Benchmark | base | now | change | allocs/op |
|---|---:|---:|---|---:|
| `BenchmarkServeHitSmall` | 3.02 µs | 2.11 µs | -30% (p<0.001) | 14 → 12 |
| `BenchmarkKeyBuild` | 2.58 µs | 1.77 µs | -31% | 9 → 9 |
| `BenchmarkAcceptEncoding` | 356 ns | 293 ns | -18% | 0 → 0 |

B/op fell from 1 640 to 1 536. What changed, in order of gain:

- `forwardHeader` no longer deletes about thirty names a strict forward cannot hold (with no `Allow` and no `Key.Headers` the map only ever holds the default-forward fields); a Connection option still removes what it names.
- The URI and origin tags are computed on use. A hit reads the tags stored on the entry, so `Classify` no longer hashes two SHA-256 tags per request, and `Classified` drops under 512 bytes, one allocator size class.
- One `time.Now` per request instead of three, and not under the miss-rate mutex.
- Fewer map reads (Range, If-Range, If-None-Match, Cache-Control read once; `IsUpgrade` indexes instead of canonicalizing constant keys), one canonical-key scan per request, one allocation for origin and partition, one for the stored body, and an early exit in the Accept-Encoding parser.

Every change to `internal/keys` is held to the old behavior by a differential fuzz target that keeps the previous implementation in the test files (`FuzzForwardHeaderMatchesReference`, `FuzzAcceptEncodingMatchesReference`, `FuzzIsCanonicalKey`) next to `FuzzForwardEqualsKey`.

### On the reference machine

Two back-to-back runs on the M4 Pro, machine otherwise idle, three builds interleaved by `benchstat` (`-count 10` each; raw: [benchmarks/hit-path-opt-m4pro.txt](benchmarks/hit-path-opt-m4pro.txt)). An earlier run with several other interactive workloads and a Chrome video stream open had a ±38% spread and is discarded.

| Build | Run 1 ns/op | Run 2 ns/op | vs first engine (run 1 / run 2) | B/op | allocs/op |
|---|---:|---:|---|---:|---:|
| first engine `73e4d2e` | 870.5 ± 1% | 854.1 ± 2% | | 1 480 | 14 |
| all defenses on `b909550` | 1 087 ± 3% | 1 070.5 ± 1% | +24.9% / +25.3% | 1 480 | 14 |
| optimized branch | 833.5 ± 3% | 821.4 ± 4% | **-4.3% / -3.8%** (p<0.01) | 1 536 | 12 |

The optimized hit path is about 4% faster than the first engine and 23% faster than the build with all defenses on, with two fewer allocations. The +32% growth since the first engine that the first reference run exposed is gone. Bytes per op are 56 above the first engine (1 536 against 1 480), not the 160 above it of the release candidate, and the allocation count is lower.

The gain is smaller on the M4 Pro than on the Xeon (-30% there against -23% here from the all-defenses build to the branch, relative to the release candidate), as expected: SHA-256 is cheaper with the ARM instructions, so the two tags no longer hashed per hit saved less.

**New baseline.** The 20% regression rule is measured from this run, a median of about 0.83 µs. The hit-path budget sets the ns/op ceiling at 1.5 times that, 1.25 µs (NFR-5), replacing the 1.9 µs derived from the first, noisier reference run. The first reference run above stays as the record of the release candidate.

## Hardening after v1.0.0-rc.1 (2026-10-12)

The changes that followed the `v1.0.0-rc.1` rehearsal (stable `QuerySort` by name, the `Cache-Control` and `Pragma` filter on the forward, `Range` ignored beside `If-Match`, per-field overhead in `Entry.Size`, the Valkey record limit, the wasm build) measured on the reference machine with Go 1.27.2. The machine was not idle (an editor and a second interactive workload), so read the load figures as pass or fail and the benchstat lines as the comparison. Raw output: [benchmarks/hardening-2026-10-12.txt](benchmarks/hardening-2026-10-12.txt).

### Benchmarks (`benchstat`, `main` at `3f7eefa` against the branch, 10 interleaved runs)

| Benchmark | main | branch | change | B/op | allocs/op |
|---|---:|---:|---|---:|---:|
| `BenchmarkServeHitSmall` | 838.6 ns ± 1% | 852.2 ns ± 1% | +1.6% (p=0.001) | 1 536 | 12 |
| `BenchmarkKeyBuild` | 775.2 ns ± 1% | 792.3 ns ± 1% | +2.2% (p<0.001) | 512 | 8 |

The cost is the directive filter, two map reads on the forwarded header for every request. Allocations and bytes are unchanged. 852 ns is 3% above the 0.83 µs baseline, inside the 20% rule and the 1.25 µs ceiling of NFR-5.

### Load scenarios (`make load`, scale 1, 472 s)

| Scenario | Measured | Threshold | Result |
|---|---|---|---|
| steady hits | 34.7 M requests, 578 k/s achieved (target 584 k/s), hit ratio 1.0000, p50 1.5 µs, p99 7.7 to 11.3 µs in each of six windows, 0 origin calls after warm | ratio > 99%, median-window p99 < 200 µs | pass |
| synchronized expiry | 10 000 keys stored in 1.7 s, all 10 000 refetched, origin peak in-flight 32, busiest second 1 455 origin calls, 0 client errors | refetch ≥ 90%, in-flight ≤ 32, ≤ 2 000 calls/s | pass |
| busting flood plus normal | 99 976 flood requests (70 784 shed), flood in-flight peak 8, normal offered 499/s with 0 drops, normal p99 53.2 µs alone and 36.9 µs under flood | in-flight ≤ 8, p99 within 20% (50 µs floor) | pass |
| origin brownout (×20) | 16 breaker open events, ended closed, 55 054 stale responses, every request 2xx before, during and after, peak 41 goroutines | breaker per config, stale served, no goroutine growth | pass |
| store flap (50%) | 11.2 M requests, 0 client errors, slowest request 49.2 ms (bound 207.5 ms), store breaker 16 open / 10 closed | bounded latency, breaker cycles | pass |

`TestGCAt1MEntries` passed: 0.93 µs of GC per request at 1M entries (1.08 µs in the first reference run, gate 2 µs), GC share 6.4% runtime-reported and 8.9% projected. Live heap after the fill is 2 099 MiB for 1M entries, the same as in the first reference run.


## v1.1.0 release candidate (2026-10-13)

`main` at `0e581b1`, the commit the `release-gate` run of the same day covers, on the reference machine, not idle, Go 1.27.1. `make load` (scale 1, 475 s): all six scenarios pass. Steady hits ran at 1 893 k requests a second (paced to half of the saturated rate) with a p99 of 5.1 µs and no origin call after warming; synchronized expiry refetched 10 000 of 10 000 keys with the origin peak at its configured 32 and no client error. `BenchmarkServeHitSmall`, six runs: 610 to 643 ns, 608 B and 8 allocations a hit, inside the NFR-5 budget and level with the 669 ns recorded for the hardening branch. `make check`, `make vuln` (Go 1.27.2) and `make cache-tests` pass on the same commit.

## Comparison with other caches, round 3 (2026-10-22)

Specification: [11-comparison-spec.md](11-comparison-spec.md). Harness and every subject's configuration: `bench/compare/`. Each subject ran as a container in Docker Desktop (12 CPUs, about 12 GiB) on the reference machine, with the machine otherwise quiet, behind the same origin, with a new container for each tier. Three repetitions per scenario and the median reported, except `large-body-herd` and `post-flood` (one repetition). Where the three repetitions differ by more than a tenth of the median, the cell gives the range after it.

This section replaces the tables of round 2 (2026-10-13), which measured `v1.1.0`. Round 3 measured Weir as the published `v1.2.0` module with the same scenarios, probes, harness logic and competitor configurations, and ran the speed and memory pillar for the first time. Round 2's tables are in the history of this file; the Weir figures that moved between the two rounds are below in "what the round changed in Weir".

Subjects: Varnish 9.1.0, nginx 1.30.5, Caddy 2.11.7 with cache-handler v0.17.0 (Caddy's cache module, built on Souin, named "cache-handler (Souin)" below), Caddy 2.11.7 with Weir v1.2.0, and Caddy 2.11.7 as a plain reverse proxy ("no cache"). `default` is the smallest configuration that turns caching on; `tuned` is each project's documented stampede and stale settings (11 §3); `hardened`, for the probes, is the default plus that project's documented way to key or strip request input (11 §5.3). `examples/weirproxy` on v1.2.0 gives the same counts as the Caddy rows, except where a note says otherwise, and is left out of the tables. It has no purge route, so `purge-herd` is not available for it.

Every figure about Varnish, nginx and cache-handler below comes from our own runs with configurations we wrote, and has not been verified by the owners of those projects. Read these tables with five limits in mind.

- No maintainer of Varnish, nginx or cache-handler has reviewed the configuration used for their project (11 R4). We have been wrong three times so far, each time against the other project and each time found by our own review before publication. Round 1: nginx and the control answered 502 from port exhaustion in our containers, and the Varnish tuned tier lacked the rule from its grace guide that abandons a failed background fetch. Round 2: cache-handler's tuned tier ran the Otter storage at its default size of 10,000, which evicted stored URLs and showed up as 7,435 origin calls in `sync-expiry` where the same tier with room makes 5,000.
- Each script ran once, on 2026-10-22, at harness commit `0147a80`, which every result file records: one invocation of `run.sh` for every subject and both tiers, then `probes.sh`, `conformance.sh` and `speed.sh`, with `WEIR_VERSION=v1.2.0`. `bench/compare` last changed at `66e651b`, so it is the same at both commits. Round 2 was stitched from 22 invocations at two harness commits; this round has no such seam.
- The `origin` control (the runner against the origin with nothing in between) ran every scenario too. In all 50 of its rows that carry requests the origin counted exactly the requests the runner sent (the other 3 are `purge-herd`, which the origin cannot run).
- The speed and memory pillar (11 §5.5) ran, and only its counts are published: memory per object, hit ratio and origin calls (below). Its latency, CPU per request and saturation figures stay measured but unpublished, because all five timing repetitions failed the drift gate of 11 §6. Measured against the first control run of its repetition, the `caddy-nocache` control's p50 moved at most 9% (under the gate's 10%), but its p99 moved 14 to 22% in four repetitions and 62% in the other, and in two repetitions one control run's p99 was several times the others'. Nothing here is a throughput or latency claim. Differential fuzzing (11 §5.6) was not run.
- The hardened tiers were written after the round 1 default results, to the headers the round 1 probes use. They show that each project's documented mechanism closes those probes. They do not show that a deployment would have known which headers to list.

```sh
cd bench/compare
WEIR_VERSION=v1.2.0 ./run.sh -reps 3      # every subject, both tiers; SUBJECTS=varnish TIERS=tuned for one
./probes.sh
./conformance.sh
./speed.sh                                 # about two hours; quiet machine (11 §5.5)
```

### Comparison: origin calls

| Scenario | clients | no cache | Weir v1.2.0 | Weir tuned | Varnish | Varnish tuned | nginx | nginx tuned | cache-handler (Souin) | cache-handler (Souin) tuned |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `cold-herd` | 1,000 | 1,000 | 1 | 1 | 1 | 1 | 1,000 | 1 | 1 | 1 |
| `slow-origin-herd` | 100 | 100 | 1 | 1 | 1 | 1 | 100 | 100 | 1 | 1 |
| `leader-abort` | 50 | 51 | 1 | 1 | 1 | 1 | 51 | 2 | 1 | 1 |
| `cold-keys` | 2,000 | 2,000 | 1,000 | 1,000 | 1,000 | 1,000 | 2,000 | 1,000 | 1,000 | 1,000 |
| `cold-keys-capped` | 2,000 | 128 | 1,000 | 1,000 | 128 | 1,000 | 128 | 228 (162 to 256) | 128 | 128 |
| `sync-expiry` | 10,000 | 10,000 | 5,000 | 5,000 | 5,000 (4,250 to 5,000) | 4,364 (3,017 to 5,000) | 5,000 | 5,000 | 5,000 | 5,000 |
| `sync-expiry-capped` | 10,000 | 1,159 (1,051 to 1,319) | 5,000 | 5,000 | 1,888 (1,872 to 2,150) | 5,000 | 1,273 | 1,078 | 1,557 (1,317 to 1,730) | 1,299 (1,134 to 1,337) |
| `uncacheable-herd` | 500 | 500 | 500 | 500 | 500 | 500 | 500 | 500 | 501 | 501 |
| `turned-private` | 300 | 300 | 300 | 201 | 201 | 201 | 300 | 5 (4 to 6) | 303 | 303 |
| `error-herd` | 600 | 600 | 3 | 3 | 600 | 600 | 600 | 600 | 3 | 3 |
| `oversize-herd` | 150 | 150 | 3 | 3 | 1 | 1 | 50 | 1 | 1 | 1 |
| `large-body-herd` | 20 | 20 | 1 | 1 | 1 | 1 | 20 | 1 | 1 | 1 |

The origin's delay and body per scenario are in a table of the [comparison specification](11-comparison-spec.md) (11 §5.1). The two `-capped` scenarios use an origin that refuses with 503 beyond 128 concurrent requests; a refused request is not counted as an origin call, so "128" for a subject without a limiter means the whole burst arrived and 128 were served. `sync-expiry` sends two waves of 5,000, two seconds apart, and reads the counters two seconds after the second, so background refreshes that a cache started during the first wave are counted. Weir's tuned tier now refreshes every expired URL, as Varnish's and nginx's tuned tiers do, at most 48 at a time: it had made 144 calls (128 to 172) when the first wave ended and 5,000 by the last read. In round 2 it made 272, because refreshes beyond those 48 were dropped and most URLs were still served stale when the counters were read. Varnish's tuned tier made between 3,017 and 5,000 calls across the three repetitions.

`oversize-herd` sends three waves of 50 clients a second apart for a 2 MiB response. Weir does not store it (the limit is 1 MiB by default), but since v1.2.0 the clients of one wave share one download, so the origin sees one call per wave instead of one per client. `large-body-herd` is one wave of 20 for a 50 MiB response: one call.

### Comparison: clients that did not get a 2xx

| Scenario | clients | no cache | Weir v1.2.0 | Weir tuned | Varnish | Varnish tuned | nginx | nginx tuned | cache-handler (Souin) | cache-handler (Souin) tuned |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `cold-herd` | 1,000 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `slow-origin-herd` | 100 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 100 | 100 |
| `leader-abort` | 50 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 50 | 50 |
| `cold-keys` | 2,000 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `cold-keys-capped` | 2,000 | 1,872 | 0 | 0 | 1,744 | 0 | 1,872 | 1,644 | 1,744 | 1,744 |
| `sync-expiry` | 10,000 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `sync-expiry-capped` | 10,000 | 8,841 | 0 | 0 | 2,334 (2,010 to 2,545) | 0 | 8,066 | 0 | 7,607 | 8,060 |
| `uncacheable-herd` | 500 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `turned-private` | 300 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `error-herd` | 600 | 600 | 600 | 600 | 600 | 600 | 600 | 600 | 600 | 600 |
| `oversize-herd` | 150 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `large-body-herd` | 20 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |

A 503 that a cache sends to protect the origin counts as a failure here (11 R5), and so does a 2xx whose body is not the length the origin sends. Notes:

- cache-handler answers 200 with an empty body to clients that waited on another client's fetch when that fetch ended in a response it does not store ([darkweak/souin#876](https://github.com/darkweak/souin/issues/876), reported from round 1, open with no reply on 2026-10-23). Counted by body, every client of `error-herd` fails (3 see the 500, 597 get the empty 200), and 1,744 of 2,000 fail in `cold-keys-capped`, where the origin served 128 fetches and each had one waiter that got a body.
- `slow-origin-herd`: the origin takes 15 s. cache-handler gave up at its 10 s timeout, and nginx's tuned tier sent all 100 clients to the origin because its cache lock lets waiters through after 5 s. Both are documented settings left at their defaults (`timeout.backend`, `proxy_cache_lock_timeout`); a deployment with an origin this slow would raise them, and the cells would then look like Varnish's.
- `leader-abort`: cache-handler answered 499 to the 50 followers of a client that disconnected.
- Varnish's tuned tier has its backend capped with Weir's default limiter numbers (rule R2: 64 concurrent, 8192 queued, 5 s wait), so in `cold-keys` and the capped scenarios the Weir and Varnish tuned columns compare two configurations of one idea.
- cache-handler's tuned `stale` setting showed no effect in `sync-expiry`, `origin-down` or `brownout`. By hand, in round 2, an origin that sends `stale-if-error=300` or `stale-while-revalidate=300` made no difference either: a 503 from the origin went to the client with the stored copy still in the cache. We do not know what the setting needs, so those cells describe our configuration.

### Comparison: failure and recovery scenarios

| Figure | no cache | Weir v1.2.0 | Weir tuned | Varnish | Varnish tuned | nginx | nginx tuned | cache-handler (Souin) | cache-handler (Souin) tuned |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `bad-route`: bystanders failed, of 200 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `origin-down`: clients failed while down, of 600 | 600 | 600 | 0 | 400 | 0 | 600 | 0 | 600 | 600 |
| `origin-down`: origin calls while down | 600 | 400 | 200 (200 to 387) | 600 | 464 | 600 | 599 | 600 | 600 |
| `origin-down`: 200s of 200, 1 s after recovery | 200 | 0 | 200 | 200 | 200 | 200 | 200 | 200 | 200 |
| `origin-down`: 200s of 200, 10 s after recovery | 200 | 200 | 200 | 200 | 200 | 200 | 200 | 200 | 200 |
| `post-flood`: stored entries lost, of 2,000 | 2,000 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `post-flood`: `POST` requests answered 2xx, of 300,000 | 300,000 | 300,000 | 300,000 | 300,000 | 300,000 | 300,000 | 300,000 | 300,000 | 300,000 |

`bad-route`: one path answers 504 to 60 requests with distinct queries, then 200 clients ask for healthy paths. `origin-down`: 200 keys with `max-age=2` are stored, the origin answers 503 for three waves of 200, then recovers. `post-flood`: 2,000 stored entries, then 300,000 `POST` requests to other paths, then the 2,000 keys are read again. The last row shows that no subject refused the flood, so a zero in the row above it means entries survived a flood that happened.

Weir's default still serves nobody a second after the origin recovers, as in round 2; ten seconds later every client is served.

### Comparison: flood, brownout, purge and restart

| Figure | no cache | Weir v1.2.0 | Weir tuned | Varnish | Varnish tuned | nginx | nginx tuned | cache-handler (Souin) | cache-handler (Souin) tuned |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `busting-flood`: bystanders failed, of 1,000 | 593 | 0 | 0 | 553 (514 to 583) | 300 | 580 | 568 | 572 | 579 |
| `busting-flood`: flood clients failed, of 10,000 | 5,851 | 8,260 | 8,264 | 5,607 | 2,901 | 5,829 | 5,658 | 5,662 | 5,734 |
| `busting-flood`: all clients failed, of 11,000 | 6,436 | 8,260 | 8,264 | 6,160 | 3,183 | 6,409 | 6,226 | 6,234 | 6,300 |
| `busting-flood`: origin calls, of 11,000 requests | 4,564 | 2,740 | 2,736 | 4,840 | 7,817 | 4,591 | 4,774 (4,406 to 4,907) | 4,766 | 4,700 |
| `busting-flood`: most origin requests at once (it serves 128) | 128 | 56 (46 to 58) | 50 (49 to 63) | 128 | 65 | 128 | 128 | 128 | 128 |
| `brownout`: clients failed during the bursts, of 4,000 | 4,000 | 3,658 | 0 | 3,393 | 0 | 3,760 | 0 | 3,680 | 3,740 |
| `brownout`: clients failed outside the bursts, of 8,000 | 0 | 182 (182 to 342) | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `brownout`: origin calls during the bursts | 4,000 | 728 | 524 (495 to 631) | 3,793 | 3,843 | 3,760 | 3,760 | 3,680 | 3,740 |
| `brownout`: seconds from recovery to the last failure | 0 | 1.5 (1 to 1.5) | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| `purge-herd`: clients failed, of 2,000 | not available | 0 | 0 | 0 | 0 | not available | not available | 0 | 0 |
| `purge-herd`: origin calls | not available | 1,000 | 960 | 1,000 | 1,000 | not available | not available | 1,000 | 1,000 |
| `purge-herd`: most origin requests at once | not available | 64 | 48 | 1,000 | 64 | not available | not available | 1,000 | 1,000 |
| `purge-herd`: answers from before the purge, first wave of 1,000 | not available | 0 | 1,000 | 0 | 1,000 | not available | not available | 0 | 0 |
| `purge-herd`: answers from before the purge, second wave of 1,000 | not available | 0 | 797 | 0 | 730 | not available | not available | 0 | 0 |
| `purge-herd`: URLs still not refreshed at the last read, of 1,000 | not available | 0 | 88 | 0 | 0 | not available | not available | 0 | 0 |
| `restart`: origin calls on replay, of 2,000 (entries lost) | 2,000 | 2,000 | 2,000 | 2,000 | 2,000 | 0 | 0 | 2,000 | 2,000 |
| `restart`: clients failed, of 2,000 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |

What each one does, and what its numbers do not say:

- `busting-flood`: the origin serves 128 requests at once and refuses the rest. For 5 s, each 100 ms brings 200 requests for one path with a query string nobody has used, and 20 requests for other paths nobody has asked for. The flood alone is more than the origin can serve. Weir holds the flooded path to its share of its 64 origin slots, so every bystander is served, and it refuses 83% of the flood to do it: **by clients failed in total, Weir is still the worst subject in this scenario** and Varnish's tuned tier, which queues everything behind one cap of 64, the best. Weir left origin capacity unused: the origin had at most 56 requests in hand, of the 128 it serves. The origin-calls row counts the 1,000 bystanders and the flood requests the origin served. Each of these caches can sort or strip query strings in configuration, and none of the tiers does, Weir's included: the scenario measures how the origin's capacity is shared, not key normalization. `weirproxy` differs from the Caddy row by 22 origin calls.
- `brownout`: 200 URLs with a 2 s lifetime, 200 requests a second for 60 s; the origin answers 503 for 10 s twice, with 5 s between, and is then healthy for 30 s. With stale serving on, Weir, Varnish and nginx fail nobody. Weir's breaker cuts origin calls during the bursts to about a seventh of Varnish's and nginx's. Without stale serving it still refuses for a moment after the origin is back: 182 requests in the median repetition and 342 in the worst, the last of them 1.5 s after recovery. `weirproxy`: 182 (62 to 263) and 1 s (0.8 to 1.1).
- `purge-herd`: 1,000 stored URLs are read, purged, read again at once and 1 s later, and read a last time 3 s after that. Each subject is purged the way its configuration documents: Weir through Caddy's admin API (a soft purge of everything), Varnish default with `return (purge)` per URL, Varnish tuned with `purge.soft()` per URL, cache-handler with its flush API. Open source nginx has no purge (`proxy_cache_purge` is commercial), and the control has no cache: "not available". A purge that deletes turns the next wave into a cold start, 1,000 origin requests at once for Varnish's default and cache-handler; Weir's default refetches the same 1,000 through its 64 slots. A soft purge with stale serving keeps answering from the old copy while it refetches. Weir's tuned tier now refreshes every purged URL, 48 at a time: 960 refetches had started by the last read, and 88 URLs still had their pre-purge content 4 s after the purge. Varnish's tuned tier had refetched all 1,000.
- `restart`: 2,000 stored URLs, a graceful stop and start of the container, and the URLs read again. nginx keeps its cache on disk and makes no origin call. Weir's tuned tier has `snapshot_dir` set and reloads its entries, but as stale ones (D15): each is checked with the origin on first use, and this origin sends no validator, so every key is fetched again. v1.2.0 added `snapshot_keep_fresh`, which keeps the entries' freshness and makes the restart cost no origin call; it is off by default (a purge sent while the process was down is then not applied to what the snapshot brings back), and the tuned tier does not set it. Varnish's persistent storage is marked deprecated in its own documentation and is not used. cache-handler has disk storages (Badger, NutsDB); neither tier here uses one.

### Comparison: speed and memory counts

From `speed.sh` (11 §5.5), first repetition. Counts only: the pillar's latency, CPU and saturation figures are not published, because every timing repetition failed the drift check in section 6 of the comparison specification (the control it is measured against moved too much). cache-handler has no rows: its default storage has no size setting we could verify, so it cannot be held to the store sizes below.

Memory per stored object: the growth of the container's anonymous plus shared memory across a fill of N new entries with 100-byte bodies, in a store large enough that nothing is evicted, divided by N.

| Subject | 100,000 entries | 1,000,000 entries |
|---|---:|---:|
| Weir v1.2.0 (Caddy) | 3,692 B | 3,651 B |
| Weir v1.2.0 (`weirproxy`) | 3,382 B | 3,250 B |
| Varnish | 1,080 B | 1,038 B |
| nginx (bodies are files on disk, not counted) | 140 B | 132 B |

Weir uses the most memory per small object of the three: more than three times Varnish's. Part of the Go figures is heap not yet collected, since nothing forces a collection before the reading, so it is an upper bound on what the entries need, but it is what the container held. nginx's figure is its key zone only: the bodies are in files, and their page cache is not counted.

Hit ratio in a 160 MiB store: 200,000 keys with 3,000-byte bodies, about 300,000 requests at 5,000 a second (vegeta sent 299,994 to 300,000 per subject). `zipf` asks with a Zipf popularity (exponent 1.1); `scan` is the same with one key in five never asked for before. Hit ratio is one minus origin calls over requests.

| Workload | distinct keys asked for | Weir v1.2.0 (Caddy) | Weir v1.2.0 (`weirproxy`) | Varnish | nginx |
|---|---:|---:|---:|---:|---:|
| `zipf`: origin calls | 42,024 | 42,023 | 42,024 | 42,024 | 42,036 |
| `zipf`: hit ratio | | 0.860 | 0.860 | 0.860 | 0.860 |
| `scan`: origin calls | 96,258 | 100,071 | 99,962 | 98,486 | 99,194 |
| `scan`: hit ratio | | 0.666 | 0.667 | 0.672 | 0.669 |

The `zipf` row does not separate the caches: the 42,024 distinct keys it asks for take about 126 MB, which fits the 160 MiB store, so each subject made about one origin call per distinct key (42,023 to 42,036; a run that sent a few requests fewer saw a key or so fewer) and evicted nothing it needed again. `scan` asks for 96,258 distinct keys, about 290 MB, so the stores evict. Weir refetched 3,813 keys it had evicted, nginx 2,936 and Varnish 2,228: Weir's S3-FIFO admission did not beat the others on this scan, and its hit ratio is the lowest of the three, 0.006 below Varnish and 0.003 below nginx. Weir charges 48 bytes per header name against the store, so its 160 MiB holds somewhat fewer of these objects than Varnish's (11 §5.5).

### Comparison: what the numbers say about Weir

Where Weir is ahead of or level with the best competitor:

- One origin call for a herd on one key, including a 15 s origin and a leader that disconnects (Varnish matches it).
- An error is fetched once per wave: 3 origin calls for 600 clients in `error-herd`, with every client told the truth.
- No client fails in any herd or expiry scenario, in either tier, except where the origin itself answers 500, and the origin never sees more than 64 requests at once. With an origin that refuses beyond 128 concurrent requests (`cold-keys-capped`, `sync-expiry-capped`), Weir's default serves everyone where the Varnish, nginx and cache-handler defaults fail between 1,744 and 8,066 clients in at least one of the two.
- With stale serving on, an origin outage or a brownout is invisible to clients, as it is for Varnish's and nginx's tuned tiers, and costs the origin fewer calls: 200 against 464 and 599 in `origin-down`, 524 against 3,843 and 3,760 in `brownout`.
- Traffic to other paths survives a flood of one path: no bystander fails in `busting-flood`, where every other subject fails between 300 and 593 of 1,000.
- A purge never reaches the origin as a herd: at most 64 requests at once, where a purge that deletes sends 1,000.
- A response over the size limit is fetched once for every group of clients waiting on it: 1 origin call for 20 clients of a 50 MiB response, as Varnish and cache-handler.

Where Weir is behind:

- In `busting-flood` Weir fails more clients in total than any other subject, the no-cache control included, because it refuses the flooded path's clients while the origin still has room. A path with many distinct query strings that is busy for honest reasons is treated the same way. v1.2.0 lets the path borrow idle slots, which moved this little (8,520 to 8,260 failed): the 1,000 bystanders leave few idle slots to borrow. An adaptive origin limit (`Limiter.AdaptiveMax`) moves it more and is off by default.
- After a soft purge with stale serving on, some URLs still serve the purged content seconds later (88 of 1,000 after 4 s). Varnish's soft purge had refreshed all of them.
- Without stale serving, the breaker still fails clients for a moment after the origin has recovered from repeated bursts: up to 1.5 s on Caddy and 1.1 s on `weirproxy` in these runs. No other subject fails a client after recovery.
- A restart costs one origin call per stored URL with the default snapshot, where nginx's disk cache costs none. The opt-in `snapshot_keep_fresh` removes that cost and was not measured here.
- Responses over 1 MiB are not stored by default, so `oversize-herd` reaches the origin once per wave of clients (3 calls for 150) where Varnish and cache-handler fetch once. The limit is tied to the memory store's shard size; `Storable.MaxObjectBytes` (`max_object_bytes` in Caddy) raises it.
- Stale serving is off by default (D6), so the default tier fails every client while the origin is down, as nginx's and cache-handler's defaults do; Varnish's default serves the first wave from grace.
- A cold start costs latency instead of origin load: 2,000 clients on 1,000 cold keys are all served, but through 64 slots, so the last ones wait about 3 s (1,000 fetches of 200 ms each) where a cache with no limit answers in one origin round trip.
- Weir drops a stored response once the origin marks it uncacheable, so `turned-private` with stale serving costs 201 origin calls. Varnish behaves the same; nginx keeps serving the stale copy with 5.
- Once the origin has marked a URL uncacheable, a wave of clients for it still waits about two origin round trips in Weir, where Varnish's wait one (`turned-private`, third wave; no timing is published, so this is stated without a figure).
- Turning stale serving on costs conformance: the tuned tier passes 246 of the 365 cache-tests where the default passes 284 (below).
- A small object costs Weir more memory than Varnish or nginx: about 3.7 KB per 100-byte entry behind Caddy, against about 1.1 KB for Varnish.
- When a scan pushes entries out of a full store, Weir's hit ratio is the lowest of the three sized caches (0.666, against 0.672 for Varnish).

### Comparison: what the round changed in Weir

Round 2 measured `v1.1.0` and found four losses: the flood, the soft purge, the breaker after a brownout, and the restart. `v1.2.0` changed the defaults behind three of them and added options for two (CHANGELOG, v1.2.0). The figures below are the Caddy subject, in round 2 and in round 3: the ones that moved most. Smaller moves (for example `busting-flood` in the tuned tier, 8,528 to 8,264 clients failed, and `brownout` failures during the bursts, 3,646 to 3,658) are in the tables above, and round 2's tables are in the history of this file. The harness logic and the competitor configurations did not change between the rounds.

| Figure | v1.1.0 | v1.2.0 |
|---|---:|---:|
| `sync-expiry`: origin calls in the tuned tier, of 10,000 clients | 272 (242 to 281) | 5,000 |
| `sync-expiry-capped`: origin calls in the tuned tier, of 10,000 clients | 273 (232 to 288) | 5,000 |
| `purge-herd`: origin calls in the tuned tier | 144 | 960 |
| `purge-herd`: answers from before the purge in the tuned tier, second wave of 1,000 | 952 | 797 |
| `purge-herd`: URLs not refreshed at the last read in the tuned tier, of 1,000 | 904 | 88 |
| `origin-down`: origin calls while down in the tuned tier | 144 (144 to 159) | 200 (200 to 387) |
| `brownout`: clients failed outside the bursts in the default tier, of 8,000 | 120 (100 to 1,980) | 182 (182 to 342) |
| `brownout`: seconds from recovery to the last failure in the default tier | 0.2 (0 to 9.6) | 1.5 (1 to 1.5) |
| `busting-flood`: all clients failed in the default tier, of 11,000 | 8,520 | 8,260 |
| `busting-flood`: origin calls in the default tier | 2,480 | 2,740 |
| `busting-flood`: most origin requests at once in the default tier | 50 (41 to 53) | 56 (46 to 58) |
| `oversize-herd`: origin calls in both tiers, of 150 clients | 150 | 3 |
| `large-body-herd`: origin calls in both tiers, of 20 clients | 20 | 1 |

What changed, and why:

- A refresh that stale-while-revalidate owes now waits for a slot instead of being dropped. That is the whole move in `sync-expiry`, `sync-expiry-capped` and `purge-herd`: every expired or purged URL is refreshed, at most 48 at a time, so the origin is called once per URL and the purged content is gone sooner.
- The same change costs the origin more calls in `origin-down`, and this figure got worse: 200 calls while the origin is down where `v1.1.0` made 144. We did not observe the mechanism in the logs. Our reading, consistent with the CHANGELOG: wave one finds 200 expired keys and now queues a refresh for each, where `v1.1.0` started 48 and dropped the rest, so a dead origin sees one call per stored key. No client saw the difference (all 600 were served stale in both rounds). The first repetition made 387. Its breaker went from open to half-open after 2.0 s, as in the second repetition, which made 200 (the third took 1.0 s), so the breaker's timing does not explain the extra 187 calls, and we did not find what does.
- The breaker probes about once a second and no longer doubles its open period after each failed probe, and once a probe succeeds it admits traffic in doubling steps. In `brownout` without stale serving this trades a lucky case for a bounded one. The median repetition got worse (182 requests failed after recovery, against 120, and the last failure 1.5 s after recovery, against 0.2 s), because the breaker now always waits for its next probe to learn of the recovery (the log shows probes 0.9 to 1.7 s apart), and once a probe succeeds it admits traffic in steps rather than all at once; both put the last failure about a second after recovery. The worst repetition got much better: 342 failed against 1,980, and 1.5 s against 9.6 s. On `weirproxy` round 2 measured 2,280 (200 to 3,980) and 11.3 s (0.6 to 14.8); round 3 measured 182 (62 to 263) and 1 s (0.8 to 1.1).
- Any client's fetch may now borrow origin slots the other paths leave idle, past the per-path cap. In `busting-flood` the flooded path got more of the origin (2,740 calls against 2,480, up to 56 at once against 50) and 260 fewer clients failed. It is still the worst column.
- Clients waiting for the same response over the size limit share one download. `oversize-herd` went from one origin call per client to one per wave, and `large-body-herd` from 20 to 1.
- The restart did not change, because the new `snapshot_keep_fresh` is off by default and the tuned tier does not set it.

Round 1's before-and-after table (`v1.0.1` and the hardening branch that became `v1.1.0`) is in the history of this file, in the round 2 section.

### Comparison: security probes

The harness origin has deliberately unsafe routes: it reflects a header no cache keys, answers a `GET` according to its body, marks a personal response cacheable, or routes on the path exactly as it was spelled. Each probe sends an attacker's request and then a victim's. "unsafe" means the victim was served what the attacker caused. An unsafe verdict describes what a cache does when the origin is wrong; it is not a vulnerability in that cache, and each of these projects documents how to key or strip the input. The hardened column is that documentation applied: `hash_data()` in `vcl_hash` for Varnish, `proxy_set_header` with an empty value plus `proxy_no_cache` and `proxy_cache_bypass` for nginx, `key.headers` and `default_cache_control` for cache-handler. Every line of those files cites the page it comes from (`bench/compare/subjects/*/hardened/`). Weir has one column because its default hides request headers, cookies and bodies it does not key from the origin. One input escapes that in this setup, and no probe tests it: Caddy's `reverse_proxy` runs after Weir and adds `X-Forwarded-For` with the client's address. Weir logs a warning about it at every start and the Caddy guide says to strip it. An origin that varied a cacheable page on that header would be unsafe behind this Weir configuration too, so the Weir column, like the hardened ones, is as good as the list of inputs we thought to probe.

| Probe | Weir v1.2.0 | Varnish | Varnish hardened | nginx | nginx hardened | cache-handler (Souin) | cache-handler (Souin) hardened |
|---|---|---|---|---|---|---|---|
| origin reflects `X-Forwarded-Host` (b) | safe | unsafe | safe | unsafe | safe | safe | safe |
| origin routes on `X-Original-URL` | safe | unsafe | safe | unsafe | safe | unsafe | safe |
| `GET` with a body the origin reads | safe | safe | safe | unsafe | safe | unsafe | unsafe |
| `X-HTTP-Method-Override` causes an error | safe | safe | safe | unsafe | safe | unsafe | safe |
| oversized header causes an error | safe | safe | safe | safe | safe | safe | safe |
| page personalized by `Cookie`, marked cacheable | safe | safe | safe | unsafe | safe | unsafe | safe |
| page personalized by `Authorization`, marked cacheable | safe | safe | safe | unsafe | safe | safe | safe |
| `Set-Cookie` on a cacheable response | safe | safe | safe | safe | safe | unsafe | unsafe |
| account page with no caching headers at a `.css` path | safe | safe | safe | safe | safe | unsafe | safe |
| partial response stored as a full one | safe | safe | safe | safe | safe | safe | safe |
| `HEAD` response served to a `GET` | safe | safe | safe | safe | safe | safe | safe |
| parameter cloaking: a second `q` after `&` or `;` | safe | safe | safe | safe | safe | safe | safe |
| path spelled differently (`//`, `/./`, `/x/../`, `%70`) (e) | safe | safe | safe | safe | safe | unsafe | unsafe |
| meta character in a header causes a cacheable error (f) | safe | safe | safe | unsafe | unsafe | safe | safe |
| origin error served as a success, 60 concurrent clients (c) | safe | safe | safe | safe | safe | unsafe | unsafe |
| wrong variant served after 20,000 values of a varied header | safe | safe | safe | safe | safe | safe | safe |
| origin calls from 50 requests with `Cache-Control: no-cache` (d) | 0 | 0 | 0 | 0 | 0 | 50 | 50 |

The tuned tier of every subject gave the same verdicts as its default. Both controls (the origin direct, and Caddy with no cache) were safe on every probe and sent all 50 `no-cache` requests to the origin, which is what a probe must report when nothing is cached. Every verdict is the same as in round 2.

- (b) For the subjects that run inside Caddy (Weir and cache-handler), Caddy's `reverse_proxy` overwrites `X-Forwarded-Host` before the origin sees it, so "safe" is Caddy's doing. `weirproxy`, with no Caddy, is safe because Weir strips the header.
- (c) Three waves of 20 clients at once, for a URL whose origin only answers 500. cache-handler gave 57 of the 60 a 200 with an empty body (issue 876 above). Round 1 sent these one after another and called every subject safe.
- (d) Not a verdict. Honoring a request's `no-cache` is what RFC 9111 §5.2.1.4 describes, and cache-handler does it. The other three ignore it in these configurations (Weir by default), which protects the origin from a client that asks for revalidation on every request.
- (e) cache-handler filed a request for `/%70age` under the key of `/page` and forwarded it as spelled, so the origin's answer to the odd spelling was served for the plain one. Reproduced by hand in round 2: its `Cache-Status` header names a key ending in `/page` on the request for `/%70age`. The key is built from the decoded path. We found no documented setting for this, so the hardened tier does not change it.
- (f) nginx forwarded a header value containing the byte `0xFF` and stored the origin's `400` with `max-age=60` under the URL alone. Reproduced by hand in round 2. The header (`X-Meta`) is not one of the three the hardened tier strips, which is the point of the last limit above: a list covers the headers someone thought of. Adding it to the same `proxy_set_header` lines would close it. A value with a control byte (`0x07`) was stored by no subject; by hand, nginx forwarded it and the origin's own server refused it with no caching headers.
- The account page with no caching headers is unsafe in cache-handler's default and tuned tiers because they leave `default_cache_control` unset. The Souin README does more than document that option: it says setting it is "highly recommended" to avoid caching responses without `Cache-Control`. The hardened tier sets it.
- `GET` with a body, and `Set-Cookie`, stay unsafe in cache-handler's hardened tier: we found no documented key or strip option that covers them (`key.disable_body` removes the body from the key; nothing adds it for a `GET`).
- nginx's hardened tier stops caching any request that carries a cookie, credentials or a body. That is the manual's own example made general, and it costs hit ratio on a site whose visitors all carry a cookie.

The last probe also counted what one client can make a cache hold for one URL: after 20,000 values of a header the origin varies on, the first and last 100 were asked for again.

| | Weir v1.2.0 | Varnish | nginx | cache-handler (Souin) | cache-handler (Souin) tuned | no cache |
|---|---:|---:|---:|---:|---:|---:|
| of 200 sampled variants, still served from the cache | header not forwarded | 200 | 199 | 100 | 100 | 0 |
| container memory before and after, MiB | 15 to 36 | 92 to 135 | 172 to 230 | 16 to 158 | 14 to 1,752 | 16 to 31 |

Weir does not forward a header it does not key, so the origin saw no value, sent one response, and the URL has one entry; a deployment that keys the header gets at most 8 variants per URL (`Key.MaxVariants`). The other three stored what they were sent, inside the storage size their configuration gives them (Varnish 512 MiB, nginx 1 GiB on disk). The memory row is read from `docker stats` once before and once after, includes whatever the 20,000 requests cost a proxy with no cache at all (last column), and is approximate. cache-handler's tuned tier (Otter storage) is the outlier, and the cause is not `Vary`: by hand, in round 2, 10,000 distinct URLs with no varying header grew the same container by 1.1 GiB. We have not looked into why, and it may be our configuration. Memory per object is measured properly in the speed and memory counts above, for every subject but cache-handler.

### Comparison: conformance

[http-tests/cache-tests](https://github.com/http-tests/cache-tests) at the revision `scripts/cache-tests.sh` pins, 365 tests, each subject in each tier. Weir forwards the suite's own request headers (`forward allow`), as its baseline run does. The suite mixes required and optional behavior. A test that did not pass is split by what the suite reports: "assertion" means the behavior under test was observed and was not what the test expects; "setup" means the test could not get that far (a request that should have been conditional was not, for example) and says nothing about the behavior. Neither is always a violation: Weir's 81 are explained in [cache-tests-expected-failures.md](cache-tests-expected-failures.md), and the other projects' are not analyzed here.

| Subject | Tier | Pass | Assertion failed | Setup failed |
|---|---|---:|---:|---:|
| Weir v1.2.0 (Caddy and `weirproxy`) | default | 284 | 67 | 14 |
| Weir v1.2.0 (Caddy) | tuned | 246 | 106 | 13 |
| cache-handler (Souin) | default | 232 | 99 | 34 |
| cache-handler (Souin) | tuned | 232 | 99 | 34 |
| cache-handler (Souin) | hardened | 210 | 121 | 34 |
| Varnish | default | 216 | 127 | 22 |
| Varnish | tuned | 216 | 127 | 22 |
| Varnish | hardened | 216 | 127 | 22 |
| nginx | default | 195 | 136 | 34 |
| nginx | tuned | 193 | 156 | 16 |
| nginx | hardened | 192 | 139 | 34 |

Weir's default row equals `testdata/cache-tests-baseline.json` test for test, on Caddy and on `weirproxy`, and its tuned row equals round 2's test for test. The tuned tier passes 38 fewer: `while_revalidate 60s` applies to every response, so the suite's second request is answered from the stale copy while the refresh runs, and tests that expect the refreshed response fail (of the 73 tests whose outcome differs between the two tiers, 33 are in the suite's freshness group and 16 in its 304 group). That is the cost of choosing stale serving for responses whose origin did not ask for it. cache-handler passed 231 in round 2's default tier and 232 here, and its hardened tier 209 and 210; one test differs between runs.
