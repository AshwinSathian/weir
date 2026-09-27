# Weir testing strategy

Status: v1.0
Date: 2026-09-27
Depends on: [01-technical-spec.md](01-technical-spec.md), [06-threat-model.md](06-threat-model.md)
Seed name: `03-testing-strategy.md` (renumbered, see [docs/README.md](README.md))

The seed scattered a test strategy under each failure mode. This document turns them into one plan: which level each behavior is tested at, the harness, the numeric pass criteria, and what "Phase 1 done" means.

## 1. Rules

1. Every requirement ID in the spec (`FR-*`, `NFR-*`) and every invariant (`INV-*`) is cited by at least one test, in a comment on the line above the test function: `// FR-COA-4, T-19`. A script (`scripts/trace.sh`, added in Phase 0) lists requirement IDs with no citing test; Phase 1 is not done while the list is non-empty.
2. Tests that involve time, timeouts, tickers or concurrency run inside `synctest.Test`. No unit or component test calls `time.Sleep` on the real clock or relies on wall-clock timing to pass. Engines, stores, test origins and test servers are created inside the bubble (channels and timers created outside a bubble panic when used inside it), and closed before the bubble function returns.
3. Every test run uses `-race`. CI also runs with `-shuffle=on` and `-count=1`.
4. Randomness in the code under test comes from `Config.Rand`. Tests that assert on distributions inject a seeded `math/rand/v2` PCG source wrapped in a mutex (`Config.Rand` is called concurrently); tests that assert exact behavior inject a scripted sequence, also mutex-guarded.
5. A bug fix starts with a failing test that reproduces it.
6. Tests assert observable behavior (responses, origin call counts, events, store contents), not unexported fields, except in the stateful component packages where internal counters are the behavior.

## 2. Levels

| Level | What | Where | Clock | Runs in |
|---|---|---|---|---|
| Unit | pure functions: directive parsing, lifetime, age, jitter, key encoding, normalizers, query rewrite, sfv, codec | `internal/*`, `store` | none or synctest | every `go test` |
| Property | invariants over generated inputs (INV-1, INV-2, INV-3, codec round trip) written as `Fuzz*` targets so the seed corpus runs in every `go test` and longer runs happen in fuzzing | same packages | none | every `go test`; fuzzing nightly |
| Component | one stateful component in isolation: flight table, limiter, breaker, missrate, memory store | `internal/*`, `store/memory` | synctest | every `go test` |
| Engine | `weir.Engine` with the memory store and `testorigin` | root package | synctest | every `go test` |
| Integration | `weirhttp` middleware and `TransportOrigin` against `httptest.NewTestServer` (in-memory network, Go 1.27) | `weirhttp` | synctest | every `go test` |
| Conformance | `storetest.Run` for each store; RFC behavior tables; external `http-tests/cache-tests` suite against `examples/weirproxy` | `store/*`, root, CI job | synctest / real | every `go test`; cache-tests nightly |
| Load and adversarial | taxonomy scenarios at volume with real time and real goroutine scheduling | `loadtest/` (build tag `load`) | real | nightly and before each milestone closes |
| Benchmarks | hit path, miss path, key build, store ops | `*_test.go` `Benchmark*` | real | on demand; compared with `benchstat` in milestone reviews |

## 3. The test origin (`internal/testorigin`)

A programmable `weir.Origin` used by engine and load tests.

```go
type Origin struct { /* unexported */ }

func New() *Origin

// Route sets the behavior for an exact path; Default applies otherwise.
func (o *Origin) Route(path string, b Behavior)
func (o *Origin) Default(b Behavior)

type Behavior struct {
	Status   int
	Header   http.Header
	Body     []byte
	Delay    time.Duration        // before headers; fake time under synctest
	Gate     <-chan struct{}      // if set, block until closed or ctx done
	Err      error                // return as a transport error
	Panic    bool
	Truncate int                  // body read fails after this many bytes (0 = off)
	BodyDelay time.Duration       // delay between header and body bytes
	Func     func(*weir.Request) (*weir.Response, error) // full control
}

func (o *Origin) SetDown(down bool)            // every call returns a transport error
func (o *Origin) Calls(path string) int
func (o *Origin) TotalCalls() int
func (o *Origin) MaxInflight() int               // high-water mark of concurrent Fetch calls
func (o *Origin) MaxInflightPartition() int      // same, per partition (path)
func (o *Origin) Requests() []*weir.Request      // every forwarded request, for INV-1 checks
func (o *Origin) Reset()
```

It asserts INV-7 on every call when constructed with `NewChecked(tb testing.TB, maxConcurrent, maxPerPartition)`: a call that would exceed either bound fails the test with `tb.Errorf` (Fetch runs on engine goroutines, where `Fatal` is not allowed) and returns `ErrOverConcurrency` without running the behavior. While `SetDown(true)` is in effect every call returns `ErrDown`. Header and body delays (`Delay`, `BodyDelay`) and `Gate` end early with `ctx.Err()` when the Fetch context is done.

## 4. Invariant and property tests

| Target | Invariant | Generator |
|---|---|---|
| `FuzzForwardEqualsKey` | INV-1: rebuild the key from the forwarded request alone (with the same config) and get the same primary key; every forwarded header is in the allowed set | request fields from fuzz bytes; config variants from a small fixed set selected by one fuzz byte |
| `FuzzKeyEncodingInjective` | INV-2: two tuples decoded from fuzz bytes that differ in any field produce different encodings | two tuples per input |
| `FuzzMalformedHeaderAbsent` | INV-3 | header values from fuzz bytes |
| `FuzzCodecRoundTrip` | decode(encode(e)) equals e | entries from fuzz bytes |
| `TestJitterNeverLengthens` | FR-FRS-5 | exhaustive over a grid of lifetimes and `u` values |

## 5. Fuzz targets

Every parser that sees attacker or origin bytes has a target. Seed corpora are checked in under `testdata/fuzz/<Target>/` and include every malformed example named in this document and the threat model.

| Target | Package | Must not | Seeds include |
|---|---|---|---|
| `FuzzAcceptEncoding` | `internal/keys` | panic; return anything outside `supported ∪ {identity}` | `br_`, `gzip;q=`, `gzip;q=1.0001`, `*;q=0`, 10 KiB of commas, NUL bytes, duplicate lines with conflicting q |
| `FuzzValidateRequest` | `internal/keys` | panic; accept a path with control bytes | `%zz`, `%0a`, `/a/../b`, absolute-form, 9 KiB path |
| `FuzzQueryRewrite` | `internal/keys` | panic; output a segment not in the input | `a=1;b=2`, `&&&`, `utm%5Fx=1`, 300 params |
| `FuzzCookies` | `internal/keys` | panic | duplicate names, missing `=`, quoted values |
| `FuzzHost` | `internal/keys` | panic; accept a host with `/` or `@` | `[::1]:0`, `a..b`, `EXAMPLE.com.`, 300-byte host |
| `FuzzCacheControl` | `internal/httpcc` | panic; negative lifetime | `max-age=-1`, `max-age="5"`, `max-age=99999999999999`, unterminated quotes |
| `FuzzHTTPDate` | `internal/httpcc` | panic | `0`, RFC 850 and asctime forms, non-GMT zones |
| `FuzzSFStringList` | `internal/sfv` | panic; exceed limits | escapes, params, non-string members |
| `FuzzDecodeEntry` | `store` | panic; allocate beyond the input-length bound | truncated fields, huge uvarint lengths |

CI runs every seed corpus in the normal test run. A nightly job runs each target for 5 minutes (`go test -run=^$ -fuzz=^Target$ -fuzztime=5m`) and uploads any new crasher as an artifact; a crasher becomes a checked-in seed with its fix.

## 6. Failure-mode test matrix

Each seed taxonomy entry maps to the tests below. "Engine" tests run under synctest with `testorigin`; "Load" tests run under the `load` tag with real time.

### T6.1 Synchronized expiry

- `TestJitterSpread` (unit): 10 000 lifetimes of 300 s with a seeded source; assert all in [270 s, 300 s], and a chi-square test over 10 buckets does not reject uniformity at p = 0.001.
- `TestJitterNeverLengthens` (unit, see §4).
- `TestBatchWriteExpirySpread` (engine): store 1 000 keys at t = 0 with `max-age=300`; advance to t = 300 s in 1 s steps while requesting all keys each step; assert the maximum origin calls in any 1 s step is at most 150 (without jitter all 1 000 land in one step).
- `TestEarlyRefreshProbability` (unit): the trigger probability is `P(U <= exp(-remaining / (Δ·β)))`. With Δ = 500 ms, β = 1, remaining = 1 s, the empirical rate over 100 000 seeded draws is within 5% of `exp(-2)` ≈ 0.135; with remaining = 0 it triggers every time; with β = 2 the rate rises to `exp(-1)`.
- `TestEarlyRefreshSingleFlight` (engine): 500 concurrent fresh hits near expiry trigger at most 1 origin call.

### T6.2 and T6.2a Hot key, stuck leader

- `TestCoalesceColdKey1000` (engine): 1 000 concurrent requests for one cold key, origin delay 200 ms; exactly 1 origin call; all 1 000 get 200 with identical bodies; 999 report `collapsed`.
- `TestCoalesceCreatorCancel` (engine): creator's context canceled mid-fetch; followers still get the response; origin call count 1.
- `TestCoalesceStuckLeader` (engine): origin gated forever; 100 followers; after `FollowerMaxWait` each follower either served stale (when a stale entry with SIE exists) or made its own fetch; no follower waits longer than `FollowerMaxWait` plus 1 ms of fake time; origin concurrency never exceeds `MaxPerPartition`.
- `TestCoalesceLeaderAging` (engine): origin gated; a request arriving after `LeaderMaxAge` starts a second flight; exactly 2 origin calls at that point.
- `TestCoalescePanic` (engine): origin panics; every waiter gets `*OriginError`; limiter in-flight returns to 0; next request fetches again.
- `TestVaryFollowersRecoalesce` (engine): cold key, origin answers `Vary: Accept-Language`; 300 concurrent requests across 3 languages end with exactly 1 + 2 origin calls (one flight, then one per remaining variant).
- `TestUncacheableNotSerialized` (engine): origin returns `private`; 50 concurrent requests; after the first flight completes, the remaining followers fetch concurrently (origin max in-flight > 1) and a marker exists; the next 50 requests make 50 calls with no coalescing.

### T6.3 Cross-key stampede

- `TestLimiterCap5000Keys` (engine): 5 000 cold keys on 5 000 distinct paths (so the per-partition cap does not bind) requested at once, origin delay 50 ms, `MaxConcurrent = 64`, `MaxQueue = 10000`, `MaxQueueWait = 1 min`; origin max in-flight is exactly 64; all 5 000 succeed.
- `TestLimiterShedsWithStale` (engine): same, with `MaxQueue = 100` and stale entries with SIE present for half the keys; those serve stale with `detail=shed`; the rest get `ErrShed` or succeed; none waits beyond `MaxQueueWait`.
- `TestPartitionFairness` (engine): 1 000 requests to unique query strings on `/search` plus 50 requests to distinct other paths; `/search` never exceeds `MaxPerPartition` in-flight; all 50 other requests complete without shedding.
- `TestLimiterSkipsFullPartition` (component): queue head blocked by its partition cap does not block a later waiter of another partition.
- `TestLimiterQueueTimeout`, `TestLimiterCancel`, `TestLimiterGrantRace` (component): the grant-versus-timeout race returns the slot exactly once (run 1 000 times with shuffled timings inside the bubble).

### T6.4 Cold start

- `TestColdStartBounded` (engine): empty store, 2 000 requests over 500 hot keys; origin max in-flight ≤ `MaxConcurrent`; origin calls ≤ 500 plus follower-timeout fetches (0 with default timeouts and 50 ms origin delay).
- `TestWarm` (engine): `Warm` with 100 URLs at `Concurrency = 4`: origin max in-flight is 4; subsequent requests are hits; fresh entries are skipped on a second `Warm`.
- `TestWarmDoesNotUseReserve` (engine): with foreground load holding `MaxConcurrent - ReserveForeground` slots, warm fetches wait instead of taking reserved slots.

### T6.5 Storage outage

- `TestStoreOutageStillCoalescedAndLimited` (engine): a store wrapper that returns `ErrUnavailable` for everything; 1 000 requests over 10 keys; origin calls ≤ 10 per flight wave; in-flight ≤ `MaxConcurrent`; the store breaker opens after 5 failures and `EvStoreBreaker` is emitted.
- `TestStoreSlowRemote` (engine): a remote-flagged store that blocks on `Get`; each request spends at most `Timeouts.Store` in the store before continuing; after the breaker opens, zero time.

### T6.6 Origin outage

- `TestOriginClockSkewDoesNotStale` (engine): origin `Date` 10 minutes behind, `max-age=300`; the second request is a hit.
- `TestStaleIfErrorOnOriginDown` (engine): entries with `max-age=10, stale-if-error=60`; origin down at t = 20 s; requests at t = 30 s get stale 200 with `detail=stale-if-error`; at t = 80 s they get 502 (`ErrOrigin`).
- `TestMustRevalidate504` (engine): `max-age=10, must-revalidate`, origin down: `ErrMustRevalidate`.
- `TestBreakerOpensHalfOpenCloses` (engine and component): 30 gateway failures in 2 s open the breaker; subsequent requests make no origin calls; after `OpenFor` (with jitter bounds checked) one probe goes through; success closes, failure reopens with doubled duration capped at `MaxOpenFor`.
- `TestBreaker500DoesNotTrip` and `TestBreakerNeedsVolume` (component): 100 consecutive 500 responses do not open it; 19 gateway failures do not open it with `MinRequests = 20`.
- `TestDefaultStaleWindowsOff` (engine): origin sends no stale directives; nothing stale is ever served (D6).

### T6.7 Unkeyed inputs

- `TestForwardEqualsKey` (engine): for a table of requests with extra headers, cookies and params, every forwarded request satisfies INV-1.
- `TestKettleUserAgent` (engine): origin varies body on `User-Agent` without `Vary`; request 1 with `User-Agent: evil`; request 2 with a normal UA receives the same response the origin gives to "no User-Agent", and the origin never saw `evil`.
- `TestVaryUnconfiguredHeader` (engine): origin sends `Vary: X-Custom` in `VaryAuto`: two requests with different `X-Custom` values in `Forward.Allow` produce two variants; in `VaryStrict` without allow, nothing is stored (`EvNotStored{vary-strict}`); the seed's T6.7 test.
- `TestVarySensitiveNotStored`, `TestVaryStar`, `TestVaryOverflow` (engine).
- `TestFatGETBodyDropped` (integration): body on GET is not forwarded.

### T6.8 Cache busting

- `TestRandomQueryFloodBounded` (engine): 10 000 requests to `/p?r=<random>`; origin in-flight never exceeds `MaxPerPartition`; hot keys on other paths stay hits.
- `TestMissRateAnomaly` (engine): the same flood raises exactly one `EvMissRateAnomaly` for `/p` per window; normal traffic at 50% miss ratio does not.
- `TestMissRateThrottle` (engine): with `Throttle`, the anomalous partition is capped to 1 in the next window.
- `TestSpaceSavingBound` (component): with 100 000 distinct partitions and one heavy hitter at 20% of traffic, the heavy hitter is tracked and reported; memory stays at `TopK` counters.

### T6.9 Malformed input

- `TestCVE202435296` (engine): 1 000 requests to one URL each with a different malformed `Accept-Encoding`; exactly 1 origin call; all served from one entry.
- Fuzz targets in §5.
- `TestInvalidRequestsCostNothing` (engine): each `FR-VAL-1` rejection makes no store or origin call.

### T6.10 Negative caching

- `TestNegativeCacheBurst` (engine): origin returns 503 for one key; 200 requests in the same 100 ms; 1 origin call; 199 synthesized 503s with `detail=negative`; after `Negative.TTL`, 1 more call.
- `TestNegativeNotFor500`, `TestNegativePrefersStale`, `TestNegativeScopedToKey`, `TestNegativeNeverReplacesResponse` (engine).
- `TestMarkerNotFromAuthorizedRequest`, `TestMarkerNotFromRequestNoStore` (engine, T-31): after such a request, the next 100 anonymous requests for the URL collapse into 1 origin call.

### T6.11 Eviction storms

- `TestS3FIFOScanResistance` (component): 1 000 hot keys accessed repeatedly, then 100 000 one-hit keys; at least 90% of hot keys remain.
- `TestOneHitWondersDoNotEvictHot` (engine): the T6.8 flood does not reduce the hit ratio of a concurrent hot-key workload below 90% of its baseline.
- `TestByteAccountingBound` (component): after every `Set`, `Bytes() <= MaxBytes`.

### T6.12 Purge herd

- `TestPurge5000KeysBounded` (engine): 5 000 cached keys; soft purge by a group they share; 5 000 concurrent requests; origin in-flight ≤ `MaxConcurrent`; with `stale-while-revalidate=30`, every request returns within 1 ms of fake time (served stale).
- `TestSoftPurgeServesStaleWhileRevalidating`, `TestHardPurgeIsMiss`, `TestSoftAfterHardStaysHard`, `TestPurgeDuringInflightFetch` (engine).
- `TestUnsafeMethodInvalidates` (engine): POST 201 invalidates the URI and a same-origin `Location`, not a cross-origin one; `Cache-Group-Invalidation` on a GET response is ignored.
- `TestGroupInvalidationIsSoft` (engine): a POST whose response invalidates group `g` leaves `g`'s entries servable under their SWR window.
- `TestInvalidationFloodBounded` (component, on the memory store): 1 000 000 soft and invalid epochs for distinct tags; store memory unchanged; every tag's lookup is at least its own epoch. (engine): 60 000 POSTs to distinct URIs inside one 60 s entry lifetime; of 1 000 hot entries with `max-age=60` that were not invalidated, at most 8% revalidate early (the sketch's expected rate is about 4%, E-8); every entry whose URI was invalidated is validated before it is served.

### T6.13 Rolling deploy

- `TestGlobalEpochSoft` (engine): `Purge{All: true}` makes every entry revalidate once and none is served beyond SWR.

### Security, lifecycle and harness tests referenced elsewhere

Named in [06-threat-model.md](06-threat-model.md), [01-technical-spec.md](01-technical-spec.md), [04-lld.md](04-lld.md) or [PLAN-weir.md](../PLAN-weir.md). Level in parentheses.

| Test | Asserts |
|---|---|
| `TestZeroConfigValid` (unit) | `New(Config{})` succeeds and every default in 01 §6 is applied |
| `TestInvalidConfigRejected` (unit) | one row per FR-LCY-1 rule; each returns an error wrapping `ErrInvalidConfig` naming the field |
| `TestServePassThroughStub` (engine, Phase 0 only) | the Phase 0 stub forwards to `testorigin` and returns its response |
| `TestNoThirdPartyImports` (unit) | `go list -deps -json ./...` for the root module lists only standard-library packages and the module's own (NFR-6) |
| `TestEveryEventKindEmitted` (engine) | a scenario per `EventKind` in 04 §9.2 makes the observer receive it |
| `TestKeyEncodingInjective` (unit) | hand-picked collision attempts: `("a","bc")` vs `("ab","c")` across every adjacent field pair, the Akamai `__` shape, empty versus absent header |
| `TestUnkeyedHeaderNotForwarded` (engine) | `X-Forwarded-Host`, `X-Forwarded-Port`, `User-Agent`, `Origin`, `Accept`, `Upgrade`, `Max-Forwards` never reach the origin on a cacheable request in strict mode |
| `TestQueryDropRemovesFromForward` (engine) | a dropped parameter is absent from the forwarded query and the key |
| `TestParameterCloakingSemicolon` (engine) | `?a=1;utm_x=2` with `QueryDrop: utm_*` forwards the segment unchanged and keys it (it is one segment named `a`) |
| `TestPathForwardedByteExact` (integration) | `/a/..%2Fb`, `/x;y.css`, `/%7euser` reach the origin byte-for-byte through `weirhttp` and `TransportOrigin` |
| `TestNoExtensionBasedCaching` (engine) | a `.css` path whose response has no freshness information is not stored |
| `TestRangeGarbageNotPoisoning` (engine) | `Range: bytes=cow` on a miss is passed through; the origin's 400 is not stored; the next plain request is a normal miss then hit |
| `TestErrorStatusesNotStored` (engine) | 400, 401, 403, 500, 502, 503 with `max-age=60` are not stored under default config |
| `TestSetCookieNotStored` (engine) | `Set-Cookie` blocks storage; with `StripSetCookie` the stored entry has no `Set-Cookie` and the triggering client still receives it |
| `TestAuthorizationRules` (engine) | RFC 9111 §3.5: stored only with `public`, `s-maxage` or `must-revalidate` |
| `TestAuthorizedNotCoalesced` (engine) | 50 concurrent `Authorization` requests on a cold key make 50 origin calls; none receives another's response |
| `TestClientNoCacheIgnored` (engine) | `Cache-Control: no-cache` and `Pragma: no-cache` requests are hits under default config |
| `TestEpochLookupErrorEmitsEvent` (engine) | a remote-flagged store failing `NewestEpoch` still serves the entry and emits `EvStoreError{epoch}` |
| `TestCacheStatusNoKey` (engine) | no emitted `Cache-Status` contains `key=` |
| `TestGroupsScopedByOrigin` (engine) | group `g` purged on `https://a.example` does not affect `g` on `https://b.example` |
| `TestPathFloodOriginBounded` (engine) | 10 000 requests to distinct paths: origin in-flight never exceeds `MaxConcurrent`; shed requests get `ErrShed` quickly |
| `TestSlowReaderDoesNotPinSlots` (engine) | 200 pass-through responses whose consumers never read the body: limiter in-flight returns to 0 once headers arrived; streams end at `Timeouts.Origin` |
| `TestRefreshNeverExceedsReserve` (engine) | under SWR load, background fetches never push in-flight above `MaxConcurrent - ReserveForeground` |
| `TestOversizedStreamedNotBuffered` (engine) | a 50 MiB response is streamed to the creator; engine heap growth stays below 2 × `MaxObjectBytes`; followers re-enter and fetch themselves |
| `TestShardDistributionAdversarial` (component) | 100 000 keys chosen to share their first 8 bytes spread across shards within 20% of uniform |
| `TestServedHeaderMutationDoesNotLeak` (engine) | `Add`, `Set`, `Del` and `Values` on a served response's headers leave the next hit's headers unchanged |

### Phase 1.x (M11 to M15)

| Test | Asserts |
|---|---|
| `TestRangeSingleFromCache` (engine) | `bytes=0-99`, `bytes=100-`, `bytes=-50` on a stored 1 000-byte entry return 206 with correct bytes and `Content-Range`; no origin call |
| `TestRangeUnsatisfiable416` (engine) | `bytes=5000-` on a 1 000-byte entry returns 416 with `Content-Range: bytes */1000` |
| `TestRangeMultiOrInvalidGets200` (engine) | `bytes=0-1,5-6`, `bytes=cow`, `items=0-1` return the full 200 |
| `TestIfRangeStrongOnly` (engine) | weak ETag or a non-strong date in `If-Range` yields the full 200 |
| `TestRangeMissBackgroundFillBounded` (engine) | 1 000 range requests across 1 000 cold URLs: origin in-flight stays within the background limit; exactly one fill per URL whose 206 total is within `MaxObjectBytes`; none for larger totals |
| `FuzzRange` (property) | no panic; `RangeOK` results always satisfy `0 <= start <= end < size` |
| `TestTargetedFieldPrecedence` (engine) | `Weir-Cache-Control` beats `CDN-Cache-Control` beats `Cache-Control`; invalid targeted field falls through |
| `TestTargetedFieldKeepsPrivate` (engine) | `CDN-Cache-Control: max-age=600` with `Cache-Control: private` is not stored |
| `TestWeirCacheControlStripped` (engine) | clients never see `Weir-Cache-Control` |
| `FuzzSFDictionary` (property) | no panic; bounded members |
| `TestSnapshotRoundTrip` (component) | close with snapshot, reopen: same entries present, file removed |
| `TestSnapshotLoadIsSoftStale` (engine) | loaded entries revalidate (or serve under SWR) on first request |
| `TestSnapshotHardEpochSurvives` (engine) | hard-purged entry stays unreachable after restart |
| `TestSnapshotCorruptRecordsSkipped` (component) | flipped bytes skip one record; truncated file (no trailer) is ignored entirely |
| `TestSnapshotRespectsDeadline` (component) | `Close` with an expired context leaves no snapshot and no temp file |
| `TestOwnerQuotaIsolatesTenants` (component) | owner A inserting 10× its quota evicts only A's entries; owner B's entries all remain |
| `TestPerHostLimiterCap` (component) | host cap holds while other hosts proceed |
| `TestEagerHardPurgeDeletesAllPartitions` (engine) | eager hard purge of a URL removes variant and keyed-header entries from the memory store; `Bytes()` drops accordingly |
| `TestEagerSoftIsError`, `TestEagerUnsupportedStore` (engine) | invalid combination rejected; store without `Scrubber` returns `ErrEagerUnsupported` after writing the epoch |

### Decisions D25 to D42

| Test | Asserts |
|---|---|
| `TestSlowUploadsDoNotStarveMisses` (engine) | 200 POSTs with bodies that never finish do not delay cacheable misses; upload pool saturates alone |
| `TestBodylessBypassUsesMainPool` (engine) | bypassed GETs with a session cookie never take upload slots |
| `TestDripOriginReleasesSlot` (engine) | origin sending 1 byte per second of a 10 KiB body: fetch fails at `Timeouts.Origin`, slot released |
| `TestStreamIdleTimeout` (engine) | a 2-minute pass-through stream that keeps sending survives; one that stalls for `StreamIdle` ends |
| `TestConnectRejected`, `TestUpgradeRejected` (engine), `TestAdaptersRouteUpgradesAround` (integration) | `Serve` returns `ErrUpgradeNotSupported`; weirhttp hands WebSocket and CONNECT to the next handler |
| `TestEventStreamNeverBuffered` (engine) | first SSE event reaches the client before the origin sends a second; nothing stored |
| `TestTraceparentValidated` (engine) | valid headers forwarded on a miss; malformed `traceparent` drops both trace headers; `NoTraceHeaders` forwards none |
| `TestStrippedCookieReport` (engine) | after the report window one log line lists the most frequent stripped names, no values |
| `TestModeExpires`, `TestModeStaleOnErrorLimits`, `TestModeBypass` (engine) | mode reverts after ttl; stale-on-error never serves hard-purged, invalidated or must-revalidate entries, nor beyond 24 h; bypass stores nothing |
| `TestDefaultStoreSizeFromMemLimit` (unit) | 1 GiB limit gives 409.6 MiB rounded to shards; unset gives 256 MiB and a warning |
| `TestMemorySizingSplit` (Caddy, Phase 2) | three unnamed-size stores share 40% of the limit |
| `TestVaryReclaimsDeadSlots` (engine) | after a variant expires, a new variant can take its slot |
| `TestRedirect302NeedsExplicitFreshness` (engine) | 302 with `max-age=60` stored; 302 with only `Last-Modified` not stored |
| `TestRetryAfterSurvivesHandleErrors` (Caddy, Phase 2) | `Retry-After` present with and without `handle_errors` |

## 7. RFC behavior tables

`rfc9111_test.go` in the root package holds table tests, one row per normative statement Weir implements, citing the section: storage conditions (§3), header storage exclusions (§3.1), Authorization (§3.5), Age generation (§4), Vary matching and normalization (§4.1), lifetime precedence and invalid directives (§4.2.1), heuristic limits (§4.2.2), age calculation with the RFC's own example values (§4.2.3), validation headers sent (§4.3.1), client conditionals (§4.3.2), 304 freshening (§4.3.4), invalidation (§4.4), `only-if-cached` (§5.2.1.7), `must-revalidate` 504 (§5.2.2.2), `must-understand` (§5.2.2.3), RFC 5861 examples verbatim (§3.1 and §4.1 of that RFC), and RFC 9211 examples for the parameters Weir emits.

## 8. External conformance: http-tests/cache-tests

`examples/weirproxy` is a small reverse proxy built from `weirhttp.Handler` and `TransportOrigin`. A nightly CI job starts it in front of the cache-tests server and runs the suite (Node.js, `http-tests/cache-tests`). Results are stored as `testdata/cache-tests-baseline.json`. The job fails when a test that passed in the baseline fails now. Tests Weir fails by design (for example client `no-cache` handling under D5, stale serving without origin permission under D6) are listed in `docs/cache-tests-expected-failures.md` with the reason and the decision ID.

The suite's authors say passing everything "means nothing" by itself. It is used here as a regression net and a way to spot unintended behavior, not as a conformance claim.

## 9. Load and adversarial tests (`loadtest/`, tag `load`)

Real time, real scheduler, in-process origin with a capacity model (a semaphore of N and a service time distribution). Each scenario prints a summary table and asserts thresholds:

| Scenario | Load | Pass criteria |
|---|---|---|
| steady hits | 64 goroutines, 10 000 hot keys, 60 s | hit ratio > 99%; engine p99 < 200 µs |
| synchronized expiry | 10 000 keys stored in 1 s with `max-age=60` | origin peak in-flight ≤ `MaxConcurrent`; origin calls per second during expiry ≤ 20% of key count |
| busting flood plus normal traffic | 5 000 rps unique queries on one path, 500 rps normal | normal traffic p99 unchanged within 20%; origin in-flight for flooded path ≤ `MaxPerPartition` |
| origin brownout | origin p50 latency ×20 for 30 s | breaker behavior per config; stale served where permitted; no goroutine growth after recovery |
| store flap | remote-flagged fake store failing 50% for 30 s | no request exceeds `Timeouts.Store` + origin time; store breaker cycles |

Goroutine count is sampled every second; the run fails if it does not return to baseline ±10 within 5 s after load stops.

## 10. Benchmarks

`BenchmarkServeHitSmall` (1 KiB body), `BenchmarkServeHitVary`, `BenchmarkServeMissCoalesced`, `BenchmarkKeyBuild`, `BenchmarkAcceptEncoding`, `BenchmarkMemoryStoreGetParallel`, `BenchmarkLimiterAcquireRelease`. Each milestone review includes `benchstat` output against the previous milestone. NFR-5 budgets apply from M10.

## 11. CI pipeline

On every push and pull request:

1. `gofmt -l` is empty; `go vet ./...`.
2. `golangci-lint run` (v2 config, see [CLAUDE.md](../CLAUDE.md)).
3. `go test -race -shuffle=on -count=1 ./...` for every module, once with the workspace and once per module with `GOWORK=off`, on each supported Go release that is at or above the `go.mod` minimum (D42; only 1.27 until Go 1.28 ships).
4. `go list -deps ./...` check for the root module: no import outside the standard library and the module itself (NFR-6).
5. `govulncheck ./...`.
6. `scripts/trace.sh` requirement coverage report (fails once Phase 1 is declared done).
7. Coverage report (`go test -coverprofile`); informational, target ≥ 85% of statements for `internal/*` and the root package.

Nightly: fuzzing (§5), cache-tests (§8), load tests (§9).

## 12. Definition of done for Phase 1

- Every T6.x test in §6 passes, under `-race`, 100 consecutive times (`-count=100`) in CI for the engine-level ones.
- `scripts/trace.sh` reports zero untested requirement IDs.
- Every fuzz target has run at least 1 hour cumulatively without a new crasher.
- Load scenarios in §9 pass on the reference machine.
- cache-tests baseline recorded with every failure explained.
- NFR-5 budgets met or the spec updated with measured numbers and a reason.
