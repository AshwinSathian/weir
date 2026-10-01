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
