# Session log

Append-only. One entry per session, newest at the bottom. `/handoff` writes it; the SessionStart hook shows the last entry. Keep entries under ~25 lines: they are read at the start of every session.

Entry template:

```
## <date> · <card ID> · <outcome: done | split | blocked | review-fixes>
- Branch / PR: card/<id>-<slug> / #<n>
- Done: what now exists (files, behavior), in 1-4 bullets
- Tests: names added; `make check` result
- Deviations: doc changes made and why, or "none"
- Follow-ups: new cards or open questions, or "none"
- Context: rough share of the session budget used (low / medium / high) and whether the card size was right
```

## 2026-09-27 · planning · done
- Branch / PR: main (direct, setup session)
- Done: design documents 00-10, PLAN-weir.md, CLAUDE.md, task cards in docs/cards/, Makefile, CI, scripts (trace, card, section), `.claude/` hooks, skills and reviewer agent, go.mod and doc.go.
- Tests: none yet; `make vet fmt-check trace` pass on the empty module.
- Deviations: none
- Follow-ups: start P0-01.
- Context: n/a

## 2026-09-27 · P0-01 · done
- Branch / PR: card/P0-01-public-types / #1
- Done: errors.go (sentinels, `RequestError`, `OriginError`, `RetryError`, `StatusCode`, `RetryAfter`); request.go (`Request`, `Response`, `CacheInfo`, `FwdReason`, `StaleReason`).
- Tests: TestStatusCode, TestRetryAfter, TestErrorsIs; `make check` passes, trace 8/151.
- Deviations: 01 §4 and 04 §1.3 disagreed. 01 lists `ErrUpgradeNotSupported` (501, FR-UPG-1) and 04 did not, so 04 §1.3 now has it. 04 §1.3 also now states that context errors are checked before `ErrOrigin`, so an `*OriginError` wrapping `context.Canceled` maps to 499. The reviewer raised this; the spec left the order open.
- Follow-ups: none. CI proves itself on this PR; fix plumbing here if it fails.
- Context: low; size S was right.

## 2026-09-27 · P0-01 · review-fixes
- Branch / PR: card/P0-01-public-types / #1
- Done: adversarial review before merge. `StatusCode` and `RetryAfter` now classify by the outermost recognized error in the chain; `*OriginError` is always 502 and hides any weir error, context error or retry hint inside it.
- Tests: new rows in TestStatusCode and TestRetryAfter for the origin's own cancellation, `http.Client` timeouts, nested-engine errors, joined errors, rounding, overflow, typed nil; `make check` passes.
- Deviations: 04 §1.3 now states the chain rule and the `timeoutOrOrigin(err, ctx, tctx)` contract (flight cancelled by `Close` gives `ErrClosed`, not 499); 04 §6.7 builds `tctx` with `WithTimeoutCause(..., ErrOriginTimeout)`. This reverses the first handoff's "context errors before `ErrOrigin`": a false 499 drops a live client with no response.
- Follow-ups: whether a caller cancellation should count as an origin-health failure in §6.7 (`originHealth: true` on every error) belongs to the breaker card. CI runs one Go version from go.mod; D42's two-version matrix waits for the next Go release.
- Context: low.

## 2026-09-27 · P0-02 · done
- Branch / PR: card/P0-02-store-observer-types / #2
- Done: `store` package types, interface, optional `Scrubber`/`Sizer`, `Entry.Size`; `Observer`, `Event`, 19 `EventKind`s with `String`, nil-safe `emit`; `Purge`, `WarmStats`, `EngineStats`, `BreakerState`, `Mode`.
- Tests: TestEntrySize, TestNoThirdPartyImports, TestEventKindString, TestEmit; `make check` passes.
- Deviations: 04 §9.2 gains an `EvMode` row (FR-MODE-1 requires it; the catalog omitted it). Reason vocabulary is new and waits on Ashwin's approval.
- Follow-ups: none. Review should-fixes (sibling-module prefix escape, GOWORK=off) fixed.
- Context: low; size S was right.

## 2026-09-27 · P0-02 · review-fixes
- Branch / PR: card/P0-02-store-observer-types / #2
- Done: adversarial review before merge. `Entry.Size` now charges vary names and 56 bytes per variant ref (a vary spec with 8 refs was under-accounted by ~450 bytes, NFR-3). `emit` caps `Event.Partition` at 256 bytes and clones it so a retaining observer does not pin the request's path. `TestNoThirdPartyImports` also checks files under the `load` tag and that go.mod requires no module.
- Tests: new rows in TestEntrySize and TestEmit; deps test verified by mutation (a `load`-tagged third-party import and a sibling-module import both fail it; the first escaped the old test). `make check` passes.
- Deviations: 04 §2 Size formula extended (vary name bytes, 56 per variant ref).
- Follow-ups: typed-nil observer (P0-03), zero Kind/EpochMode rejection (M1-08), both in STATUS notes.
- Context: low.

## 2026-09-27 · P0-03 · done
- Branch / PR: card/P0-03-config / #3
- Done: config.go with `Config` and sub-structs from 04 §1.1, `VaryMode`, `ForwardMode`, and `prepareConfig` (copy slices, defaults per 01 §6 except the store, header canonicalization, FR-LCY-1 validation). Validation also rejects NaN/Inf floats, unknown mode values, non-token header names, a non-sf-token `CacheStatus` and a typed-nil `Observer`.
- Tests: TestZeroConfigValid, TestInvalidConfigRejected (38 rows), TestLimiterDerivedDefaults, TestConfigHeaderNamesCanonicalized, TestConfigCopiesCallerSlices, TestEmptyStatusesKept, TestReportStrippedCookiesNegativeDisables, TestNoJitterKeepsZero, TestShortOriginTimeoutClampsCoalesceDefaults; `make check` passes.
- Deviations: 01 §6 had four fields 04 §1.1 lacked (NoTraceHeaders, MaxUpload, StreamIdle, ReportStrippedCookies); added to 04. User decisions: `NoCacheStatus` bool replaces "empty CacheStatus disables" (01 §6, FR-SRV-9, 04, 06 T-27); negative ReportStrippedCookies disables. D29 reworded to match FR-FWD-6. Coalesce defaults clamp to a shorter origin timeout (reviewer finding; confirmation requested).
- Follow-ups: typed-nil Store in P0-05; unchecked limiter/breaker ranges in STATUS notes.
- Context: medium; card size right.

## 2026-09-27 · P0-03 · review-fixes
- Branch / PR: card/P0-03-config / #3
- Done: adversarial review before merge. Cookie names in `Key.Cookies` and `Bypass.Cookies` must be tokens: `a;b` would have split into an unkeyed extra cookie in the rewritten `Cookie` header (INV-1, T-3). `Key.AcceptEncoding` entries must be tokens because the chosen one is forwarded (T-13). `NoJitter` now forces `Jitter` to 0 over an explicit value. 1xx statuses rejected. Repeated names after canonicalization are dropped, first occurrence wins.
- Tests: new TestInvalidConfigRejected rows, TestConfigDuplicateNamesRemoved, stronger TestNoJitterKeepsZero; all failed before the fix. `make check` passes.
- Deviations: 04 §1.1 `New` paragraph describes dedupe and token checks.
- Follow-ups: `Forward.Allow: [Cookie]` or `[Authorization]` forwards unkeyed input; R-3 accepts operator risk, but New should warn like ForwardAll (P0-05).
- Context: low.

## 2026-09-27 · P0-04 · done
- Branch / PR: card/P0-04-testorigin / #4
- Done: `internal/testorigin` per 07 §3: routes and default behavior, Delay, Gate, Err, Panic, Truncate, BodyDelay, Func, SetDown, call counts, request snapshots, in-flight high-water marks (total and per path). `NewChecked` fails tb via Errorf and returns `ErrOverConcurrency` on an INV-7 breach.
- Tests: TestGateBlocksUntilClosed, TestGateUnblocksOnContextDone, TestDelayTakesFakeTime, TestBodyDelay, TestPanicBehaviorPanics, TestTruncateFailsBodyRead, TestNewCheckedFailsOnOverConcurrency (fake TB), plus behavior, Func, snapshot and MaxInflight tests; all under synctest. `make check` passes.
- Deviations: 07 §3 `NewChecked` takes `testing.TB`; `ErrDown` and `ErrOverConcurrency` sentinels documented.
- Follow-ups: in-flight counts exclude body reads (see STATUS note for M4-02).
- Context: low; size M was generous.

## 2026-09-27 · P0-04 · review-fixes
- Branch / PR: card/P0-04-testorigin / #4
- Done: adversarial review before merge. Response bodies now behave like `http.Client` bodies: reads fail once the Fetch context ends, `Close` unblocks a pending `BodyDelay` read, reads after `Close` return `ErrBodyClosed`. `Fetch` with a done context fails. `Route`/`Default` copy `Header` and `Body` so later test edits cannot race with engine goroutines.
- Tests: TestBodyCloseUnblocksAndFailsReads, TestBodyReadFailsAfterContextDone, TestFetchWithDoneContextFails, TestBehaviorCopiedOnSet; all failed before the fix. `make check` passes.
- Deviations: 07 §3 describes the body and copy semantics.
- Follow-ups: in-flight counts still end at Fetch return, while buffered fetches hold their slot through the body read (04 §6, FR-LIM-1); M4-02 decides whether the checker counts until body close for non-streamed fetches.
- Context: low.

## 2026-09-27 · P0-05 · done
- Branch / PR: card/P0-05-engine / #5
- Done: `Origin`/`OriginFunc`, `Engine`, `New` (nop store default, `store.Sizer` check, typed-nil `Store` rejected, forwarding warnings for `ForwardAll` and credential headers in `Forward.Allow`), pass-through `Serve`, `Close` with grace period then cancel, `goBackground`. `fetch.go` holds the only `Origin.Fetch` call; panics, `(nil, nil)` and bad statuses become `*OriginError`, origin timeout via `WithTimeoutCause`, streamed body keeps the timeout until closed.
- Tests: TestServePassThroughStub, TestServeOriginFailures, TestServeCallerCanceled, TestServeStreamBoundedByOriginTimeout, TestCloseStopsGoroutines, TestCloseRacingGoBackground, TestServeAfterClose, TestNewRejectsStore. `make check` passes.
- Deviations: 04 §6.7 now says statuses outside 200..999 (not only 1xx) become `*OriginError`. `safeFetch` returns plain errors and `timeoutOrOrigin(ctx, tctx, err)` wraps once; same result as the pseudo-code.
- Follow-ups: reviewer's must-fix (goBackground racing Close could panic the WaitGroup or outlive Close) fixed with a mutex and a race test.
- Context: medium; size M was right.

## 2026-09-27 · P0-05 · review-fixes
- Branch / PR: card/P0-05-engine / #5
- Done: adversarial review before merge. `fetch` zeroes `Cache` on origin responses (01 §4 says it is ignored; an origin could otherwise report `Hit: true`). A body-less response keeps `http.NoBody` instead of a wrapper, so adapters can still detect it.
- Tests: TestServeIgnoresOriginCacheInfo (failed before the fix), TestNewWarnsOnCredentialForwarding. `make check` passes; root package stable over 20 shuffled race runs.
- Deviations: 04 §6.1 documents `mu`, `closeDone` and the goBackground/Close locking rule.
- Follow-ups: `Close` does not wait for in-flight foreground `Serve` calls; once `Serve` uses the store (M1-12), closing an engine-owned store under them needs a decision.
- Context: low.

## 2026-09-27 · P0-06 · done
- Branch / PR: card/P0-06-storetest / #6
- Done: `store/storetest.Run(t, newStore, opts...)` with 14 cases from 05 §8 (not CodecRoundTrip, EpochNeverUnderInvalidates, EpochHardCap). Options `WithoutEpochs()` (epoch cases skip) and `Synctest()` (time-dependent cases run in their own bubble). An exact map store in mapstore_test.go passes all cases.
- Tests: TestRun, TestRunWithoutEpochs. Mutation checks: expiry ignored, last-epoch-only, exclusive since, least-severe mode, severe mode with another mode's At, entry mutation, Get after Close and no-op Delete each fail a case. `make check` passes.
- Deviations: 05 §8 and 02 §3 updated. `Synctest()` replaces the card's advance hook because `synctest.Test` forbids `t.Run` inside a bubble and stores read `time.Now` (D9). API approval is pending with Ashwin.
- Follow-ups: CLAUDE.md rule 6 vs 05 §8 real-clock sleep for remote stores (in STATUS). Open nit: `SetPastExpiresIsNoop` does not decide whether a past-Expires Set may drop an existing record (05 §2.3 is ambiguous).
- Context: low; size M was right.

## 2026-09-27 · P0-06 · review-fixes
- Branch / PR: card/P0-06-storetest / #6
- Done: adversarial review before merge. Removed three wrong trace citations from `TestRun` (FR-FRS-8, FR-INV-3, NFR-3 are not what the cases test; FR-PRG-7 stays). `EpochFastPath` queries the unset tag a minute after the store's base instant, where a sketch store cannot legitimately over-invalidate. `normalize` treats empty and nil maps and slices alike, so a codec may decode either. Fixed the revive lint failure that broke CI on the first push.
- Tests: suite rerun against a map store that rounds epochs up to whole seconds (as the sketch will): passes. `make check` passes.
- Deviations: none
- Follow-ups: none new.
- Context: low.

## 2026-09-27 · M1-01 · done
- Branch / PR: card/M1-01-directives / #7
- Done: `internal/httpcc` directives parser: `ParseResponse`, `ParseRequest`, `Seconds`. Comma scanner as `iter.Seq` with zero allocations; quotes open only at the start of an argument, and an unclosed quote rescans the line so later `private`/`no-store` still count. delta-seconds reject signs and non-digits, accept quoted form, clamp at 2147483648.
- Tests: `TestParseResponseDirectives`, `TestParseRequestDirectives`, `FuzzCacheControl` with 10 seeds (30 s fuzz clean). `make check` passes.
- Deviations: 04 §4.1 comment for `Duplicates` now names all four delta-seconds directives (matches FR-FRS-2); parser-rules line documents quote handling.
- Follow-ups: Pragma-with-Cache-Control question under Waiting on Ashwin; `unqualified no-cache` wording in FR-SRV-1/FR-STL-3 vs the RFC table.
- Context: low; size M was right.

## 2026-09-27 · M1-01 · review-fixes
- Branch / PR: card/M1-01-directives / #7
- Done: adversarial review before merge. Directive names now fold ASCII only: `strings.EqualFold` matched Unicode look-alikes, so `ſ-maxage=99999` (long s) set `s-maxage`, a name no RFC cache downstream recognizes. Package comment moved to `internal/httpcc/doc.go`.
- Tests: look-alike cases in both table tests; `FuzzCacheControl` now also checks that any prefix followed by `, no-store, private` still yields both (60 s clean); seed11. Removed test citations of FR-STO-4, FR-SRV-1, FR-SRV-6, FR-SRV-8, FR-STL-3, T-14, T-31: parser tests do not verify those; the engine cards will. `make check` passes.
- Deviations: none
- Follow-ups: none new.
- Context: low.

## 2026-09-27 · M1-02 · done
- Branch / PR: card/M1-02-lifetime / #8
- Done: `Lifetime` (s-maxage, max-age, Expires minus Date, heuristic), `Jitter`, `StaleWindows` in internal/httpcc/lifetime.go; `CorrectedInitialAge`, `CurrentAge` in age.go. Pure functions; results clamped to the delta-seconds ceiling, ages saturate.
- Tests: FuzzHTTPDate (6 seeds, also fuzzes Age), TestLifetimePrecedence, TestHeuristicLimits, TestCorrectedInitialAge, TestJitterNeverLengthens, TestJitterSpread, TestStaleWindows. `make check` passes; also passes under TZ=America/Los_Angeles.
- Deviations: 04 §4.2 names `httpcc.Config`, switches stale defaults to per directive, documents clamping and GMT-only dates. 01 FR-STL-2 reworded per directive (user approved). Review fixes: non-GMT dates rejected (host-TZ-dependent parse), CorrectedInitialAge saturates, operator defaults clamped, Jitter clamps its cut.
- Follow-ups: none new.
- Context: low; size M was right.

## 2026-09-27 · M1-02 · review-fixes
- Branch / PR: card/M1-02-lifetime / #8
- Done: adversarial review before merge. `parseDate` rejected valid RFC 850 `GMT` dates on hosts in Europe/London during summer (Go reports them as BST, +1h), so a correct `Expires` read as the past on those hosts only. It now picks layouts itself and requires a ` GMT` suffix for RFC 850. `Jitter` returned a hugely negative lifetime on amd64 for NaN `u` or infinite `frac` (Go's min/max propagate NaN); it now returns `lt` for any non-positive or NaN cut.
- Tests: TestParseDateIgnoresHostZone (swaps `time.Local` across 4 zones); NaN, negative and infinite inputs in TestJitterNeverLengthens. Fuzz 30 s clean. `make check` passes, also with GOARCH=amd64.
- Deviations: none. RFC 850 two-digit year pivot left as a `ponytail:` note.
- Follow-ups: none new.
- Context: low.

## 2026-09-27 · M1-03 · done
- Branch / PR: card/M1-03-evaluate / pending
- Done: `httpcc.Evaluate` and `State` in internal/httpcc/evaluate.go per 04 §4.3. Soft-purge staleness is computed as max(age - lifetime, now - purge time), equal to the pseudo-code and free of overflow. Negative stored lifetimes count as zero.
- Tests: TestEvaluate (20 rows: fresh, no-cache, SWR and SIE edges at ±1ns, soft purge before/at/after expiry and in the future, invalid, hard, unknown mode, non-applicable epoch, saturation). `make check` passes.
- Deviations: 04 §4.3 gains a `default: return Unusable` line; the reviewer found an unknown epoch mode failed open (T-9).
- Follow-ups: none new. M1.1 ticked in PLAN.
- Context: low; size S was right.
