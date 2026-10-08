# Weir benchmarks

Updated: 2026-10-08

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
| `BenchmarkServeMissCoalesced` | `weir` | a cold key requested by 8 goroutines at once; the origin sleeps 100 µs so followers can join; reports `origin-calls/op` |
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

`ServeMissCoalesced` includes spawning 8 goroutines per operation, so it is a relative number. An earlier version slept 100 µs in the origin and measured the Linux timer floor (about 1 ms) instead of Weir. Origin calls per operation is slightly above 1 because a request can miss the store just before the leader stores, then join after the flight has been removed and start a second flight (lookup in `serve.go`, `Join` in `flight.go`). The cost is a second origin fetch for about 0.3% of cold keys under this burst, never a wrong response. FR-COA-1 speaks of concurrent requests sharing a flight and does not close this window; it is recorded here and not fixed (STATUS notes).

### NFR-5

`BenchmarkServeHitSmall` is 2.7 µs/op and 14 allocs/op here, inside the budget of 4 µs and 16 allocs, but this box is not the reference machine and the M1 baseline on the M4 Pro is 0.85 µs, so the absolute budget has 4.7x slack there and cannot gate anything. The 20% regression rule is the gate that works, and the +18% above sits at its edge on this box. NFR-5 is left as written ("provisional"); finalizing it needs the reference-machine rerun and Ashwin's approval of any wording change. Pending that rerun, treat the M1 to M10 growth as a finding to explain, not a pass.

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

Result: at 1M entries GC takes 13.6% (reported over 12 cycles) to 15.4% (projected from the cost per cycle) of busy CPU under saturated hits, above the 10% line in PLAN M10.5b. A pointer-light layout card is therefore added (M16-01 in `docs/cards/11-phase1x.md`). D36 does not change by this PR: the card starts with the decision.

The true share is higher than these figures. The non-GC CPU per request here is 8.7 to 10.6 µs, against 2.7 µs for a request in `BenchmarkServeHitSmall`; the rest is the harness (goroutine scheduling on 4 cores, random keys, `Request` construction), and a smaller denominator means a larger share. The figures are a lower bound for a server doing nothing but hits and a rough measure for one that also does other work. They also depend on entry size: about 2.1 KiB of live heap per entry here; smaller bodies carry more pointers per live byte. The GC CPU per cycle includes idle-priority workers on otherwise idle cores, which overstates the paced cost somewhat; the runtime-reported figure has no such correction and is 13.6%. Latency: the p99 with a forced cycle every 5 s is 57 µs against 53 µs for the small heap, so the cost shows up as CPU, not as a visible tail at this rate. Re-run on the reference machine and replace this table.
