# Hardening cards (v1.1.0)

Updated: 2026-10-13

These cards answer the adversarial review of 2026-10-13 (the findings are summarized under each card; the probe numbers come from throwaway tests run against `v1.0.1`). They ship together as `v1.1.0`: several add exported fields, which a patch release may not do (README, Compatibility).

At Ashwin's request the cards are implemented in one branch and one pull request, one commit per card, instead of one session and one pull request per card. `docs/progress/LOG.md` records that as a deviation.

## Decisions taken in the plan

Each one changes a requirement, a default or the exported API, so each is listed in the pull request for Ashwin to accept or reverse before merging.

| # | Decision | Why this and not the alternative |
|---|---|---|
| D-H1 | A follower whose wait expires and has no servable stale entry keeps waiting for its flight until `Timeouts.Origin + Limiter.MaxQueueWait` after it joined, and only then fetches for itself | The flight is already bounded by those two settings. Fetching at `FollowerMaxWait` turned one slow fetch into `MaxPerPartition` fetches and shed the rest. The later fallback keeps the liveness bound for an origin that ignores its context |
| D-H2 | New `Breaker.MinPartitions` (0: 2). The breaker opens only when the failures in the window come from at least that many partitions, or when no fetch in the window succeeded | One failing route must not open the breaker for every route. "Nothing succeeded" keeps a single-URL site covered. Not chosen: a breaker per host (unbounded request input, and one backend usually serves all hosts of a site) |
| D-H3 | A failed half-open probe's partition does not probe again during the first second of the next half-open period | Stops one failing route from taking every probe. The one second bound keeps a single-URL site recovering |
| D-H4 | New errors `ErrRequestBody` (400) and `ErrRequestBodyTimeout` (408). A fetch that failed while the client's body read was blocked or had failed is not an origin failure | A stalled upload said nothing about the origin, yet 16 of them opened the breaker |
| D-H5 | The upload pool's partition cap is half the pool (at least 1), not the whole pool | One path could hold every upload slot |
| D-H6 | An over-size response writes a hit-for-miss marker. A marker may replace the stale response the request found. A request that finds a marker fetches unbuffered | Without the marker every wave waited for the first `MaxObjectBytes` and fetched again. Unbuffered under a marker ends the per-request `MaxObjectBytes` buffer. Cost: a URI that becomes storable again is stored after `HitForMissTTL`, not at once |
| D-H7 | A response whose `Content-Length` exceeds `MaxObjectBytes` is streamed without the buffered read | The length already says it cannot be stored |
| D-H8 | `Timeouts.StreamIdle` also bounds the time a consumer leaves a stream unread; `weirhttp.WriteResponse` sets a rolling write deadline of the same length on live streams | A reader that stops reading pinned an origin connection without a limit. Stored bodies get no deadline: they pin nothing |
| D-H9 | With an in-process store, no keyed header, no keyed cookie and no experiment, an unsafe request writes its invalidation epoch only when the URI has a record or a fetch in progress | A URI with neither has nothing to invalidate, and its epoch only loads the sketch that every other entry shares. Remote stores keep the unconditional write: another node's fetch is invisible |
| D-H10 | New `Request.Client` and `Limiter.MaxPerClient` (0: off). `weirhttp` fills `Client` from `RemoteAddr`; the Caddy adapter from Caddy's `client_ip` and a new `max_per_client` key | Every existing cap is keyed on the URL, which the client chooses. Off by default because behind a load balancer every client has one address |
| D-H11 | `Close` returns after its grace period plus 5 s even when an engine goroutine has not stopped, with an error that says so | An origin that ignores its context made `Close` hang forever. FR-LCY-2 changes from a guarantee to a guarantee for origins that honor their context |
| D-H12 | `weirhttp.TransportOrigin` removes client-supplied `Forwarded`, `X-Forwarded-*` and `X-Real-Ip` from pass-through requests by default. New field `XForwarded`: `XForwardedStrip` (zero), `XForwardedKeep`, `XForwardedSet` (set from `Request.Client`, scheme and host). New `Request.PassThrough`, set by the engine, tells an `Origin` which requests those are | A client could name its own address to the origin on every `POST`. First built to strip from every request, which the branch review showed removes a field that `Key.Headers` or `Forward.Allow` names from cacheable fetches too (a site keying on `X-Forwarded-Proto` would cache the origin's redirect); a cacheable fetch is now never touched. `XForwardedKeep` restores `v1.0` |
| D-H13 | Response trailers of a streamed response reach the client. A response body may implement `Trailer() http.Header`; `TransportOrigin` and `HandlerOrigin` bodies do, the engine's stream bodies forward it and `WriteResponse` sends it. Stored responses carry no trailers | Pass-through dropped them. A method on the body, not a `Response.Trailer` field as first planned: net/http replaces its trailer map while an HTTP/2 body is read, so a field copied at the headers would be the wrong map. Not a claim of gRPC support: request trailers and h2c upstreams are out of scope |
| D-H14 | The Caddy adapter gives every forwarded request its own replacer, bound to the forwarded request | Placeholders after `weir` read the first client's headers, cookies and URI. `RemoteAddr` stays the first client's: masking it would make every cacheable fetch look like one client to an origin that rate-limits by address |
| D-H15 | New `Config.KeyGeneration()`: a digest of the settings that change what an unchanged key means. The memory store's snapshot documentation says to put it in the file name | Library users had no equivalent of the Caddy adapter's key-generation purge |
| D-H16 | A `GET` or `HEAD` with a body is passed through unstored, with its body, through the upload pool | Dropping the body and caching the answer served one query's result for another (search APIs that take a body on `GET`). T-5 still holds: nothing from such a request is stored |
| D-H17 | Host names may contain `_` | Container and cluster service names use it. It has no meaning to a key or a URL parser |
| D-H18 | An encoded response is stored as if `Vary` named `Accept-Encoding` | A gzip body without `Vary` was served to clients that asked for identity. First planned with a second rule, not storing a response in a coding the fetch did not ask for; `make cache-tests` showed that rule refuses what the conformance suite stores (`headers-store-Content-Encoding`), and with the implied `Vary` each coding only ever gets what the origin sends for it, so the rule was dropped |
| D-H19 | A 304 whose entity-tag does not weakly match the stored one is discarded and the request repeated without conditionals (was: strong tags only) | RFC 9111 §4.3.4 selects the stored response by validator; no match means no update |
| D-H20 | With `Client.HonorRevalidation`, request `max-age` and `min-fresh` are applied. `max-stale` stays ignored | They only tighten. `max-stale` loosens, and a shared cache may ignore it |
| D-H21 | A 502, 503 or 504 with `Cache-Control: no-store` or `private` writes no negative entry | The origin said the answer is for this client |
| D-H22 | The `Retry-After` of a shed request is `MaxQueueWait` to 1.5 times that, drawn from `Config.Rand` | A constant sent every shed client back in the same second |
| D-H23 | Memory store: `MaxHardEpochs` default 100 000 (was 10 000). Default shard count grows with the store size if HRD-13's benchmark shows an eviction pause above 1 ms | 10 000 hard purges a day is a small CMS. The shard change is gated on a measurement, not assumed |
| D-H24 | `TRACE` and `OPTIONS` with `Max-Forwards`: the value is decremented on the forward; at 0 Weir answers itself (405 for `TRACE`, 204 for `OPTIONS`) | RFC 9110 §7.6.2 |

Not changed in code, with the reason, and documented by HRD-16:

- Coalescing is per process and a remote store has no local tier in front of it. Both are features of their own size (a distributed lock with a fencing rule; a two-tier store), not fixes. Deferred card HRD-20.
- `Limiter.MaxQueueWait` (2 s) and stale-if-error (off) keep their defaults. The first is the designed shed-fast behavior, the second is a default that protects and changes only in a major release. The tuning page gains the numbers.
- An unknown method such as lowercase `get` still invalidates on 2xx or 3xx: RFC 9111 §4.4 requires it. D-H9 removes the amplification.
- `identity;q=0` in `Accept-Encoding` still gets `identity`. A cache that answers 406 for it helps nobody.
- An unconditional fetch answered with 304 still writes a marker. The origin is broken and the marker is the cheap outcome.
- A flood of failing paths (not query strings) still opens the breaker, and a flood of distinct success paths can still hold it shut in a partial outage. D-H2 narrows the first to "at least two failing paths". Threat model residual.
- Per-client fairness is off by default (D-H10), so the default configuration still sheds legitimate misses under a flood of distinct paths.

## Review of this plan

Read against the code before any card started. What it changed:

- D-H1 first read "followers wait until their own context ends". An origin that ignores its context would then hold every follower for as long as the client waits. The second deadline was added.
- D-H2 first counted each partition once per second. A site with one URL could then never reach `MinRequests`. Replaced by the partition floor with the "nothing succeeded" escape. A second version (open only when failing partitions outnumber succeeding ones) let a client hold the breaker shut by requesting cheap distinct paths; dropped.
- D-H3 first barred a failed probe's partition until the breaker closed. A single-URL site would never close. Bounded to one second.
- D-H9 first skipped the epoch whenever the lookup missed. A fetch in progress, sent before the unsafe request, would then store a response the unsafe request should have invalidated. The in-progress count closes that, and the proof is in the card. It also first applied to every store; a remote store hides other nodes' fetches.
- D-H12 first kept the old default and added an opt-in. The default would have stayed spoofable. Reversed, with `XForwardedKeep` as the way back.
- D-H14 first masked `RemoteAddr`. Dropped: see the table.
- A `Via` option on `TransportOrigin` was cut: `Rewrite` already adds it.
- HRD-13's shard change is gated on its own benchmark.

## Order

High findings first, then Medium, then Low, then documents, review and release. Every card ends with its package tests green; `make check` runs after each card and once more at the end.

### [x] HRD-01 Followers outwait a healthy flight
- Plan: v1.1 · Size: M · Depends on: none
- Finding: 100 clients, one key, origin answers in 15 s: 16 answers (at 15 to 25 s), 84 responses 503 at 12 s, 16 origin calls
- Read: 01 FR-COA-3, FR-COA-4, FR-COA-5; 04 §6.4, §6.5; 06 T-19
- Touch: flight.go, config.go (comments), coalesce_test.go, docs/01, docs/04
- Tests: `TestFollowersOutwaitSlowFlight`, `TestFollowerFallsBackAfterFlightDeadline`, `TestCoalesceStuckLeader` (unchanged, still passes)
- AC: with defaults and an origin that answers in 15 s, 100 concurrent requests for one key cause 1 origin call and 100 responses 200; a follower with a servable stale entry still gets it at `FollowerMaxWait`; a follower of a flight that has not published `Timeouts.Origin + Limiter.MaxQueueWait` after the follower joined fetches for itself and emits `EvCoalesceTimeout{direct}`; a creator is unchanged
- Out of scope: hedging a second shared flight
- Notes: D-H1. `EvCoalesceTimeout{direct}` now means the later deadline

### [x] HRD-02 Breaker needs more than one failing partition
- Plan: v1.1 · Size: M · Depends on: none
- Finding: 20 responses 504 on one path with distinct query strings opened the breaker for every path; one request a second on that path took every half-open probe for 120 s
- Read: 01 FR-CB-1 to FR-CB-6; 02 ADR-7; 04 §8.3
- Touch: internal/breaker/breaker.go, internal/breaker/breaker_test.go, fetch.go, config.go, engine.go, stale_test.go or a new breaker_scope_test.go, docs/01, docs/04, docs/06
- Tests: `TestBreakerNeedsTwoPartitions`, `TestBreakerOpensWhenNothingSucceeds`, `TestBreakerProbeYieldsFailedPartition`, `TestOneBadRouteLeavesBreakerClosed`
- AC: D-H2 and D-H3 hold; the failing-partition set is bounded (at most 8 hashes a bucket, overflow counts as "many"); `Breaker.MinPartitions` is validated (negative rejected) and 1 restores the `v1.0` rule
- Out of scope: a breaker per host
- Notes: the breaker stays one mutex; the set is a fixed array per bucket

### [x] HRD-03 A stalled upload is not an origin failure
- Plan: v1.1 · Size: M · Depends on: none
- Finding: 16 requests whose bodies never arrive, 4 normal misses: breaker open after `Timeouts.Origin`
- Read: 01 FR-LIM-7, FR-TMO-1, FR-CB-1; 04 §1.3, §6.7; 06 T-39
- Touch: fetch.go, errors.go, engine.go, limit_test.go, errors_test.go, docs/01, docs/04, docs/06
- Tests: `TestStalledUploadIsNotOriginFailure`, `TestFailedUploadReadIsRequestError`, `TestSlowOriginWithBodyStillCounts`, `TestUploadPoolPartitionCap`
- AC: D-H4 and D-H5 hold; an origin that is slow while the body has been fully read still records a failure; `StatusCode` maps the two new errors to 400 and 408; no negative entry and no breaker count for either
- Out of scope: per-client caps (HRD-07)
- Notes: the body wrapper records "a Read is blocked" and "a Read failed"; the timer callback samples the first before it cancels

### [x] HRD-04 Markers for responses that stopped being storable
- Plan: v1.1 · Size: M · Depends on: none
- Finding: an object over `MaxObjectBytes` and a URI that turned `private` both made every wave wait for a flight and then fetch again, with no marker ever written
- Read: 01 FR-STO-12, FR-COA-5, FR-STR-1; 04 §6.7; 06 T-31, T-37
- Touch: serve.go, fetch.go, flight.go, serve_test.go, coalesce_test.go, docs/01, docs/04
- Tests: `TestOversizeWritesMarker`, `TestMarkerReplacesFoundEntry`, `TestMarkerFetchIsUnbuffered`, `TestContentLengthOverLimitSkipsBuffer`, `TestNoMarkerAfterUnkeyedInput` (unchanged)
- AC: D-H6 and D-H7 hold; the marker rules of T-31 (no marker under `Authorization`, request `no-store` or unkeyed input) apply to the new markers; a 5xx or a 304 never replaces a stored response with a marker; a store that returns a fresh copy per `Get` still has its found entry replaced (compared by request and response time)
- Out of scope: the consumer idle bound (HRD-05)

### [x] HRD-05 A stream nobody reads ends
- Plan: v1.1 · Size: S · Depends on: HRD-04
- Finding: a reader that stops reading held an origin connection, and for an over-size object `MaxObjectBytes` of heap, with no limit; docs/06 T-18 said the origin timeout bounds it, FR-TMO-2 and the code said nothing does
- Read: 01 FR-TMO-2, FR-LIM-1; 06 T-18
- Touch: fetch.go, weirhttp/convert.go, limit_test.go, weirhttp/weirhttp_test.go, docs/01, docs/06
- Tests: `TestUnreadStreamIsCancelled`, `TestWriteResponseDeadlineOnLiveStream`, `TestWriteResponseNoDeadlineOnStoredBody`
- AC: D-H8 holds; an event stream that sends one event a minute to a reading client is not cut at defaults (the origin read is in progress, and each write completes); T-18 and FR-TMO-2 say the same thing
- Notes: `WriteResponse` finds the idle time through an unexported-type method, so no exported signature changes

### [x] HRD-06 Invalidate only what exists
- Plan: v1.1 · Size: M · Depends on: none
- Finding: bodyless `POST`s to distinct URIs sent 4% of untouched entries back to the origin after 60 000, 35% after 190 000 more, 76% after 500 000 more
- Read: 01 FR-INV-1 to FR-INV-3, FR-PRG-7; 05 E-7, E-8; 06 T-29
- Touch: purge.go, internal/coalesce/table.go, internal/coalesce/table_test.go, flight.go, warm.go, purge_test.go, docs/01, docs/05, docs/06
- Tests: `TestInvalidationSkipsAbsentURI`, `TestInvalidationSkipsAbsentLocation`, `TestInvalidationCoversFetchInProgress` (on a flight, fetched directly), `TestInvalidationCoversWarm`, `TestInvalidationReachesOrphanVariant`, `TestInvalidationSeesOtherEnginesFetch`, `TestInvalidationUnconditionalWithKeyedInputs`, `TestInvalidationUnconditionalOnRemoteStore`, `TestInvalidationFloodLeavesCacheIntact`
- AC: D-H9 holds; 1 000 000 unsafe requests to URIs with no record send 0 of 2 000 stored entries back to the origin; a `GET` sent before an unsafe request and stored after it is invalid at its next lookup, whether it ran on a flight, directly or under `Warm`; a store error on the existence check writes the epoch
- Notes: proof of the skip. The engine reads the clock (the epoch time), then asks whether a fetch for the key is counted, then reads the key. A fetch sent before the epoch time was counted before it was sent. If it is no longer counted it has finished, and anything it stored is visible to the read. A fetch sent after the epoch time is not covered by the epoch at all (FR-PRG-7)

### [x] HRD-07 A cap per client
- Plan: v1.1 · Size: M · Depends on: HRD-03
- Finding: 1 100 connections to distinct slow paths: a legitimate miss is shed at once
- Read: 01 FR-LIM-3, FR-FAIR-1, FR-FAIR-3; 04 §8.2; 08 §2; 06 T-11
- Touch: request.go, config.go, internal/keys/classify.go, internal/keys/request.go, internal/limiter/limiter.go, internal/limiter/limiter_test.go, fetch.go, engine.go, weirhttp/convert.go, caddy/config.go, caddy/caddyfile.go, caddy/module.go, limit_test.go, docs/01, docs/04, docs/06, docs/08
- Tests: `TestClientFloodLeavesOthersServed`, `TestClientFloodWithoutCap`, `TestClientCapCountsOnlyOriginWork`, `TestClientOverCapNeverLeadsFlight`, `TestClientIPv6CountsPerPrefix`, `TestClientNotForwardedOnCacheableFetch`, `TestClientCapServesStale`, `TestRequestFromSetsClient`, `TestRequestForClient`, Caddyfile and module tests for `max_per_client` (the gate reuses the limiter, so `internal/limiter` gains no test)
- AC: D-H10 holds; with `MaxPerClient` 8, 1 100 requests from one client to distinct slow paths leave a miss from another client served; an empty `Client` is never capped; limiter memory stays O(`Max + MaxQueue`); `Client` never reaches the key, the forwarded request or an event
- Out of scope: X-Forwarded policy (HRD-10)
- Notes: over the M file count because one field crosses the library and both adapters; the Caddy half may split to HRD-07b

### [x] HRD-08 Close returns
- Plan: v1.1 · Size: S · Depends on: none
- Read: 01 FR-LCY-2; 04 §12
- Touch: engine.go, engine_test.go, docs/01, docs/04
- Tests: `TestCloseReturnsWhenOriginIgnoresContext`
- AC: D-H11 holds; the error wraps the context's error and names the goroutines still running; an origin that honors its context sees no change

### [x] HRD-09 Fewer shared locks on the hit path
- Plan: v1.1 · Size: M · Depends on: HRD-07
- Read: 01 FR-MR-1, FR-MR-2, NFR-5; 04 §8.2, §8.4
- Touch: internal/missrate/tracker.go, internal/missrate/tracker_test.go, internal/limiter/limiter.go, internal/limiter/limiter_test.go, engine.go, bench_test.go
- Tests: `TestTrackerHitOnUntrackedPartitionTakesNoCounter`, existing miss-rate and limiter tests, `BenchmarkServeHitParallel`, `BenchmarkLimiterReleaseFullQueue`
- AC: a hit takes the tracker's read lock only and never replaces a counter; a release with a full queue walks to the first waiter that can run and stops once no slot is free; `goBackground` takes a read lock; the hit path stays within the NFR-5 allocation budget; benchmark numbers before and after are in the pull request
- Notes: an anomaly needs misses, so a partition that only hits needs no counter

### [x] HRD-10 Forwarded fields, trailers and Max-Forwards
- Plan: v1.1 · Size: M · Depends on: HRD-07
- Read: 01 FR-FWD-3; 03 §5; 06 T-4
- Touch: weirhttp/origin.go, weirhttp/convert.go, request.go, internal/keys/classify.go, serve.go, weirhttp/handler_origin_test.go, weirhttp/weirhttp_test.go, examples/weirproxy/main.go, docs/01, docs/06
- Tests: `TestTransportOriginStripsForwarded`, `TestTransportOriginSetsForwarded`, `TestTransportOriginKeepsForwarded`, `TestResponseTrailersReachClient`, `TestHandlerOriginTrailers`, `TestMaxForwards`
- AC: D-H12, D-H13 and D-H24 hold; a cacheable fetch never carries an `X-Forwarded-*` field in any mode (INV-1); a stored response has no trailer; `weirproxy` uses `XForwardedSet`

### [x] HRD-11 Caddy placeholders read the forwarded request
- Plan: v1.1 · Size: M · Depends on: HRD-07
- Read: 08 §4, §6; 06 T-4, T-45
- Touch: caddy/origin.go, caddy/serve_test.go, caddy/warn.go, docs/08, docs/06
- Tests: `TestPlaceholdersReadForwardedRequest`, `TestForwardedReplacerLeavesClientAlone`, `TestPlaceholderHeaderWarning`
- AC: D-H14 holds; `{http.request.header.X}` after `weir` is empty for a header that is not forwarded, and `{http.request.uri}` is the rewritten URI; operator variables set before `weir` are still visible; the warning list keeps only what is still true
- Notes: check the pinned Caddy source for `PrepareRequest` before relying on it

### [x] HRD-12 Key generation for library snapshots
- Plan: v1.1 · Size: S · Depends on: none
- Read: 08 §3; 01 FR-SNP-1, FR-SNP-2; 06 R-3
- Touch: config.go or keygen.go (new), config_test.go, caddy/keygen.go, store/memory/store.go (comment), docs/01, docs/runbook.md
- Tests: `TestKeyGenerationChangesWithForwarding`, `TestKeyGenerationStableAcrossKeyOnlySettings`, the Caddy key-generation tests unchanged (the digest must not change, or every shared cache is purged on upgrade)
- AC: D-H15 holds; the Caddy adapter's hash is byte-for-byte what it was

### [x] HRD-13 Memory store limits
- Plan: v1.1 · Size: S · Depends on: none
- Read: 05 E-6, §5.1, §5.3
- Touch: store/memory/store.go, store/memory/epochs.go, store/memory/store_test.go, engine.go, loadtest or bench file, docs/05
- Tests: `BenchmarkEvictionPauseLargeShard`, `TestShards`, `TestNewEpochConfig`
- AC: D-H23 holds, with the benchmark result recorded in docs/benchmarks.md whichever way it decides

### [x] HRD-14 Requests with a body, hosts with an underscore
- Plan: v1.1 · Size: S · Depends on: HRD-03
- Read: 01 FR-FWD-4, FR-VAL-1; 06 T-5, T-6
- Touch: internal/keys/classify.go, internal/keys/host.go, internal/keys/classify_test.go, internal/keys/validate_test.go, weirhttp/convert.go, serve_test.go, testdata/fuzz seeds, docs/01, docs/06
- Tests: `TestGetWithBodyPassesThrough`, `TestGetWithBodyIsPass`, `TestRequestFromBodyOfGet`, `TestNormalizeHost`, a fuzz seed for the host
- AC: D-H16 and D-H17 hold; nothing from a request with a body is stored or served from the store; docs/06 §6 checklist answered in the commit
- Notes: security-sensitive (internal/keys)

### [x] HRD-15 Conformance and small behaviors
- Plan: v1.1 · Size: M · Depends on: HRD-04
- Read: 01 FR-SRV-3, FR-SRV-8, FR-STO-1 to FR-STO-10, FR-NEG-1, FR-LIM-5; RFC 9111 §4.3.4, §5.2.1
- Touch: conditional.go, fetch.go, serve.go, storable.go, negative.go, conditional_test.go, storable_test.go, negative_test.go, limit_test.go, docs/01, docs/04
- Tests: `TestNotModifiedWeakTagMismatchRefetches`, `TestRequestMaxAgeAndMinFresh`, `TestEncodingMismatchNotStored`, `TestNegativeHonorsNoStore`, `TestShedRetryAfterJitter`
- AC: D-H18 to D-H22 hold; docs/06 §6 checklist answered for the storability change

### [x] HRD-16 Documents for what is not fixed in code
- Plan: v1.1 · Size: S · Depends on: HRD-01 to HRD-15
- Touch: docs/06, docs/runbook.md, README.md, CHANGELOG.md, site content that restates any changed default
- AC: every "not changed in code" item above has a threat model row or residual and a runbook line; CHANGELOG lists every decision D-H1 to D-H24 under Added, Changed or Fixed

### [x] HRD-19 Performance pass
- Plan: v1.1 · Size: M, in parts a to f · Depends on: HRD-16
- Why: Ashwin asked for a performance review of the branch, a plan, the fixes and the numbers, in this pull request. Measured first, on the reference machine (M4 Pro), `main` against the branch after HRD-01 to HRD-16, six interleaved runs: `ServeHitSmall` 922 against 927 ns (no change), `ServeHitParallel/missrate-on` 931 against 367 ns, 12 allocations and 1 536 B per hit on both. `make load`: steady hits 578 k/s before, 1 308 k/s after; every scenario passes; GC is 0.94 µs per request, which is now 17% of the CPU a saturated hit costs, so allocation is the next cost to cut. `make cache-tests`: baseline unchanged.
- Review, with prejudice. What a memory profile of one hit says: the `keys.Classified` value escapes to the heap on every request (33% of the bytes), the forwarded header is built for every request although only a miss, a pass-through or a vary lookup reads it (27%), and the served header map is cloned from the entry (26%). Elsewhere: after any invalidation, every hit on an older entry takes the epoch table's lock once per tag to look for a hard epoch that almost never exists; the Prometheus observer resolves its counter by label on every request; `weirhttp.WriteResponse` deep-copies the header and, for a stored response the origin sent chunked, has no length to send, so every hit is written in flushed chunks; the Caddy adapter splits the host on every request to decide a warning it has usually already given.
- Read: 01 NFR-5, INV-1; 04 §3.1, §6.2; 05 §5.4; 07 §10
- Parts:
  - a. `Classified` stays on the stack for a hit. The request paths that hand it to another goroutine or to the origin take a heap copy first. AC: `go build -gcflags=-m` no longer reports it moved to the heap in `Serve`; a hit allocates one object and about 500 B less
  - b. The forwarded header is built when it is first needed (a miss, a pass-through, a vary spec), when no keyed header, keyed cookie or experiment makes the key depend on it. AC: for every request, classifying with the deferral and then building the forward equals classifying without it (property and fuzz test); a cacheable fetch never reaches the origin without its forward (a test that fails if the build is skipped)
  - c. Hard-epoch lookups are skipped when no hard epoch is newer than the entry. AC: after an invalidation, a hit on an older entry takes no lock in the epoch table
  - d. The Prometheus observer keeps the request counters it has resolved. AC: `Observe(EvRequest)` allocates nothing and makes no label lookup after the first event of an outcome
  - e. A stored response gets a `Content-Length` when the origin sent none, and `WriteResponse` copies header values without a deep clone. AC: a hit on a response the origin sent chunked is written with its length; the stored value slices are still never written (P4)
  - f. The Caddy adapter's host check returns at once after its warning or with `multi_host`
- Tests: `TestServeHitAllocations` (budget lowered), `TestDeferredForwardEqualsClassify`, `FuzzDeferredForward`, `TestCacheableFetchAlwaysHasForward`, `TestEpochHitTakesNoHardLock`, `BenchmarkObserveRequest`, `TestStoredResponseHasContentLength`
- AC: parts a to f hold; `benchstat` of `main`, the branch before this card and after it is in docs/benchmarks.md; `make load` and `make cache-tests` are rerun and recorded
- Review of this plan: pooling `Classified` instead of part a was rejected, because a recycled value that a background fetch still holds would hand one request's key to another. Part b first deferred the forward for every configuration; with a keyed header the key is computed from the forward itself (INV-1), so those configurations build it at once as before. Cutting the header clone (the third cost) was rejected: a served header map must be the caller's own (P4)

### [x] HRD-17 Adversarial review of the branch
- Plan: v1.1 · Size: M · Depends on: HRD-16
- AC: every probe from the 2026-10-13 review is rerun against the branch and its result recorded in the pull request; the card-reviewer agent has read the whole diff; every must-fix finding is fixed with a test; `make check`, `make vuln` and `make fuzz-short` pass

### [x] HRD-18 Release v1.1.0
- Plan: v1.1 · Size: S · Depends on: HRD-17 merged
- AC: docs/release.md steps 1 to 10 for `v1.1.0`, all four modules; `release-gate` green on the merged commit; the site and the `caddy-published` job name `v1.1.0`; the site is deployed
- Notes: cannot start before Ashwin merges the pull request (`main` is protected). Tags are immutable on the module proxy, so the rehearsal tag `v1.1.0-rc.1` comes first

### [x] HRD-21 Findings of the comparison round
- Plan: hardening · Size: M · Depends on: HRD-17
- Read: 01 FR-LIM-2, FR-LIM-3, FR-CB-4, FR-STO-9; 04 §8.2, §8.3; 06 T-11, INV-7, R-14; docs/11 §8
- Touch: internal/limiter/limiter.go, internal/breaker/breaker.go, fetch.go, serve.go, config.go, engine.go, caddy/{module,pool,config,caddyfile}.go
- Tests: `TestDefaultQueueHoldsExpiryBurst`, `TestMarkedURLUsesFreeSlots`, `TestHeadroomLeavesSlotsForOtherPartitions`, `TestHeadroomStrictCases`, `TestHeadroomDoesNotStarveOwnPartition`, `TestBurstBehindProbe`, `TestProbeWaitersBounded`, `TestStaleNotHeldBehindProbe`, `TestBreakerAllowOrWait`, `TestMaxObjectBytes`
- Decisions, each a default or behavior change made at the owner's request to fix the four findings (CMP-F1 to CMP-F4 in 21-comparison.md):
  - D-H25 (F1): `Limiter.MaxQueue` 1024 to 8192 and `MaxQueueWait` 2 s to 5 s. Origin concurrency is unchanged; clients wait longer before a 503. Stale serving stays off by default (D6).
  - D-H26 (F2): new `Limiter.PartitionHeadroom` (0: `MaxPerPartition`). Only a fetch under a hit-for-miss marker may pass its partition's cap, and only while that many slots stay free below the foreground reserve (32 of 64 in use at the defaults). Slots past the cap are counted apart, so they do not starve the partition's other requests. Rejected: letting every fetch borrow, which would have tripled what a cache-busting flood on one path takes from the origin (T-11, hard rule 12).
  - D-H27 (F4): a foreground request without a stale response waits for the half-open probe, bounded by `MaxQueueWait` and by `MaxQueue`/4 waiters. A follower's bound grows to `Timeouts.Origin + 2 × MaxQueueWait`.
  - D-H28 (F3): the default `Storable.MaxObjectBytes` stays 1 MiB. It is tied to the memory store's shard size (a tenth of a 16 MiB shard), and a larger default means fewer shards and more memory buffered per fetch for everyone. Checked again with the owner's leave to decide (2026-10-13) and kept: at 4 MiB the default 256 MiB store drops from 16 shards to 4 of 64 MiB, whose worst eviction pause is 2.6 ms against 0.5 ms (`BenchmarkEvictionPauseLargeShard`), and the buffering bound `MaxConcurrent × MaxObjectBytes` grows from 64 MiB to 256 MiB, the size of the store itself. That is a cost for every user to help sites with objects between 1 and 4 MiB, who can set the limit; objects above any default still miss. The Caddy adapter gets `max_object_bytes`, which it lacked. One fetch streamed to many waiting clients without storing it is not built: deferred as HRD-22.

### [x] HRD-23 Losses of the second comparison round
- Plan: hardening · Size: L · Depends on: CMP-03b (the harness that measures them)
- Read: 01 FR-LIM-3, FR-LIM-4, FR-CB-3, FR-CB-4, FR-SNP-2; 04 §8.2, §8.3; 06 T-11, T-20, T-33; docs/11 §8; CMP-F5 to CMP-F8 in 21-comparison.md
- Touch: internal/limiter/limiter.go, internal/breaker/breaker.go, background.go, fetch.go, serve.go, engine.go, config.go; then the adaptive limit and the snapshot loader
- Tests: `TestRefreshQueuesBehindForeground`, `TestRefreshBacklogBounded`, `TestOwedRefreshesAllRun`, `TestOwedRefreshHoldsNoFlight`, `TestBusyPathBorrowsIdleSlots`, `TestBusyPathYieldsUnderContention`, `TestBreakerRampsClosed`, `TestBreakerRampFailureReopens`, `TestBreakerQuietRampCloses`, `TestBreakerNoticesRecoveryWithinOpenFor`, `TestBurstRampsInAfterProbe`
- Decisions, each a requirement or default change the owner approved on 2026-10-13:
  - D-H29 (F5): every client's fetch, and every owed refresh, may borrow idle slots past `MaxPerPartition` under the headroom rule of D-H26. This reverses D-H26's rejection. One flooded path can hold 32 of 64 origin slots while the others are idle. Measured with the working tree: flood failures in `busting-flood` 8,520 to about 8,260 of 10,000, because the bystanders keep more than 32 slots busy and there is little to borrow. The loss in that scenario is the global limit of 64 against an origin that serves 128, which only an adaptive limit addresses (still to build under this card).
  - D-H30 (F6): a refresh that stale serving owes waits for a slot in a bounded backlog, behind every waiting client; early refreshes are still dropped. Measured: `purge-herd` tuned, URLs still stale 4 s after a soft purge 904 to 88 of 1,000 (240 refreshes a second, so all by about 4.2 s); `sync-expiry` tuned, 5,000 origin calls at 48 at once where it showed 272. One number gets worse and more honest: `origin-down` tuned, origin calls while down 144 to 434, because the refreshes that used to be dropped now reach the dead origin until the breaker opens (Varnish 464, nginx 548).
  - D-H31 (F7): `Breaker.MaxOpenFor` defaults to `OpenFor`, and closing is a ramp (each successful probe doubles the fetches admitted; closed at `MaxConcurrent`). The half-open give-up time is its own value, one minute.
  - D-H32 (F7): `Breaker.OpenFor` defaults to 1 s. Measured in `brownout`, default tier, requests failed after recovery of 8,000 and last failure: v1.1 100 to 3,980 and up to 14.8 s; fixed 5 s cadence 79 to 2,120 and up to 5.5 s; 1 s cadence 104 to 382 and up to 1.5 s. Origin calls during the bursts stay about 720 of 4,000.
  - D-H33 (F8): `memory.Config.SnapshotKeepFreshness` and Caddy's `snapshot_keep_fresh`, off by default. The loader skips its load-time invalid epoch only when the option is on, the file is marked, nothing was lost and the clock is not behind the file's; the writer leaves out entries any epoch covers, since soft and invalidating epochs cannot be written out. Measured through a real Caddy restart: 0 origin calls for 2,000 stored URLs, where the snapshot alone made 2,000. Price: a purge sent during downtime is not applied (T-33), so it stays opt-in. Tests: `TestSnapshotKeepsFreshness`, `TestSnapshotKeepLeavesOutInvalidatedEntries`, `TestSnapshotKeepFallsBack`, `TestRestartKeepsCacheWarmWhenAsked`, two fuzz seeds.
  - D-H34 (F5): `Limiter.AdaptiveMax` and Caddy's `limiter { adaptive_max }`, off by default (FR-LIM-8; closes D13 and OQ-3). Growth by an eighth per window under pressure, a quarter off on an origin failure or doubled fetch time, the failing level remembered for 10 s, back to `MaxConcurrent` after 10 idle seconds. Two things the first measurement taught: a fetch held back by the borrow margin must count as pressure, or a flooded path never moves the limit (it never fills it); and refused fetches must stay out of the latency baseline, or their speed makes every healthy window after them look slow. Measured, `busting-flood` with `adaptive_max 256` against an origin that serves 128: flood failures 3,856 to 4,793 of 10,000 (8,520 without), bystanders failed 0 to 17 of 1,000 (0 without; other caches 280 to 607), all failed 3,856 to 4,795 of 11,000 (no cache 6,535, Varnish tuned 3,125). `sync-expiry-capped`: 0 to 163 of 10,000 clients get the origin's 503 while the limit finds the cap. Tests: `TestAdaptiveGrowsWhileLimited`, `TestAdaptiveBacksOffAndRemembers`, `TestAdaptiveBacksOffOnLatency`, `TestAdaptiveOffAndIdleReset`, `TestAdaptiveGrowsForABorrowingPartition`, `TestAdaptiveBaselineIgnoresRefusals`, `TestAdaptiveLimitFindsOriginCapacity`.
- Not done here: the comparison tables still show v1.1.0. They are rerun and republished when v1.2 is released (11 §2 measures published modules); the figures above come from working-tree builds at this branch's commits, with temporary tiers for the two opt-in settings.

### [~] HRD-22 Share one over-size fetch among waiting clients
- Plan: hardening · Size: L · Depends on: none
- Notes: deferred. Clients of an object over `MaxObjectBytes` each fetch it (`oversize-herd` and `large-body-herd` in docs/benchmarks.md). Sharing needs a bounded broadcast buffer with a rule for slow readers and late joiners, or a store that streams. Needs a design first.

### [~] HRD-20 Coalescing across nodes and a local tier for remote stores
- Plan: v1.2 · Size: design first
- Notes: deferred. Needs its own spec section: a store-side lock with a fencing token and a bounded wait, and a small in-process tier that honors epochs. Trigger: a multi-node deployment reports origin load that scales with node count
