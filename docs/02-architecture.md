# Weir architecture

Status: v1.0
Date: 2026-09-27
Depends on: [01-technical-spec.md](01-technical-spec.md)

This document fixes the structure: what the parts are, which way dependencies point, who owns which state, and why each major choice was made. Request-level behavior is in [03-hld.md](03-hld.md). Exact types are in [04-lld.md](04-lld.md).

## 1. System context

```
            client
              |
              v
   +----------------------+        HTTP server, TLS, routing, HTTP/1-2-3.
   |  host server         |        Caddy in Phase 2, net/http via weirhttp
   |  (adapter)           |        in Phase 1. Converts wire requests to
   +----------+-----------+        weir.Request and weir.Response back.
              |  Serve(ctx, req, origin)
              v
   +----------------------+
   |  weir.Engine         |  policy: key, freshness, coalescing, limiter,
   |                      |  breaker, stale, negative, purge, observability
   +----+------------+----+
        |            |
        | Store      | Origin.Fetch (called by the engine, never the adapter)
        v            v
   +---------+   +--------------------------+
   | store.  |   | origin (adapter-supplied)|
   | Store   |   | reverse proxy, RoundTripper, Caddy next handler
   +---------+   +--------------------------+
```

The adapter owns bytes on the wire. The engine owns every caching decision. The store owns bytes at rest. The origin owns generating responses. Nothing crosses these lines: the engine never writes to a socket, the adapter never decides whether to cache, the store never evaluates freshness.

## 2. Principles

These are rules. Code review rejects changes that break them.

P1. The key is a security boundary (seed §7.3). Code that reads a request attribute into the key, or rewrites a forwarded request, lives only in `internal/keys` and is reviewed against the checklist in [06-threat-model.md §6](06-threat-model.md).

P2. Forwarded request equals keyed request. Every request input the origin can see for a cacheable request is either part of the key or was explicitly allowed by the operator. Normalization rewrites the request; it never only rewrites the key. (Kettle, "Web Cache Entanglement", 2020: "avoid rewriting cache keys, rewrite requests instead".)

P3. Every origin fetch is budgeted. There is exactly one function that calls `Origin.Fetch`, and it holds a limiter slot and consults the breaker. No path, including errors, storage failure, background refresh and warm-up, reaches the origin any other way.

P4. Stored data is immutable. A `store.Entry` is never modified after it is handed to `Store.Set`. Freshening creates a new entry that shares the body slice. This removes a whole class of races between readers and the refresh path.

P5. Bounded everything. Every map, queue and table states its bound in [01-technical-spec.md NFR-3](01-technical-spec.md). Attacker-controlled cardinality (paths, query strings, header values, group names) never maps to unbounded memory.

P6. Degrade toward stale, then toward fast errors. Never toward unbudgeted origin traffic, and never toward requests waiting without a deadline.

P7. Standard library first. The root module imports nothing outside the standard library. Anything that needs a dependency is its own module.

P8. Tests control time. Code uses `time.Now`, `time.NewTimer` and channels directly; tests run it inside `testing/synctest` bubbles. Blocking that tests must reason about uses channels or `sync.Cond`, never a bare `sync.Mutex` held across a wait, because only the former are durably blocking inside a bubble.

## 3. Modules and packages

### 3.1 Root module `github.com/AshwinSathian/weir` (Phase 0 and 1)

```
weir/
  go.mod                    go 1.27, no require block
  doc.go                    package overview
  engine.go                 Engine, New, Serve, Close
  serve.go                  the lookup/serve state machine
  fetch.go                  the single origin-fetch function (P3)
  purge.go                  Purge, invalidation
  warm.go                   Warm
  config.go                 Config and sub-configs, defaults, validation
  request.go                Request, Response, CacheInfo, FwdReason, StaleReason
  errors.go                 sentinel errors, RequestError, OriginError, StatusCode, RetryAfter
  observer.go               Observer, Event, EventKind
  store/
    store.go                Store, Entry, Key, Epoch, ErrNotFound, ErrUnavailable
    codec.go                Entry binary encoding (used by storetest now, Valkey later)
    memory/                 sharded byte-weighted S3-FIFO Store
    storetest/              conformance suite: storetest.Run(t, newStore)
  weirhttp/                 net/http middleware and RoundTripper origin
  internal/
    httpcc/                 Cache-Control, Expires, Age, Date parsing; lifetime; age
    keys/                   validation, normalizers, query/cookie rewrite, key encoding
    sfv/                    RFC 9651 List-of-Strings parser (Cache-Groups)
    coalesce/               flight table
    limiter/                global + per-partition + priority limiter
    breaker/                rolling-window circuit breaker
    missrate/               Space-Saving top-K per window
    testorigin/             programmable fake origin for tests
  examples/weirproxy/       minimal reverse proxy (weirhttp + TransportOrigin); target for cache-tests
  loadtest/                 build tag `load`: real-time adversarial scenarios
  scripts/trace.sh          requirement-ID to test traceability report
```

Dependency rules, enforced by a test that inspects `go list -deps` output:

- `store` imports only the standard library. It does not import `weir`.
- `store/memory` imports `store`. The root `weir` package imports `store` and `store/memory` (for the default store). There is no import cycle because `store` knows nothing about the engine.
- `internal/*` packages import only the standard library and `store` where needed. They never import `weir`, so each is testable alone.
- `weirhttp` imports `weir` and `net/http`. Nothing in the root package imports `weirhttp`.
- `net/http` is imported by the root package only for `http.Header`, `http.ParseTime` and `http.CanonicalHeaderKey`. The engine never sees `*http.Request` or `http.ResponseWriter` (seed §7.2 boundary).

### 3.2 Later modules

Each has its own `go.mod` so importers of the core do not inherit its dependencies.

| Module path | Phase | Depends on |
|---|---|---|
| `github.com/AshwinSathian/weir/store/valkey` | 2.5 | `valkey-io/valkey-go` |
| `github.com/AshwinSathian/weir/observe/prom` | 1 (M10) | `prometheus/client_golang` |
| `github.com/AshwinSathian/weir/observe/otel` | 1 (M10), optional | `go.opentelemetry.io/otel/metric` |
| `github.com/AshwinSathian/weir/caddy` | 2 | `caddyserver/caddy/v2` |

The repository uses a `go.work` file at the root for local development across modules. `go.work` is committed; CI also tests each module alone with `GOWORK=off` so a module never depends on an unpublished sibling by accident.

## 4. Components

| Component | Package | Owns | Stateful |
|---|---|---|---|
| Validator and key builder | `internal/keys` | request validation, normalizers, forwarded-request rewrite, key encoding | no |
| Freshness | `internal/httpcc` | directive parsing, lifetime, age, jitter math, stale permissions | no |
| Flight table | `internal/coalesce` | in-flight fetches keyed by coalescing key | yes, in-process only |
| Limiter | `internal/limiter` | origin slots, FIFO wait queue, partition counts | yes |
| Breaker | `internal/breaker` | rolling outcome buckets, state | yes |
| Miss-rate tracker | `internal/missrate` | Space-Saving counters for current window | yes |
| Store | `store`, `store/memory` | entries, vary specs, markers, epochs | yes |
| Engine | `weir` | wiring, the serve state machine, the single fetch function, background goroutines | yes (lifecycle only) |
| Adapter | `weirhttp`, later `caddy` | wire conversion, origin implementation | no |

Stateless components are pure functions with table and fuzz tests. Stateful components each have exactly one mutex or channel discipline, documented in [04-lld.md](04-lld.md).

## 5. Runtime model

### 5.1 Goroutines

- Request goroutines belong to the host server. They run validation, lookup, cache serving and waiting.
- One fetch goroutine per flight. It runs `Origin.Fetch`, reads the body, stores the result and wakes waiters. It exists so no requester's cancellation can cancel a fetch others depend on (FR-COA-2).
- One goroutine per background refresh (SWR or early refresh), created only after a limiter slot is acquired, so their number is bounded by `MaxConcurrent`.
- No ticker goroutines. Breaker buckets and miss-rate windows rotate lazily on the next call after a boundary, so a window's anomaly is reported when the next request arrives. When traffic stops, nothing needs reporting.
- `Warm` runs its own bounded worker pool for the duration of the call.

`Engine.Close` tracks all engine-owned goroutines in one `sync.WaitGroup`.

### 5.2 State ownership

| State | Owner | Shared with | Protection |
|---|---|---|---|
| stored entries | store | engine (read-only pointers or decoded copies) | store-internal locks; entries immutable (P4) |
| flights | flight table | request goroutines, fetch goroutine | sharded mutex for the map; each flight's result published once via a closed channel |
| limiter slots and queue | limiter | request and fetch goroutines | one mutex; each waiter has its own ready channel |
| breaker buckets | breaker | fetch goroutines | one mutex |
| miss-rate counters | tracker | request goroutines | one mutex (cheap; O(TopK) work at most) |
| epochs | store | engine | store-internal |

### 5.3 Time

Entries store `time.Time` values from `time.Now()`, which carry both a wall and a monotonic reading. Inside one process every subtraction (age, staleness, epoch comparison) therefore uses the monotonic clock, and a wall-clock step cannot extend freshness or hide a purge (FR-FRS-8). The codec keeps only the wall reading, because entries cross process boundaries in Phase 2.5, where node clocks must stay within the skew allowance of [05 §4.3](05-storage-interface-spec.md). Origin-supplied times (`Date`, `Expires`, `Last-Modified`) are compared only with each other.

## 6. Architecture decision records

Each record states the context, the decision, the alternatives and the consequences. New decisions append here with the next number.

### ADR-1 Engine owns the fetch (D1)

Context: the seed sketched `Decide`/`Complete`, where the adapter fetches and reports back. SWR needs a background fetch after the client's response is sent; coalescing needs a leader whose cancellation does not strand followers; the limiter needs slot release on every path including panics.

Decision: `Serve(ctx, req, origin)`. The adapter supplies an `Origin`; the engine calls it.

Alternatives: keep `Decide`/`Complete` (every adapter reimplements background fetch, lock release, panic safety; any adapter bug leaks leader locks or slots); export both layers (twice the API to specify and test, and the low-level one is still easy to misuse).

Consequences: adapters are small. The Caddy adapter must implement `Origin` by calling the next handler with a cloned request, including after the triggering request has finished (for SWR), which [08-caddy-adapter-spec.md §4](08-caddy-adapter-spec.md) addresses.

### ADR-2 Two-level key with vary spec

Context: `Vary` is a property of the response. The first request for a URL cannot know which headers matter.

Decision: primary key from request-only inputs; a vary-spec record under the primary key; variant entries under `SHA-256(primary || normalized vary values)`. Lookups cost two store reads when a vary spec exists (one otherwise, since the primary record is the entry itself when the response had no `Vary`).

Alternatives: one blob per primary key holding all variants (every variant write rewrites the blob and races with concurrent writers; the whole blob is evicted together); ignore `Vary` and key on a fixed header list (unsafe, violates RFC 9111 §4.1).

Consequences: the store holds four record kinds (response, vary spec, hit-for-miss marker, negative). The memory store can serve both reads under one shard lock only by coincidence, so the engine does two `Get` calls. Phase 2.5 can pipeline them.

### ADR-3 SHA-256 over a tagged, length-prefixed encoding

Context: key-injection attacks (Kettle 2020, the Akamai `__` example) come from concatenating fields with delimiters. Non-cryptographic hashes let an attacker search for collisions offline and poison a victim key.

Decision: canonical binary encoding with a version byte, field tags and uvarint lengths, hashed with `crypto/sha256`. Keys are `[32]byte`.

Alternatives: `hash/maphash` (seeded per process, so keys differ between nodes, breaking a shared store); FNV or xxHash (fast, but collisions are cheap to search); storing the full canonical string (unbounded key size).

Consequences: about 1 µs or less per key on hardware with SHA extensions, well inside the hit-path budget. Memory-store shard selection must not use raw key bytes, because an attacker can grind inputs to target one shard; it uses `maphash` with a per-process seed over the key.

### ADR-4 Strict forwarding by default (D4)

Context: every documented poisoning and poisoning-DoS technique surveyed (Kettle 2018, 2019, 2020; Doyhenard 2024) needs a request input that the origin reads and the cache does not key.

Decision: cacheable requests forward only keyed, allowed, and protocol-required headers. `ForwardAll` exists as an explicit opt-out that logs a warning.

Alternatives: forward everything and rely on `Vary` (depends on every origin sending a correct `Vary`, which is exactly what fails in practice).

Consequences: operators must list headers their origin needs (for example `Accept-Language` in `Key.Headers`). Origins that read `User-Agent` without declaring it get the default response for everyone, which is the safe outcome. Documented prominently in the README and the Caddy adapter docs.

### ADR-5 Epoch-based purge

Context: seed T6.12 wants soft purge; T6.13 wants generation bumps. Iterating tagged entries is O(n), needs a reverse index in every store, and for large tags does a lot of work at purge time.

Decision: each purge writes `epoch[tag] = (time, mode)`. Every entry carries three implicit tags (the global tag, its origin `scheme://host:port`, and its URI `scheme://host:port/path?query` after query rewriting, which deliberately excludes keyed headers and cookies so a URL purge or unsafe-method invalidation reaches every variant and every keyed-header partition of that URL) plus one tag per `Cache-Groups` member, scoped to its origin. At lookup, an entry whose producing request was sent at or before the most severe applicable epoch among its tags is soft-stale, invalid, or hard-purged depending on that epoch's mode (comparing against request time, not store time, closes the purge race of T-10).

Alternatives: reverse index plus iteration (what Varnish xkey and Souin do); key generation counters folded into the key (purges become misses, which is hard purge only).

Consequences: purge and invalidation are O(tags). Lookup does up to `MaxGroups + 3` epoch reads, lock-free atomics in the memory store and one pipelined round trip for Valkey, and usually none at all thanks to the newest-epoch fast path. Because tags are attacker-influenced (unsafe requests to distinct URIs create MUST-invalidate epochs), soft and invalid epochs live in a fixed-size max-timestamp sketch that never under-invalidates, and only operator hard purges are kept exactly ([05 §4.4](05-storage-interface-spec.md), T-29). Correctness in Phase 2.5 depends on node clocks being within the configured skew allowance ([05-storage-interface-spec.md §4.3](05-storage-interface-spec.md)).

### ADR-6 Byte-weighted S3-FIFO for the memory store (D2)

Context: seed T6.11 asks for admission awareness. A cache-busting flood inserts many one-hit wonders; pure LRU lets them evict the working set.

Decision: S3-FIFO (Yang et al., SOSP 2023) per shard, with capacities in bytes: small queue 10%, main queue 90%, ghost queue of fingerprints sized to the main queue's entry count, promotion from small to main at frequency 2 or more (the reference implementation's `move-to-main-threshold=2`), main-queue reinsertion at frequency 1 or more with decrement.

Alternatives: sharded LRU (no scan resistance); W-TinyLFU as in Caffeine and otter v2 (better hit ratio on some traces, but a count-min sketch, a window LRU and a segmented LRU per shard, which is more code to get right for a first version); depending on otter (rejected by D2 and D3).

Consequences: one-hit wonders never leave the small queue, so a busting flood can churn at most 10% of the cache. Known weakness: objects accessed exactly twice with the second access after they left the small queue are missed (acknowledged in the paper). Revisit with trace data after Phase 1.

### ADR-7 Failure-ratio breaker counting gateway failures only

Context: a consecutive-failure breaker can be opened by any client that can make the origin fail repeatedly, taking the whole site to stale-or-503.

Decision: rolling window, minimum volume, failure ratio, and only transport errors, timeouts, 502, 503 and 504 count.

Alternatives: consecutive failures (gobreaker's default `ReadyToTrip`); per-partition breakers (many small breakers each need volume to be meaningful, and an origin that is down is down for every path).

Consequences: an attacker must produce at least half of all origin traffic as gateway failures within the window. At that point the origin is in trouble regardless.

### ADR-8 Per-partition caps and a foreground reserve inside the global limiter

Context: a global bulkhead protects the origin but a single flooded path can take every slot, so legitimate misses on other paths shed.

Decision: partition cap (`MaxPerPartition`) inside the global cap; background work may not use the last `ReserveForeground` slots.

Alternatives: weighted fair queuing across partitions (more code, and partitions are attacker-controlled so weights mean little); adaptive concurrency (OQ-3).

Consequences: a flood on one path is limited to `MaxPerPartition` concurrent origin fetches. A flood across many distinct paths is not prevented by this, and is documented as T-12 and residual risk R-2 in the threat model; upstream rate limiting is the answer there.

### ADR-9 Space-Saving for miss-rate anomalies

Context: tracking miss rate per path with a map is unbounded under attack.

Decision: Space-Saving (Metwally, Agrawal, El Abbadi, 2005) with `TopK` counters per window, each counter holding requests and misses. Heavy hitters are exact enough; the summary's error bound is `N / TopK`.

Alternatives: count-min sketch (needs a separate heavy-hitter structure to name the path); a bounded LRU map of counters (evicts exactly the entries under attack when the attacker also varies paths).

Consequences: O(TopK) memory and O(TopK) worst-case update under one mutex. At `TopK = 64` this is cheap enough for the hit path.

### ADR-10 `testing/synctest` instead of a Clock interface (D9)

Context: the seed asked for a controllable clock. A `Clock` interface threads through every component and still cannot control timers created by the standard library (for example inside `context.WithTimeout`).

Decision: production code calls `time` directly. Tests run inside `synctest.Test`, where time is fake for every goroutine in the bubble. Go 1.27 adds `synctest.Sleep` and `httptest.NewTestServer`, which uses an in-memory network compatible with bubbles, so even `weirhttp` integration tests are deterministic.

Consequences: waits the tests depend on must be durably blocking (channels, `sync.Cond`, `time.Sleep`, timers), which P8 requires. Tests of the future Valkey store use real time and a real server.

### ADR-11 RFC 9211 and RFC 9875 instead of custom headers

Context: the seed mentions an optional staleness header and surrogate keys.

Decision: emit `Cache-Status` and consume `Cache-Groups` / `Cache-Group-Invalidation`.

Consequences: the Cache-Groups parser needs RFC 9651 List-of-Strings parsing, implemented in `internal/sfv` (about 100 lines, fuzzed). `Surrogate-Key` support is not planned; operators can map it in an adapter if needed.

## 7. Extension points left open for later phases

- Phase 2.5 store: the `store.Store` interface and `store/storetest` suite. The engine never type-asserts on the memory store; it only checks for small optional capability interfaces (`MaxObjectBytes() int64`, `Bytes() int64`) that any store may implement.
- Phase 2 Caddy: `Origin`, `Serve`, `Purge`, `Observer`. The adapter adds no engine API.
- Phase 3 experiment dimensions: `internal/keys` has a single place where primary-key fields are appended and where the forwarded request is rewritten. A dimension will be a function that returns a name and a value, appended to the key and forwarded as a header, so P2 holds. It is not exported in Phase 1. Decisions for Phase 3 are in [10-experiments-spec.md](10-experiments-spec.md).
- Adaptive limits (OQ-3): the limiter's cap is read through one method, so it can become dynamic without touching callers.
