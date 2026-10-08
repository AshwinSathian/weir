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

Machine: 4 vCPU Intel Xeon 2.1 GHz Linux container (not the reference machine), Go 1.27.0 linux/amd64. Both sides were run on this machine, back to back, medians of 6: M1 at the merge of PR 29 (`73e4d2e`), M10 at `b909550` plus the new benchmarks. The M1 row above was measured on an M4 Pro and cannot be compared with this box, so the comparison reruns M1 here. Raw output: [benchmarks/m1-linux4.txt](benchmarks/m1-linux4.txt), [benchmarks/m10.txt](benchmarks/m10.txt); full `benchstat`: [benchmarks/m1-vs-m10.benchstat.txt](benchmarks/m1-vs-m10.benchstat.txt). The box is shared, so run-to-run spread is 5 to 20%.

| Benchmark | M1 ns/op | M10 ns/op | change | allocs/op (M1 → M10) |
|---|---:|---:|---|---:|
| `BenchmarkServeHitSmall` | 2 262 | 2 444 | +8% (p=0.002) | 14 → 14 |
| `BenchmarkKeyBuild` | 1 975 | 2 255 | +14% (p=0.002) | 9 → 9 |
| `BenchmarkAcceptEncoding` | 311 | 330 | no significant change | 0 → 0 |
| `BenchmarkMemoryStoreGetParallel` | 59.5 | 58.7 | no significant change | 0 → 0 |

Milestones 2 to 9 were not recorded; the M1 to M10 difference is the only series. Bytes and allocations per operation are unchanged on every row.

Benchmarks added after M1 (M10 only, same box):

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkServeHitVary` | 3 036 | 1 496 | 15 |
| `BenchmarkServeMissCoalesced` (8 callers, 100 µs origin) | 988 000 | 23 183 | 215 (1.00 origin calls/op) |
| `BenchmarkServeHitParallel/missrate-on` (4 procs) | 2 634 | 1 510 | 14 |
| `BenchmarkServeHitParallel/missrate-off` (4 procs) | 1 124 | 1 492 | 14 |
| `BenchmarkLimiterAcquireRelease/uncontended` | 166 | 24 | 1 |
| `BenchmarkLimiterAcquireRelease/full_queue` | 11 960 | 24 | 1 |
| `BenchmarkObserveFlood` (`internal/missrate`) | 245 | 0 | 0 |

### NFR-5

`BenchmarkServeHitSmall` is 2.4 µs/op and 14 allocs/op on a slower machine than the reference, inside the budget of 4 µs and 16 allocs. NFR-5 keeps its numbers; only the word "provisional" is gone. Re-run on the reference machine and replace this section before relying on the headroom. The +8% and +14% since M1 are below the 20% review threshold. The cause of the `KeyBuild` growth was not investigated; allocations did not change.

### Parallel hits and the miss-rate tracker

`Tracker.Observe` takes one mutex per cacheable request, hits included. With 4 procs a hit costs 2.63 µs with the tracker and 1.12 µs without, so the mutex adds about 1.5 µs and 2.3 times the time per hit under contention (the M8-02 run on an M4 Pro with 12 procs gave 600 against 343 ns). Allocations are identical. Decision (Ashwin, 2026-10-08): record only. NFR-5 gets no parallel budget and the mutex stays. The two options for later are `TryLock` (367 ns on the M4 Pro, but it drops half the samples at 12 procs and so doubles the effective `MinMisses`) and per-shard counters; neither has a card.

## M10 GC cost at 1M entries (2026-10-08)

D36 keeps the store on the Go heap until GC cost is measured. `TestGCAt1MEntries` (`make load`, `loadtest/gc_test.go`) fills a memory store with 10 000 and then 1 000 000 entries of 1 KiB, serves hits from 64 goroutines at 20 000 requests/s for 30 s and reports the result. Same box as above (4 vCPU Xeon, Go 1.27.0), one run, `GOGC` default. Raw output: [benchmarks/m10-gc.txt](benchmarks/m10-gc.txt).

The runtime updates its CPU class metrics only when a GC cycle ends, and at this rate a 2.5 GiB heap would take minutes to trigger one. So the test forces a cycle every 5 s under load, measures GC CPU per cycle, and projects the natural frequency (a cycle per live-heap bytes allocated) from the measured allocation rate. The forced cycles also put their effect into the p99, so the p99 below is worse than a natural one.

| | 10 000 entries | 1 000 000 entries |
|---|---:|---:|
| heap in use after fill | 31 MiB | 2 605 MiB |
| GC CPU per cycle | 13.2 ms | 2 444 ms |
| allocation rate | 31.8 MiB/s | 29.0 MiB/s |
| busy CPU | 0.48 cores | 0.52 cores |
| projected GC share of busy CPU | 3.5% | 6.6% |
| hit p50 | 6.1 µs | 8.2 µs |
| hit p99 (with a forced cycle every 5 s) | 53 µs | 57 µs |
| slowest request | 6.9 ms | 6.5 ms |

Result: 6.6% of busy CPU is below the 10% line in the card, so no pointer-light layout card is added to `docs/cards/11-phase1x.md` and D36 stands. The share is roughly independent of the request rate (allocation and busy CPU both scale with it). It is not independent of entry size: 1 KiB bodies carry about 2.6 KiB of heap per entry here (key, header map, struct, body), and smaller bodies mean more pointers per live byte, so a store of 1M tiny entries would cost more. The projection is an upper-bound style estimate: GC CPU includes idle-priority mark workers on otherwise idle cores, while the divisor is non-idle CPU, and the small column also leaves its natural cycles in the busy figure. The share is noisy: a second run at a tenth of the window length gave 2.9% for the large heap (busy CPU 0.88 cores, 21 MiB/s allocated), the first 6.6%; both are under 10%. The 10 000-entry column is the same load on a small heap, for scale. Re-run on the reference machine and replace this table.
