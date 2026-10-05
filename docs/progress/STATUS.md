# Status

Updated: 2026-10-05
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: card/M7-02-vary-policies
PR: none
Next card: M7-03

## Blockers

none

## Waiting on Ashwin

- M7-02 (not blocking): should the overflowing request carry a Cache-Status detail? FR-KEY-10 says "counted as `vary-overflow`"; today only `EvVaryOverflow` counts it and the response shows `fwd=vary-miss` without `stored`. A detail is public surface, so it was not added.

## Decided 2026-10-02

Every item that was waiting on Ashwin, decided under his delegation after an adversarial review (PR #44):

- FR-NEG-4 exclusions (M6-01): approved as written. Each one keeps a client from writing a negative entry everyone else then gets (T-17, T-31); background and warm fetches never have a waiting client to protect. The cost, more origin calls from excluded requests during an outage, is bounded by the limiter and breaker.
- FR-NEG-3: names `ttl` (seconds until the negative entry expires), as the code and 04 §6.6 already emit.
- FR-MODE-3 and FR-BYP-1: `only-if-cached` gets `ErrOnlyIfCached` in bypass mode and under bypass rules; the stored entry is not served, because both exist for when the cache must not answer.
- FR-MODE-2 reach: no default change. Keeping every entry 24 h past expiry for an incident mode that is rarely on would hold dead entries in remote stores and keep validator-less responses only for this. FR-MODE-2 now says the mode widens what may be served, not what is kept, and how to buy more reach (`Freshness.Keep`, `DefaultStaleIfError`). Pinned by `TestModeStaleOnErrorReachIsRetention`.
- FR-MODE-2 names `s-maxage` (RFC 9111 §5.2.2.10); an explicit `stale-if-error` keeps its own window.
- `Timeouts.Background` is wired: it bounds background refresh and Warm fetches, `Timeouts.Origin` foreground ones (FR-TMO-1, FR-COA-2). Removing the field was rejected: it is the knob that lets foreground fetches fail fast while refreshes wait for a slow origin. Pinned by `TestTimeoutByClass`.
- FR-STL-1 and FR-FRS-6 state the refresh gate for `Authorization` and `no-store` requests (T-8, T-31).
- P0.0: hold the public flip. D24 amended: public with `v0.1.0` once M7-05 (key-boundary security review) merges, since #44 found key-boundary poisoning paths and M7-02..05 are still open. `SECURITY.md` is added now. The M7-05 card tells the session after it to confirm the irreversible flip in chat and run it.

- FR-KEY-11 (delegated by Ashwin after the #44 review): Vary values are keyed exactly as forwarded, no line combining or whitespace removal. A generic normalizer cannot know a field's syntax (commas in quoted strings), and an origin reading one line answers `a` + `b` lines differently from `a,b`, so equating them served one client's answer to another. RFC 9111 §4.1 always permits not matching; the cost is a split variant. Rejected: forwarding the normalized form (rewrites values origins depend on, and Vary is unknown at forward time); documenting the gap under T-31 (leaves a poisoning primitive). 01 FR-KEY-11, 04 §3.3, 06 T-15 and 07 updated.

## Decided 2026-10-01

- Followers of a 5xx flight (#41 adversarial review): Ashwin approved amending FR-COA-5. A 500/502/503/504 flight response is an error condition, so every waiter gets it through §7.2 instead of refetching.
- Limiter queue exhaustion (#34 review): Ashwin approved capping queued waiters per partition, shedding past it with `queue-full`. The #35 adversarial review measured `MaxPerPartition` (16) as too low for a legitimate one-path cold start (32 of 2 000 served vs 656 uncapped); Ashwin chose max(`MaxPerPartition`, `MaxQueue`/4). FR-LIM-3 and T-11 say so.

## Decided 2026-09-28 (delegated by Ashwin after the #24 adversarial review)

Already built, now confirmed: the weirhttp default transport (compression off, no proxy), the coalesce-default clamp to min(10s, `Timeouts.Origin`), and the storetest `Run(t, newStore, opts...)` API with `Synctest()`. The other nine decisions are cards, and each card changes its spec text together with its code:

- M1-17c (merged), the key boundary: h2c is served normally; `#` is rejected in path and query; the cookie limit counts keyed pairs only.
- M1-17d (merged), storability: `s-maxage` must be valid to permit an `Authorization` response; markers are suppressed after unkeyed input; remote conformance tests move behind an `integration` tag.
- M1-17e (merged), responses: the engine strips hop-by-hop fields; a 304 keeps `Content-Encoding` and `Content-Type`; Pragma is ignored when `Cache-Control` is present.

The cards' Notes give the reasons and the options rejected. All three come before M1-18, because closing M1 makes the repo public.

## Notes for the next session

- M7-01: `lookup` sets `lk.ck` (variant key under a spec, else Primary); flights, early refresh, warm, markers and negative entries all key on it. `storeResponse` writes the variant, then the spec (`setVariant`, serve.go); `setUnlessResponse` also keeps a live spec. Followers share only when `keys.VariantKey(...) == fr.vk`, else `reenter` (one more pass, then `fetchDirect`).
- M7-02: `setVariant` (serve.go) reads each kept ref on a spec write and drops those the store answers `ErrNotFound` for (04 §14); any other store error keeps the ref, so a failing store cannot lift the cap. `recordingStore` (conditional_test.go) now has `down` (one key whose Get fails) and `firstVariant`; the expired-ref subtest runs on `lazyStore` so the ref's `Expires` check is pinned apart from the read. The card listed storable.go and fetch.go, but M7-01 put the policy checks in storable.go and the cap in serve.go, so only serve.go changed. The reclaim reads count as accesses in the memory store's S3-FIFO, so variants of a busy URI look a little hotter than they are.
- M7-02: the memory store keeps nothing past `MaxRetention` (24 h), while a ref's `Expires` follows the entry; a variant with a longer lifetime is a "record gone" ref after 24 h and is reclaimed by the read. Tests that sleep past 24 h hit this.
- M7-02 review nit left open: the reclaim reads run on every spec write, up to `MaxVariants` - 1 store round trips per variant store on a remote store. Reading only when the spec is at the cap gives the same cap behavior with fewer reads but departs from 04 §14's wording; decide when the Valkey store lands (Phase 2.5).
- M7-02: overflow is counted by `EvVaryOverflow` only. FR-KEY-10 says "counted as `vary-overflow`" and names no Cache-Status detail, so none was added; the overflowing request shows `fwd=vary-miss` without `stored`. Ask Ashwin if a detail is wanted (public surface).
- M7-01: a request forwarding a `Forward.Allow` field stays `Unkeyed` even when the response's Vary keys that field, so it gets no marker or negative entry. M7-03 may refine `Unkeyed` against the spec's names. The Accept-Encoding bucket is still keyed only through Vary (04 §3.3); the marker note below holds for the first response of a URI, before a spec exists.
- M7-01 review nits open: `VaryNames` allocates for every name before the `vary-too-many` check (origin-controlled, bounded by response header limits); the LLD's `lk.spec`/`fetchSpec.spec` are not in the code (`setVariant` rereads the primary key).

- M6-01: negative writes happen in `setNegative` (negative.go), called by `fetchStored` before the flight publishes; `lookup` returns a live negative record as `lk.neg` and `cacheable` serves it before the only-if-cached and Range checks. Markers and negative entries share `setUnlessResponse` (serve.go), which replaces only a hard-purged or expired response.
- M6-01: tests whose next request must reach the origin after a 502/503/504 or transport failure set `Negative.Disable` (coalesce, stale, serve, weirhttp handler-origin tests). New engine tests with failing origins need the same.
- M6-01 adversarial review: `FuzzRetryAfter` (seeds in testdata/fuzz) covers the origin `Retry-After` parser; the served value counts down with the entry's age. Mutants of the `sp.lk.entry != nil` and `ctx.Err() == nil` guards survive because `setUnlessResponse` and `timeoutOrOrigin` already enforce them; they stay as cheap early exits.
- M7: the `Accept-Encoding` bucket is forwarded but not in `PrimaryKey`, so an origin that fails only for one coding writes a negative entry every coding sees (2 s, same bound as the marker note below). Keying the bucket in M7-01 closes it.

- M5-04: `staleOK` (mode.go) is the single stale-on-error test for `onFetchError` and `staleOnTimeout`; The mode widens only entries still stored, so it helps entries with a validator (kept `Freshness.Keep`) or an SIE/SWR window.
- M5-04: bypass uses `keys.Classified.AsBypass()`, which re-runs the `ClassPass` builder on the original request. M7-03 bypass rules (FR-BYP-1) can call the same method.
- M5-04: mode expiry is noticed lazily by the next `Serve`; a racing `SetMode` and expiry can emit `EvMode` events out of order (rare, cosmetic).

- M5-03: `fetch` calls `e.cb.Allow` before the limiter; shed and caller-gone end in `Cancel`. Outcomes are recorded at the headers, or after the body for buffered fetches (a truncated body is a `Failure`). Only Foreground fetches probe; Background and Warm get `ErrCircuitOpen` while not Closed, and Background also emits `EvRefreshDropped{circuit-open}`.
- M5-03: `ErrCircuitOpen`'s Retry-After is `breaker.Remaining()` floored at 1 s (half-open has no end time). `Remaining` is a new internal method in 04 §8.3; the earlier note asked to check a new accessor with Ashwin, so the PR flags it for confirmation.
- M5-03: `onFetchError` (flight.go) applies 01 §7.2 for direct fetches and every flight waiter, each with its own lookup. Followers of a buffered 5xx get a copy built from `flightResult.errHeader` and `fr.ci.FwdStatus`; they must never read `fr.resp`, which the creator's caller owns.
- M5-03: a flight cut short by `Close` (`ErrClosed`) serves a stale-if-error entry with reason `sie`; harmless, label could be its own.

- M5-02: `cacheable` serves a `StaleSWR` entry before the only-if-cached and Range checks (it answers both, full 200 for Range) and calls `backgroundRefresh` unless the request is `Authorization` or `no-store`. A honored client `no-cache` turns `StaleSWR` into `NeedsValidation`.
- M5-02: an SWR refresh whose response is unstorable (origin switched to `no-store`, or `no-freshness`) does not replace the stale entry (storeResponse keeps a found response), so the stale body is served until the SWR window ends, with one refresh flight at a time. RFC 5861 allows it; M6 negative caching or a marker policy may want to revisit.
- M5-02: `runRFCRow` now calls `synctest.Wait()` before counting origin calls, so a row's `calls` includes the background refresh its step started.

- M5-01: `New` accepts `Breaker.MaxOpenFor < OpenFor` (a reopen is then shorter than the first open). Reject it in `New` when wiring (config rule: ask Ashwin).
- `EngineStats.BreakerState` can read `breaker.State()`; the State constants share weir.BreakerState's order.

- M4-05: `Warm` acquires its `Warm` slot before joining a flight (`fetchSpec.permit`, the new last `fetch` argument `held`), so a queued warm fetch never holds a flight. Every warm fetch runs `runFlight` on its own engine goroutine; no-flight requests use `coalesce.NewFlight()` (outside the table).
- M4-05: decided 2026-10-01 (delegated by Ashwin): 01 FR-WRM-1/2 now state the not-sent and skip rules; `New` rejects `ReserveForeground >= MaxConcurrent` and `Warm.Concurrency > MaxConcurrent - ReserveForeground` (default lowered to fit). Concurrent Warm calls each take up to `Warm.Concurrency` queue places (04 §6.8a).
- M4-05: a warm fetch looks up twice (before the wait, to skip fast; after the slot, to catch live traffic), so remote stores see one more Get and NewestEpoch per fetched URL.

- M4-04: every store call goes through `e.sg` (storeguard.go). Use `e.sg.get/set/newestEpoch/setEpoch`, never `e.sg.s` directly, except `Close`. Delete and Scrub are not wrapped; whoever first calls them (M9 purges) adds a guard method.
- M4-04: any store error except `ErrNotFound` counts toward the breaker (05 S-3), unless the caller's context ended first (05 S-2 makes stores return `ErrUnavailable` then; counting it would let disconnecting clients open the breaker). After an open period all calls go through, no single half-open probe.
- M4-04: a miss makes four store calls (lookup Get, newest-wins Get, Set, purge-check NewestEpoch), so a dead remote store costs a miss up to 4 × `Timeouts.Store` until the breaker opens. 07 T6.5 now says "each store call"; Ashwin may prefer a request to stop calling the store after its first failure.
- M4-04: `EngineStats` has no store-breaker state yet; EvStoreBreaker is the only signal.

- M4-03: `fetch` takes the slot from `e.upl` (upload pool, `MaxUpload` slots, partition cap min(`MaxPerPartition`, `MaxUpload`), own `MaxQueue` queue, no reserve) when `c.HasBody`. EvShed does not name the pool; add it when observability wants to tell an upload flood from main-pool saturation.
- M4-03: the origin deadline is an `AfterFunc` timer on a `WithCancelCause` context. Streams (pass-through, oversized, event-stream) stop it at headers and wrap the body in `idleBody`: each `Read` arms `StreamIdle`, time between reads is not counted (04 §14). Total stream duration is unbounded by design (FR-TMO-2).
- Streams have no total deadline and the idle timer counts only a Read in progress, so a client that keeps reading slowly holds an origin connection (not a slot) without limit. Adapter docs (M10) should tell operators to set `http.Server.WriteTimeout` or rely on Caddy's write timeout.
- M4-03: PLAN M4.3b stays unticked: its AC lists `TestBodylessBypassUsesMainPool`, which M7-03 owns. `HasBody` trusts a declared `Content-Length: 0` even with a real body; only direct library callers can do that (net/http gives NoBody). Consider in M7-03.

- M4-02: `fetch` takes a limiter slot of the given class for `c.PartitionH` before the origin timeout starts and releases it on return (after the buffered body, or at stream headers). Shed is `*RetryError{ErrShed, MaxQueueWait}` plus `EvShed`; Background sets `bgDropped` and also emits `EvRefreshDropped` `no-slot`. A follower of a dropped background flight fetches directly, uncoalesced (bounded by the limiter).
- M4-02: each partition queues at most max(`MaxPerPartition`, `MaxQueue`/4) waiters (FR-LIM-3, T-11). Floods spread over many paths pass any per-path cap; M8's miss-rate throttle is the answer there. Release walk with 1 023 waiters is about 5 us.
- M4-02: config allows `MaxQueueWait` above `Coalesce.LeaderMaxAge` or `FollowerMaxWait`. Then a queued flight ages out and a new request starts a second flight for the key, or followers give up and fetch directly, defeating coalescing under load. Consider rejecting or clamping it in `New` (ask Ashwin: it is a config rule).
- M4-02: config allows `ReserveForeground >= MaxConcurrent`, which silently disables background refresh and (M4-05) warm; negatives are rejected. Decide in M4-05 whether `New` should reject it. `EngineStats{Inflight, Queued}` still needs a limiter accessor.
- M4-02: coalescing tests that need more than 16 concurrent fetches on one path use `wideLimiter` (coalesce_test.go); new engine tests must use `testorigin.NewChecked` with the engine's caps.
- M4-03: the upload pool is a second limiter with the same algorithm (04 §14); `fetch` picks the pool from `c.HasBody`.
- M8: the `throttled` cap overrides go into `capFor` (04 §8.2 comment).

- M3-01: early refresh lives in background.go. `tryAcquireBackground` is a stub that always returns true and runs before `flights.Join`. M4-02 must acquire inside `fetch`, after Join (04 §6.8); acquiring before Join would hold a slot for every hit that finds a flight already running. Until M4-02 nothing bounds refresh fetches below one per distinct key near expiry.
- M3-01: early refresh skips `Authorization` and request `no-store` requests (same rule as leading a flight, T-8, T-31), now in 04 §6.8 and pinned by `TestEarlyRefreshGates`. FR-FRS-6 now states it (decided 2026-10-02).
- M3-01 adversarial review: early-refresh flights already mark their creator gone and close an unclaimed stream (`TestEarlyRefreshClosesUnclaimedStream`); M5-02's AC item for this is met for early refresh, M5-02 must keep it for SWR refresh. Hits skip the `Rand` draw when more than 37·Δ·β is left (`TestEarlyRefreshShortcutIsExact`), which brought `BenchmarkServeHitSmall` back to its pre-card cost.
- M3-01: the `JitterMinLifetime` gate compares the jittered lifetime, so a `max-age=10` entry jittered below 10 s never refreshes early. A fresh hit under `only-if-cached` can start a refresh (the cache's decision, RFC 9111 allows it).
- M2-03: a follower of an unshareable flight result re-enters `cacheable` with `prevCK`; the same key fetches directly (FR-COA-5). Until M7-01 that equals `fetchDirect`. M7-01 must compare the lookup's coalescing key, cap re-entry at one attempt and add `keys.VaryMatches` (`ponytail:` in flight.go).
- M2-03: followers share storable flight responses that need validation (`no-cache`, `max-age=0`); Ashwin decided 2026-10-01, written into FR-COA-5 and pinned by `TestCoalesceSharesNoCacheResponse`.
- M4: until the limiter lands, a client disconnect no longer cancels the origin fetch (FR-COA-2), so flights per second times `Timeouts.Origin` bounds the table, not MaxConcurrent + MaxQueue. M4-02 must test the table size under a cold-start flood.
- A panic while reading a buffered origin body is recovered in `runFlight` but the body is not closed; a `defer` in `fetch` around `io.ReadAll` would be the root fix. `BenchmarkServeMissCoalesced` (07 §10) still has no card.
- `Publish` must be called exactly once per flight (a second call panics on the double close). `runFlight` is the only caller.

- M1-18: `rfc9111_test.go` is a step table (`rfcRow`/`rfcStep`); rows tagged M5, M7 or M9 skip. Cards that land those milestones untag their rows (M5: must-revalidate 504, RFC 5861 SWR/SIE; M7: Vary variant; M9: Cache-Group-Invalidation). M11/M12 cards add rows here too.
- M1-18: benchmarks live in the package they measure (`bench_test.go`, `internal/keys/bench_test.go`, `store/memory/bench_test.go`); baseline in `docs/benchmarks.md` with raw output in `docs/benchmarks/m1.txt`. `BenchmarkServeHitVary` and `BenchmarkServeMissCoalesced` (07 §10) have no owning card; add them to the M7 and M2 cards when those start. The 1 MiB Cookie benchmark and the hard-epoch prune / S3-FIFO walk measurements carried below were not done here.
- Engine-level RFC 9110 §6.6.1 gap (pre-existing, found by the #29 adversarial review): a forwarded response whose origin sent no `Date` is returned by `Serve` without one, though the stored copy gets it (FR-STO-13) and hits carry it. weirhttp is compliant on the wire because net/http's server adds `Date`; any non-net/http adapter would not be. Fixing it changes FR-STO-13 or adds a requirement, so ask Ashwin and give it a card.
- CI tests only the `go.mod` Go version (`go-version-file`), but D42 says the two latest releases. No card owns the matrix; add it to M10-04 or a small card.
- `rfc9111_test.go` mutation check (40 hand mutants of storable, conditional, serve, purge, entry, respond): all killed except the `Forwarded.Method != GET` storability guard, which the engine cannot reach (only GET/HEAD reach storability, both forwarded as GET).
- M1-18 added `TestCVE202435296` (serve_test.go), listed in M1.5's AC but owned by no card. The bucket is not in `PrimaryKey` yet (M7), so today it proves forwarding collapses to `identity`; once M7 keys the bucket it also proves no key minting.

- M1-17e: `fetch` strips hop-by-hop and `Connection`-named fields from every origin response (FR-FWD-7), but keeps the received header in `fetchResult.recv` (only when `Connection` is present) and every decision reads it through `fetchResult.received()` (storability, buildEntry's `Age`/`Date`, isEventStream, invalidate), so a named `Cache-Control`/`Vary`/`Set-Cookie`/`Age`/`Location` keeps its effect (T-8). M2 moving the store into the flight must carry `recv` along. `freshened` now takes a header, not a `*Response`.
- M1-17e: `ParseRequest` ignores `Pragma` whenever any `Cache-Control` line exists, including an empty one (deliberately conservative; fewer client-forced validations).
- M1-17d: `keys.Classified.Unkeyed` suppresses hit-for-miss markers only. T-31 also covers negative entries; M6 (FR-NEG-4) should decide whether `Unkeyed` suppresses them too.
- M1-17d: the `Unkeyed` check in `forwardHeader` does not consider `Key.Headers` (Classify does not key them yet). Whoever wires `Key.Headers` into `PrimaryKey` should exclude those names, or `New` should reject Allow entries naming keyed fields (carried item). It fails safe: only markers are lost.
- M7 (variant keying): the `Accept-Encoding` bucket reaches the origin but is not in `PrimaryKey` (keyed only through Vary, M7-01). Until then a client choosing its bucket can plant a marker when the origin's storability differs by coding (for example `Vary: Accept-Encoding` only on gzip responses, refused as `vary-unsupported`). Bounded: 30 s, and the next storable response replaces it. M7 should key the bucket or treat it like `Unkeyed` for markers.
- `validSMaxAge` uses `httpcc.ResponseDirectives.Unusable()` (the FR-FRS-2 predicate, shared with `Lifetime`).

- weirhttp `RequestFrom` now cuts absolute-form targets from the raw bytes (M1-17c adversarial review: `EscapedPath` hid `#` as `%23`). An absolute-form target with an empty path (`GET http://example.com`) is still rejected as `path`; RFC 9110 §4.2.3 treats it as `/`. Pre-existing; decide whether the adapter should send `/`.
- M1-17c: `keys.IsUpgrade` ignores an `Upgrade` whose only token is `h2c`; `Http2-Settings` is now in `hopByHop` (so also stripped from stored and served responses). `FuzzForwardEqualsKey` adds the h2c shape when the high bit of `sel` is set. `keyedCookies` no longer early-exits on large raw lines: it scans every line (cost header bytes times `len(Key.Cookies)`); M1-18 may want a benchmark with a 1 MiB Cookie header.
- `cacheable` (serve.go) validates StaleSWR and NeedsValidation entries with validators via `fetch(..., prior)`; SWR still validates in the foreground (`ponytail:`, M5). Under M5, StaleSWR must be served before the `only-if-cached` and Range checks, which today reject or pass through stale SWR entries.
- A Range request that no entry answers (miss or stale) goes through `pass` via `keys.Classified.AsRangePass()` with Range and If-Range (FR-SRV-5 updated). HEAD with Range goes forward as GET and the body is dropped (FR-FWD-4). M11-01 adds 206 from entries; FR-RNG-4's background fill hooks into that `pass` call.
- With `HonorRevalidation`, `no-cache`/`max-age=0` turn Fresh into NeedsValidation (`forcesValidation`), except under `only-if-cached`.
- `fetch` retries a strong-ETag-mismatch 304 under the same timeout; M4 must keep the retry under the same limiter slot. After a validation whose response is unstorable and response-driven, `setMarker` relies on the read-before-write to skip the marker.
- Unowned events: no card emits `EvRequest`, `EvFetchStart` or `EvFetchEnd` (`EvStoreError{epoch}` landed in M4-04); over-size and 5xx responses emit no `EvNotStored`. M1-16 decided not to fold `EvRequest` in (its "every hit/stale/miss/.../error" scope is bigger than a Size S card and depends on M6/M7 reason values); give it its own card. CONNECT/upgrade rejection stays event-less too: `EvKeyRejected`'s reason vocabulary is `RequestError.Reason` values only, and FR-UPG-1 has adapters intercept these before `Serve`.
- Carried: `New` must reject `Forward.Allow` entries naming keyed or hop-by-hop fields and compile query patterns; decide whether `Close` waits for foreground `Serve` calls; codec header-name case; hard-epoch prune and S3-FIFO walk to measure in M1-18.
- `invalidate` (purge.go) handles FR-INV-1 URI, `Location` and `Content-Location`; FR-INV-2 groups are M9-03. Failed `SetEpoch` writes are ignored with no event.
- Newest-wins lives in `storeResponse` (serve.go), not `fetch`; M2 moving the store into the flight must keep the found-record exemption (`sameRecord`). `TestNewerResponseWins` sends a second GET while the first is gated, so under M2 coalescing it must use a key that cannot join the flight.
- Event streams (FR-STR-1, M1-16): `fetch` checks `Content-Type` against `text/event-stream` or `Storable.StreamTypes` right after headers arrive, before the buffered `io.ReadAll`, and sets `fetchResult.stream`; `cacheable` (serve.go) treats it like `res.over` (skip `storeResponse`, leave `resp.Body` as fetch wired it) but never marks it `over`. M2 coalescing and M5 background refresh must keep a `stream` response out of the flight/refresh path (FR-COA-5: followers re-enter, not share it).
- M1-16 review nits left open (not must-fix): no test exercises the operator-configured `Storable.StreamTypes` branch of `isEventStream` (only the `text/event-stream` literal is covered); `TestConnectRejected`/`TestUpgradeRejected` check no origin call but not that no event fires.
- weirhttp (M1-17): `TransportOrigin.Fetch` sends `//` paths in absolute form, suppresses Go's default `User-Agent`, and relies on `DisableCompression` (04 §10). `Middleware` routes upgrades with `keys.IsUpgrade` (exported from `isUpgrade`).
- M1-17 review nits left open: an origin-form `//x` target through `RequestFrom`'s `RequestURI` branch is untested (needs a raw connection; the test's `//` case goes absolute-form); a nil `TransportOrigin.Target` panics (documented contract).
- weirhttp `HandlerOrigin` (M1-17b): enforces a declared `Content-Length` like net/http's server (short body reads fail with `io.ErrUnexpectedEOF`, so fetch.go never stores it), refuses 204/304 bodies, discards HEAD bodies, recovers panics and `runtime.Goexit` as `ErrOrigin`. A handler that ignores its context and never writes outlives `Close` (04 §10). The Caddy `nextOrigin` should reuse this writer rather than copy it.
- Threat-model gap for the Caddy card: an in-process origin sees the creator request's context values (FR-COA-9: Caddy vars, auth identity set by upstream middleware). Those are unkeyed input `TransportOrigin` never exposes; docs/06 has no row for it. Decide before the Caddy adapter lands.
