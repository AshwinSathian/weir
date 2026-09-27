# RFC: Weir build plan

> Status: approved for Phase 0 and Phase 1
> Scale: Epic
> Target start: 2026-09-28
> Created: 2026-09-27
> Author: Ashwin Sathian

This plan turns [docs/01-technical-spec.md](docs/01-technical-spec.md) into milestones, tasks and acceptance criteria. The unit of work for a Claude Code session is a task card in [docs/cards/](docs/cards/README.md); each card names the PLAN task it serves, and `/handoff` ticks a PLAN task when all its cards are done. Current state is in [docs/progress/STATUS.md](docs/progress/STATUS.md). It does not restate designs; each task points at the section that defines it. Work milestones in order. Within a milestone, tasks are ordered so each one leaves `go test -race ./...` green.

## Goals

Phase 1 is done when every criterion in [docs/07-testing-strategy.md §12](docs/07-testing-strategy.md) holds: the T6.1–T6.13 test matrix passes under `-race` 100 times in a row, every requirement ID is cited by a test, fuzz targets have run an hour each without crashers, load scenarios pass on the reference machine, and the root module has zero third-party imports.

Three months after start, the engine should be usable behind `examples/weirproxy` for a real site, and the Caddy adapter should be the next piece of work.

## Background

See [docs/00-design-doc.md](docs/00-design-doc.md) §2 and §5. The build is a solo project; estimates below assume part-time work of roughly 10 hours per week and are in working weeks at that pace.

## Non-goals

- A standalone proxy, WAF, bot filter, CDN, or per-client rate limiter ([01 §1.2](docs/01-technical-spec.md)).
- Range caching, trailer storage, RFC 9213 targeted fields, persistence across restarts in Phase 1.
- Any third-party dependency in the root module.
- Tuning for hit ratio beyond S3-FIFO defaults before trace data exists.
- Phase 3 experiment dimensions before the Caddy adapter ships.

## Architecture

See [docs/02-architecture.md](docs/02-architecture.md) (packages, ADRs), [docs/03-hld.md](docs/03-hld.md) (flows), [docs/04-lld.md](docs/04-lld.md) (types and algorithms). All file paths below are to be created.

## Alternatives considered

Recorded as ADRs in [docs/02-architecture.md §6](docs/02-architecture.md). The build order itself had one real alternative: following the seed's M1–M10 order literally, with key hardening in M7. Rejected because M1's key code would be rewritten in M7 and because the seed's own §7.3 forbids an unhardened key.

## Tradeoffs

- Building the store in-repo (D2) costs roughly two weeks versus wrapping otter, in exchange for understanding the eviction behavior and keeping the core dependency-free.
- Strict forwarding (D4) will surprise some origins. The cost is documentation and a `Forward.Allow` list; the benefit is that a whole class of poisoning bugs cannot happen.
- One engine per origin keeps the breaker and limiter simple, at the cost of one engine per site in multi-site deployments.

## Risks

| Risk | Likelihood | Impact | Score | Mitigation | Owner |
|---|---|---|---|---|---|
| Coalescing, limiter and SWR interactions produce deadlocks or goroutine leaks that only show under load | M | H | High | synctest deadlock detection on every engine test; goroutine-count check in load tests; `goroutineleak` profile in the load harness; FR-LCY-2 test | Ashwin |
| Strict forwarding breaks real origins in ways the docs do not anticipate | M | M | Medium | `examples/weirproxy` trial against a real site before M10 closes; README section on `Forward.Allow`; `EvNotStored` reasons make misconfiguration visible | Ashwin |
| S3-FIFO hit ratio worse than expected on real traffic | L | M | Low | Store interface allows swapping; capture a trace from the weirproxy trial and compare with an LRU baseline before Phase 2.5 | Ashwin |
| Hit-path latency misses NFR-5 because of header cloning and forwarded-header construction | M | L | Low | benchmarks from M1; budget revisited with data in M10 per NFR-5 | Ashwin |
| Scope creep into Phase 2 or 3 features during Phase 1 | M | M | Medium | CLAUDE.md forbids work outside the current milestone; new ideas go to Open questions | Ashwin |
| `synctest` limitations (mutex waits are not durable) make some concurrency tests flaky | L | M | Low | P8 channel-based waits; any flaky test is a bug, fixed or quarantined within the milestone | Ashwin |
| Caddy API drift before Phase 2 (`RegisterDirectiveOrder` is EXPERIMENTAL) | M | L | Low | 08 is a draft; re-verify at Phase 2 kickoff | Ashwin |

## Dependencies

- Upstream: Go 1.27 toolchain; golangci-lint v2; govulncheck; GitHub Actions (`actions/checkout`, `actions/setup-go`, `golangci/golangci-lint-action`); Node.js for the nightly `http-tests/cache-tests` job.
- Downstream: the BYOD custom-domain project (single Caddy node shared in Phase 2, D17).
- External: none in Phase 1. Phase 2 needs `xcaddy`. Phase 2.5 needs a Valkey server for tests (Docker).

## Phases and milestones

Each task has an acceptance criterion (AC) that is either true or false. "Tests" means the named tests in [07](docs/07-testing-strategy.md) exist and pass under `-race`.

### Phase 0: Skeleton (~1 week)

Goal: a repository where every later task only adds code.
Deliverable: compiling public API with stub behavior, CI, test harness.

- [x] P0.1 `go.mod` (`module github.com/AshwinSathian/weir`, `go 1.27`), `LICENSE` (Apache-2.0, already committed) and `NOTICE`, `.gitignore`, `doc.go`. AC: `go build ./...` succeeds with no `require` block.
- [x] P0.2 Public types from [04 §1](docs/04-lld.md) and [01 §4](docs/01-technical-spec.md): `Config` and sub-configs, `Request`, `Response`, `CacheInfo`, enums, errors, `StatusCode`, `RetryAfter`, `Observer`, `Event`, `Purge`, `WarmStats`, `EngineStats` and `Engine.Stats`. AC: `go doc` shows every exported identifier with a doc comment; `StatusCode` table test passes.
- [x] P0.3 `store` package types and interface ([05 §1](docs/05-storage-interface-spec.md)), `Entry.Size`. AC: compiles; `store` imports only the standard library.
- [ ] P0.4 `New` with defaults and validation (FR-LCY-1); `Serve` that validates nothing and calls the origin through the single fetch function in `fetch.go` (no limiter yet); `Close`. AC: `TestZeroConfigValid`, `TestInvalidConfigRejected` (one row per FR-LCY-1 rule), `TestServePassThroughStub`.
- [ ] P0.5 `internal/testorigin` per [07 §3](docs/07-testing-strategy.md). AC: its own tests cover gate, delay under synctest, panic, truncate, `NewChecked` failing on over-concurrency.
- [ ] P0.6 `store/storetest.Run` with all cases from [05 §8](docs/05-storage-interface-spec.md) (they will fail until M1 provides a store; the suite itself compiles). AC: compiles; a trivial map-backed store in `storetest`'s own test passes the non-epoch cases.
- [ ] P0.0 Private GitHub repo `AshwinSathian/weir` (created 2026-09-27); when M1 closes: add `SECURITY.md`, flip to public, enable private vulnerability reporting (D40), tag `v0.1.0` (D24). AC: repo visibility and reporting settings match the current phase.
- [x] P0.7 CI workflow: gofmt, vet, golangci-lint v2, `go test -race -shuffle=on`, dependency check (NFR-6) as a test (`TestNoThirdPartyImports` using `go list -deps -json`), govulncheck. AC: workflow green on the first push.
- [x] P0.8 `scripts/trace.sh`: extracts IDs (`FR-*`, `NFR-*`, `INV-*`) from `docs/01` and `docs/06`, greps `_test.go` for citations, prints uncited IDs, exits 0 (report mode) until `TRACE_STRICT=1`. AC: runs locally and in CI.

Exit criteria: CI green; `Serve` round-trips a request to `testorigin`.

### Phase 1: The engine (~14 weeks)

#### M1 RFC 9111 core and structural key hardening (~3 weeks)

Refs: FR-VAL-*, FR-KEY-1..6, FR-KEY-8, FR-KEY-12, FR-FWD-*, FR-STO-* (except Vary support), FR-FRS-1..4, FR-FRS-7, FR-SRV-*, FR-INV-1, FR-INV-3, FR-PRG-7, T6.9, T6.11.

- [ ] M1.1 `internal/httpcc` parsing, lifetime, heuristic, age ([04 §4](docs/04-lld.md)). AC: RFC table rows for §4.2.1–4.2.3 pass; `FuzzCacheControl`, `FuzzHTTPDate` exist with seeds.
- [ ] M1.2 `internal/keys` validation, host, path, query rewrite, cookies extraction for keyed cookies, `Accept-Encoding` normalizer, canonical encoding, tags, strict and all forwarding ([04 §3](docs/04-lld.md)). AC: `FuzzValidateRequest`, `FuzzHost`, `FuzzQueryRewrite`, `FuzzCookies`, `FuzzAcceptEncoding`, `FuzzKeyEncodingInjective`, `FuzzForwardEqualsKey`, `FuzzMalformedHeaderAbsent` exist with seeds and pass.
- [ ] M1.3 `store/memory` S3-FIFO with byte accounting, sharding, epoch sketch and hard-epoch table, `OnEvict` ([05 §4.4, §5](docs/05-storage-interface-spec.md)). AC: `storetest.Run` passes; `TestS3FIFOScanResistance`, `TestByteAccountingBound`, `TestShardDistributionAdversarial` pass.
- [ ] M1.4 `store/codec.go`. AC: `FuzzCodecRoundTrip`, `FuzzDecodeEntry` pass.
- [ ] M1.5 Engine: lookup, fresh hit, miss fetch and store (uncoalesced for now, via the single fetch function), storability, Age, `Cache-Status`, client conditionals (304), validation with 304 freshening, HEAD as GET, Range rules, `only-if-cached`, request `no-store`, unsafe-method URI invalidation with epochs compared by `RequestTime`, newest-response-wins store rule. Responses with `Vary` are not stored yet (`EvNotStored{vary-unsupported}`, removed in M7). AC: RFC behavior table in `rfc9111_test.go` passes for every row not tagged M5, M7 or M9; `TestCVE202435296`, `TestInvalidRequestsCostNothing`, `TestKettleUserAgent`, `TestFatGETBodyDropped`, `TestPurgeDuringInflightFetch`, `TestUnsafeMethodInvalidates` (URI part), `TestOriginClockSkewDoesNotStale`, `TestServedHeaderMutationDoesNotLeak` pass.
- [ ] M1.5b Upgrade/CONNECT rejection, event-stream handling, 302/307 storability, trace-header forwarding, default store sizing from `GOMEMLIMIT` (D27, D28, D29, D35, D39). AC: `TestConnectRejected`, `TestUpgradeRejected`, `TestEventStreamNeverBuffered`, `TestTraceparentValidated`, `TestRedirect302NeedsExplicitFreshness`, `TestDefaultStoreSizeFromMemLimit` pass.
- [ ] M1.6 `weirhttp` (`Middleware`, `Handler`, `TransportOrigin`, `HandlerOrigin`, `RequestFrom`, `WriteResponse`, `WriteError`, upgrade routing) and `examples/weirproxy`. AC: `TestAdaptersRouteUpgradesAround` passes; integration tests with `httptest.NewTestServer` inside synctest pass; weirproxy serves a hit for a second identical request.
- [ ] M1.7 Benchmarks `BenchmarkServeHitSmall`, `BenchmarkKeyBuild`, `BenchmarkAcceptEncoding`, `BenchmarkMemoryStoreGetParallel`. AC: numbers recorded in `docs/benchmarks.md` with machine and Go version.

#### M2 Coalescing (~1.5 weeks)

Refs: FR-COA-*, FR-STO-12, T6.2, T6.2a, T-19, T-31.

- [ ] M2.1 `internal/coalesce` table with aging and stream hand-off state. AC: component tests for join, aging replacement, publish-removes-only-current, claim/abandon exactly-once (run 1 000 iterations).
- [ ] M2.2 Engine coalesced fetch, detached flight context (values from creator, cancel from `Close`), follower wait, re-entry, direct fetch, hit-for-miss markers, `Authorization` rule, panic recovery. AC: `TestCoalesceColdKey1000`, `TestCoalesceCreatorCancel`, `TestCoalesceStuckLeader`, `TestCoalesceLeaderAging`, `TestCoalescePanic`, `TestUncacheableNotSerialized`, `TestAuthorizedNotCoalesced`, `TestMarkerNotFromAuthorizedRequest`, `TestMarkerNotFromRequestNoStore` pass.

#### M3 Jitter and early refresh (~0.5 week)

Refs: FR-FRS-5, FR-FRS-6, T6.1.

- [ ] M3.1 Jitter at store and freshen time. AC: `TestJitterSpread`, `TestJitterNeverLengthens`, `TestBatchWriteExpirySpread` pass.
- [ ] M3.2 Early refresh draw on fresh hits using background flights (the background class is a no-op `TryAcquire` until M4). AC: `TestEarlyRefreshProbability`, `TestEarlyRefreshSingleFlight` pass.

#### M4 Limiter, storage failure, warm (~2 weeks)

Refs: FR-LIM-*, FR-STF-*, FR-WRM-*, T6.3, T6.4, T6.5, T-18.

- [ ] M4.1 `internal/limiter` ([04 §8.2](docs/04-lld.md)). AC: `TestLimiterSkipsFullPartition`, `TestLimiterQueueTimeout`, `TestLimiterCancel`, `TestLimiterGrantRace`, `BenchmarkLimiterAcquireRelease` pass.
- [ ] M4.2 Wire limiter into the fetch function; streaming slot release at headers. AC: `TestLimiterCap5000Keys`, `TestPartitionFairness`, `TestSlowReaderDoesNotPinSlots`, `TestColdStartBounded` pass; `testorigin.NewChecked` used in all engine tests from here on.
- [ ] M4.3 Store guard with remote timeouts and store breaker ([04 §5.2](docs/04-lld.md)). AC: `TestStoreOutageStillCoalescedAndLimited`, `TestStoreSlowRemote` pass.
- [ ] M4.3b Upload pool and timeout split (D25, D26). AC: `TestSlowUploadsDoNotStarveMisses`, `TestBodylessBypassUsesMainPool`, `TestDripOriginReleasesSlot`, `TestStreamIdleTimeout` pass.
- [ ] M4.4 `Warm`. AC: `TestWarm`, `TestWarmDoesNotUseReserve` pass.

#### M5 Stale serving and circuit breaker (~2 weeks)

Refs: FR-STL-*, FR-CB-*, T6.6, T-16, T-20.

- [ ] M5.1 `internal/breaker`. AC: `TestBreakerOpensHalfOpenCloses` (component), `TestBreaker500DoesNotTrip`, `TestBreakerNeedsVolume` pass.
- [ ] M5.2 SWR serving with background refresh; SIE on every error condition; decision table 7.2 in full; operator default windows; `must-revalidate` 504. AC: `TestStaleIfErrorOnOriginDown`, `TestMustRevalidate504`, `TestDefaultStaleWindowsOff`, `TestLimiterShedsWithStale`, `TestRefreshNeverExceedsReserve` pass; RFC 5861 example rows pass.

- [ ] M5.3 Incident modes via `SetMode` (D33). AC: `TestModeExpires`, `TestModeStaleOnErrorLimits`, `TestModeBypass` pass.

#### M6 Negative caching (~0.5 week)

Refs: FR-NEG-*, T6.10, T-17.

- [ ] M6.1 Negative entries and synthesized responses. AC: `TestNegativeCacheBurst`, `TestNegativeNotFor500`, `TestNegativePrefersStale`, `TestNegativeScopedToKey`, `TestNegativeNeverReplacesResponse` pass.

#### M7 Vary, keyed headers and cookies, bypass (~2 weeks)

Refs: FR-KEY-7, FR-KEY-9..11, FR-BYP-1, FR-STO-6, T6.7, T-8, T-15.

- [ ] M7.1 Vary spec and variant records, `VaryAuto`/`VaryStrict`, sensitive names, variant cap, coalescing on variant keys. AC: `TestVaryUnconfiguredHeader`, `TestVarySensitiveNotStored`, `TestVaryStar`, `TestVaryOverflow`, `TestVaryFollowersRecoalesce` pass; the M1 `vary-unsupported` reason is removed.
- [ ] M7.2 `Key.Headers`, `Key.Cookies`, `Forward.Allow`, `Bypass`, `StripSetCookie`. AC: `TestForwardEqualsKey` covers each; `TestSetCookieNotStored`, `TestAuthorizationRules` pass.
- [ ] M7.2b `weir.TrackingParams` preset, stripped-cookie report, vary slot reclaim (D30, D31, D37). AC: `TestStrippedCookieReport`, `TestVaryReclaimsDeadSlots` pass.
- [ ] M7.3 Security review of `internal/keys` against [06 §6](docs/06-threat-model.md) checklist, written up in the PR. AC: checklist answered in the PR description; every T-1..T-8 test present.

#### M8 Miss-rate signal (~1 week)

Refs: FR-MR-*, T6.8, T-11.

- [ ] M8.1 `internal/missrate` Space-Saving with windows. AC: `TestSpaceSavingBound` passes.
- [ ] M8.2 Engine wiring and throttle callback into the limiter. AC: `TestRandomQueryFloodBounded`, `TestMissRateAnomaly`, `TestMissRateThrottle`, `TestOneHitWondersDoNotEvictHot` pass.

#### M9 Purge and Cache Groups (~1 week)

Refs: FR-PRG-*, FR-INV-2, FR-STO-10, T6.12, T6.13, T-10, T-23, T-25.

- [ ] M9.1 `internal/sfv` List-of-Strings parser. AC: `FuzzSFStringList` passes; RFC 9651 examples for lists of strings pass.
- [ ] M9.2 `Purge` (soft, hard, all, URLs, groups), `Cache-Groups` tags on stored entries, `Cache-Group-Invalidation` on unsafe responses, epoch overflow behavior. AC: `TestPurge5000KeysBounded`, `TestSoftPurgeServesStaleWhileRevalidating`, `TestHardPurgeIsMiss`, `TestSoftAfterHardStaysHard`, `TestGlobalEpochSoft`, `TestGroupsScopedByOrigin`, `TestGroupInvalidationIsSoft`, `TestInvalidationFloodBounded`, `TestUnsafeMethodInvalidates` (groups part) pass.

#### M10 Observability, performance, conformance (~2 weeks)

Refs: FR-OBS-*, NFR-*, seed §7.5.

- [ ] M10.1 Event catalog complete ([04 §9.2](docs/04-lld.md)); `Stats()`; one test per event kind asserting it fires. AC: `TestEveryEventKindEmitted` passes.
- [ ] M10.2 `observe/prom` module (own `go.mod`, added to `go.work`). AC: metrics from [04 §9.3](docs/04-lld.md) exported; `testutil` checks in its tests; root module still has zero third-party imports.
- [ ] M10.3 Load tests ([07 §9](docs/07-testing-strategy.md)) and nightly workflow. AC: all scenarios pass on the reference machine; results in `docs/benchmarks.md`.
- [ ] M10.4 Nightly fuzz and cache-tests jobs; `testdata/cache-tests-baseline.json`; `docs/cache-tests-expected-failures.md`. AC: both jobs green; every expected failure cites a decision ID.
- [ ] M10.5 NFR-5 measured; spec updated if the provisional budget was wrong. AC: `docs/benchmarks.md` has before/after `benchstat` for M1 to M10.
- [ ] M10.5b GC cost at 1M entries measured (D36). AC: `docs/benchmarks.md` reports GC CPU share and p99; if GC CPU exceeds 10% at 1M entries, a pointer-light layout task is added to Phase 1.x.
- [ ] M10.6 `TRACE_STRICT=1` in CI. AC: trace report empty.
- [ ] M10.7 README usage guide: quick start with `weirhttp`, the strict-forwarding explanation, every opt-out that weakens a default (R-3). AC: README reviewed against [06 §5](docs/06-threat-model.md).

Exit criteria for Phase 1: [07 §12](docs/07-testing-strategy.md).

### Phase 1.x: Post-M10 features (~5 weeks)

Refs: [01 §13](docs/01-technical-spec.md), decisions D11, D12, D15, D16, D18.

- [ ] M11 Single-range responses. AC: `TestRangeSingleFromCache`, `TestRangeUnsatisfiable416`, `TestRangeMultiOrInvalidGets200`, `TestIfRangeStrongOnly`, `TestRangeMissBackgroundFillBounded`, `FuzzRange` pass.
- [ ] M12 Targeted cache-control (`Weir-Cache-Control`, `CDN-Cache-Control`) with the stricter `private` rule. AC: `TestTargetedFieldPrecedence`, `TestTargetedFieldKeepsPrivate`, `TestWeirCacheControlStripped`, `FuzzSFDictionary` pass; RFC 9213 examples pass.
- [ ] M13 Memory-store snapshots. AC: `TestSnapshotRoundTrip`, `TestSnapshotLoadIsSoftStale`, `TestSnapshotHardEpochSurvives`, `TestSnapshotCorruptRecordsSkipped`, `TestSnapshotRespectsDeadline` pass.
- [ ] M14 Per-host fairness (limiter cap, per-owner quota). AC: `TestOwnerQuotaIsolatesTenants`, `TestPerHostLimiterCap` pass; defaults off in the library.
- [ ] M15 Eager hard purge via `store.Scrubber`. AC: `TestEagerHardPurgeDeletesAllPartitions`, `TestEagerSoftIsError`, `TestEagerUnsupportedStore` pass.

### Phase 2: Caddy adapter (~3 weeks)

- [ ] 2.1 Re-verify [08](docs/08-caddy-adapter-spec.md) against the current Caddy release; mark 08 v1.0. AC: every Caddy API named in 08 exists at the pinned version.
- [ ] 2.2 Module, Caddyfile parsing (required `name`), store pool, key-generation hash with global soft purge on change, per-host fairness defaults for multi-host sites, `nextOrigin`. AC: `caddytest` scenarios for T6.2, T6.6, T6.12 pass; reload test (100 warm keys survive a limiter change; a `forward.allow` change makes them revalidate; adding a host changes nothing); `xcaddy build` in CI.
- [ ] 2.3 Admin API purge (with `eager`), mode switch (`SetMode`) and stats; errors returned as `caddyhttp.Error` (D34), upgrades routed around, memory budget split (FR-MEM-1). AC: `TestRetryAfterSurvivesHandleErrors`, `TestMemorySizingSplit` pass; Prometheus metrics on Caddy's registry. AC: purge via admin endpoint changes the next `Cache-Status` to `fwd=stale`.
- [ ] 2.4 Single-node deployment guide for the BYOD instance (T-38), including snapshot path and shutdown grace period. AC: guide in `docs/runbook.md`.

### Phase 2.5: Valkey store (~3 weeks)

Goal: prove the store interface has no in-process assumptions and unlock multi-node deployments.

- [ ] 2.5.1 `store/valkey` module per [05 §7](docs/05-storage-interface-spec.md). AC: `storetest.Run` passes against Valkey in Docker in CI.
- [ ] 2.5.2 Engine test suite (T6.x matrix) re-run with the Valkey store, real time, via a build tag. AC: all pass; any failure is an interface bug fixed in [05](docs/05-storage-interface-spec.md) first.
- [ ] 2.5.3 Vary-spec compare-and-set via Lua. AC: 64 concurrent variant writers never exceed `MaxVariants`.
- [ ] 2.5.4 Eviction-storm review (seed T6.11) with a Valkey `maxmemory` experiment; `Scrubber` via background `SCAN`. AC: written up in `docs/09-research-notes.md`.
- [ ] 2.5.5 Multi-node Caddy guide (lifts D17). AC: purge on one node is observed on another in an integration test.

### Phase 3: Experiment dimensions (~4 weeks, after Phase 2.5)

Spec: [docs/10-experiments-spec.md](docs/10-experiments-spec.md) (decisions E1 to E7 locked). First task: finalize the spec's open items and write tests for T-35 and T-36.

## Testing strategy

[docs/07-testing-strategy.md](docs/07-testing-strategy.md). Rollout is by library version: `v0.x` tags per milestone from M1; `v1.0.0` at the end of Phase 1. Rollback is a version pin.

## Operations

- Observability: [04 §9](docs/04-lld.md).
- Alerts (recommended in README at M10): breaker open for more than 1 minute; shed rate above 1% of requests for 5 minutes; any `EvMissRateAnomaly`; store breaker open.
- Runbook: `docs/runbook.md` written in M10 (what each alert means and the first three things to check).
- New failure modes introduced by Weir itself: strict forwarding misconfiguration (visible as origins receiving fewer headers); breaker open (visible via `Cache-Status: detail` and metrics).

## Open questions

- [x] OQ-1 to OQ-4 and OQ-C1 to OQ-C3: resolved 2026-09-27 (decisions D11 to D24 in [01 §2](docs/01-technical-spec.md), E1 to E7 in [10](docs/10-experiments-spec.md)).
- [ ] Phase 3 open items in [10 §6](docs/10-experiments-spec.md). Owner: Ashwin. Target: Phase 3 kickoff.

## Appendix

Prior art and sources: [docs/09-research-notes.md](docs/09-research-notes.md).
