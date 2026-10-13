# Claims register

Not published. Every measured figure and every statement about another project that appears in hand-written copy (site content, the home page, README) is listed here with its source. `scripts/check.mjs` fails when a figure in the copy is missing from this file, or when a figure whose source is a repository file is not in that file.

Figures in pages rendered straight from repository documents (the benchmark tables, the threat table, the configuration table) are not listed: they cannot differ from their source.

## Measured figures

Machine for every `docs/benchmarks.md` row unless noted: Apple M4 Pro, 12 cores, Go 1.27.1, reference run of 2026-10-11.

| Figure | Source | Checked | Where it comes from |
|---|---|---|---|
| `852 ns` | `docs/benchmarks.md` | 2026-10-12 | `BenchmarkServeHitSmall` after the rc.1 hardening, Go 1.27.2, machine not idle |
| `852.2 ns` | `docs/benchmarks.md` | 2026-10-12 | same run, benchstat value |
| `12 allocations` | `docs/benchmarks.md` | 2026-10-12 | allocs/op of the same benchmark |
| `669 ns` | `docs/01-technical-spec.md` | 2026-10-13 | NFR-5: `BenchmarkServeHitSmall` on the v1.1 branch, Go 1.27.1, machine not idle |
| `8 allocations` | `docs/01-technical-spec.md` | 2026-10-13 | NFR-5: allocs/op of the same run |
| `964 ns` | `docs/01-technical-spec.md` | 2026-10-13 | NFR-5: `v1.0.1` in the same interleaved run |
| `225 ns` | `docs/benchmarks.md` | 2026-10-13 | `BenchmarkServeHitParallel/missrate-on`, v1.1 branch, rounded from 224.5 |
| `1,005 ns` | `docs/benchmarks.md` | 2026-10-13 | the same benchmark on `v1.0.1`, rounded from 1 004.5 |
| `1,005` | `docs/benchmarks.md` | 2026-10-13 | the same figure as the README writes it |
| `54,155` | `docs/benchmarks.md` | 2026-10-13 | origin brownout, stale responses, v1.1 branch |
| `16 allocations` | `docs/01-technical-spec.md` | 2026-10-12 | NFR-5 budget |
| `1.25 µs` | `docs/01-technical-spec.md` | 2026-10-12 | NFR-5 ceiling |
| `10,000` | `docs/benchmarks.md` | 2026-10-12 | synchronized-expiry scenario, keys stored |
| `1,485` | `docs/benchmarks.md` | 2026-10-12 | synchronized expiry, busiest second of origin calls |
| `52,975` | `docs/benchmarks.md` | 2026-10-12 | origin brownout, stale responses |
| `99,976` | `docs/benchmarks.md` | 2026-10-12 | busting flood, flood requests |
| `36.9 µs` | `docs/benchmarks.md` | 2026-10-12 | busting flood, normal p99 alone |
| `41.0 µs` | `docs/benchmarks.md` | 2026-10-12 | busting flood, normal p99 under flood |
| `1.08 µs` | `docs/benchmarks.md` | 2026-10-12 | GC per request at 1M entries, reference machine |
| `1.76 µs` | `docs/benchmarks.md` | 2026-10-12 | GC per request at 1M entries, 4-vCPU Linux container |
| `0.014 µs` | `docs/benchmarks.md` | 2026-10-12 | GC per request, pointer-light prototype (M16-01) |
| `2 µs` | `docs/benchmarks.md` | 2026-10-12 | the D36 gate |

## Defaults quoted in copy

| Figure | Source | Checked | Where it comes from |
|---|---|---|---|
| `8,192` | `docs/01-technical-spec.md` | 2026-10-13 | `Limiter.MaxQueue` |
| `50 ms` | `docs/01-technical-spec.md` | 2026-10-12 | `Timeouts.Store` |

## Illustrations, not measurements

| Figure | Source | Checked | Where it comes from |
|---|---|---|---|
| `1,000` | none | 2026-10-12 | the made-up herd in the coalescing page's opening scenario |

## Counts on the home page

| Claim | Source | Checked |
|---|---|---|
| 284 of 365 cache-tests pass, 81 explained | `docs/cache-tests-expected-failures.md` | 2026-10-12 |
| threat, risk and invariant counts | counted from `docs/06-threat-model.md` at build time (`lib/tables.ts`); the README's numbers are checked by `scripts/check.mjs counts` | 2026-10-12 |
| 30 fuzz targets, 60 minutes each; failure-mode tests 100 times under `-race` | `docs/release.md` gate log, run 38369566716 | 2026-10-13 |
| 0 third-party imports in the root module | `deps_test.go` | 2026-10-12 |
| transcript on the home page and in the README | captured from `examples/weirproxy` at `v1.0.0`, Go 1.27.1, with `scripts/transcript.sh` | 2026-10-12 |

## Statements about other projects (compare page)

Each was read in the project's own documentation on 2026-10-12. Versions are the default branch or current page on that date; none of the pages showed a release number.

| Project | Statement | Source |
|---|---|---|
| Varnish | coalesces requests for one object automatically; has grace and keep | https://github.com/varnishcache/varnish-cache/blob/master/doc/sphinx/users-guide/vcl-grace.rst |
| nginx | `proxy_cache_lock`, its timeout and age (5 s defaults), `proxy_cache_use_stale updating`, `proxy_cache_background_update`, default `proxy_cache_key`, `proxy_cache_purge` commercial; page documents no lifetime randomization, breaker or global miss limit | https://nginx.org/en/docs/http/ngx_http_proxy_module.html |
| Souin | self-description, RFC 7234, Cache-Status, RFC 9213, surrogate keys, ESI, request coalescing, storage list, integrations; README does not mention jitter, a breaker, an origin limit or key security | https://github.com/darkweak/souin |
| Caddy cache-handler | based on Souin; cached body size "unlimited if omited"; API default paths | https://github.com/caddyserver/cache-handler |
| Pingora | Rust framework; caching integration "should be considered experimental" | https://github.com/cloudflare/pingora |
| singleflight | package overview; documentation silent on timeouts, panics and stored results (page showed x/sync v0.24.0) | https://pkg.go.dev/golang.org/x/sync/singleflight |
| sturdyc | self-description; stampede protection, early refreshes, batching | https://github.com/viccon/sturdyc |
| cache-tests | the suite's results "should not be used evaluate or compare feature support" (the notice at the top of the results page, read 2026-10-13; quoted in the conformance chart's caption) | https://cache-tests.fyi/ |
| Fastly | definition of request collapsing; serial queue on uncacheable responses; hit-for-pass | https://www.fastly.com/documentation/guides/concepts/edge-state/cache/request-collapsing/ |

## Measured statements about other projects (benchmarks and compare pages)

Every figure and verdict about Varnish, nginx and the Caddy cache-handler on those pages is rendered from `docs/benchmarks.md`, section "Comparison with other caches, round 1 (2026-10-13)", measured with `bench/compare/` on 2026-10-13: Varnish 9.1.0, nginx 1.30.5, Caddy 2.11.7 with cache-handler v0.17.0. The charts and the headline tiles on those pages are drawn from the same section by `lib/results.ts`, and `scripts/check.mjs charts` compares every drawn number with its table cell after a build. The hand-written copy around them states one: Varnish failing 400 of 600 clients in `origin-down` before our configuration mistake was fixed, from the first limit listed in that section. No maintainer of those projects has reviewed the configurations (docs/11 R4), and each page that shows the figures says they are our runs, not verified by the projects' owners.
