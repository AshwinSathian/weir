# Weir benchmarks

Updated: 2026-10-01

Benchmark results per milestone, so each milestone review can compare against the previous one with `benchstat` ([07 §10](07-testing-strategy.md)). NFR-5 budgets apply from M10; until then these numbers are a baseline, not a gate.

Reproduce a row with:

```sh
go test -run '^$' -bench . -count 6 ./ ./internal/keys/ ./store/memory/
```

Store the raw output next to the table when a milestone adds a row, so `benchstat old.txt new.txt` works without rerunning the old commit.

## What each benchmark measures

| Benchmark | Package | Path |
|---|---|---|
| `BenchmarkServeHitSmall` | `weir` | `Serve` on a fresh 1 KiB entry in the memory store, body read to `io.Discard`: classify, store `Get`, freshness, response build |
| `BenchmarkKeyBuild` | `internal/keys` | `Classify` of a GET with a query, four request headers and a keyed cookie: validation, normalization, forwarded request, primary key |
| `BenchmarkAcceptEncoding` | `internal/keys` | the Accept-Encoding bucket for a five-member value with qvalues |
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

Not yet measured: `BenchmarkLimiterAcquireRelease` (card M4-01's test list), `BenchmarkServeHitVary` and `BenchmarkServeMissCoalesced` (07 §10 lists them; no card owns them yet).

## M10 load tests (2026-10-07)

Machine: 4 vCPU Linux container (not the reference machine), Go 1.27.0 linux/amd64. `make load`, scale 1, one run, 294 s. Branch `claude/gifted-meitner-19l6l6` on base `44a69ae`. Scenarios and thresholds: [07 §9](07-testing-strategy.md).

| Scenario | Measured | Threshold | Result |
|---|---|---|---|
| steady hits | 6.84 M requests, 114 k/s achieved (target 173 k/s, 50% of the saturated rate; the achieved load is about a third of saturation), hit ratio 1.0000, p50 7.7 µs, p99 147 µs | ratio > 99%, p99 < 200 µs | pass |
| synchronized expiry | 10 000 keys stored in 1.9 s, `max-age=60`, origin peak in-flight 32 (`MaxConcurrent` 32), busiest second 1 475 origin calls | in-flight ≤ 32, ≤ 2 000 calls/s | pass |
| busting flood plus normal | 99 950 flood requests (73 190 shed), flood in-flight peak 8 (`MaxPerPartition` 8), normal p99 98 µs alone, 82 µs under flood | in-flight ≤ 8, p99 within 20% (50 µs floor) | pass |
| origin brownout (×20) | 16 breaker open events, ended closed, 54 298 stale responses, 58 285 of 58 285 requests 2xx during the brownout, peak 66 goroutines | breaker per config, stale served, no goroutine growth | pass |
| store flap (50%) | 4.01 M requests, 0 client errors, slowest request 67 ms (bound 208 ms), store breaker 25 open / 21 closed | bounded latency, breaker cycles | pass |

Every scenario also met the goroutine check (baseline ±10 within 5 s after load), before and after `Close`. The first run of the harness measured p99 74 µs at a lower achieved rate; p99 here moves with load, so compare runs only at a similar rate. The 4-core run is a floor: re-run `make load` on the reference machine and replace this table.
