# Weir high-level design

Status: v1.0
Date: 2026-09-27
Depends on: [01-technical-spec.md](01-technical-spec.md), [02-architecture.md](02-architecture.md)

This document walks through what happens to a request, step by step, for every path the spec defines. It names components and the order they are consulted. It does not give type definitions; [04-lld.md](04-lld.md) does.

## 1. The serve pipeline

Every `Serve` call runs the same stages. A stage can finish the request early.

```
 Serve(ctx, req, origin)
   |
   1 closed? ------------------------------------------------> ErrClosed
   2 validate + classify (internal/keys)  -- invalid --------> ErrInvalidRequest
   |     method class: cacheable (GET, HEAD) | unsafe/unknown | other safe
   3 unsafe/unknown/other-safe method -----------------------> pass-through path (§2.9)
   4 bypass rule? -------------------------------------------> pass-through path, fwd=bypass
   5 build forwarded request + primary key + URI tag
   6 store lookup (primary) -> entry | vary spec | marker | negative | miss
   |     vary spec -> compute variant key -> store lookup (variant)
   7 epoch check on the found entry (global, origin, URI, groups)
   8 decide:
   |     fresh ........................ serve, maybe early refresh (§2.1)
   |     stale within SWR ............. serve stale + background refresh (§2.3)
   |     negative (no servable stale) . synthesized error (§2.8)
   |     marker ....................... uncoalesced fetch (§2.6)
   |     range request, not fresh ..... pass-through with Range, no store
   |     miss / needs validation ...... coalesced fetch (§2.2, §2.4)
   9 record outcome: missrate tracker, observer event, Cache-Status
```

Stages 2 and 5 are the security boundary. Nothing after stage 5 reads the client's raw headers again; everything downstream sees the forwarded request.

## 2. Flows

### 2.1 Fresh hit

```
client     engine                    store
  |  Serve   |                          |
  |--------->| validate, key            |
  |          | Get(primary) ----------->|
  |          |<------------ entry ------|
  |          | epochs OK, fresh         |
  |          | client conditional? -> 304 or full
  |          | early refresh draw (§2.5)
  |<---------| body reader over stored bytes, Age, Cache-Status: Weir; hit; ttl=N
```

No origin contact, no limiter, no breaker. The store returns an immutable entry; the response body is a reader over the entry's byte slice with no copy.

### 2.2 Cold miss with coalescing

Three concurrent requests A, B, C for the same key.

```
A        B        C       flights         limiter       fetch goroutine     origin
|join(k)-------------------->| new flight F, A=leader                        |
|        |join(k)----------->| F exists, B=follower                          |
|        |        |join(k)-->| F exists, C=follower                          |
|        |        |          | spawn ---------------------------------->|   |
|        |        |          |              acquire(fg, partition) <----|   |
|        |        |          |              granted ------------------->|   |
|        |        |          |                                          |-->| Fetch
|        |        |          |                                          |<--| response
|        |        |          |                                          | read body (<= MaxObjectBytes)
|        |        |          |                                          | storable? Set(entry)
|        |        |          |              release <-------------------|
|        |        |          |<-------- publish result, close(F.done) --|
|<-------+--------+----------| all three read F.result
```

A is the leader only in the sense that it created the flight; it waits exactly like B and C. If A's client disconnects, the fetch continues for B and C (FR-COA-2). Each waiter gets its own body reader over the shared buffered bytes.

When the result is not reusable by a follower (not storable, too large, Vary mismatch), that follower goes through lookup once more and then fetches on its own if needed (FR-COA-5). When the response was too large to buffer, only the flight creator receives the streamed body; everyone else re-enters.

### 2.3 Stale-while-revalidate

```
client    engine                     flights/limiter          origin
  |Serve    |                             |                      |
  |-------->| entry stale, within SWR     |                      |
  |<--------| serve stale now             |                      |
  |         | Cache-Status: hit; ttl=-N; detail=stale-while-revalidate
  |         | flight exists for key? ---->| yes: nothing to do   |
  |         |                             | no: background flight|
  |         |                             | tryAcquire(bg) ok -->| conditional GET
  |         |                             |                      | 304 -> freshen
  |         |                             |                      | 200 -> store new
```

Background refresh never queues. If `tryAcquire(bg)` fails because the reserve is reached, the refresh is dropped and counted; the next request inside the window tries again. If the breaker is open the refresh is skipped.

### 2.4 Validation (stale outside SWR, `no-cache`, soft purge, invalidation)

This is the coalesced-fetch path with conditional headers added from the stored entry's validators. On 304 the engine creates a new entry from the old body and merged headers, applies fresh jitter, stores it, and serves it with `fwd=stale; fwd-status=304`. On a full response the storability rules apply as for a miss. On an origin-health failure the stale-if-error rules apply (§3).

Entries without validators are refetched unconditionally.

### 2.5 Early refresh (XFetch)

On a fresh hit the engine draws `U` and checks `-Δ·β·ln(U) >= remaining`. When true it starts a background refresh exactly as in §2.3, except the served response is a normal fresh hit. Because it is coalesced and background-priority, a hot key produces at most one early refresh at a time, and none when the limiter is near capacity.

Jitter (applied at store time) spreads expiry across keys written together. Early refresh smooths the moment of expiry for each hot key. They address different parts of T6.1 and both are on by default.

### 2.6 Uncacheable resources (hit-for-miss)

When a flight's response is not storable, the fetch goroutine writes a marker under the coalescing key (the variant key when a vary spec exists, otherwise the primary key) with `HitForMissTTL`. The next request for that key sees the marker and fetches directly, without creating a flight, so private or per-user responses are never serialized behind each other. Each such fetch still takes a limiter slot. If a later response is storable, it replaces the marker.

### 2.7 Stuck leader

```
t=0     A creates flight F1, fetch hangs
t=0..10 B, C join F1 and wait
t=10    D arrives; F1 age >= LeaderMaxAge -> D creates F2 (F1 is left alone)
t=10    B, C hit FollowerMaxWait:
          stale permitted? serve stale, detail=coalesce-timeout
          otherwise fetch independently through the limiter
t=12    F2 completes; D and anything that joined F2 get the result; entry stored
t=30    F1's fetch hits Timeouts.Origin and ends; its late result is stored only if it is newer
```

The worst case for one key is one new flight per `LeaderMaxAge` plus independent follower fetches, all inside the per-partition cap.

### 2.8 Origin failure, breaker, negative entries

```
          fetch fails (origin-health failure)
               |
     breaker.record(failure)
               |
     stale within SIE and permitted? --yes--> serve stale, detail=stale-if-error
               | no
     entry must-revalidate? --yes--> ErrMustRevalidate (504), or the origin's 5xx response
               | no
     no stored response for the key? --yes--> write negative entry (TTL 2 s)
               |
     return ErrOrigin / ErrOriginTimeout / origin's 502-504 response
```

When the failure ratio crosses the threshold the breaker opens. Subsequent requests that need the origin skip the fetch entirely: stale if permitted, otherwise `ErrCircuitOpen`. After `OpenFor` the next foreground fetch is a probe. The probe's outcome closes or reopens the breaker.

Negative entries cover the seconds before the breaker has enough volume: a burst of misses on one key produces one origin failure and then fast synthesized errors until the negative TTL passes.

### 2.9 Pass-through (unsafe methods, bypass, range miss, other safe methods)

```
engine: breaker.allow -> limiter.acquire(fg, partition) -> Origin.Fetch(all headers, body)
        -> breaker.record; slot released when headers arrive (the body streams)
        -> unsafe method and 2xx/3xx? write invalid epochs for the URI and same-origin
           Location/Content-Location; soft epochs for Cache-Group-Invalidation groups
        -> return origin response unchanged except Cache-Status: fwd=method|bypass|...
```

Pass-through responses are never stored and never shared. The body streams from the origin to the caller.

### 2.10 Purge

`Purge` translates its input into tags (the global tag for `All`, a URI tag per URL, a group tag per group scoped to `Purge.Origin`, see [04-lld.md §7](04-lld.md)) and writes one epoch per tag with the purge mode. It returns once the epochs are written. Nothing else happens at purge time.

The next lookup of an affected entry sees an epoch newer than the entry's request time (not its store time, so a fetch that was already in flight when the purge happened cannot store pre-purge content that survives it):

- soft: the entry is treated as stale since the purge time; SWR and SIE windows apply from that time, capped at the entry's original expiry, and the next request revalidates it through the normal coalesced, limited path;
- invalid (from unsafe methods, for the target URI and same-origin `Location`/`Content-Location`): must validate; never served stale. Group invalidations from `Cache-Group-Invalidation` are soft (FR-INV-2, T-28);
- hard: treated as absent.

Because affected entries are refreshed only when requested, one at a time per key, and through the limiter, a purge of 5 000 keys produces at most `MaxConcurrent` concurrent origin fetches, and under SWR, zero blocking requests (T6.12).

### 2.11 Storage outage

```
store.Get -> timeout or ErrUnavailable
   store breaker counts it; after 5 in a row it opens (1 s, doubling to 30 s)
   lookup treated as miss
   coalescing still works (flights are in-process)
   limiter and origin breaker still apply
   store.Set failures are counted and dropped
```

The origin sees at most `MaxConcurrent` fetches, and duplicate keys are still collapsed, so a store outage looks to the origin like a cold cache under the limiter, not an unbounded flood (T6.5).

### 2.12 Warm

`Warm` iterates the sequence and, for each request, does stages 2 to 8 in "warm" mode: skip if fresh; otherwise run a coalesced fetch at background priority that is allowed to wait for a slot but never uses the foreground reserve. `Warm.Concurrency` workers run in parallel. It returns counts. Operators call it after a restart with a list of hot URLs; a snapshot source is a later addition on top of the same call.

## 3. State machines

### 3.1 Entry lifetime

```
 stored ---> FRESH ------(age > lifetime_eff)-----> STALE
               |                                     |
               | early refresh may replace it        |-- within SWR: serve + refresh
               |                                     |-- within SIE: serve only on error
               |                                     |-- beyond both: validate or refetch
               |                                     v
               |                              RETAINED (until lifetime + max(SWR,SIE) + Keep)
               |                                     |
 soft purge ---+--> STALE (from purge time)          v
 invalidation -+--> INVALID (validate, never stale)  gone (store evicts or expires it)
 hard purge ---+--> gone
```

### 3.2 Circuit breaker

```
          failure ratio >= threshold and volume >= MinRequests
 CLOSED -----------------------------------------------------> OPEN (OpenFor ±20%)
   ^                                                              |
   | probe success                                     timer done |
   |                                                              v
   +----------------------------------------------------------- HALF-OPEN
                        probe failure: back to OPEN, OpenFor doubled (max 60 s)
```

### 3.3 Flight

```
 created -> waiting-for-slot -> fetching -> reading-body -> storing -> published (done closed)
      \              \               \
       \              +-- shed ------+--- error/timeout/panic --> published with error
        +-- aged after LeaderMaxAge: still runs to completion, no longer joinable
```

A flight is removed from the table when published. If a newer flight for the same key already replaced it (aging), removal only deletes the table entry when it still points at this flight.

## 4. Failure mode to component map

| Mode | Components that answer it |
|---|---|
| T6.1 | httpcc jitter (store time), engine early refresh |
| T6.2 / 6.2a | flight table, fetch goroutine detachment, leader aging, follower timeout |
| T6.3 | limiter global cap and queue |
| T6.4 | limiter; `Warm` |
| T6.5 | store timeouts, store breaker, in-process flights |
| T6.6 | stale rules, origin breaker |
| T6.7 | keys: strict forwarding, vary spec, VaryAuto/VaryStrict, sensitive Vary names |
| T6.8 | limiter partition cap, missrate tracker, S3-FIFO small queue |
| T6.9 | keys validation and normalizers, fuzzing |
| T6.10 | negative entries |
| T6.11 | memory store S3-FIFO |
| T6.12 | epoch soft purge + SWR + limiter |
| T6.13 | global epoch |

## 5. What each adapter must do

An adapter (weirhttp now, Caddy later) is responsible for:

1. Building `weir.Request` from the wire request: `Path` and `RawQuery` from the raw request target (not a decoded and re-encoded URL), `Host` from the Host header or `:authority`, `Scheme` from the connection.
2. Implementing `Origin` so the forwarded request is sent exactly as given (path, query and headers), so a background call after the client request has finished still works, and so `ctx` cancellation stops the call and its body reads promptly (`Close` waits for it).
3. Adding transport headers such as `Via` or `X-Forwarded-*` only in the origin implementation, and knowing that per-client values (`X-Forwarded-For`) sent to the origin on cacheable requests are unkeyed input. [06-threat-model.md](06-threat-model.md) T-4 covers this.
4. Writing `Response` to the wire, including `Cache-Status`, and closing the body.
5. Mapping `Serve` errors with `weir.StatusCode` and `weir.RetryAfter`.
6. Calling `Close` on shutdown.
