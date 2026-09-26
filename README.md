# Weir

Weir is a Go library for shared HTTP caching in front of an origin that has to stay up. It implements RFC 9111 caching and adds the defenses most caches leave out: TTL jitter, request coalescing with timeouts, an origin concurrency budget with per-path fairness, stale-while-revalidate and stale-if-error, a circuit breaker, short negative caching, lazy soft purges, and a cache key built as a security boundary.

A weir is a low dam that regulates flow without stopping it. That is the job: let traffic through to the origin at a rate it can survive, whatever the cache is doing.

## Status

Design complete, implementation not started. Phase 0 (skeleton) is next. Nothing here is usable yet.

| Phase | Scope | State |
|---|---|---|
| 0 | skeleton, CI, test harness | next |
| 1 | the engine, milestone by milestone (M1 to M10) | planned |
| 1.5 | Valkey store | planned |
| 2 | Caddy module | draft spec |
| 3 | experiment-aware key dimensions | not specified |

## Why another cache

Existing caches solve RFC correctness and same-key coalescing well. Weir exists for the cases that still take origins down: many keys expiring together, purges and cold starts that turn into cross-key stampedes, storage outages that fail open, and attackers who generate guaranteed misses or poison entries through inputs the cache does not key. The full argument, with prior art, is in [docs/00-design-doc.md](docs/00-design-doc.md).

## Two defaults worth knowing before you use it

- Strict forwarding. For cacheable requests, the origin receives only headers that are part of the cache key, explicitly allowed, or required by HTTP (`Authorization`, `Cache-Control`, `Pragma`, a normalized `Accept-Encoding`). If your origin reads `Accept-Language` or a cookie to build a page, list it in the key config. This is what makes unkeyed-input cache poisoning structurally impossible.
- Weir serves stale content only when the origin allows it with `stale-while-revalidate` or `stale-if-error`, unless you configure defaults.

## Design documents

Start with [docs/README.md](docs/README.md). The build plan is [PLAN-weir.md](PLAN-weir.md). Agent instructions are in [CLAUDE.md](CLAUDE.md).

## Requirements

Go 1.27 or later. The core module has no dependencies outside the standard library.

## License

MIT. See [LICENSE](LICENSE).
