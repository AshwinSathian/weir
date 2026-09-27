# Phase 0 cards

Plumbing done in the planning session (2026-09-27): `go.mod`, `doc.go`, `Makefile`, CI workflow, `scripts/trace.sh`, `scripts/card.sh`, `scripts/section.sh`, `.claude/` hooks, skills and reviewer agent, `docs/progress/`. PLAN tasks P0.1, P0.7 and P0.8 are covered by that work; P0.0's remaining steps (SECURITY.md, public flip, v0.1.0) happen when M1 closes.

## Phase 0

### [x] P0-01 Public request, response and error types
- Plan: P0.2 · Size: S · Depends on: none
- Read: 01 §4 (up to the store paragraph); 04 §1.3
- Touch: request.go, errors.go, errors_test.go (all new)
- Tests: TestStatusCode (one row per error in 01 §4 table, including `context.DeadlineExceeded` 504 and `context.Canceled` 499), TestRetryAfter, TestErrorsIs (every wrapper type satisfies `errors.Is` with its sentinel)
- AC: `Request`, `Response`, `CacheInfo`, `FwdReason`, `StaleReason` and every error in 04 §1.3 exist with doc comments; `RetryError`, `RequestError`, `OriginError` unwrap correctly; `make check` passes
- Out of scope: Config (P0-03), store types (P0-02), any behavior
- Notes: this is the first PR, so it also proves CI. If CI fails for plumbing reasons, fix the plumbing in this card and say so in the LOG.

### [x] P0-02 Store types, observer and remaining public types
- Plan: P0.2, P0.3 · Size: S · Depends on: P0-01
- Read: 04 §2 (types only), §1.2, §9.1, §9.2 (kind names only); 05 §1; 01 §14.7 (Mode names)
- Touch: store/store.go, store/doc.go, observer.go, types.go (Purge, WarmStats, EngineStats, Mode, BreakerState), deps_test.go (all new)
- Tests: TestNoThirdPartyImports (NFR-6: `go list -deps -json ./...` shows only standard library and module packages), TestEntrySize
- AC: `store` imports only the standard library; all `EventKind` constants from 04 §9.2 declared; an unexported `emit(obs Observer, ev Event)` helper that is a no-op when `obs` is nil (the engine wraps it in P0-05); optional interfaces `store.Scrubber` and `store.Sizer` declared; `make check` passes
- Out of scope: codec (M1-08), any implementation
- Notes: `Entry.Size` formula is in 04 §2. Keep `EventKind` a `uint8` with a `String()` method; exporters use the strings as labels.

### [ ] P0-03 Config, defaults and validation
- Plan: P0.4 · Size: M · Depends on: P0-02
- Read: 04 §1.1; 01 §6, §5.19 (FR-LCY-1); 01 §14.8 (FR-MEM-1 for the store-size rule, implementation deferred)
- Touch: config.go, config_test.go (new)
- Tests: TestZeroConfigValid, TestInvalidConfigRejected (one row per FR-LCY-1 rule that does not need a store)
- AC: `applyDefaults` fills every default in 01 §6 except the store; validation errors wrap `ErrInvalidConfig` and name the field; header names canonicalized; `Storable.Statuses` rejects 206, 304, 500, 502, 503, 504; `make check` passes
- Out of scope: default store construction (P0-05 uses a no-op store, M1-09 swaps in memory), query pattern compilation beyond storing the strings (M1-05)
- Notes: every boolean must default to false (04 §1.1 note). Keep `Config` copying explicit: `New` must never retain caller slices.

### [ ] P0-04 Test origin
- Plan: P0.5 · Size: M · Depends on: P0-01
- Read: 07 §3; 07 §1 rules 2 and 4
- Touch: internal/testorigin/origin.go, origin_test.go (new)
- Tests: gate blocks until closed or ctx done; delay under synctest takes fake time; panic behavior panics; truncate fails body read at N bytes; `NewChecked` fails the test on over-concurrency (use a fake `testing.TB` to observe the failure)
- AC: API exactly as 07 §3; all waits durably blocking (channels, timers); `make check` passes
- Out of scope: anything in the engine
- Notes: partition for `MaxInflightPartition` is the request path. Use `synctest.Test` in every test here.

### [ ] P0-05 Engine skeleton and the single fetch function
- Plan: P0.4 · Size: M · Depends on: P0-03, P0-04
- Read: 04 §6.1, §6.7 (structure only), §12; 02 P3, P8; 01 §5.19
- Touch: engine.go, fetch.go, nopstore.go (internal to the package), engine_test.go (new)
- Tests: TestServePassThroughStub, TestCloseStopsGoroutines (FR-LCY-2 skeleton), TestServeAfterClose (ErrClosed)
- AC: `New` builds an engine with a no-op store when `Config.Store` is nil; `Serve` forwards the request through `(*Engine).fetch`, the only caller of `Origin.Fetch` (grep proves it); panics in `Origin.Fetch` become `*OriginError`; `Close` waits for `wg`; `make check` passes
- Out of scope: limiter, breaker, caching of any kind
- Notes: create engines inside `synctest.Test` bubbles and close them before the bubble ends (07 §1 rule 2).

### [ ] P0-06 Store conformance suite
- Plan: P0.6 · Size: M · Depends on: P0-02
- Read: 05 §2, §4.2, §8
- Touch: store/storetest/storetest.go, store/storetest/mapstore_test.go (new)
- Tests: every case in 05 §8 except `CodecRoundTrip` (added in M1-08) and the epoch cases' sketch-specific ones (`EpochNeverUnderInvalidates`, `EpochHardCap`, added in M1-10)
- AC: `storetest.Run(t, factory)` exists; a map-backed store in the test file passes all non-epoch cases; epoch cases are present and `t.Skip` when the store under test reports it does not support epochs through a small option; `make check` passes
- Out of scope: the memory store (M1-09)
- Notes: time-dependent cases take a `func(d time.Duration)` advance hook so the memory store can run them in synctest and remote stores with real sleeps.
