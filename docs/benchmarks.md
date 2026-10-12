# Weir benchmarks

Updated: 2026-10-12

Benchmark results per milestone, so each milestone review can compare against the previous one with `benchstat` ([07 §10](07-testing-strategy.md)). NFR-5 budgets apply from M10; until then these numbers are a baseline, not a gate.

Reproduce a row with:

```sh
go test -run '^$' -bench . -count 6 ./ ./internal/keys/ ./store/memory/ ./internal/limiter/ ./internal/missrate/
```

Store the raw output next to the table when a milestone adds a row, so `benchstat old.txt new.txt` works without rerunning the old commit.

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

Every scenario also met the goroutine check (baseline ±10 within 5 s after load), before and after `Close`. Steady-hit p99 sits one histogram bucket (12.5%) under the 200 µs bound on this box, and moves with the achieved rate (an earlier lighter run measured 74 µs); compare runs only at a similar rate. The 4-core run is a floor: re-run `make load` on the reference machine and replace this table.

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

The D36 gate (at most 2 µs of GC per request at 1M entries) holds with 1.08 µs, against 1.76 µs on the 4-core Xeon. The share of busy CPU is 6.2% to 6.8%, under the 10% line that PLAN M10.5b set. The pointer-light layout (M16-02) stays deferred.

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

Two back-to-back runs on the M4 Pro, machine otherwise idle, three builds interleaved by `benchstat` (`-count 10` each; raw: [benchmarks/hit-path-opt-m4pro.txt](benchmarks/hit-path-opt-m4pro.txt)). An earlier run with several Claude Code sessions and a Chrome video stream open had a ±38% spread and is discarded.

| Build | Run 1 ns/op | Run 2 ns/op | vs M1 (run 1 / run 2) | B/op | allocs/op |
|---|---:|---:|---|---:|---:|
| M1 `73e4d2e` | 870.5 ± 1% | 854.1 ± 2% | | 1 480 | 14 |
| M10 `b909550` | 1 087 ± 3% | 1 070.5 ± 1% | +24.9% / +25.3% | 1 480 | 14 |
| optimized branch | 833.5 ± 3% | 821.4 ± 4% | **-4.3% / -3.8%** (p<0.01) | 1 536 | 12 |

The optimized hit path is about 4% faster than M1 and 23% faster than M10 on the M4 Pro, with two fewer allocations. The +32% growth since M1 that the first reference run exposed is gone. Bytes per op are 56 above M1 (1 536 against 1 480), not the 160 above M1 of the release candidate, and the allocation count is lower.

The gain is smaller on the M4 Pro than on the Xeon (-30% there against M10-to-branch -23% here, relative to the release candidate), as expected: SHA-256 is cheaper with the ARM instructions, so the two tags no longer hashed per hit saved less.

**New baseline.** The 20% regression rule is measured from this run, a median of about 0.83 µs. NFR-5 sets the ns/op ceiling at 1.5 times that, 1.25 µs (docs/01), replacing the 1.9 µs derived from the first, noisier reference run. The first reference run above stays as the record of the release candidate.

## Hardening after v1.0.0-rc.1 (2026-10-12)

The changes that followed the `v1.0.0-rc.1` rehearsal (stable `QuerySort` by name, the `Cache-Control` and `Pragma` filter on the forward, `Range` ignored beside `If-Match`, per-field overhead in `Entry.Size`, the Valkey record limit, the wasm build) measured on the reference machine with Go 1.27.2. The machine was not idle (an editor and one agent session), so read the load figures as pass or fail and the benchstat lines as the comparison. Raw output: [benchmarks/hardening-2026-10-12.txt](benchmarks/hardening-2026-10-12.txt).

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

