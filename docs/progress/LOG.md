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
- Branch / PR: card/M1-03-evaluate / #9
- Done: `httpcc.Evaluate` and `State` in internal/httpcc/evaluate.go per 04 §4.3. Soft-purge staleness is computed as max(age - lifetime, now - purge time), equal to the pseudo-code and free of overflow. Negative stored lifetimes count as zero.
- Tests: TestEvaluate (20 rows: fresh, no-cache, SWR and SIE edges at ±1ns, soft purge before/at/after expiry and in the future, invalid, hard, unknown mode, non-applicable epoch, saturation). `make check` passes.
- Deviations: 04 §4.3 gains a `default: return Unusable` line; the reviewer found an unknown epoch mode failed open (T-9).
- Follow-ups: none new. M1.1 ticked in PLAN.
- Context: low; size S was right.

## 2026-09-27 · M1-04 · done
- Branch / PR: card/M1-04-validate / #10
- Done: new package internal/keys: `Request`, `Config`, `ErrUpgrade`, `RequestError` with the seven reasons (request.go); `Validate` per FR-VAL-1 with CONNECT and Connection-upgrade detection first (validate.go); host normalization per 04 §3.7 (host.go).
- Tests: TestValidateRejects (a row per reason), TestValidateAccepts, TestOptionsAsterisk, TestConnectRejected, TestNormalizeHost, TestValidateReturnsNormalizedHost, FuzzValidateRequest, FuzzHost with seeds. `make check` passes.
- Deviations: 04 §3.1 step 1 now says upgrade detection runs before validation. 04 §3.7 now covers the edge cases: port form, bracketed IPv4 rejected, IPv6 rewritten to canonical form, `a..` rejected so normalization is idempotent.
- Follow-ups: mapping keys errors to weir errors lands with Classify (M1-07).
- Context: low; size M was generous.

## 2026-09-27 · M1-04 · review-fixes
- Branch / PR: card/M1-04-validate / #10
- Done: adversarial review before merge. `FuzzValidateRequest` now also fuzzes the `Connection` value and checks that upgrade detection neither misses nor invents an upgrade (FR-VAL-4 wants every request-path parser fuzzed; `isUpgrade` had none), plus that `*` passes only with `OPTIONS`. Two new seeds. A mutation that drops the OWS trim fails the seeds.
- Tests: FuzzValidateRequest extended; 30 s fuzz clean; `make check` passes.
- Deviations: none.
- Follow-ups: FR-VAL-1 `#` question in STATUS "Waiting on Ashwin". Leaked `keys` errors would map to 502, not 400/501, until M1-07 wires the mapping (already in STATUS notes).
- Context: low.

## 2026-09-27 · M1-05 · done
- Branch / PR: card/M1-05-query-cookies-encoding / #11
- Done: `internal/keys` query rewrite on raw `&` segments with drop, keep, `prefix*` patterns and optional sort (`query.go`); keyed cookie extraction with conflicting or malformed duplicates treated as absent, plus the forwarded Cookie builder (`cookies.go`); Accept-Encoding bucket with integer qvalues, where malformed, oversized or non-ASCII input gives `identity` (`encoding.go`).
- Tests: TestRewriteQuery, TestParameterCloakingSemicolon, TestKeyedCookies, TestCookieHeader, TestAcceptEncodingBuckets, FuzzQueryRewrite, FuzzCookies, FuzzAcceptEncoding (seeds in testdata); 20 s fuzz each clean; `make check` passes.
- Deviations: none. Review nits applied: independent fuzz oracle with order check, 10 KiB and non-ASCII query seeds, comment on cookie value bytes.
- Follow-ups: Cookie size-limit question in STATUS "Waiting on Ashwin"; query pattern validation at `New` in STATUS notes.
- Context: low; size M was right.

## 2026-09-27 · M1-05 · review-fixes
- Branch / PR: card/M1-05-query-cookies-encoding / #11
- Done: adversarial review before merge. Probed cookie parsing (comma inside values, quoted and unquoted duplicates, case-changed or percent-encoded names, bare names), Accept-Encoding weights (`q=0.`, `q=1.`, repeated `q`, spaced `q =`, `x-gzip`, repeated `*`), query cloaking with `;` under keep and drop rules, and sort with duplicate names. No defect: every forwarded byte is keyed, and unkeyed cookie spellings (`LANG`, `l%61ng`) never reach the origin in strict mode. Pinned the edge behaviors as table rows. Default query path is 0 allocs; keyedCookies is 1 alloc.
- Tests: 9 rows added to TestKeyedCookies and TestAcceptEncodingBuckets; `make check` passes.
- Deviations: none.
- Follow-ups: under `ForwardAll` the raw Cookie header reaches the origin, so case-insensitive cookie frameworks (ASP.NET Core) see unkeyed `LANG=`; that is accepted risk R-3, worth a README line when M1-07 lands.
- Context: low.

## 2026-09-27 · M1-06 · done
- Branch / PR: card/M1-06-key-encoding / #12
- Done: `internal/keys/encode.go` (`PrimaryKey`, 04 §3.2 tagged length-prefixed encoding, `weir/key/v1`, pooled buffer, 0 allocs); `tags.go` (four tags per 04 §2); `path.go` (`normalizePath`, RFC 3986 §6.2.2.1-2, no dot-segment resolution, 0 allocs when canonical).
- Tests: TestKeyEncodingMatchesSpec, TestKeyEncodingInjective (adjacent pairs, `__`, empty vs absent, 127/128 and 10 KiB length prefixes), TestKeyedCookiesFeedEncoder, TestPrimaryKeyNoAllocs, FuzzKeyEncodingInjective (6 seeds), TestTagsMatchSpec, TestTagsDistinct, TestNormalizePath, TestNormalizePathNoAllocWhenCanonical, FuzzNormalizePath (5 seeds); `make check` passes.
- Deviations: added FuzzNormalizePath (hard rule 8). Malformed escapes leave the path unchanged so normalization stays idempotent; Validate rejects them anyway. No doc changes.
- Follow-ups: M1-07 wires normalizePath into key, forward and URI tag.
- Context: low; size M was right.

## 2026-09-27 · M1-06 · review-fixes
- Branch / PR: card/M1-06-key-encoding / #12
- Done: adversarial review before merge. Probed concurrent PrimaryKey/TagURI under -race (deterministic), escape edge cases (`%00`, `%7f%80%ff`, `%5C`, `%3b%3F%23`, mixed `.%2e`), tag namespace separation, and the cookie pairing contract. Defect: appendKey dropped cookies out of Key.Cookies order or outside it, which M1-07 would then forward unkeyed (poisoning). Unmatched cookies are now keyed after the config loop; valid inputs encode unchanged.
- Tests: TestEncoderKeysEveryCookieValue (3 rows); `make check` and CI pass.
- Deviations: 04 §3.2 gains the leftover-cookie loop and a note.
- Follow-ups: `/.%2e/x` normalizes to `/../x` (by spec: key equals forward, origin resolves); worth a README line with NormalizePath.
- Context: low.

## 2026-09-27 · M1-07 · done
- Branch / PR: card/M1-07-classify-forward / #13
- Done: `keys.Classify` (classify.go) builds `Classified` per 04 §3.1: method class, Unsafe, Head, Range, Authorized, HasBody, primary key, URI and origin tags, partition and hash, request directives, client conditionals. forward.go builds strict and ForwardAll headers (FR-FWD-1/2), removes hop-by-hop and Connection-named fields, and checks trace fields (FR-FWD-6). Pass-through keeps path, query and body, and tags the rewritten URI.
- Tests: FuzzForwardEqualsKey, FuzzMalformedHeaderAbsent (with seeds), TestTraceparentValidated, TestPassURITagUsesRewrittenQuery, TestAllowCannotForwardRawCookie, plus classify tables; `make check` passes.
- Deviations: 04 §3.1 defines `ClientConditionals` (If-None-Match bounded by MaxKeyedHeaderBytes) and `HasBody` (pass-through only, `http.NoBody` excluded); §3.5 pseudocode shows the real order and trace filtering.
- Follow-ups: INV-1 trace-field wording and a `tracestate` limit are waiting on Ashwin. Key.Headers, bypass and asRangePass remain for M7-03 and the engine.
- Context: medium; size M was right.

## 2026-09-27 · M1-07 · review-fixes
- Branch / PR: card/M1-07-classify-forward / #13
- Done: adversarial review before merge. Probed ForwardAll and strict-with-Allow on a GET with a body, Connection options naming framing fields, nil headers, multi-line and `*` If-None-Match, and partition truncation. Defect: a cacheable fetch (it has no body) still carried `Content-Length`, `Expect` and `Trailer`, and a raw `Host` field that disagreed with the normalized `Forwarded.Host` (P2). Those fields are now removed on every forward (`Host` on pass-through too).
- Tests: TestBodylessForwardHasNoBodyFields. FuzzForwardEqualsKey now adds a random canonical header name and checks that the client header is never mutated and that pass-through never forwards hop-by-hop fields or `Host`. Fuzzed 120s and 60s clean; `make check` passes.
- Deviations: 04 §3.1 and §3.5 list the removed fields.
- Follow-ups: TRACE and OPTIONS ignore `Max-Forwards` (RFC 9110 §7.6.2), and pass-through and ForwardAll forward `Proxy-Authorization`. Both are spec questions, recorded in STATUS.
- Context: low.

## 2026-09-27 · M1-08 · done
- Branch / PR: card/M1-08-entry-codec / #14
- Done: `store/codec.go` with `Encode` and `Decode(b, maxBytes)` per 05 §6. Decode rejects oversize input, then checks every length and header value count against the remaining bytes before use; unknown tags are skipped; duplicates, wrong fixed sizes, status over 999, unsorted or empty names and zero-count headers are errors wrapping `ErrUnavailable`. Output never aliases the input. Storetest gained `CodecRoundTrip`.
- Tests: FuzzCodecRoundTrip, FuzzDecodeEntry (no panic, allocation within 64x input + 64 KiB, re-encode stable), TestDecodeRejects, TestDecodeBoundsBeforeAllocation, TestDecodeSkipsUnknownFields, TestDecodeCopiesInput, TestEncodeRejectsUnrepresentable, TestCodecStripsMonotonic. Fuzzed 90s+60s decode and 45s+30s round trip clean; the fuzzer found an empty-body round-trip mismatch (fixed, seed kept). `make check` passes.
- Deviations: 05 §6 now gives the header name its length prefix (was ambiguous), the two signatures, the time-0 rule and the full decode error list.
- Follow-ups: API approval in STATUS; card-reviewer should-fix on amplification fixed by rejecting empty names and zero-count headers.
- Context: low; size S was right.

## 2026-09-27 · M1-08 · review-fixes
- Branch / PR: card/M1-08-entry-codec / #14
- Done: adversarial review before merge. Probed invalid kinds, extreme and epoch-instant times, non-UTC zones, case-distinct header names, non-minimal uvarints, negative `maxBytes` and worst-case amplification per field shape. Defect: `Encode` accepted kind 0 or above `KindNegative`, which `Decode` rejects, so a remote `Set` would succeed and every `Get` report `ErrUnavailable` (feeding the store breaker). `Encode` now rejects unknown kinds.
- Tests: TestEncodeRejectsUnrepresentable gains zero and unknown kind cases. `make check` passes.
- Deviations: 05 §6 lists the unknown-kind Encode error. The earlier "16-24x" amplification note was wrong: one-byte vary names reach 27x; STATUS now says under 32x (FuzzDecodeEntry asserts 64x).
- Follow-ups: Ashwin approved the codec API by asking for the merge. Case-distinct header names and non-minimal uvarints are accepted from the trusted store; revisit with the Valkey store.
- Context: low.

## 2026-09-28 · M1-09 · done
- Branch / PR: card/M1-09-memory-store / #15
- Done: `store/memory` (store.go, shard.go, fifo.go, ghost.go): maphash sharding with a per-process seed, byte-weighted S3-FIFO per shard, read-lock-only hits, oversize `Set` declined, `Bytes`, `MaxObjectBytes`, `OnEvict`. Engine default store is now the memory store; `nopstore.go` deleted.
- Tests: TestConformance (storetest, no epochs, synctest), TestS3FIFOScanResistance, TestByteAccountingBound, TestShardDistributionAdversarial, TestSetDeclinesOversize, TestGetHitTakesReadLock, TestExpiredGetUnlinks, TestGhostHitInsertsIntoMain, TestGhostBoundFollowsMain, TestNewConfig; TestNewRejectsStore covers the default store limit. `make check` passes.
- Deviations: none. E-11 retention clamp and epoch config fields left to M1-10 (05 §4.4 is its reading).
- Follow-ups: reviewer should-fixes applied (ghost-hit test; ghost trimmed when main shrinks). Nits applied: engine closes its own store when validation fails, citations. Open nits: `Shards` upper bound, ad hoc config errors, `MaxObjectBytes` margin (STATUS notes).
- Context: low; size M was right.

## 2026-09-28 · M1-09 · review-fixes
- Branch / PR: card/M1-09-memory-store / #15
- Done: adversarial review before merge. Probed config extremes, declined replacements, concurrent eviction under -race, hash cost (maphash.Comparable on a Key: 3 ns, 0 allocs) and whether the epoch stub is reachable (engine does not call the store before M1-12). Defects: `Shards: 1<<40` passed validation and killed the process with a fatal out-of-memory (NFR-2); now capped at `MaxShards` (1<<16). An oversize `Set` left the older record at that key in place, so a declined replacement kept serving the old version; it now deletes it.
- Tests: TestNewConfig gains the huge-shards case; TestSetDeclinesOversize covers the declined replacement; TestByteAccountingBound checks every hit returns the latest Set; new TestConcurrentEvictionInvariants walks every queue after a 16-goroutine run. Mutation checks: reverting the delete fails two tests. `make check` passes.
- Deviations: 05 §5.1 (Shards bound) and §5.3 (declined Set deletes the old record) updated, date bumped.
- Follow-ups: `Set` with a past `Expires` is still a no-op per 05 §2.3 and leaves an older record; harmless while the engine never writes one. Worst-case eviction walk noted in STATUS.
- Context: low.

## 2026-09-28 · M1-10 · done
- Branch / PR: card/M1-10-memory-epochs / #16
- Done: `store/memory/epochs.go`: global tag exact in atomics (E-5), hard epochs in a capped map with opportunistic pruning (E-6), soft and invalid sketch planes of `uint32` seconds rounded up (E-7), newest-epoch fast path (E-10). `MaxRetention`, `MaxHardEpochs`, `EpochSlots` config; retention clamp (E-11) stored on the node. `store.TagGlobal()` added; `keys.TagGlobal` delegates, so `store/memory` imports only `store` (02 §3).
- Tests: storetest `EpochNeverUnderInvalidates`, `EpochHardCap` (new `HardEpochCap` option); memory `TestInvalidationFloodBounded` (1M epochs, 0 allocs), `TestNewEpochConfig`, `TestEpochSketchRounding`, `TestGlobalHardEpochOutsideCap`, `TestHardEpochPrune`, `TestRetentionClamp`. Mutations (floor rounding, StoredAt clamp, no prune) each fail a test. `make check` passes.
- Deviations: 05 E-5, E-11, §5.1, §8 and 04 §2 updated. Ashwin approved the RequestTime clamp, `MaxEpochSlots`, `HardEpochCap` and moving `TagGlobal` into `store`.
- Follow-ups: card-reviewer nit left open: invalid-mode `SetEpoch` error wraps no sentinel.
- Context: medium; size M was right.

## 2026-09-28 · M1-10 · review-fixes
- Branch / PR: card/M1-10-review-fixes / #17 (#16 was merged before this review)
- Done: adversarial review of the merged epoch code. Two under-invalidation defects (E-8): seconds rounding added before dividing, so an epoch `At` saturated in the future (year 9999) overflowed into cell 1 and was lost for soft and invalid; an `At` saturated in the past (about 292 years before `New`, or zero) collided with the `noEpoch` sentinel and was lost in every mode, including the global tag. Rounding now divides first; offsets are lifted off the sentinel. 05 §5.4 now says hard epochs are pruned when a new tag finds the table full, which is what the code does.
- Tests: TestEpochSaturatedTimesNotLost (both directions, every mode, plain and global tag), TestEpochConcurrentRaise (synctest, 8 writers and 8 readers, readers never go backwards, final value is the max). Mutation checks: reverting either fix fails the new test. `make check` passes. card-reviewer re-review: findings fixed.
- Deviations: 05 §5.4 wording only.
- Follow-ups: none.
- Context: low.

## 2026-09-28 · M1-11 · done
- Branch / PR: card/M1-11-storability / #18
- Done: `storability` (FR-STO-1..9, reason plus `responseDriven` for T-31, D39 explicit freshness for 302/307, `vary-unsupported` until M7) and `buildEntry` (FR-STO-11 exclusions, FR-STO-13 Date, clipped header slices, jitter, stale windows, flags, tags, owner, retention with Keep and a 1 s floor). `normalizeResponse` canonicalizes origin header keys so a lowercase `set-cookie` or `cache-control: private` cannot skip storability (INV-4, card-reviewer finding). `httpcc.ParseDate` and `keys.DropHopByHop` exported for reuse.
- Tests: TestErrorStatusesNotStored, TestSetCookieNotStored, TestAuthorizationRules, TestNoExtensionBasedCaching, TestRedirect302NeedsExplicitFreshness, TestEntryHeadersClipped, TestStorabilityReasons, TestBuildEntry, TestRetentionFloorAndSaturation, TestOriginHeaderKeysCanonicalized. `make check` passes.
- Deviations: 04 §9 lists the defensive `method` not-stored reason. FR-STO-10 moved to M9-03 (needs the sfv parser).
- Follow-ups: review nits left open (invalid `Expires` counts as explicit freshness for 302/307; `Connection: Cache-Control` drops the stored Cache-Control while its directives still apply).
- Context: low; size M was right.

## 2026-09-28 · M1-11 · review-fixes
- Branch / PR: card/M1-11-storability / #18
- Done: adversarial review before merge. `responseDriven` was true under `Authorization` or request `no-store` whenever a response check (status, private, set-cookie) failed first; it relied on a second guard in the planned `fetch` (T-31). Now false inside `storability`. `normalizeResponse` wrote into the Origin's header map and value arrays (a data race for Origins that reuse headers) and merged in map order; now builds a new map, canonical-first then sorted.
- Tests: TestUnkeyedRequestNeverResponseDriven, TestNormalizeResponseLeavesOriginHeaderAlone (both failed before the fix). `make check` passes.
- Deviations: none.
- Follow-ups: FR-STO-5 with malformed `s-maxage` (Waiting on Ashwin).
- Context: low.

## 2026-09-28 · M1-12 · done
- Branch / PR: card/M1-12-serve / #19
- Done: serve.go (classification in `Serve`, lookup with epochs and markers, fresh hits, uncoalesced miss, store or hit-for-miss marker), respond.go (`fromEntry`, `finish`), cachestatus.go (RFC 9211 member, no `key`), fetch.go (buffered read up to `MaxObjectBytes`, over-size stream, origin header map cloned before Cache-Status).
- Tests: TestFreshHitNoOrigin, TestMissStoresThenHits, TestAgeHeader, TestCacheStatusNoKey, TestServedHeaderMutationDoesNotLeak, TestInvalidRequestsCostNothing, TestKettleUserAgent, TestFatGETBodyDropped, plus TestOversizedBodyStreamed, TestBufferedBodyFailures, TestHardPurgedEntryIsMiss from review. Two older tests adjusted (POST for streaming, Serve-set CacheInfo). `make check` passes.
- Deviations: none; stale entries refetch in the foreground until M1-13/M5 (`ponytail:`).
- Follow-ups: unowned events (STATUS notes). Reviewer nit declined: upgrades emit no `EvKeyRejected`, since its vocabulary is `RequestError.Reason` (04 §9).
- Context: medium; size M was right.

## 2026-09-28 · M1-12 · review-fixes
- Branch / PR: card/M1-12-serve / #19
- Done: adversarial review before merge. A hard-purged response still blocked the hit-for-miss marker, since `setMarker` saw it in the store; the purged entry may now be replaced. Probes with no findings: HEAD miss then GET hit (full body), nil request header, 50 concurrent hits mutating headers under `-race`, CRLF in `CacheStatus` (rejected by `New`), Range and conditionals not forwarded, store use after Close.
- Tests: TestHardPurgedEntryIsMiss extended (failed before the fix). `make check` passes.
- Deviations: none.
- Follow-ups: markers from other unkeyed forwarded inputs (Waiting on Ashwin).
- Context: low.

## 2026-09-28 · M1-13 · done
- Branch / PR: card/M1-13-revalidation / #20
- Done: conditional validation of stale entries with ETag/Last-Modified, 304 freshening into a new entry (headers merged except Content-Length, freshness and jitter recomputed), strong-ETag-mismatch retry without conditionals, client If-None-Match/If-Modified-Since 304s on hits with the RFC 9110 §15.4.5 fields (conditional.go, fetch.go, serve.go, respond.go).
- Tests: TestRevalidation304Freshens, TestStrongETagMismatchRetries, TestClientIfNoneMatch304, TestRevalidation304WithBody, TestStaleWithoutValidatorsRefetches; `make check` passes.
- Review fixes: a 304 with an over-size body was streamed to the client (now closed and freshened); a malformed If-None-Match let If-Modified-Since produce a 304 (keys now drops IMS whenever If-None-Match is present, RFC 9110 §13.1.3).
- Deviations: none. A 304 without Date dates the freshened entry at receipt (FR-STO-13), not with the stored Date.
- Follow-ups: marker after validation relies on read-before-write (STATUS notes).
- Context: medium; size M was right.

## 2026-09-28 · M1-13 · review-fixes
- Branch / PR: card/M1-13-revalidation / #20
- Done: adversarial review before merge. A body stored from an `Authorization` request lost the FR-STO-5 permission check when a request without `Authorization` freshened it, so a 304 dropping `public` still stored it; freshening now evaluates storability as authorized when the prior entry carries `FlagFromAuthorized`. Probes with no findings: epochs and soft purge through validation, HEAD validation, request no-store during validation, 304 with Vary or Set-Cookie (served, not stored), header canonicalization of the 304, single retry bound.
- Tests: TestFreshenKeepsAuthorizedRule (failed before the fix). `make check` passes.
- Deviations: none.
- Follow-ups: 304 relabelling Content-Encoding (Waiting on Ashwin).
- Context: low.

## 2026-09-28 · M1-14 · done
- Branch / PR: card/M1-14-head-range / #21
- Done: `only-if-cached` returns `ErrOnlyIfCached` (504) without an origin call; a Range request with no usable entry (miss or stale) passes through with Range and If-Range, unstored, no marker, via `keys.Classified.AsRangePass`; `HonorRevalidation` makes `no-cache`/`max-age=0`/`Pragma: no-cache` validate a fresh entry. HEAD-from-GET and request `no-store` already worked; now tested.
- Tests: TestHeadFromGetEntry, TestRangeGarbageNotPoisoning, TestOnlyIfCached, TestOnlyIfCachedBeatsNoCache, TestRequestNoStore, TestClientNoCacheIgnored, TestAsRangePass. `make check` passes.
- Deviations: FR-SRV-5 now forwards If-Range with Range (Ashwin approved, review finding); 04 §6.2 passes Range through whenever no entry answers, not only on `lk.entry == nil` (Ashwin chose FR-SRV-5 over the LLD); 04 §3.1 lists `AsRangePass`.
- Follow-ups: M5 must serve StaleSWR before the only-if-cached and Range checks.
- Context: low; size S was right.


## 2026-09-28 · M1-14 · review-fixes
- Branch / PR: card/M1-14-head-range / #21
- Done: adversarial review before merge. A fresh entry validated because of `HonorRevalidation` directives reported `fwd=stale`; it now reports `fwd=request` (RFC 9211 §2.2). Probes with no findings: HEAD Range body close cancels the origin timeout, hard-purged and marker keys under Range, only-if-cached with Range, request no-store with Range, Range plus client If-None-Match (dropped, 206 is valid), aliasing of the client's Range lines.
- Tests: TestClientNoCacheIgnored asserts `FwdRequest` (failed before the fix). `make check` passes.
- Deviations: none.
- Follow-ups: none.
- Context: low.

## 2026-09-28 · M1-15 · done
- Branch / PR: card/M1-15-invalidation-epochs / #22
- Done: unsafe-method invalidation of target URI and same-origin `Location`/`Content-Location` (purge.go, FR-INV-1/3); newest-wins read-before-write in `storeResponse`, exempting the record the request found; default memory store at 40% of `GOMEMLIMIT` clamped to [16 MiB, 8 GiB], else 256 MiB plus a warning (FR-MEM-1).
- Tests: TestUnsafeMethodInvalidates (URI part, incl. 303 and conditional revalidation), TestPurgeDuringInflightFetch, TestOriginClockSkewDoesNotStale, TestDefaultStoreSizeFromMemLimit, TestNewerResponseWins, TestNewerResponseWinsSkipsPurgedEntry. `make check` passes.
- Deviations: 04 §6.7 now states the found-record exemption from newest-wins (card-reviewer should-fix). Store write stays in serve.go, not fetch.go.
- Follow-ups: none new; notes in STATUS.
- Context: low; size M was right.

## 2026-09-28 · M1-15 · review-fixes
- Branch / PR: card/M1-15-invalidation-epochs / #22
- Done: adversarial review before merge. Zero-config `New` failed under `GOMEMLIMIT` below about 400 MiB (16 shards left a small queue under 1 MiB); `defaultShards` halves the shard count until one small queue holds `Storable.MaxObjectBytes`. Invalidation now emits `EvPurge{invalid}` (04 §9.2). Newest-wins ignores a record past its `Expires` that a lazy store still returns.
- Tests: TestDefaultStoreSizeFromMemLimit (zero Config at 1/40/200/399 MiB limits), TestUnsafeMethodInvalidates (event count), TestNewerResponseWinsSkipsExpiredRecord; each failed before its fix. TestNewRejectsStore now uses a 1 GiB object. `make check` passes.
- Deviations: 01 §6 `Store` default row and 05 §5.1 now describe the shard reduction; 04 §6.7 names the expired-record exemption.
- Follow-ups: none.
- Context: low.

## 2026-09-28 · M1-16 · done
- Branch / PR: card/M1-16-pass-through-upgrades-trace / #(filled after push)
- Done: event streams (FR-STR-1) — `fetch` detects `Content-Type: text/event-stream` or `Storable.StreamTypes` right after headers, before the buffered read, and streams immediately (never stored, no marker); user-approved fixes to two open questions — `internal/keys` now caps forwarded `Tracestate` at 512 bytes of visible ASCII combined across lines (W3C limit), dropping it alone and keeping `Traceparent` (FR-FWD-6); docs/06 INV-1 now lists `traceparent`/`tracestate`/`X-Request-Id` as unkeyed forwarded fields. CONNECT/upgrade rejection (FR-UPG-1) and traceparent validation (FR-FWD-6) were already implemented by prior cards; this card added engine-level regression tests and decided no event fires for the upgrade path (`EvKeyRejected`'s vocabulary is `RequestError.Reason` only, and adapters intercept CONNECT/upgrade before `Serve` in production).
- Tests: TestConnectRejected, TestUpgradeRejected, TestEventStreamNeverBuffered, TestTraceparentValidated (engine level, serve_test.go); new tracestate-cap cases in internal/keys' TestTraceparentValidated table. Review added: FuzzForwardEqualsKey now varies a second `Tracestate` line so its combined-length check gets multi-line fuzz coverage (existing seed corpus files updated for the new fuzz signature). `make check` and `make fuzz-short` pass.
- Deviations: docs/01 §14.5 (FR-FWD-6) states the tracestate cap; docs/06 INV-1 lists the three trace fields; docs/04 §1.1 `StorableConfig` now lists `StreamTypes`.
- Follow-ups: none new; two review nits left open in STATUS (StreamTypes branch has no test; upgrade-rejection tests don't assert event absence).
- Context: low; size S was right.

## 2026-09-28 · M1-17 · done
- Branch / PR: card/M1-17-weirhttp-adapter / #24
- Done: new `weirhttp` package: `RequestFrom` (raw `RequestURI`, absolute-form fallback), `WriteResponse` (clones header values), `WriteError` (499 writes nothing, `Retry-After`), `Middleware`/`Handler` (upgrades go to next or 501), `TransportOrigin` (`URL.Opaque`). `examples/weirproxy` smoke-tested: miss then `Cache-Status: Weir; hit`. `internal/keys.isUpgrade` became `IsUpgrade(method, header)` so the adapter and engine share one check (no logic change).
- Tests: TestPathForwardedByteExact, TestAdaptersRouteUpgradesAround, TestWeirhttpEndToEnd, TestWriteError, TestDefaultTransportDisablesCompression. A mutation run of each wire fix fails the tests. `make check` passes.
- Deviations: 04 §10 documents three net/http client hazards `Fetch` blocks: a `//` path via Opaque became `http://evil.example/x` with Host evil.example; the default User-Agent; transparent gzip. The default transport changed to set `DisableCompression`; this is in Waiting on Ashwin.
- Follow-ups: card M1-17b (`HandlerOrigin`, deferred because the card's AC and tests did not cover it). Two review nits are in STATUS.
- Context: medium; size M was right without HandlerOrigin.

## 2026-09-28 · M1-17 · review-fixes
- Branch / PR: card/M1-17-weirhttp-adapter / #24
- Done: an adversarial review, at the user's request, found and fixed two issues. `WriteResponse` now strips hop-by-hop response fields: misses and pass-through leaked the origin's `Keep-Alive` and `Connection`-named fields to clients. The default transport drops `ProxyFromEnvironment`, because with `HTTP_PROXY` set, `URL.Opaque` paths reached the proxy in origin form.
- Tests: TestWriteResponseDropsHopByHop, TestDefaultTransportIgnoresProxyEnv. `make check` passes.
- Deviations: 04 §10 documents both fixes.
- Follow-ups: two new open questions in STATUS (h2c upgrade returns 501; hop-by-hop stripping in the engine instead of each adapter).
- Context: low.

## 2026-09-28 · open questions · done
- Branch / PR: card/M1-17-weirhttp-adapter / #24
- Done: decided all 12 open questions at Ashwin's request, after the #24 adversarial review. Three were already built and are confirmed. The other nine became cards M1-17c (key boundary), M1-17d (storability) and M1-17e (responses), with reasons and rejected options in each card's Notes. M1-18 now depends on M1-17e.
- Tests: none (planning only)
- Deviations: none yet; each card changes its spec text with its code.
- Follow-ups: M1-17b, then M1-17c to M1-17e, then M1-18.
- Context: low.

## 2026-09-28 · M1-17b · done
- Branch / PR: card/M1-17b-handler-origin / #25
- Done: `weirhttp.HandlerOrigin` runs the handler on its own goroutine writing into an `io.Pipe`; `Fetch` returns at headers. Cancel or body close cancels the handler and fails reads and writes. Enforces `Content-Length`, refuses 204/304 bodies, discards HEAD bodies, maps panics and `runtime.Goexit` to `ErrOrigin`, passes https as `r.TLS`, closes the request body.
- Tests: TestHandlerOriginStreams (16 subtests), TestHandlerOriginCancel (4), TestHandlerOriginThroughEngine; `make check` passes.
- Deviations: 04 §10 extended with cancel, panic, Content-Length, 204/304/HEAD, trailer and parse-failure behavior. No requirement or signature changed.
- Follow-ups: context-values threat gap for the Caddy adapter (STATUS notes).
- Context: medium; two adversarial review rounds found a must-fix (truncated and 204/304 bodies stored). Size S was right for the code, tight for the review loop.

## 2026-09-28 · M1-17c · done
- Branch / PR: card/M1-17c-key-boundary / #26
- Done: `keys.IsUpgrade` serves a lone `h2c` Upgrade as a plain request; `Http2-Settings` joins `hopByHop`. `checkPath` rejects raw `#` and `?`, `checkQuery` raw `#` (`path`/`query` reasons). `keyedCookies` applies `MaxKeyedHeaderBytes` to the forwarded keyed pairs, not the raw lines.
- Tests: TestH2CUpgradeServedNormally (keys, weirhttp), TestH2CKeyedLikePlainRequest, TestH2CUpgradeNotForwarded, TestFragmentInTargetRejected, TestKeyedCookieLimitCountsKeyedPairs; FuzzValidateRequest gains an Upgrade argument (corpus extended, 6 seeds), FuzzCookies checks the limit (2 seeds), FuzzForwardEqualsKey covers the h2c shape (mutation-checked); `make check` passes.
- Deviations: FR-UPG-1, FR-VAL-1, FR-VAL-3 reworded as the card decided; FR-FWD-2 hop-by-hop list and 04 §3.1/§3.5 follow; 06 T-6, T-13, T-44 and 07 FR-UPG-1 row list the new tests.
- Follow-ups: none (large-Cookie benchmark noted for M1-18 in STATUS).
- Context: low-medium; size M was right. Reviewer found no must-fix; both should-fix (04 upgrade text, INV-1 fuzz gap) fixed.

## 2026-09-28 · M1-17c · review-fixes
- Branch / PR: card/M1-17c-key-boundary / #26
- Done: adversarial review found `GET http://example.com/a#x` bypassed the fragment rejection (`RequestFrom` used `EscapedPath`, which re-encodes `#` as `%23`). `RequestFrom` now cuts absolute-form targets after the authority from the raw bytes.
- Tests: TestRequestFromAbsoluteFormRaw, TestAbsoluteFormFragmentRejected, FuzzRequestFrom; `make check` and CI pass.
- Deviations: 04 §10 `RequestFrom` text, 06 T-6 test list.
- Follow-ups: absolute-form empty path (STATUS note).
- Context: low. Merged by the session at Ashwin's request.

## 2026-09-28 · M1-17d · done
- Branch / PR: card/M1-17d-storability / #27
- Done: an `Authorization` response needs `public`, `must-revalidate` or an `s-maxage` in a usable field (`validSMaxAge`, new `httpcc.ResponseDirectives.Unusable`). `keys.Classified.Unkeyed` (Allow field sent, or ForwardAll) clears `responseDriven`, so no marker. storetest `ExpiredIsNotFound` on the real clock runs only under `-tags integration`.
- Tests: TestAuthorizationNeedsValidSMaxage, TestNoMarkerAfterUnkeyedInput; `make check` passes, storetest passes with `-tags integration`.
- Deviations: FR-STO-5, FR-STO-12, 04 Classified and marker pseudocode, 05 §8, 06 T-8/T-31, 07 lists, CLAUDE.md rule 6. The Unkeyed flag lives in internal/keys (forward.go, classify.go), not serve.go as the card's Touch list said. Review should-fix applied: any invalid delta-seconds directive voids the s-maxage permission, not only duplicates.
- Follow-ups: M6 decides Unkeyed for negative entries; Key.Headers exclusion (STATUS notes).
- Context: low; size M was right.

## 2026-09-28 · M1-17d · review-fixes
- Branch / PR: card/M1-17d-storability / #27
- Done: adversarial review. Probed Unkeyed against Connection-named, hop-by-hop, dropped, trace-filtered and empty Allow fields, ForwardAll, Range pass and revalidation paths; s-maxage against quoted, overflowing, case-varied and multi-line forms. No code defects. Fixed a doc collision: 07's "Integration" tier (every `go test`) versus the new `integration` build tag; the Valkey planning card now requires `-tags integration` in CI.
- Tests: none added; `make check` and CI pass.
- Deviations: 07 §2 rows, docs/cards/20-later.md P25-00 AC.
- Follow-ups: none new.
- Context: low. Merged by the session at Ashwin's request.

## 2026-09-28 · M1-17d · review-fixes
- Branch / PR: card/M1-17d-storability / #27
- Done: second adversarial review before merge. Probed `Unusable` against equal and unequal duplicates, quoted, missing and clamped arguments, and invalid SWR/SIE beside a valid `s-maxage`; invalid `s-maxage` still suppresses operator stale defaults. Traced every marker write (only `serve.go` via `responseDriven`). Built and tested `store/...` with `-tags integration`. No code defects. Fixed a wrong comment in `forwardHeader`: the `Accept-Encoding` bucket is not in the primary key.
- Tests: none added; `make check` passes.
- Deviations: none.
- Follow-ups: STATUS note for M7 on markers planted through the unkeyed `Accept-Encoding` bucket. Correction: the previous entry says the session merged #27; it had not. This session merges it after CI.
- Context: low.

## 2026-09-28 · M1-17e · done
- Branch / PR: card/M1-17e-responses / (PR in next commit)
- Done: fetch.go strips hop-by-hop and `Connection`-named response fields once for every path (new FR-FWD-7), keeping the received header for storability. conditional.go: a 304 no longer overwrites `Content-Encoding`/`Content-Type`. httpcc `ParseRequest`: `Pragma` counts only without `Cache-Control` (RFC 9111 §5.4).
- Tests: TestFetchDropsHopByHop, TestConnectionNamedFieldsStillDecideStorage, TestFreshenDropsHopByHop, TestFreshenKeepsRepresentationMetadata, TestPragmaIgnoredWithCacheControl, httpcc table rows; `make check` passes, trace 79/152.
- Deviations: docs/01 FR-FWD-7 added, FR-SRV-3 and FR-SRV-8 reworded; docs/04 §6.7 and ParseRequest notes; docs/07 rows. Reviewer must-fix (strip before storability let `Connection: Cache-Control`/`Vary`/`Set-Cookie` make private responses shared) fixed by deciding storage on the received header.
- Follow-ups: none new.
- Context: medium; size M was right.

## 2026-09-28 · M1-17e · review-fixes
- Branch / PR: card/M1-17e-responses / #28
- Done: adversarial review found three more regressions from stripping before decisions: a `Connection`-named `Age` no longer aged the entry, a named `Content-Type: text/event-stream` was buffered until the origin timeout, and a named `Location` on a POST 201 no longer invalidated. `fetchResult.received()` now feeds storability, buildEntry, isEventStream and invalidate.
- Tests: TestConnectionNamedFieldsStillInform; TestFetchDropsHopByHop also checks the stored copy; `make check` passes; FuzzCacheControl 15 s, FuzzForwardEqualsKey 10 s clean.
- Deviations: FR-FWD-7 now says every engine decision reads the received response; docs/04 §6.7, docs/07 row.
- Follow-ups: none.
- Context: low.

## 2026-10-01 · M1-18 · done
- Branch / PR: card/M1-18-rfc-bench / #29
- Done: `rfc9111_test.go` RFC behavior table (62 rows; 57 run, 5 tagged M5/M7/M9 skip) over 07 §7's sections; `BenchmarkServeHitSmall`, `BenchmarkKeyBuild`, `BenchmarkAcceptEncoding`, `BenchmarkMemoryStoreGetParallel`; `docs/benchmarks.md` baseline (M4 Pro, Go 1.27.1).
- Tests: TestRFC9111, TestCVE202435296 (missing M1.5 AC test, no owning card); `make check` passes.
- Deviations: benchmarks live in three packages, not one `bench_test.go`, because two measure unexported code. PLAN M1.5 and M1.7 ticked: M1 is complete.
- Follow-ups: P0.0 (public repo, SECURITY.md, v0.1.0) waits on Ashwin; no card owns `BenchmarkServeHitVary` or `BenchmarkServeMissCoalesced`.
- Context: medium; size S was right. card-reviewer: 0 must-fix, 5 should-fix fixed (three rows could not fail), nits mostly fixed.

## 2026-10-01 · M1-18 · review-fixes
- Branch / PR: card/M1-18-rfc-bench / #29
- Done: adversarial review by mutation testing (40 hand mutants of the engine against `TestRFC9111`). Five survivors were real gaps and are now killed: a 302 stored through its validator, a conditional on a stored non-200, a 304 without `Date`, only-if-cached against a forced validation, and a vacuous cross-origin Location row (steps now take a `host`).
- Tests: rows added for the non-200 conditional and for only-if-cached under HonorRevalidation; 64 rows, 59 run; `make check` passes.
- Deviations: none.
- Follow-ups: the engine returns forwarded responses without `Date` (RFC 9110 §6.6.1 MUST; the wire is fine through net/http); CI lacks the D42 two-version matrix. Both are in STATUS.
- Context: low.

## 2026-10-01 · M2-01 · done
- Branch / PR: card/M2-01-flight-table / #30
- Done: `internal/coalesce` (doc.go, table.go): 64-shard flight table with aging (FR-COA-3), `Publish` that removes only the current flight, `Done` channel, stream claim/abandon CAS.
- Tests: TestJoinAndShare, TestAgingReplacesEntry, TestPublishRemovesOnlyCurrent, TestStreamClaimedOrAbandonedOnce, TestCreatorGone, TestStreamClosedWhenCreatorLeavesDuringPublish (1 000 iterations each for the races); `make check` passes.
- Deviations: card-reviewer found that 04 §6.4 could leak a stream when the creator times out between runFlight's `Publish` and its `CreatorIsGone` check. `CreatorGone` now returns whether the flight had published, and 04 §6.4 gained `leaveFlight`, which closes the stream in that case. 04 §8.1 and the date line updated. Internal signature only.
- Follow-ups: M2-02 implements `leaveFlight` as written in 04 §6.4. The table's size bound (MaxConcurrent + MaxQueue) comes from the limiter (M4) and needs a test there.
- Context: low; size S was right. card-reviewer: 0 must-fix, 1 should-fix fixed, 2 nits handled.

## 2026-10-01 · M2-02 · done
- Branch / PR: card/M2-02-coalesced-fetch / #31
- Done: flight.go (coalesced fetch, detached flight context with values from the creator and cancel only from `Close`, follower timeout to stale-if-error or direct fetch, stream hand-off via `leaveFlight`, panic recovery including body reads); serve.go split into `fetchStored` and `respond`; engine flight table. Authorized and marker requests fetch directly.
- Tests: TestCoalesceColdKey1000, TestCoalesceCreatorCancel, TestCoalesceStuckLeader, TestCoalesceLeaderAging, TestCoalescePanic, TestCoalesceSkipsDirectRequests, TestCoalesceCanceledByClose; TestNewerResponseWins now uses LeaderMaxAge 1s; `make check` passes.
- Deviations: 04 §6.4: on timeout without stale the creator keeps waiting on its own flight instead of refetching (default FollowerMaxWait equals Timeouts.Origin under 10 s; refetching doubled origin load). Followers unchanged (FR-COA-4).
- Follow-ups: M2-03 re-entry; M7-01 VaryMatches for followers; M4 table bound. In STATUS.
- Context: medium; size M was right. card-reviewer: 0 must-fix, 3 should-fix fixed, Vary nit commented, 2 nits noted in STATUS.

## 2026-10-01 · M2-02 · review-fixes
- Branch / PR: card/M2-02-coalesced-fetch / #31
- Done: adversarial review of #31. Fixed: an origin calling `runtime.Goexit` crashed the process from the flight goroutine (nil result in the deferred publish); followers that arrived after a purge got the pre-purge flight result (FR-PRG-7), now checked once before `Publish`; a `no-store` request could lead a flight whose result is never shared, making every follower wait and refetch (T-31), now fetched directly.
- Tests: TestCoalescePanic "Goexit in Fetch", TestCoalescePurgeDuringFlight, TestCoalesceNoStoreRequestNotLeader (each fails with its fix reverted); `make check` passes.
- Deviations: 04 §6.4 and §6.5 updated for the three fixes.
- Follow-ups: none new; follower sharing of `no-cache`/`max-age=0` responses left as is (common CDN practice; strict RFC 9111 §4 reading would stop it), noted in STATUS.
- Context: low.

## 2026-10-01 · M2-03 · done
- Branch / PR: card/M2-03-markers-reentry / #32
- Done: FR-COA-5 re-entry: a follower of an unshareable flight result (not storable, over-size, stream, purged) re-enters lookup once with `prevCK`, and the same key fetches directly. Markers, Authorization and request no-store rules were already in place from M2-02; this card pins them.
- Tests: TestUncacheableNotSerialized, TestAuthorizedNotCoalesced, TestMarkerNotFromAuthorizedRequest, TestMarkerNotFromRequestNoStore, TestOversizedStreamedNotBuffered, TestMarkerNeverReplacesResponse, TestCoalesceSharesNoCacheResponse; each guard checked by a mutant; `make check` passes.
- Deviations: 01 FR-COA-5 records that storable responses needing validation are shared (Ashwin's decision). 04 §6.2 direct-fetch condition aligned with the code (Authorized always direct, request no-store).
- Follow-ups: "background flights mark creator gone" moved to M5-02 AC; followers of a 5xx flight refetch until M5-03 (AC added); M7-01 re-entry key and attempt cap (`ponytail:`).
- Context: low; size M was generous, since M2-02 had built most of it. card-reviewer: 2 must-fix (test gaps) fixed, 3 should-fix fixed or moved to cards, 2 nits handled.

## 2026-10-01 · M3-01 · done
- Branch / PR: card/M3-01-jitter-early-refresh / #33
- Done: background.go: `maybeEarlyRefresh` on every fresh hit (FR-FRS-6 XFetch with Δ clamped to [1 ms, 10 s]), `backgroundRefresh` on a flight no request waits on (04 §6.8), stub `tryAcquireBackground`. Jitter (FR-FRS-5) was already wired in `buildEntry`; this card adds its engine test.
- Tests: TestEarlyRefreshProbability, TestEarlyRefreshDeltaClamp, TestBatchWriteExpirySpread, TestEarlyRefreshSingleFlight, TestEarlyRefreshGates; each checked by a mutant; `make check` passes.
- Deviations: 04 §6.8 now states the gates (NoEarlyRefresh, JitterMinLifetime, Authorization, request no-store). The stub acquire runs before Join, unlike 04 §6.8; M4-02 moves it (STATUS note).
- Follow-ups: FR-FRS-6 wording for the Authorization/no-store exclusion (ask Ashwin).
- Context: low; size S was right. card-reviewer: 0 must-fix, 4 should-fix fixed (gate tests, LLD line, STATUS note, bound in ponytail comment), 1 nit fixed, 3 nits noted.

## 2026-10-01 · M3-01 · review-fixes
- Branch / PR: card/M3-01-jitter-early-refresh / #33
- Done: adversarial review of #33. Fixed: every fresh hit paid a `Rand` draw and `math.Log` (+35 ns/op on `BenchmarkServeHitSmall`); hits with more than 37·Δ·β left now skip the draw, which can never trigger there (u >= 2^-53). Added tests for Close canceling a stuck refresh and for an event-stream refresh answer being closed (no creator claims it).
- Tests: TestEarlyRefreshShortcutIsExact, TestEarlyRefreshNoDrawFarFromExpiry, TestCloseCancelsEarlyRefresh, TestEarlyRefreshClosesUnclaimedStream; shortcut and CreatorGone mutants each fail a test. Forcing the default Rand to always trigger breaks no existing test (no flake exposure); 40 shuffled runs clean. `make check` passes.
- Deviations: none.
- Follow-ups: none new.
- Context: low.

## 2026-10-01 · M4-01 · done
- Branch / PR: card/M4-01-limiter / #34
- Done: `internal/limiter`: global cap read through `limit()` (D13), per-partition cap through `capFor`, foreground reserve, bounded FIFO queue that skips waiters of full partitions (ADR-8). Classes Foreground (waits MaxWait), Background (never queues), Warm (waits on ctx, respects reserve). Shed sentinels `ErrShed`, `ErrQueueFull`, `ErrQueueTimeout`.
- Tests: TestLimiterSkipsFullPartition, TestLimiterFIFO, TestLimiterQueueTimeout, TestLimiterCancel, TestLimiterGrantRace (1 000 shuffled rounds, both race outcomes hit), TestLimiterStateBounded, BenchmarkLimiterAcquireRelease (uncontended 272 ns/op, full queue 2.9 µs/op); 100% statement coverage; `make check` passes.
- Deviations: 04 §8.2 rewritten to the code (Config/New, slice queue, int counts, three sentinels, FIFO invariant, `throttled` deferred to M8); 04 §6.8 no longer mentions `TryAcquire`.
- Follow-ups: none new; M4-02 notes in STATUS.
- Context: low; size M was right. card-reviewer: 0 must-fix, 4 should-fix fixed (FIFO test, shed detail sentinels, LLD sync, queued benchmark), 4 nits noted for M4-02.

## 2026-10-01 · M4-01 · review-fixes
- Branch / PR: card/M4-01-limiter / #34
- Done: adversarial review of #34. Added a randomized model check (200 seeds, every class, short deadlines): inflight within Max and equal to the permits handed out, partition counts within the cap, no runnable waiter left queued. Documented in code and 04 §8.2 that raising `limit()`/`capFor` later must run the grant walk.
- Tests: TestLimiterInvariants; 8 hand mutants (no walk, LIFO walk, Warm ignores reserve, Warm times out, Background queues, no partition cap, race-grant leak, double release, waiter left queued) each fail a test. `make check` passes.
- Deviations: none.
- Follow-ups: a flood on one partition can fill the shared queue (FR-LIM-3 as written); a fix is proposed under Waiting on Ashwin, to land in M4-02 if approved.
- Context: low.

## 2026-10-01 · M4-02 · done
- Branch / PR: card/M4-02-limiter-fetch / #35
- Done: limiter wired into `fetch` (fetch.go, engine.go): slot from before the request until the buffered body or stream headers; shed maps to `*RetryError{ErrShed}` with `EvShed`; background refresh takes its slot inside the flight and drops with `bgDropped`/`EvRefreshDropped`; followers of a dropped background flight refetch as foreground. Limiter caps queued waiters per partition at `PerPartition` (approved change to FR-LIM-3, T-11). All engine tests use `testorigin.NewChecked`.
- Tests: TestLimiterCap5000Keys, TestLimiterShedsWithStale (shed half), TestPartitionFairness, TestSlowReaderDoesNotPinSlots, TestColdStartBounded, TestBackgroundRefreshDroppedWithoutSlot, TestBackgroundDroppedFollowerFetches, TestLimiterPartitionQueueCap; invariant check extended to `queuedBy`. Mutants (no partition queue cap, background as foreground, no bgDropped re-entry) each fail a test. `make check` passes.
- Deviations: 01 FR-LIM-3, 06 T-11, 04 §8.2 and §6.7, 07 T6.3 updated for the per-partition queue cap and test wording. Full-queue benchmark reworked (one waiter per partition), ~5 us per release.
- Follow-ups: `ReserveForeground >= MaxConcurrent` is accepted by config (STATUS note).
- Context: medium; size M was right. card-reviewer: 0 must-fix, 2 should-fix fixed (ctx check after grant, bgDropped follower test), 4 nits fixed or noted.

## 2026-10-01 · M4-02 · review-fixes
- Branch / PR: card/M4-02-limiter-fetch / #35
- Done: adversarial review of #35. The per-partition queue cap of `MaxPerPartition` shed most of a legitimate cold start on one path (2 000 cold `/product?id=N`: 32 served vs 656 uncapped); Ashwin chose max(`MaxPerPartition`, `MaxQueue`/4). Added the flight-table bound test that STATUS asked M4-02 for (`coalesce.Table.Len`, test export `Flights`).
- Tests: TestLimiterPartitionQueueCapQuarter, TestFlightTableBoundedUnderFlood; TestPartitionFairness now expects 41. The PerPartition-only mutant fails a test. 10 shuffled race runs of the limiter and coalescing tests clean. `make check` passes.
- Deviations: FR-LIM-3, T-11, 04 §8.2 and 07 T6.3 updated to the quarter cap (approved).
- Follow-ups: `MaxQueueWait` above `LeaderMaxAge`/`FollowerMaxWait` defeats coalescing under load (STATUS note).
- Context: low.

## 2026-10-01 · M4-03 · done
- Branch / PR: card/M4-03-upload-pool / #36
- Done: upload pool (engine.go `upl`, picked in `fetch` by `c.HasBody`, FR-LIM-7, T-39). Origin deadline is now an `AfterFunc` timer, so streams drop it at headers and use `idleBody` with `StreamIdle` per Read (FR-TMO-2); buffered bodies stay under `Timeouts.Origin` (FR-TMO-1, T-41).
- Tests: TestSlowUploadsDoNotStarveMisses, TestBodylessPassUsesMainPool, TestDripOriginReleasesSlot, TestStreamIdleTimeout; TestSlowReaderDoesNotPinSlots now expects streams to outlive `Timeouts.Origin`. `make check` passes.
- Deviations: 04 §14 records the upload pool's queue rules (01 says only "its own queue rules"); 04 §6.7 pseudocode and §14 timeouts bullet updated to the timer and idle reader.
- Follow-ups: PLAN M4.3b waits on M7-03 (`TestBodylessBypassUsesMainPool`).
- Context: low; size M was right. card-reviewer: 0 must-fix, 2 should-fix (04 §6.7, §14 wording) fixed, 4 nits left (deadline/idle boundary races on the real clock, EvShed pool name, Content-Length: 0 trust).

## 2026-10-01 · M4-03 · review-fixes
- Branch / PR: card/M4-03-upload-pool / #36
- Done: adversarial review of #36. Probed SSE idle and no total deadline, a consumer pausing 90 s between reads, caller cancel during a stream (499, not timeout), Close during a blocked Read, upload-pool shed, and Range pass (no body forwarded, main pool). All behaved. Mutants: the idle timer left armed after Read (counting consumer time) survived the card tests, because the test body ignored its context after a burst.
- Tests: TestStreamIdleCountsOnlyReads added; TestStreamIdleTimeout gains an event-stream row; `dripBody` fails reads once its context ends, like an http.Client body. Both idleBody mutants (no Stop after Read, no cancel on Close) now fail a test. 3 shuffled race runs of the limiter and timeout tests clean. `make check` passes.
- Deviations: none.
- Follow-ups: `Timeouts.Background` unused (STATUS note); adapter docs to require a write timeout (STATUS note).
- Context: low.

## 2026-10-01 · M4-04 · done
- Branch / PR: card/M4-04-store-guard / #37
- Done: storeguard.go: every engine store call goes through `storeGuard` (`Timeouts.Store` deadline for remote stores only, FR-STF-1; breaker after 5 consecutive failures, 1 s doubling to 30 s, FR-STF-2; `EvStoreError` per op and `EvStoreBreaker` open/closed). engine.go `store` field became `sg`.
- Tests: TestStoreOutageStillCoalescedAndLimited, TestStoreSlowRemote, TestEpochLookupErrorEmitsEvent, plus internal TestStoreGuardCallerCancelNotCounted, TestStoreGuardBackoff. Mutants (cancel counted, no 30 s cap, openTil kept on success) fail a test. Hit benchmark unchanged (847 ns, 14 allocs). `make check` passes.
- Deviations: 04 §5.2 records the error rule (05 S-3 plus caller-cancel exemption), no half-open probe, openTil reset. 07 T6.5 `TestStoreSlowRemote` wording changed from "each request" to "each store call" (a miss makes four).
- Follow-ups: per-request store budget question (STATUS note).
- Context: low; size S was right. card-reviewer: 2 must-fix (caller cancel opened the breaker; 04 vs 05 S-3 conflict) fixed, 4 should-fix fixed (openTil, local-deadline test, cap/consecutive test, 07 wording), 3 nits fixed.

## 2026-10-01 · M4-04 · review-fixes
- Branch / PR: card/M4-04-store-guard / #37
- Done: adversarial review of #37. Found `fails` growing without bound while the store stays down: an `atomic.Int32` wraps negative after 2^31 failed calls, after which the breaker never opens again. It now stops at the threshold. Stress probe (64 goroutines, 0 / 100 / 50 % failure phases, caller deadlines shorter and longer than `Timeouts.Store`): healthy phases always end closed with `fails` 0, a dead store always opens it. Open-breaker path costs 33 ns (141 ns at 12 cores, global mutex), 0 allocs; left as is.
- Tests: TestStoreGuardBackoff checks `fails` stays at 5 after 100 failures while open. `make check` passes.
- Deviations: none.
- Follow-ups (not fixed): `EvStoreBreaker` open is emitted again on each reopen with no `closed` between, and events are emitted after unlocking, so observers may see them out of order; a gauge must not assume open/closed pairs. Invalidation epochs (`setEpoch`) skipped while the breaker is open are lost with no event, as they were when the store failed (T-9 family). A corrupt remote record on a hot key counts as a store failure (S-3) and can help open the breaker in low traffic.
- Context: low.

## 2026-10-01 · M4-05 · done
- Branch / PR: card/M4-05-warm / #38
- Done: warm.go: `Engine.Warm` with `Warm.Concurrency` engine-owned workers, Warm-class slot acquired before the flight is joined, fetch via `runFlight` on its own goroutine (panic and Goexit safe). `fetchSpec.bg` became `class` plus `permit`; `fetch` takes a held permit; `limFor` picks the pool; `coalesce.NewFlight` for flights outside the table. A warm flight its caller leaves publishes `bgDropped`.
- Tests: TestWarm, TestWarmDoesNotUseReserve, TestWarmCanceledFollowerFetches, TestWarmJoinsRunningFlight, TestCloseDuringBlockedWarm, TestWarmOriginBodyPanic. Mutants (Warm as Foreground class, no bgDropped on cancel) fail a test. `make check` passes.
- Deviations: 04 §6.8a added (warm flow and counting rules); 04 §6.7 fetchSpec comment updated.
- Follow-ups: two questions in STATUS "Waiting on Ashwin" (01 wording for warm skips; ReserveForeground >= MaxConcurrent).
- Context: medium; size S was about right. card-reviewer: 2 must-fix (Close hung on a blocked iterator; direct path unrecovered panic) fixed, 4 should-fix fixed (priority inversion via queued warm flight, 30 s sleep, bgDropped untested, running-flight skip untested), nit on 04 comment fixed; 01 wording and queue-bound note left to Ashwin (STATUS).

## 2026-10-01 · M4-05 · review-fixes
- Branch / PR: card/M4-05-warm / #38
- Done: adversarial review of #38. Probe found `Close` during a long `Warm` let the workers walk every remaining URL (each `goBackground` failed fast), count them `Failed` and return nil. Workers now stop taking requests once `closed` is set and signal the feed loop; running fetches finish within the grace; `Warm` returns `ErrClosed`. Probes also checked: over-size and event-stream warm bodies are closed (2 of 2), a stale ETag entry revalidates with one 304 and is stored, a queued warm waiter does not block foreground behind it in the FIFO.
- Tests: TestWarmStopsOnClose (failed before the fix: 92 counted `Failed`, nil error). Race ×20 on warm tests, ×3 on the whole module. `make check` passes.
- Deviations: 04 §6.8a states the Close behavior.
- Follow-ups (not fixed): a warm call that joins another Warm's or a background refresh's flight that drops counts `Failed`, though it could fetch itself (background refresh only runs on fresh entries, which Warm skips, so the window is small). `Warm.Concurrency` above `MaxQueue` sheds the excess as `queue-full`; `New` does not cap it.
- Context: low.

## 2026-10-01 · M4-05 · review-fixes
- Branch / PR: card/M4-05-warm / #38
- Done: resolved the open questions (Ashwin delegated). 01 FR-WRM-1/2 state the not-sent and skip rules. `New` rejects `ReserveForeground >= MaxConcurrent` (it disabled background refresh and hung Warm) and `Warm.Concurrency` above `MaxConcurrent - ReserveForeground` (extra workers could only hold queue places); the default is lowered to fit, like the coalesce defaults. Warm looks up again after its slot arrives, and retries once when a flight it joined was dropped by another warm caller leaving.
- Tests: TestWarmConcurrencyDefault, TestWarmRetriesDroppedFlight, three TestInvalidConfigRejected rows; TestWarmDoesNotUseReserve now expects Skipped with one origin call. Mutants (no recheck, no retry) fail. Race x30 on warm tests, x2 on the module. `make check` passes.
- Deviations: 01 §5.16, FR-LCY-1 and the defaults table; 04 §1 WarmConfig comment and §6.8a.
- Follow-ups: none.
- Context: low.

## 2026-10-01 · M5-01 · done
- Branch / PR: card/M5-01-breaker / #39
- Done: `internal/breaker`: failure-ratio breaker over a 10-bucket rolling window with minimum volume, ±20% open jitter, doubling reopen capped at `MaxOpenFor`, half-open probes, nil-receiver safe. `Outcome` is Success, Failure or Status500 (counted only with `CountStatus500`).
- Tests: TestBreakerOpensHalfOpenCloses, TestBreaker500DoesNotTrip, TestBreakerNeedsVolume, TestBreakerOddWindow, TestBreakerProbeLifecycle, TestBreakerNilReceiver; `make check` passes.
- Deviations: 04 §8.3: `Probe{gen uint64}` replaces `{isProbe bool}` so a probe from an earlier half-open period cannot close or reopen a later one; adds `State()` and the Outcome rules.
- Follow-ups: reviewer must-fixes fixed (bucket misalignment for windows not dividing the epoch gap; divide by zero for Window < 10 ns). Open for M5-03: remaining open time accessor, background callers taking the probe, MaxOpenFor < OpenFor validation (STATUS notes).
- Context: low; size S was right.

## 2026-10-01 · M5-01 · review-fixes
- Branch / PR: card/M5-01-breaker / #39
- Done: adversarial review of #39. Found and fixed:
  - A probe that never ended in Record or Cancel held the breaker half-open forever (site-wide circuit-open). A half-open period now lasts at most `MaxOpenFor`, then a new one admits fresh probes.
  - `Config.Rand` was drawn on every Record. It is now drawn once per open.
  - Buckets used wall time, so a clock step could resurrect old outcomes or (before 1970) index out of range. They now use monotonic offsets from creation.
  - The trip check ran only on failures. A success that completes the volume now trips too (FR-CB-2 states a condition, not an event).
  - Doubling an enormous open period overflowed into the past. It now saturates.
  - The `openFor` reset on close was dead code and is deleted.
- Tests: TestBreakerLeakedProbeExpires, TestBreakerDrawsOnlyOnOpen, TestBreakerTripsWhenVolumeArrives, TestBreakerDoublingSaturates, TestBreakerConcurrentProbesBounded (64 goroutines, peak probes <= 3), TestBreakerCloseClearsWindow. 17 hand mutants, all killed. Race x5. `make check` passes.
- Deviations: 04 §8.3 states the half-open bound, the monotonic buckets, the per-outcome check and when rnd is drawn.
- Follow-ups: the wall-clock fix has no test (no injected clock, D9); it is structural. M5-03 items in STATUS stand.
- Context: low.

## 2026-10-01 · M5-02 · done
- Branch / PR: card/M5-02-swr / #40
- Done: serve.go serves a `StaleSWR` entry at once (`detail=stale-while-revalidate`) and starts one background refresh through the existing `backgroundRefresh` (creator gone, Background class, dropped without a slot). SWR answers only-if-cached and Range requests; `Authorization`/`no-store` requests serve stale without a refresh; honored `no-cache` validates in the foreground.
- Tests: TestSWRServesAndRefreshesOnce, TestSWRGates (5 rows), TestRefreshNeverExceedsReserve; TestBackgroundDroppedFollowerFetches renamed TestForegroundJoinsDroppedBackgroundFlight; RFC 5861 §3 row untagged with Cache-Status check. `make check` passes.
- Deviations: 04 §6.8 states the Authorization/no-store refresh gate for SWR.
- Follow-ups: `Timeouts.Background` unwired and the FR-STL-1 gate wording, both under Waiting on Ashwin. Reviewer should-fixes (Cache-Status, Range/only-if-cached rows, STATUS record) fixed.
- Context: low; size M was generous (most plumbing existed from M3-01/M4-02).

## 2026-10-01 · M5-02 · review-fixes
- Branch / PR: card/M5-02-swr / #40
- Done: adversarial review of #40. Found and fixed:
  - An SWR hit emitted no `EvStaleServed` (04 §12 lists reason `swr`). It does now.
  - A refresh that loses the race with `Close` published `ErrClosed` silently. It now emits `EvRefreshDropped{closed}` (04 §12), for SWR and early refresh alike.
  - No test covered the SWR refresh's conditional request or the 304 freshen. An RFC 5861 §3 row does now.
- Tests: TestSWRRefreshDroppedOnClose; TestSWRServesAndRefreshesOnce counts EvStaleServed. 9 hand mutants (gates, forced validation, detail, ttl sign, refresh, validators, CreatorGone, class), all killed. Probed and found correct: HEAD SWR (refresh is GET), unsafe-method invalidation (never SWR, FR-STL-5), `s-maxage` with a default SWR window (no stale), window end, Close during refresh. `make check` passes.
- Deviations: none.
- Follow-ups: a refresh whose response is unstorable (origin switched to `no-store`) leaves the stale entry in place, by storeResponse's existing rule, so it is served until the SWR window ends and each request in the window refreshes (one flight at a time). RFC 5861 allows it; noted in STATUS.
- Context: low.

## 2026-10-01 · M5-03 · done
- Branch / PR: card/M5-03-stale-if-error / #41
- Done: breaker wired into `fetch` (Allow before the limiter, Cancel on shed or caller gone, Record at headers or after a buffered body, foreground-only probes, background dropped with `EvRefreshDropped{circuit-open}`, `EvBreakerState` plus a log line). `onFetchError` applies 01 §7.2: stale-if-error with reasons `sie`/`shed`/`circuit-open`, `ErrMustRevalidate`, 5xx pass-through. Flight followers get the flight's buffered 5xx instead of refetching. `breaker.Remaining()` gives the Retry-After hint (floor 1 s).
- Tests: TestStaleIfErrorOnOriginDown, TestMustRevalidate504, TestDefaultStaleWindowsOff, TestBreakerOpensHalfOpenCloses (engine), TestFollowersShareFlight5xx, TestFollowersDoNotReadCreatorResponse, TestInvalidatedEntryNotServedStaleOnError, TestBreakerCountsBodyFailures, TestBreakerHalfOpenRetryHint, TestBreakerRemaining; stale half of TestLimiterShedsWithStale; RFC 5861 §4 rows and the must-revalidate row untagged. `make check` passes.
- Deviations: 04 §8.3 adds `Remaining`, the 1 s floor, foreground-only probes and body-failure recording; 04 §6.7 notes the buffered record point.
- Follow-ups: negative entries from §7.2 in M6-01. Reviewer must-fix (followers read `fr.resp`, a data race) fixed test-first; should-fixes (half-open hint, body failures, FR-STL-5 engine test) fixed.
- Context: medium; size M about right, touched flight.go, engine.go and internal/breaker beyond the card's list.

## 2026-10-01 · M5-03 · review-fixes
- Branch / PR: card/M5-03-stale-if-error / #41
- Done: adversarial review of #41. A follower that served stale on coalesce timeout emitted no `EvStaleServed{coalesce-timeout}`; fixed and asserted in TestCoalesceStuckLeader. 01 FR-COA-5 said followers re-enter on an unstorable response, which conflicted with sharing a 5xx (card AC, 04 §6.6); Ashwin approved amending FR-COA-5.
- Tests: probed and found correct: HEAD followers of a 5xx, event-stream 5xx with stale-if-error, Warm while open, must-revalidate with breaker open, client deadlines not tripping the breaker. 5 shuffled `-race` runs clean; `make check` passes.
- Deviations: 01 FR-COA-5 amended.
- Follow-ups: none.
- Context: low.

## 2026-10-01 · M5-04 · done
- Branch / PR: card/M5-04-incident-modes / #42
- Done: mode.go: `SetMode` (ttl in (0, 24 h], `EvMode` and a log line on change and on expiry), `currentMode` on an `atomic.Pointer[modeState]`, `staleOK` widening stale-if-error under `ModeStaleOnError` (staleness in [0, 24 h], no hard or invalidating epoch, none of must-revalidate, proxy-revalidate, no-cache, s-maxage) for `onFetchError` and `staleOnTimeout`. `ModeBypass` forwards through `pass()` with the new `keys.Classified.AsBypass()` (request as received, FR-FWD-3).
- Tests: TestSetModeRejectsInvalid, TestModeExpires, TestModeStaleOnErrorLimits, TestModeBypass (asserts the forwarded request), TestModeBypassThroughBreaker, TestModeStaleOnErrorFreshForcedValidation, TestCoalesceStuckLeader "stale-on-error mode" subtest. `make check` passes.
- Deviations: 04 §14 modes bullet now names `staleOK`, `staleOnTimeout`, `AsBypass` and the full flag list.
- Follow-ups: FR-MODE-2 and s-maxage under Waiting on Ashwin. Reviewer must-fix (bypass sent the normalized cacheable forward) and should-fixes (fresh entry served on forced validation, missing tests, LLD drift) fixed.
- Context: low; resumed an uncommitted start from an earlier session. Touched internal/keys and coalesce_test.go beyond the card's list.

## 2026-10-01 · M5-04 · review-fixes
- Branch / PR: card/M5-04-incident-modes / #42
- Done: adversarial review of #42. An `only-if-cached` request in bypass went to the origin; FR-SRV-6 always honors it, so it now gets `ErrOnlyIfCached`. FuzzMalformedHeaderAbsent failed on `main`: its oracle measured the raw Cookie line, but FR-VAL-3 counts the keyed pair after OWS trimming. The oracle is fixed and the seed is kept.
- Tests: TestModeBypass checks only-if-cached; TestModeExpires checks expiry at exactly ttl; TestSetModeRejectsInvalid adds `ModeBypass+1`. 16 hand mutants: 15 killed, 1 survives (a non-CAS expiry can emit duplicate EvMode under a race; cosmetic). Race tests with shuffle passed 5 times; every fuzz target ran clean, keys cookie target for 60 s. `make check` passes.
- Deviations: 04 §14 notes only-if-cached in bypass.
- Follow-ups: FR-MODE-3 vs FR-SRV-6 wording, and stale-on-error reach under default retention, under Waiting on Ashwin.
- Context: low.

## 2026-10-01 · M6-01 · done
- Branch / PR: card/M6-01-negative-caching / #43
- Done: negative.go (`setNegative`, `healthStatus`, `retryAfter`, `fromNegative`): a foreground 502/503/504 or transport failure on a key with no response writes a status-only entry for `Negative.TTL`; hits get a synthesized response with `detail=negative`. Marker and negative writes share `setUnlessResponse`.
- Tests: TestNegativeCacheBurst, TestNegativeFromTransportFailure, TestNegativeNotFor500, TestNegativePrefersStale, TestNegativeScopedToKey, TestNegativeNeverReplacesResponse, TestNegativeReplacesExpiredRecord. `make check` passes.
- Deviations: 01 FR-NEG-4 lists the extra exclusions (request no-store, unkeyed forward, background and warm), 04 §6.6 moves the write from `onFetchError` to `fetchStored`, 06 T-31 drops "markers also". FR-NEG-4 needs Ashwin's approval (STATUS).
- Follow-ups: reviewer should-fix (an expired record a lazy store returns blocked the negative write) fixed test-first. FR-NEG-4 approval and FR-NEG-3 `ttl` wording under Waiting on Ashwin.
- Context: low; size S right. Touched four existing test files to disable negative caching where a test expects repeated origin calls.

## 2026-10-01 · M6-01 · review-fixes
- Branch / PR: card/M6-01-negative-caching / #43
- Done: adversarial review of #43. Hard rule 8 gap: the origin `Retry-After` parser had no fuzz target; added `FuzzRetryAfter` with seeds. The served `Retry-After` now counts down from the origin's value (rounded up) instead of repeating it for the whole TTL. 01 §7.2's "negative entry" note and 04 §6.6 match FR-NEG-4.
- Tests: TestParseRetryAfter, FuzzRetryAfter (30 s clean), TestNegativeRetryAfterAges. 10 hand mutants of `setNegative`/`retryAfter`: 7 killed, 3 equivalent survivors (guards duplicated downstream). Negative, coalesce and stale tests passed 30 times under race and shuffle. `make check` passes.
- Deviations: none beyond the doc alignment above.
- Follow-ups: Accept-Encoding bucket note for M7-01 in STATUS; FR-NEG-4 and FR-NEG-3 wording still under Waiting on Ashwin.
- Context: low.

## 2026-10-02 · M7-01 · done
- Branch / PR: card/M7-01-vary-variants / #44
- Done: internal/keys/variant.go (`VaryNames`, `VariantKey`, FR-KEY-11 normalizer). Two-level lookup through the vary spec; variant then spec writes with a `MaxVariants` cap (expired refs dropped); flights, refresh, warm, markers and negatives key on `lk.ck`; followers of another variant re-enter once and coalesce on their own key. Storability: `vary-star`, `vary-too-many`, `vary-sensitive`, `vary-strict`; `vary-unsupported` removed.
- Tests: TestVaryUnconfiguredHeader (auto), TestVaryStar, TestVaryFollowersRecoalesce, TestVaryNormalizesValues, TestVaryTooManyNames, TestVaryNegativeStaysInVariant, TestWarmFollowerOfOtherVariant, TestVaryPolicyStorability, TestVaryNames, TestVariantKey, FuzzVaryNames (8 seeds); RFC row "Vary selects the stored variant" untagged. `make check` passes.
- Deviations: 04 §3.3 names kept in canonical case, not lowercase; 04 §6.4 follower check compares variant keys; 04 §6.7 marker and negative writes keep a live spec and go to `lk.ck`.
- Follow-ups: reviewer should-fixes 1, 2, 4 fixed (sensitive/strict refusal pulled from M7-02, pool buffer cap, 10 KiB seed); finding 3 (normalized key vs raw forward) under Waiting on Ashwin.
- Context: medium; size M right, slightly over by pulling M7-02's cap and policy checks.

## 2026-10-02 · M7-01 · review-fixes
- Branch / PR: card/M7-01-vary-variants / #44
- Done: adversarial review of #44. Found a cache-poisoning path: a request key in non-canonical case (`x-custom`, library callers) reached the origin under `ForwardAll` while `VariantKey` read `X-Custom` as absent, so the origin's answer to it was served to every client without the field. Same root cause let a lowercase `accept-encoding` or `connection` slip past the bucket rewrite and hop-by-hop removal. `keys.Classify` now canonicalizes request keys first (`keys.CanonicalHeader`, moved from engine.go, also used for responses).
- Tests: TestVaryNonCanonicalRequestKey, TestClassifyCanonicalizesRequestKeys (both fail without the fix). Vary/warm/coalesce/negative/RFC tests 15× under race and shuffle; FuzzForwardEqualsKey, FuzzValidateRequest, FuzzVaryNames, FuzzMalformedHeaderAbsent, FuzzKeyEncodingInjective 20 s each, clean. BenchmarkServeHitSmall ~910 to ~985 ns/op, 14 allocs unchanged. `make check` passes.
- Deviations: 04 §3.1 documents the canonicalization.
- Follow-ups: FR-KEY-11 normalized key vs raw forwarded lines (quoted strings with commas included) stays under Waiting on Ashwin.
- Context: low.

## 2026-10-02 · M7-01 · review-fixes
- Branch / PR: card/M7-01-vary-variants / #44
- Done: decided the open FR-KEY-11 question (delegated by Ashwin): `VariantKey` keys Vary values exactly as forwarded (line count plus each line length-prefixed) instead of a generic list normalization that equated renderings the origin can tell apart.
- Tests: TestVaryKeysExactLines, TestVariantKey (two lines vs one, quoted comma, moved line boundary), RFC row "values match only as forwarded". Both fail on the normalizing code. FuzzVaryNames 20 s clean. `make check` passes.
- Deviations: 01 FR-KEY-11 rewritten, 04 §3.3, 06 T-15, 07 §7 wording; dates bumped.
- Follow-ups: none from this PR. Older Waiting on Ashwin items untouched.
- Context: low.

## 2026-10-02 · M7-01 · review-fixes
- Branch / PR: card/M7-01-vary-variants / #44
- Done: decided every Waiting on Ashwin item (delegated). Code: `timeoutFor` (fetch.go) applies `Timeouts.Background` to background refresh and Warm. Docs: 01 FR-NEG-3, FR-MODE-2, FR-MODE-3, FR-BYP-1, FR-STL-1, FR-FRS-6, FR-TMO-1, FR-COA-2, D24, D40, error table; PLAN P0.0; M7-05 card; new SECURITY.md.
- Tests: TestTimeoutByClass, TestModeStaleOnErrorReachIsRetention. `make check` passes.
- Deviations: requirement and decision-table text changed under Ashwin's delegation (STATUS "Decided 2026-10-02").
- Follow-ups: P0.0 after M7-05 merges.
- Context: low.

## 2026-10-05 · M7-02 · done
- Branch / PR: card/M7-02-vary-policies / (recorded in the next commit)
- Done: `setVariant` (serve.go) reads each kept ref on a spec write and drops those the store answers `ErrNotFound` for, so an evicted or deleted variant frees its slot (FR-KEY-10, D37, 04 §14). Any other store error keeps the ref (T-15). Strict, sensitive, overflow and expired-ref reclaim were already in the code from M7-01; this card pins them at engine level.
- Tests: TestVaryUnconfiguredHeader (strict subtest), TestVarySensitiveNotStored, TestVaryOverflow, TestVaryReclaimsDeadSlots (expired on a lazy store, record gone, store error keeps the slot). Only "record gone" failed before the change. Mutants of the `ErrNotFound` and `Expires` conditions fail the tests. `make check` passes.
- Deviations: 07 line for TestVaryReclaimsDeadSlots now names the record-gone and failed-read cases. The card's Touch list names storable.go and fetch.go; neither changed, because M7-01 put the policy checks in storable.go and the cap in serve.go. PLAN M7.1 ticked.
- Follow-ups: Cache-Status detail for overflow (Waiting on Ashwin, not blocking). Reclaim reads on every spec write vs only at the cap (STATUS note, Phase 2.5).
- Context: low; size S right.

## 2026-10-05 · M7-02 · review-fixes
- Branch / PR: card/M7-02-vary-policies / #45
- Done: decided every open question in STATUS and LOG follow-ups (delegated by Ashwin), each after trying to break the obvious answer. Code: `setVariant` reads refs only when the write would be refused at the cap; `Breaker.MaxOpenFor` defaults to max(60 s, `OpenFor`) and `New` rejects it below `OpenFor`. No Cache-Status detail for overflow. A reject rule for `MaxQueueWait` above the coalesce waits was built and withdrawn: two existing limiter tests are legitimate configs it refused.
- Tests: TestVaryReclaimsDeadSlots "no reads below the cap", TestVaryOverflow (Cache-Status member), TestInvalidConfigRejected "breaker max open below open", TestDependentDefaultsFollow. Each failed before its change. `make check` passes.
- Deviations: 01 FR-KEY-10, FR-LIM-2, FR-CB-3, FR-LCY-2 and the defaults table; 04 §1 config comment and §14 reclaim; 06 T-45 and R-6; 07 reclaim row. Dates bumped. New card M10-07 (PLAN M10.8); notes on M7-03, M10-01, M10-04, P2-00.
- Follow-ups: "codec header-name case" in the carried list has no recorded question; drop or describe it by M7-05.
- Context: medium.

## 2026-10-05 · M7-02 · review-fixes
- Branch / PR: card/M7-02-vary-policies / #45
- Done: "Waiting on Ashwin" was empty; the one undecided item was "codec header-name case". Traced it to the M1-08 review and decided it (delegated): `store.Encode` and `store.Decode` reject header names that are not canonical. Non-minimal uvarints stay accepted.
- Tests: TestDecodeRejects (two rows), TestEncodeRejectsUnrepresentable (one row), TestStoredEntriesEncode (an origin sending lowercase names still yields encodable entries), fuzz seed `non-canonical-header`. The codec rows failed first. FuzzDecodeEntry 15 s and FuzzCodecRoundTrip 10 s clean. `make check` passes.
- Deviations: 05 §6 (Encode errors, decoding rules, the reason, the uvarint decision); date bumped.
- Follow-ups: none. Nothing is waiting on Ashwin.
- Context: medium.

## 2026-10-05 · M7-03 · done
- Branch / PR: card/M7-03-keyed-headers-bypass / (recorded in the next commit)
- Done: `Key.Headers` enter the key and the forward in normalized form, strict and ForwardAll (keys/headers.go, forward.go); the key reads its header values from the finished forward. Bypass rules (`Bypass.Headers`, `Bypass.Cookies`) classify a GET/HEAD as `FwdBypass`: forwarded as received, never stored or coalesced, `only-if-cached` gets `ErrOnlyIfCached`. `New` rejects `Forward.Allow` entries naming a keyed or hop-by-hop field. `StripSetCookie` already existed (M1), storable.go unchanged.
- Tests: TestForwardEqualsKey, TestKeyedInputsSplitEntries, TestUnkeyedHeaderNotForwarded, TestQueryDropRemovesFromForward, TestBypassNeverStored, TestBypassNotCoalesced, TestBodylessBypassUsesMainPool, TestNewRejectsAllowOfKeyedOrHopByHop; keys: TestNormalizeHeader, TestClassifyKeyedHeaders, TestClassifyBypass, TestIsHopByHop, FuzzBypassed, two new FuzzForwardEqualsKey configs and seven seeds. Fuzz 30 s and 10 s clean. `make check` passes.
- Deviations: 01 §5.2.3 (generic normalizer details) and the `Forward.Allow` config row; 04 §3.1 step 3 and §3.5; 06 T-1 and T-13 test lists; 07 fuzz table. Review: no must-fix; should-fix 1-4 fixed (allocation bound, fuzz and test coverage), 5 answered by making the code follow FR-VAL-3 and listing three choices under Waiting on Ashwin.
- Follow-ups: the three confirmations in STATUS (not blocking); dead `Forward.Allow` and `Key.Headers` names and FR-WRM-1 wording for M7-05 (STATUS notes).
- Context: medium; size M right.

## 2026-10-05 · M7-03 · review-fixes
- Branch / PR: card/M7-03-keyed-headers-bypass / #46
- Done: decided every open question from the handoff and the notes addressed to M7-03 (delegated by Ashwin), each after trying to break the obvious answer. Code: bypass cookie names match any whole token in any case (keys/headers.go), because the exact `name=` match was evadable under ForwardAll (T-8); `New` rejects every `Key.Headers` and `Forward.Allow` name that is never forwarded (`keys.Unforwardable`), `Cookie` included. No change: empty keyed header stays apart from absent, limit on the normalized form, `Unkeyed` under Vary, `HasBody`, default Accept-Encoding keying (operator knob documented).
- Tests: TestBypassCookieEvasion, TestNewRejectsDeadForwardNames (replaces TestNewRejectsAllowOfKeyedOrHopByHop), TestUnforwardable (replaces TestIsHopByHop), TestWarmSkipsBypassed, TestKeyedAcceptEncodingSeparatesBuckets, new TestClassifyBypass rows. The bypass and rejection rows failed first. FuzzBypassed and FuzzForwardEqualsKey 15 s clean. Hit-path benchmarks equal on main and branch. `make check` passes.
- Deviations: 01 FR-BYP-1, FR-LCY-1, FR-WRM-1, §5.2.3 and the config table; 04 §3.1 and §3.5; 06 T-8 and R-3. Two existing tests changed because `Cookie` in `Forward.Allow` is now rejected (TestNewWarnsOnCredentialForwarding, TestNoMarkerAfterUnkeyedInput).
- Follow-ups: none. Nothing is waiting on Ashwin.
- Context: medium.

## 2026-10-05 · M7-03 · review-fixes
- Branch / PR: card/M7-03-keyed-headers-bypass / #46
- Done: "Waiting on Ashwin" was already empty (decided in the entry above). Second adversarial pass over the three decisions it had held: empty-versus-absent and the size limit stand; the bypass cookie match had one more evasion, a percent-encoded name (`sess%69on`), which `tokenIs` (keys/headers.go) now decodes before comparing.
- Tests: TestClassifyBypass (four rows), TestBypassCookieEvasion (one shape), FuzzBypassed seed; the new rows failed first. FuzzBypassed 20 s clean. `make check` passes.
- Deviations: 01 FR-BYP-1, 04 §3.1 step 3, 06 R-3 name percent-decoding.
- Follow-ups: none. Nothing is waiting on Ashwin.
- Context: medium.

## 2026-10-05 · M7-04 · done
- Branch / PR: card/M7-04-tracking-preset-cookie-report / (recorded in the next commit)
- Done: presets.go (`weir.TrackingParams()`, 21 `Key.QueryDrop` patterns, opt-in per D30); cookiereport.go (Space-Saving summary of 32 stripped cookie names, logged once at Info after `Bypass.ReportStrippedCookies`, names only); one field in engine.go and one call in serve.go. After the report the hit path pays one atomic load.
- Tests: TestStrippedCookieReport (9 subtests: order, no values, once, 32-name bound, pair cap, any header key case, concurrent, default window, disabled), TestTrackingParamsPreset. The report test failed 4 subtests with the `observe` call removed. `make check` passes, trace 119/152.
- Deviations: 04 §14 stripped-cookie bullet rewritten (where it is called, bounds, no timer). Review: no must-fix; should-fix 1 (cookies under a non-canonical header key were missed) and 4 (no concurrent test) fixed test-first; 2 and 3 need Ashwin's approval because they touch a requirement and a public signature.
- Follow-ups: two confirmations under Waiting on Ashwin in STATUS (not blocking); 01 D30 and FR-OBS-5 wording after them.
- Context: low; size S right.

## 2026-10-05 · M7-04 · review-fixes
- Branch / PR: card/M7-04-tracking-preset-cookie-report / #47
- Done: decided both Waiting on Ashwin items and the open review nits (delegated), each after trying to break it. `TrackingParams()` stays a function. Report bounds changed in cookiereport.go: names over 64 bytes are cut and marked instead of dropped, 256 pairs per request instead of 32, one count per name per request. TryLock skip, no timer and the log call under the lock stand.
- Tests: TestStrippedCookieReport subtests "one request reads at most 256 pairs", "one request moves a counter once", "a long name is cut to 64 bytes"; TestTrackingParamsPreset "every preset member is dropped". The three report subtests failed first. `make check` passes.
- Deviations: 01 D30 and FR-OBS-5 (bounds, no timer, what is counted); 04 §14 report bullet.
- Follow-ups: none. Nothing is waiting on Ashwin.
- Context: low.

## 2026-10-05 · M7-05 · done
- Branch / PR: card/M7-05-key-boundary-security-review / (recorded in the next commit)
- Done: security review of internal/keys, storable.go and store/codec.go against 06. All 36 tests named on rows T-1..T-8, T-13, T-31, T-40, T-44 exist and pass. 36 fuzz seeds added across 11 targets for shapes 06 §6 item 3 asks for (empty, 10 KiB, non-ASCII, duplicate lines, separators only). One real break found by the attack review and fixed in keys/forward.go: an unkeyed `Cache-Control` or `Pragma` now sets `Unkeyed`, so no marker or negative entry follows it (T-31).
- Tests: TestFreshenRefusesUnstorable304 (new, passed first: coverage gap, not a bug); new cases in TestNoMarkerAfterUnkeyedInput, TestNegativeNotFor500, TestClassifyKeyedHeaders (all failed before the fix); 504 in TestErrorStatusesNotStored. `make check` passes, trace 119/152; `make fuzz-short` clean on 18 targets before the fix, FuzzForwardEqualsKey 15 s clean after.
- Deviations: 01 FR-STO-12 and FR-NEG-4 (the fix, approved by Ashwin in the session); 06 T-7 row reworded (its test proves Range passes through unstored), T-8 and T-31 rows, INV-1 now names the Accept-Encoding bucket; 04 §3.1 comment; 07 two rows.
- Follow-ups: four open attack-review findings in STATUS notes (two hardening, one hypothesis, one nit). The normal review ran before the fix; the fix itself had no independent review. Next: PLAN P0.0 with Ashwin's confirmation.
- Context: medium; size S was right for the review, the fix added about 10 lines.

## 2026-10-05 · M7-05 · review-fixes
- Branch / PR: card/M7-05-key-boundary-security-review / #48
- Done: decided and implemented every finding the attack reviews left open (delegated), then had the new code attacked again and fixed what that found. keys/headers.go `tokenIs` (literal match first; decoded separators and `+` trimmed around the name only); keys/forward.go trace byte set, 32-line cap, no empty line, no `..`; keys/classify.go drops keyed cookies the forward lacks; httpcc `ResponseDirectives.Malformed` and bare-only `public`, read by the Authorization case in storable.go.
- Tests: TestBypassedCookieShapes, TestForwardAllConnectionNamedCookieNotKeyed, TestAuthorizationNeedsBarePublic (new); cases in TestTraceparentValidated, TestParseResponseDirectives, TestBypassCookieEvasion; `fuzzAllCfg` gains a keyed cookie and a seed. All failed first. `make check` passes; FuzzBypassed, FuzzForwardEqualsKey, FuzzCacheControl, FuzzEvaluate clean for 10 to 12 s each.
- Deviations: 01 FR-STO-5, FR-FWD-6, FR-BYP-1, FR-STO-12; 04 §3.5 trace filter and the `ResponseDirectives` listing; 06 T-8, T-40, R-3, new R-7; 07 rows. Second-review must-fix (257 empty `tracestate` lines) and three should-fix all fixed.
- Follow-ups: none open. The last round of fixes (trim-only `tokenIs`, `Malformed`, line cap) was not reviewed a third time. Next: PLAN P0.0 with Ashwin's confirmation.
- Context: high; the review card grew to five small production fixes, each from a reproduced finding.


## 2026-10-05 · M8-01 · done
- Branch / PR: card/M8-01-space-saving-tracker / (recorded in the next commit)
- Done: PLAN P0.0 first, after Ashwin confirmed it in chat: repository public, private vulnerability reporting enabled, annotated tag `v0.1.0` on 560d2af. Then internal/missrate (tracker.go, doc.go): Space-Saving summary of requests and misses per partition, `TopK` counters, lazy window rotation, nil-receiver safe, one `emit([]Anomaly)` call per closed window outside the lock.
- Tests: TestSpaceSavingBound, TestAnomalyThresholds, TestLazyWindowRotation, TestSampleTruncated, TestNilTracker, TestObserveConcurrent, TestEmitOncePerWindow, TestDegenerateConfig. Tests and code were written together, not tests first; eight mutations of the code were then each caught by a test (two only after an added assertion). `make check` passes, trace 122/152.
- Deviations: 04 §8.4 now lists `Config` and `Anomaly`, `emit func([]Anomaly)` once per window (was `func(Anomaly)`, which could not clear a throttle after a quiet window), the one-miss floor, nil `New`, lazy rotation. No requirement, default or public signature changed.
- Follow-ups: review had no must-fix. Should-fix S1 (emit shape) and S3 (zero thresholds) fixed; S2 (windows can be reported out of order after a full-window stall) left for M8-02, noted in STATUS. The fixes after the review were not reviewed again.
- Context: medium (P0.0 plus the card); size S was right.

## 2026-10-05 · M8-01 · review-fixes
- Branch / PR: card/M8-01-space-saving-tracker / #49
- Done: decided every item the handoff left open (delegated), each after trying to break it. `emit` is now `func(end time.Time, found []Anomaly)`: the window's end time orders reports and shows their age. Sample copy removed (0 allocs per flood request). Attack runs found two things: a report delivered an hour late would be throttled on, and a flood under 1/`TopK` of traffic is not reported.
- Tests: TestLateReportCarriesWindowEnd, TestHeavyHitterAboveBoundReported, BenchmarkObserveFlood (new); window ends asserted in TestEmitOncePerWindow. The new tests failed to compile before the change. `make check` passes.
- Deviations: 01 FR-MR-1 (states the 1/`TopK` limit); 04 §8.4 (`emit` shape, receiver rules, detection limit, sample bound); M8-02 card notes (limiter expiry and ordering).
- Follow-ups: the limiter half of the late-report and ordering rules is M8-02's. This round was attacked by the session that wrote it, not by an independent reviewer.
- Context: medium.

## 2026-10-06 · M8-02 · done
- Branch / PR: card/M8-02-miss-rate-wiring / (recorded in the next commit)
- Done: missrate.go (`missWindow`: event, warning log, limiter caps), tracker built in `New`, `Serve` observes each cacheable request once its outcome is known. internal/limiter: `Throttle(end, until, caps)`, lazy expiry, grant walk shared by `Release`, `Throttle`, expiry and waiter timeout. flight.go: `sharedError` marks a follower's error so it is not counted as a miss.
- Tests: the five card tests plus TestMissRateIgnoresFreeRequests, TestMissRateThrottleHoldsAcrossWindows, TestMissRateCountsOwnFetchesOnly, TestMissRateThrottleSkipsLateReport, TestLimiterThrottle. TestMissRateAnomaly and TestMissRateThrottle failed against a stub first; the three flood and eviction tests pin behavior that already held. `make check` passes, trace 122/152.
- Deviations: 01 FR-MR-1 (definition of a miss) and FR-MR-3 (throttle holds until the next report, at most two windows), both under Ashwin's delegation; 04 §8.2 and §8.4; 06 R-8 (new residual risk) and T-11 tests; 07 T6.8 tests; M8-02 card note. flight.go is outside the card's Touch list.
- Follow-ups: two adversarial reviews and the card review ran; no must-fix left. Open: tracker mutex contention at high core counts (M10-05), runbook text for `Throttle` (M10-06), `MinRatio` above 1 disables detection silently. Not tested: half-open probes, background refresh and `Warm` on a throttled partition.
- Context: high; size S was too small once the reviews ran (three review rounds, nine tests).

## 2026-10-06 · M8-02 · review-fixes
- Branch / PR: card/M8-02-miss-rate-wiring / #50
- Done: closed every item PR #50 listed as left open (delegated; "Waiting on Ashwin" was empty). `MissRate.MinRatio` above 1 is rejected by `New`. The eviction test runs its flood concurrently. The tracker-mutex numbers and the `Throttle` runbook points moved into the M10-05 and M10-06 card notes, so they outlive STATUS.
- Tests: TestThrottledPartitionReturnsProbe, TestThrottleBindsBackgroundAndWarm (new, passed at once: the behavior was already right); TestInvalidConfigRejected row for `MinRatio` (failed first). Five mutations, each caught: promotion broken, `sharedError` unwrap removed, cap floor removed, probe not handed back on shed, plus the config row. `make check` passes.
- Deviations: 01 config table (`MinRatio` at most 1); 04 §5 and §8.2 (the cap binds every class); 07 T6.8 list.
- Follow-ups: none open from the PR. This round was attacked by the session that wrote it, not by an independent agent.
- Context: high.

## 2026-10-06 · M9-01 · done
- Branch / PR: card/M9-01-sf-list-parser / (recorded in the next commit)
- Done: internal/sfv (`ParseStringList`, `ErrInvalid`): RFC 9651 List of Strings with member count and decoded length limits. Each line is parsed alone, with the result the ", " join gives except that a String spanning two lines fails. Parameters of every bare-item type are validated and dropped.
- Tests: TestParseStringList (65 rows), TestParseStringListZeroLimits, TestParseStringListParameterCostLinear, FuzzSFStringList (13 seeds; limits, line-by-line equals joined, round trip). Compile failure first; the cost test failed first at 330 MB for a 64 KiB line. `make check` passes, trace 124/152. Fuzzed 30 s twice, no crasher.
- Deviations: none in the docs. internal/sfv/doc.go is outside the card's Touch list (package doc convention).
- Follow-ups: card review found one must-fix (display-string buffer sized to the rest of the line, quadratic), fixed with a regression test; both should-fix items and the nits are done. Left as is: byte-sequence parameters are checked for alphabet only. The member aliasing note for M9-03 is in STATUS.
- Context: low; size S was right.

## 2026-10-06 · M9-01 · review-fixes
- Branch / PR: card/M9-01-sf-list-parser / #51
- Done: decided the item PR #51 left open and its listed design choices (delegated; "Waiting on Ashwin" was empty). One code change: Byte Sequence parameter values must now be decodable base64 (characters, then no more padding than the length calls for). The other four choices are kept, with reasons in STATUS "Decided 2026-10-06 (M9-01)".
- Tests: six TestParseStringList rows for byte sequences (five reject rows failed first, one accept row), seed14. `make check` passes; fuzzed 30 s, no crasher.
- Deviations: none.
- Follow-ups: none open. This round was attacked by the session that wrote it, not by an independent agent.
- Context: low.

## 2026-10-06 · M9-02 · done
- Branch / PR: card/M9-02-purge-api / #52
- Done: `(*Engine).Purge` in purge.go: soft, hard, `All`, URLs and group tags, validated in full before the first epoch; `classifyURL` cuts purge URLs by hand so the tag equals the request's `URITag`. `Eager` follows FR-PRG-8 for a store without `Scrubber`.
- Tests: TestPurge5000KeysBounded, TestSoftPurgeServesStaleWhileRevalidating, TestHardPurgeIsMiss, TestSoftAfterHardStaysHard, TestGlobalEpochSoft, TestPurgeRejectsInvalidInput, TestPurgeGroupsAndStoreErrors, TestPurgeDuringInflightFetchPurgeAPI; `make check` passes, trace 126/152.
- Deviations: 04 §7 names `e.classifyURL` (the `keys.ClassifyURL` and `keys.NormalizeOrigin` it cited never existed), lists the purge reasons, the partial-failure event, `ErrClosed` and the 1 s soft delay. 07 T6.12 names the URL form (here) and the group form (M9-03) of the 5 000-key test; the M9-03 card lists it.
- Review: card reviewer found one must-fix (any `@` in a URL was rejected), fixed test-first. An adversarial agent then attacked ten open decisions: four changed (partial `EvPurge`, `Eager`, error text, sharper 48-refresh assertion), the rest kept; see STATUS "Decided 2026-10-06 (M9-02)".
- Follow-ups: two questions under "Waiting on Ashwin" (1 s soft purge delay; hard-epoch cap opening the store breaker). Group-name validation goes to M9-03.
- Context: medium; size M was right.

## 2026-10-06 · M9-02 · review-fixes
- Branch / PR: card/M9-02-purge-api / #52
- Done: decided the two items that waited on Ashwin, under his delegation. `httpcc.Evaluate` floors a soft epoch's staleness at 0, so a soft purge by URL is stale at the next lookup instead of up to 1 s later. `Purge` writes epochs through `storeGuard.purgeEpoch`, which does not count a refusal toward the store breaker.
- Tests: TestSoftPurgeAppliesAtOnce, TestSoftPurgeRepeatsEndWithTheSecond, TestCappedHardPurgeKeepsStoreBreakerClosed, four TestEvaluate rows (one M1-03 row reversed); `make check` passes.
- Deviations: 01 FR-PRG-2 and FR-STF-2, 04 §4.3, §5.2 and §7, 05 E-6 and E-8 state the new behavior and its two bounded costs.
- Follow-ups: none open. Group-name validation stays with M9-03.
- Context: medium.

## 2026-10-06 · M9-03 · blocked
- Branch / PR: card/M9-03-cache-groups / none
- Done: `Cache-Groups` parsed in `storability` (`EvNotStored{groups}` on a bad or over-limit list), one `TagGroup` per distinct name in `buildEntry`, `Cache-Group-Invalidation` soft-purges groups in `invalidate`, `Purge` rejects unmatchable group names (`purge-group`).
- Tests: TestGroupsScopedByOrigin, TestGroupInvalidationIsSoft, TestInvalidationFloodBounded (engine), TestCacheGroupsStorability, groups part of TestUnsafeMethodInvalidates, group form of TestPurge5000KeysBounded. `make check` passed twice, but TestInvalidationFloodBounded fails about 1 run in 20 (4 of 60).
- Deviations: 04 §7 gains a groups paragraph and the `purge-group` reason; 04 §8.5 says the invalid-field warning is logged once per engine (FR-OBS-3 wins over "a warning is logged").
- Review: card reviewer found the flake and its store-level cause (must-fix, needs Ashwin), a per-request warning (fixed: once per engine), a weak `/gm` assertion (fixed), duplicate group tags (fixed: deduped), missing accept rows for `""` and a 128-byte name (added). Open: the `Ignore` row of TestGroupInvalidationIsSoft has no teeth until question 2 is answered.
- Follow-ups: three questions under "Waiting on Ashwin": the origin tag in the epoch sketch (blocks), the scope of `CacheGroups.Ignore`, the `purge-group` reason.
- Context: medium; size M was right for the card, the store finding is extra.

## 2026-10-06 · M9-03 · done
- Branch / PR: card/M9-03-cache-groups / #53
- Done: decided the three items that waited on Ashwin, under his delegation. Entries drop the origin tag from `Entry.Tags` (it stays as `Owner`); `CacheGroups.Ignore` switches off `Cache-Group-Invalidation` only; `purge-group` stays.
- Tests: TestBuildEntry and TestCacheGroupsStorability rows changed first and failed; TestInvalidationFloodBounded passes 300 of 300 (was about 1 failure in 20); the `Ignore` row of TestGroupInvalidationIsSoft now fails when the `Ignore` branch is removed. `make check` passes, trace 127/152.
- Deviations: 01 §2 glossary, FR-STO-10 and the defaults table; 02 purge ADR; 04 §3 and §7; 05 §4 and E-8; 06 T-23 and T-29. Reasons in STATUS "Decided 2026-10-06 (M9-03)".
- Review: card reviewer ran once, before these decisions; its findings are all closed. The decisions were attacked by this session, not by a second agent.
- Follow-ups: notes for M10-01 in STATUS (events for an invalid invalidation field and a rejected purge; engine-level `EvNotStored{groups}` test).
- Context: high; size M was right for the card, the store finding doubled it.

## 2026-10-06 · M9-03 · review-fixes
- Branch / PR: card/M9-03-cache-groups / #53
- Done: closed the four items PR #53 left open. `invalidate` emits `EvPurge{group}` (`Status` = distinct groups) and `EvPurge{group-invalid}`. The 32-epochs-per-response residual (T-23) is pinned by a test and stated with the RFC 9875 reason it cannot be capped lower. An independent second review ran on the whole branch.
- Tests: TestMalformedCacheGroupsNotStored, TestGroupInvalidationFloodStaysServable, TestInvalidGroupInvalidationLoggedOnce; event, other-origin and repeated-name rows in TestGroupInvalidationIsSoft; a DEL row in TestPurgeRejectsInvalidInput. `make check` passes, trace 128/152.
- Deviations: 04 §7, §9.2 and §13.5 (events, `Status`), 05 E-8 and 06 T-23, T-28, T-29 (residuals), 07 (two test lines).
- Review: second card reviewer, 20 mutations: 16 killed at first, now 18. Fixed: no test pinned the origin scope of group invalidation; the once-per-engine log was untested; DEL untested; stale comment in store/store.go. Left: `Partition` on the group events is unchecked; moving the `groups` case in `storability` changes only the reason label.
- Follow-ups: one must-fix could only be documented, not fixed: a group tag shared by many entries can be falsely invalidated as a whole by a URI invalidation flood (about 4% per group). The fix changes the store contract or a default; question 1 under "Waiting on Ashwin", not blocking the merge.
- Context: high. This session went well past one card's budget; the next one should start fresh.

## 2026-10-06 · M10-01 · done
- Branch / PR: card/M10-01-event-catalog-stats / #54
- Done: audit of 04 §9.2 against the emit sites. Added `EvRequest` (Serve wraps `serve`), `EvFetchStart`/`EvFetchEnd` around each origin call, `EvNotStored` for over-size (`too-large`), buffered 5xx (`status`) and a failed body read (`incomplete`), reasons on `EvCoalesceTimeout`, `EvEvict` from the store `New` builds. `Engine.Stats()` in stats.go, with `limiter.Counts` and a read-only `breaker.Peek`.
- Tests: TestEveryEventKindEmitted (7 scenarios, all 19 kinds, every recorded reason checked against the catalog), TestStats. `make check` passes, trace 129/152. Hit benchmark unchanged at 14 allocs/op.
- Deviations: 04 §9.2 rows for EvRequest, EvFetchStart/End, EvCoalesceTimeout, EvNotStored and EvEvict state what the fields carry; 04 §9.3 says `Stats` only reads. One behavior change: a creator with no stale entry that keeps waiting no longer emits `EvCoalesceTimeout`.
- Review: card reviewer, no must-fix. Fixed: `Stats` moved the breaker and called the Observer (now `Peek`); the timeout change and the vocabulary were unpinned (exact count, catalog check); missing assertions for EvRequest count, empty partition, fetch-end `pass`. Open: four nits in STATUS notes.
- Follow-ups: question 2 under "Waiting on Ashwin" (four catalog choices, not blocking).
- Context: medium; size M was right.

## 2026-10-06 · M10-01 · review-fixes
- Branch / PR: card/M10-01-event-catalog-stats / #54
- Done: decided every item under "Waiting on Ashwin", under his delegation. New `EvRequest` reason `collapsed` for followers answered from their flight; new `EvNotStored` reason `stream`; `revalidated` only for a forward made for a stale entry; `EvFetchEnd` deferred so it pairs with its start through `runtime.Goexit`. The epoch-sketch question became card M10-08 (optional `store.SharedTagEpochs` capability), now the next card.
- Tests: TestEveryEventKindEmitted gains rows for collapsed, stream, Range pass, unconditional 304, the repeated conditional fetch and an origin Goexit scenario; each failed first. `make check` passes, trace 129/152.
- Deviations: 04 §9.2 (two new reasons, exact `revalidated`, paired fetch events). Reasons in STATUS "Decided 2026-10-06 (M10-01)".
- Review: no second agent ran on these changes; the decisions were attacked by this session.
- Follow-ups: M10-08 added to docs/cards/10-m10.md and PLAN M10.9.
- Context: high; start M10-08 in a fresh session.

## 2026-10-07 · M10-08 · done
- Branch / PR: claude/quirky-feynman-yni9ir / #55
- Done: optional `store.SharedTagEpochs` (`NewestEpochShared`), memory store support in epochs.go, `storeGuard.newestEpoch` splits `Entry.Tags` after `[global, URI]` and passes groups as shared; falls back to `NewestEpoch`.
- Tests: TestSharedTagsSkipInvalidPlane, TestInvalidationFloodLeavesGroupsServable (fails without the guard change), TestSharedTagsKeepURIInvalidation, storetest `SharedTagEpochs`. Race tests, vet, gofmt pass, trace 129/152; hit benchmark unchanged at 14 allocs/op. `make check` could not run lint here (golangci-lint built with Go 1.25, config targets 1.27).
- Deviations: 04 §3 (tag order), 05 capability list, E-8 and new E-12, 06 T-28 and T-29.
- Review: card reviewer, no must-fix. Nit fixed: URI invalidation with the capability present is now tested. Nit checked: allocations unchanged.
- Follow-ups: run lint in CI. Branch is claude/quirky-feynman-yni9ir, not card/M10-08-...
- Context: medium; size M was right.

## 2026-10-07 · M10-08 · review-fixes
- Branch / PR: claude/quirky-feynman-yni9ir / #55
- Done: decided every open item from an independent adversarial review (STATUS "Decided 2026-10-07 (M10-08)"). Added `TestSharedTagsKeepURIInvalidationUnderFlood`, a tag-order comment in the Cache-Groups table test, wrapper note on `SharedTagEpochs`, `Entry.Tags` order on the type comment, docs/07 entries, P25-00 AC for `SharedTagEpochs`, stale STATUS note replaced.
- Tests: race tests, vet, gofmt pass, trace 129/152. Lint still unrun locally; CI must be green.
- Deviations: none beyond the original PR.
- Review: adversarial agent, no must-fix; all findings (stale note, implicit order, docs/07, combined test, wrapper note) fixed or decided.
- Follow-ups: none.
- Context: low.

## 2026-10-07 · M10-02 · in-progress (awaiting /handoff)
- Branch / PR: claude/optimistic-davinci-9ac46r / none yet
- Done: `observe/prom` module (client_golang v1.24.1, approved): `Observer` (counters, fetch histogram), `Collector` (gauges from `Engine.Stats`); `go.work`; Makefile `modules` target (vet, lint, race test per submodule with GOWORK=off) wired into `make check`; CI cache path.
- Tests: prom_test.go (names and labels per 04 §9.3, lint, concurrency, unknown kinds). Race tests, vet, gofmt pass; root `TestNoThirdPartyImports` passes; trace 130/152. Lint unrun locally (golangci-lint built with Go 1.25/1.26, config targets 1.27).
- Deviations: none to normative docs. `gateway_failure` = status 502, 503 or 504 (04 §9.3 does not define it); go.mod uses `replace => ../..` until the root is tagged.
- Follow-ups: card review and card mark by /handoff; `make vuln` covers only the root module.
- Context: low; size M was right.

## 2026-10-07 · M10-02 · done
- Branch / PR: claude/optimistic-davinci-9ac46r / (see STATUS)
- Done: `observe/prom` complete as in the entry above, plus `TestExporterWithRealEngine` (real engine, synctest) and one sentence in 04 §9.3 defining the fetch `result` label.
- Tests: all prom tests pass under -race with GOWORK=off; root race tests, vet, gofmt pass; trace 130/152. `make check` stops at lint (golangci-lint built with Go 1.25 here); CI must confirm.
- Deviations: 04 §9.3 (result definition, clarification only).
- Review: card-reviewer, no must-fix. Should-fix (real-engine test) done; doc nit done; vuln and fuzz loops for submodules deferred (no effect today).
- Follow-ups: `make vuln` should cover submodules (add to M10-04 or a later card); drop the `replace` once the root is tagged.
- Context: low; size M was right.

## 2026-10-07 · M10-02 · review-fixes
- Branch / PR: claude/optimistic-davinci-9ac46r / #56
- Done: decided every item of an independent adversarial review (Ashwin delegated; "Waiting on Ashwin" was empty). Fixed: invalid-UTF-8 reasons scrubbed in `Observe` (client_golang panics on them and FR-OBS-2 forbids recovery); fetch buckets extended to 60 s; `make vuln` now also scans submodules; Makefile `find` skips `.claude` and `.git`; CI cache glob `**/go.sum`; docs/07 lists the new tests; 04 §9.3 corrected (`gateway_failure` is 502/503/504 only, 500 is the breaker's separate option) and states the caveats on `error`, purges and evictions; `doc.go` states one Observer and Collector per registry, lazy series, lazy half-open, unmapped events.
- Tests: TestInvalidReasonDoesNotPanic, TestSeriesBoundedByVocabulary added; prom race tests, root tests, vet, gofmt pass, trace 130/152. Lint and govulncheck unrun locally (tool builds older than Go 1.27); CI must be green.
- Deviations: 04 §9.3 and 07 (clarifications and test list).
- Review: adversarial agent, no must-fix. Declined: dependabot (a new config the card does not ask for); a metric for `EvStoreBreaker` (04 §9.3 does not list one, so adding it is a spec change for Ashwin); pre-seeding labelled series (would hard-code vocabularies; documented instead).
- Follow-ups: v0.1.0 exists on the root but predates `Stats` and the M10 events, so the `replace` stays until the next root tag.
- Context: low.

## 2026-10-07 · M10-03 · done
- Branch / PR: claude/gifted-meitner-19l6l6 / none yet
- Done: `loadtest/` (tag `load`): capacity-model origin, log-linear histograms, goroutine/breaker/origin-rate sampler, the five 07 §9 scenarios; `make load` (about 5 minutes, `WEIR_LOAD_SCALE` for smoke runs); results in docs/benchmarks.md.
- Tests: all five scenarios pass on a 4-core box, goroutine check before and after Close. Root race tests, vet (also with `-tags load`), gofmt pass; trace 130/152. Lint unrun locally (Go 1.25 build); CI must confirm.
- Deviations: 07 §9 got a notes paragraph. Steady hits paces the load to half the saturated rate (Ashwin chose this when asked: 64 closed-loop goroutines on 4 cores measure run-queue wait, p99 3.1 ms). Synchronized expiry ignores the scale for `max-age`. Flood p99 tolerance has a 50 µs floor (reviewer finding, bucket noise).
- Review: card-reviewer, no must-fix; five should-fix items applied (achieved rate reported, goroutines checked before Close, flap bound from observed origin time, breaker asserted from events, flood floor) plus nits.
- Follow-ups: AC says "on the reference machine": rerun `make load` there and replace the table. The loadtest is not in CI (real time, 5 minutes); nightly belongs to M10-04/M10.3 workflow.
- Context: moderate; size M was right.

## 2026-10-07 · M10-03 · review-fixes
- Branch / PR: claude/gifted-meitner-19l6l6 / #57
- Done: decided every item of an independent adversarial review (no must-fix; Ashwin delegated). Fixed: median-window steady p99, wall-clock open-loop pacing with offered-rate and drop assertions, non-vacuous expiry assertions, requirement citations, Makefile duration, table rows. Declined with reasons: per-test goroutine baseline kept, seeded randomness, tighter flood floor and flap bound. See STATUS "Decided".
- Tests: full `make load` passes (294 s); smoke run, vet with and without `-tags load`, gofmt pass. Lint unrun locally; CI must confirm.
- Deviations: none beyond the 07 §9 notes already in the PR.
- Follow-ups: rerun `make load` on the reference machine and replace the benchmarks table.
- Context: low.

## 2026-10-07 · M10-04 · blocked
- Branch / PR: claude/zealous-ritchie-wdpztv / none yet
- Done: `.github/workflows/nightly.yml` (fuzz matrix, 5 min per target, crasher artifacts; cache-tests job), `scripts/cache-tests.sh` (suite pinned to d644cf4, rerun-once, `UPDATE=1`), `make cache-tests`, `testdata/cache-tests-baseline.json` (260 pass, 105 fail), docs/cache-tests-expected-failures.md, Go version matrix in ci.yml (D42), `-forward-allow` flag on examples/weirproxy.
- Tests: three full local suite runs gave identical pass/fail; a doctored baseline made the script fail. `make check` and card-reviewer not run: `/handoff` must be run by Ashwin.
- Deviations: 07 §8 paragraph added; weirproxy flag is outside the card's file list (strict forwarding hides the suite's headers). Only 79 of 105 failures cite a decision or requirement; 27 are listed as unexplained.
- Follow-ups: decide the unexplained 26 (bug cards likely for invalid Expires, Age parameters, max-age spacing, If-Modified-Since). AC "nightly green once" needs a run on GitHub.
- Context: medium; size S was a little small because of the failure triage.

## 2026-10-07 · M10-04 · done
- Branch / PR: claude/zealous-ritchie-wdpztv / see STATUS
- Done: handoff of the work in the previous entry, plus review fixes: fuzz list no longer hides compile errors and fails on an empty matrix, script waits for origin and proxy and prints CLI errors, `npm ci` with fallback, weirproxy `splitList` test, `headers-store-Transfer-Encoding` moved to Unexplained, `conditional-etag-forward*` cites D4 only.
- Tests: TestSplitList added; root race tests, gofmt, vet and trace (130/152) pass; the baseline run still reports no regressions. Lint unrun locally (Go 1.25 build); CI must confirm.
- Deviations: none beyond the docs/07 §8 paragraph.
- Review: card-reviewer, no must-fix. Left open: Unexplained failures need decisions (STATUS); matrix check rename; crasher upload also matches checked-in seeds; cache-tests failure leaves no logs artifact; Makefile/docs diff noise in the review was stacking only.
- Follow-ups: decide the 27 unexplained failures; run nightly once.
- Context: medium.

## 2026-10-07 · M10-04 · review-fixes
- Branch / PR: claude/zealous-ritchie-wdpztv / #58
- Done: decided every item of an adversarial review (no must-fix; Ashwin delegated). Fixed: fuzz list failing on compile errors, script process groups, port precheck, rerun intersection, ref warning, log artifact, 30 min timeout, `check` aggregator job, counts (the earlier "26/27 unexplained" and "79 of 105" figures were wrong: 18 and 87). Settled the 18 in the doc; seven became card M10-09.
- Tests: scripts/cache-tests.sh run twice, no regressions, no leaked processes; doc and baseline cover the same 105 ids once each. Lint unrun locally.
- Deviations: none.
- Follow-ups: M10-09; run the nightly workflow once after merge.
- Context: low.

## 2026-10-08 · M10-07 · done
- Branch / PR: claude/optimistic-curie-2mn579 / #59
- Done: `stampDate` in fetch.go gives every forwarded or stored response a valid `Date` at receipt (miss, pass-through, stream, 304); `RequestFrom` maps an empty absolute-form path to `/`; FR-STO-13 says "stored or forwarded".
- Tests: TestForwardedResponseGetsDate (also checks a valid origin Date is kept), TestAbsoluteFormEmptyPath; race tests pass on all packages. Lint unrun (golangci-lint built with Go 1.25).
- Deviations: none beyond the FR-STO-13 wording.
- Review: card-reviewer, no must-fix; should-fix (valid Date case) and test-placement nit fixed. The 304 path is covered by the existing rfc9111 test.
- Follow-ups: none.
- Context: low; size S was right.

## 2026-10-08 · M10-07 · review-fixes
- Branch / PR: claude/optimistic-curie-2mn579 / #59
- Done: adversarial review, no must-fix. Added TestRevalidation304WithoutDateStamps; removed three STATUS notes that still described the fixed gaps; FR-STO-13 notes the one-second precision of the stamp.
- Tests: root race tests pass. Lint unrun locally; CI must confirm.
- Deviations: none.
- Follow-ups: none.
- Context: low.

## 2026-10-08 · M10-09 · done
- Branch / PR: claude/cool-edison-zzoboq / #60
- Done: `ParseDate` rejects one-digit hours and days and doubled spaces; whitespace around `=` in a delta-seconds directive is a non-integer; `If-Modified-Since` compares at whole seconds. Four cache-tests ids flip to pass (baseline 264/101).
- Tests: TestParseDateRejectsLooseForms, TestMaxAgeWhitespaceIsNonInteger, TestClientIMSWholeSecond, fuzz seeds; race tests pass. Lint unrun (Go 1.25 binary).
- Deviations: AC not fully met. `conditional-lm-fresh-no-lm`, `conditional-lm-stale` and `pragma-response-no-cache-heuristic` stay failing and are documented as by design (causes found: test expectation vs RFC 9110 §13.1.3; preconditions not applied after revalidation; Go's fixPragmaCacheControl). Question to Ashwin in STATUS.
- Review: card-reviewer, no must-fix; ANSIC zero-padded day nit fixed, doc row clarified.
- Follow-ups: possible card to apply client preconditions after revalidation (needs FR-SRV-2 decision).
- Context: medium; size S was right.

## 2026-10-08 · M10-09 · review-fixes
- Branch / PR: claude/cool-edison-zzoboq / #60
- Done: adversarial review, no must-fix. `ParseDate` rejects RFC 850 fractional seconds and a misplaced asctime double space; tests added; decided the Waiting item (kept by design, optional card M10-10). Noted request `max-age =0` no longer forces revalidation, and that a loose `Last-Modified` falls to `DefaultTTL` when it is above 0.
- Tests: httpcc and root race tests pass; FuzzHTTPDate 15 s clean. Lint unrun locally.
- Deviations: none.
- Follow-ups: M10-10 (optional).
- Context: low.

## 2026-10-08 · M10-05 · done
- Branch / PR: claude/peaceful-wright-an4zuy / #61
- Done: BenchmarkServeHitVary, BenchmarkServeMissCoalesced, BenchmarkServeHitParallel (tracker on/off); TestGCAt1MEntries in loadtest (load tag); docs/benchmarks.md with M1 to M10 benchstat (both rerun on one 4-core box) and GC cost at 1M entries (about 6.6% of busy CPU, under 10%, no layout card).
- Tests: root, all-package race tests and observe/prom pass; go vet with the load tag passes. Lint unrun (Go 1.25 binary); CI must confirm.
- Deviations: NFR-5 wording only (dropped "provisional" as the spec itself planned, numbers unchanged). Parallel tracker cost recorded only, decided by Ashwin.
- Review: card-reviewer, no must-fix. Fixed: raw GC output stored, projection bias and noise stated, GiB figure and citation nits. 
- Follow-ups: rerun on the reference machine; optional M10-10.
- Context: medium; size S was right.

## 2026-10-08 · M10-05 · review-fixes
- Branch / PR: claude/peaceful-wright-an4zuy / #61
- Done: adversarial review found the 6.6% GC share was inflated by load-generator CPU. `TestGCAt1MEntries` now adds a saturated stage; result 13.6% to 15.4% at 1M entries, over 10%, so cards M16-01 (prototype, decision) and M16-02 are added to docs/cards/11-phase1x.md and the test only reports. NFR-5 edit reverted to "provisional" (the reviewer was right that it needs approval). Coalesced benchmark no longer measures the timer floor; parallel benchmark resets the timer, offsets goroutines, checks hits. M1 pooled over two runs; M10 growth stated as 10 to 20%.
- Tests: root and all-package race tests, vet with the load tag; full `TestGCAt1MEntries` run (198 s). Lint unrun.
- Deviations: none (NFR-5 reverted).
- Follow-ups: M16-01 (needs Ashwin's go-ahead, D36), reference-machine rerun, the lookup-to-Join second-flight window.
- Context: medium.

## 2026-10-08 · M10-06 · done
- Branch / PR: claude/funny-bardeen-pjrvvk / #62
- Done: README quick start (weirhttp), strict-forwarding section and a table of every setting that takes a protection back (R-1, R-3, R-6 to R-8); docs/runbook.md (deployment, signals, incidents, MissRate.Throttle); `make check` runs trace-strict with an M11+ allowlist in scripts/trace.sh; 7 uncited IDs now cited.
- Tests: TestTransientBodyMemoryBounded (NFR-4), TestExportedIdentifiersDocumented (NFR-7); citations FR-KEY-3, FR-VAL-2, FR-LCY-3, INV-5, INV-6 on existing tests. Root and all-package race tests pass, trace 136/136. Lint unrun (Go 1.25 binary); CI must confirm.
- Deviations: docs/06 T-21 row lists the new test (date bumped).
- Review: card-reviewer, one must-fix (metric label `sie`, not `stale-if-error`), fixed with the soft-purge wording, R-6/R-7 rows, bypass-invalidation and 499 notes, hollow INV-5 citation removed.
- Follow-ups: M10-10 (optional), M16-01 (needs approval).
- Context: medium; size S was right.

## 2026-10-08 · M10-06 · review-fixes
- Branch / PR: claude/funny-bardeen-pjrvvk / #62
- Done: adversarial review; fixed README and runbook errors, NFR-4 wording (stream read-ahead), D36 and NFR-5 wording, M16-01 approved with changes, M10-10 approved and widened. Waiting on Ashwin is empty.
- Tests: root race test, trace-strict pass. Lint unrun.
- Deviations: docs/01 D36, NFR-4, NFR-5 changed under Ashwin's delegation; docs/07 §11 item 6; cards M10-10, M16-01, M16-02 notes.
- Follow-ups: M10-10, M16-01; optional cap on live over-limit streams (no card).
- Context: medium.

## 2026-10-09 · M10-10 · blocked
- Branch / PR: claude/admiring-curie-sv9a2w / none yet
- Done: `respond` answers a matching client `If-None-Match` or `If-Modified-Since` with a 304 for any response built from a stored or just-stored entry (revalidated, cold miss, creator, followers), judged against the final entry. FR-SRV-2 widened in docs/01, LLD §6.10 note, `conditional-lm-stale` now passes (baseline 265/100).
- Tests: TestClientConditionalAfterRevalidation, TestClientConditionalUsesFinalEntry, TestClientConditionalColdMissAndFollowers. Root race tests, vet, gofmt, trace-strict pass. Lint unrun (Go 1.25 binary); CI must confirm.
- Deviations: docs/01 FR-SRV-2 wording and date (approved 2026-10-08 per the card); docs/04 note.
- Follow-ups: `/handoff` not run (the Skill tool refuses it): needs card-reviewer pass, card mark, PR. docs/07 has no FR-SRV rows, so it is untouched.
- Context: low; size S was right.

## 2026-10-09 · M10-10 · done
- Branch / PR: claude/admiring-curie-sv9a2w / #63 https://github.com/AshwinSathian/weir/pull/63
- Done: reviewer pass on 49cbf53. Fixed the stale count in docs/cache-tests-expected-failures.md (265/100), moved the LLD sentence, and added tests for Vary variants and Authorization plus a discarded-304 case. Card marked done; supersedes the `blocked` entry above.
- Tests: TestClientConditionalVariantsAndAuthorization, TestClientConditionalUsesFinalEntry (rewritten, discarded 304 names "2", final entry "3"). Root race tests, vet, trace-strict pass; lint unrun (Go 1.25 binary), CI must confirm.
- Deviations: none beyond the FR-SRV-2 widening already logged.
- Follow-ups: RFC 9110 §13.2 text not re-checked; Range plus matching conditional untested (Range bypasses the cache). PLAN M10.4 stays unticked until the nightly run is green.
- Context: low; size S was right.

## 2026-10-09 · M10-10 · review-fixes
- Branch / PR: claude/admiring-curie-sv9a2w / #63 https://github.com/AshwinSathian/weir/pull/63
- Done: independent adversarial review of PR #63 (RFC text fetched, new tests run against a reverted serve.go: all four failed). Fixed: the creator's own 200 keeps a `Set-Cookie` when StripSetCookie dropped it from the stored entry (FR-STO-6); follower test now has a matching follower; discarded-304 test asserts the retry happened; accidental ETag edit in TestStoredEntriesEncode reverted; LLD Range wording corrected. Decisions: keep the stored-200 limit; no code for Range on a miss (pass-through drops preconditions, FR-FWD-1; M11 to revisit).
- Tests: TestClientConditionalKeepsCreatorSetCookie, TestClientConditionalBeforeRangeOnHit, TestClientConditionalColdMissAndFollowers (4 clients). Root race tests and vet pass; lint and cache-tests script not re-run.
- Deviations: none new.
- Follow-ups: M11 card: Range plus precondition on a miss. 203/204 are not evaluated.
- Context: low.

## 2026-10-09 · M11-01 · done
- Branch / PR: claude/busy-ramanujan-mhd5kg / #64 https://github.com/AshwinSathian/weir/pull/64
- Done: `httpcc.ParseRange` and `IfRangeApplies` (internal/httpcc/range.go); `fromEntry` answers a single satisfiable range on a stored 200 with a 206 slice, an unsatisfiable one with 416, everything else with the full 200; `Classified` gains `RangeValue`, `HasIfRange`, `IfRange`.
- Tests: TestRangeSingleFromCache, TestRangeUnsatisfiable416, TestRangeMultiOrInvalidGets200, TestIfRangeStrongOnly, TestRangeOnSWREntry, TestRangeAfterClientConditional, TestRangeOnNonOKEntryNotSliced, TestParseRange, FuzzRange. Race tests, vet, gofmt and trace-strict pass; lint unrun (Go 1.25 binary), CI must confirm.
- Deviations: FR-SRV-5 in docs/01 said a hit always gets the full 200; reworded to point at FR-RNG-1..3. TestRangeGarbageNotPoisoning now expects 206 on a fresh entry. trace.sh allowlist narrowed to FR-RNG-4.
- Follow-ups: M11-02 (background fill). Reviewer found no must-fix; its test-gap suggestions were added.
- Context: low; size M was right.

## 2026-10-09 · M11-01 · review-fixes
- Branch / PR: claude/busy-ramanujan-mhd5kg / #64 https://github.com/AshwinSathian/weir/pull/64
- Done: adversarial review of PR #64. Must-fix: the handoff had overwritten STATUS.md and dropped every "Decided" section; restored from main, only the header and notes changed. Decided: a stored `Accept-Ranges: none` makes Weir ignore Range (full 200), since serving a 206 next to that header contradicts the origin; FR-RNG-1 and docs/04 say so. Docs: docs/04 `Classified` fields, `fromEntry` 206/416 and §13.1 416; docs/07 rows match the tests. The earlier LOG line "Reviewer found no must-fix" referred to the first reviewer only.
- Tests: TestRangeIgnoredWhenAcceptRangesNone. Race tests, vet, gofmt, trace-strict pass; lint unrun.
- Deviations: FR-RNG-1 gained the Accept-Ranges clause (narrows to the safe side, decided under Ashwin's delegation).
- Follow-ups: cache-tests rows and baseline need `UPDATE=1 make cache-tests` (node and network); recorded in STATUS. Weir adds no `Accept-Ranges: bytes` to 206/416 (advisory).
- Context: low.

## 2026-10-09 · M11-02 · done
- Branch / PR: claude/zen-carson-gp029q / https://github.com/AshwinSathian/weir/pull/65
- Done: `rangeMiss` (serve.go) starts one background-class full fetch after a Range miss when the 206 declares a total within `MaxObjectBytes` and would be storable as a 200; `contentRangeTotal` parser; `backgroundRefresh` accepts a key with no entry. Credentialed, no-store and hit-for-miss requests never fill.
- Tests: TestRangeMissBackgroundFillBounded, TestRangeMissFillRevalidatesStaleEntry, TestContentRangeTotal, FuzzContentRangeTotal. `make check` passes (lint built with the Go 1.27 toolchain).
- Deviations: docs/04 §13.1 said the fill lives "in `pass()`"; it lives in `rangeMiss`, which wraps it. Reworded, plus the stale-entry and gate notes.
- Follow-ups: reviewer found no must-fix; its stale-entry test, docs and checklist notes were applied. HEAD+Range fill is unpinned by a test.
- Context: low; size S was right.

## 2026-10-09 · M11-02 · review-fixes
- Branch / PR: claude/zen-carson-gp029q / https://github.com/AshwinSathian/weir/pull/65
- Done: adversarial review. Must-fix: T-37 mitigations were unpinned (mutating the fill to Foreground class or dropping the marker gate passed all tests); added flood, marker, over-size and HEAD tests, both mutations now fail. Fixed: HEAD never fills; over-size fill writes a marker; fill gate counts header bytes; `contentRangeTotal` strict (`first <= last < total`). Decided: no code change for Range-miss preconditions (FR-SRV-5 says so); failing fills stay a documented ponytail.
- Tests: TestRangeMissFillFloodBounded, TestRangeMissNoFillUnderMarker, TestRangeMissOversizeFillNotRepeated, TestHeadWithRangeMissNoFill, extended TestContentRangeTotal and the no-fill table. `make check` passes.
- Deviations: docs/01 FR-SRV-5, FR-RNG-4; docs/04 §13.1; docs/06 T-37; docs/07 row clarified, no requirement weakened. Reviewer's HEAD routing change (HEAD takes the normal miss path) not taken: it contradicts a tested M11-01 behavior; noted in STATUS.
- Follow-ups: Vary and hard-purge fill tests; HEAD Range routing; failing-fill suppression if a profile shows it.
- Context: low.

## 2026-10-09 · M12-01 · done
- Branch / PR: claude/beautiful-brown-di2exe / https://github.com/AshwinSathian/weir/pull/66
- Done: `internal/sfv/dict.go` with `ParseDictionary(lines, maxMembers)`, `Dict`, `Item`, `Kind`; per-line parsing, parameters and inner lists validated and dropped, any error returns no members, repeats count against the limit.
- Tests: TestParseDictionary (incl. RFC 9651 §3.2 examples), TestParseDictionaryBounds, FuzzSFDictionary with six seed files. Race tests, vet, gofmt and trace-strict pass; lint unrun (built with Go 1.25), CI must confirm.
- Deviations: docs/04 §13.2 gained the Dict shape. No requirement touched.
- Follow-ups: reviewer found no must-fix; RFC examples, zero-Item note and doc comment applied. Linear-time of the inner-list test is not asserted. M12-02 next.
- Context: low; size S was right.

## 2026-10-09 · M12-01 · review-fixes
- Branch / PR: claude/beautiful-brown-di2exe / https://github.com/AshwinSathian/weir/pull/66
- Done: adversarial review, no must-fix. Fixed: a later field line may start with a tab (agrees with `ParseStringList`); fuzz target now checks the key grammar and SP-wrapping equivalence.
- Tests: "later line may start with a tab", extended FuzzSFDictionary. Race tests, vet, gofmt, trace-strict pass; lint unrun.
- Deviations: scripts/trace.sh keeps `TCC` in `later` (FR-TCC-2..5 still uncited; drop in M12-02). Linear-time stays untested; measured about 78 ms for 4 MB.
- Follow-ups: M12-02 notes in STATUS (maxMembers, Kind checks, presence of `private` wins).
- Context: low.

## 2026-10-09 · M12-02 · done
- Branch / PR: claude/blissful-galileo-ynivsa / https://github.com/AshwinSathian/weir/pull/67
- Done: targeted fields in `httpcc.ParseResponse` (Weir- then CDN-Cache-Control, `Targeted` flag makes Expires ignored), stricter private/no-store/no-cache from Cache-Control, `finish` strips Weir-Cache-Control on every response path; LLD 13.2 updated; TCC dropped from trace.sh `later`.
- Tests: TestTargetedFieldPrecedence, TestTargetedFieldKeepsPrivate, TestWeirCacheControlStripped, TestParseResponseTargeted, two RFC 9213 rows. Race tests, vet, gofmt, trace-strict pass; lint unrun (built with Go 1.25).
- Deviations: ParseResponse keeps its signature (target list is an internal constant, D12); must-understand rule for targeted fields added to LLD 13.2.
- Review: card-reviewer found no must-fix. Applied: 64-member bound and canonical-key note in LLD 13.2, rows for Weir-field private/no-cache, must-understand in both fields, origin 304 freshness, Authorization + public; parenthesized storable.go. Left open: none.
- Follow-ups: lint unrun here, CI must confirm. After merge run `UPDATE=1 make cache-tests` and drop the cdn-* rows in docs/cache-tests-expected-failures.md.
- Context: low; size S was right.

## 2026-10-09 · M12-02 · review-fixes
- Branch / PR: claude/blissful-galileo-ynivsa / https://github.com/AshwinSathian/weir/pull/67
- Done: adversarial review, no must-fix. Fixed: an ignored targeted field (decimal, unparsable, over 64 members) still contributes private, no-store, no-cache; Weir-Cache-Control deleted on fromEntry's own clone (no double clone); `Cdn-Cache-Control` copied into 304 and 416; false canonical-key sentence removed from LLD 13.2.
- Tests: ignored-field restriction rows (unit and engine), Authorization with targeted s-maxage and must-revalidate, FuzzCacheControl covers targeted fields (20s run clean). Race tests pass; lint unrun.
- Deviations: LLD 13.2 states that targeted public, s-maxage and must-revalidate count for Authorization requests (decided: the field is the operator's shared-cache statement). Ashwin may overturn.
- Follow-ups: none.
- Context: low.

## 2026-10-09 · M13-01 · done
- Branch / PR: claude/amazing-bohr-2fi9ve / https://github.com/AshwinSathian/weir/pull/68
- Done: store/memory/snapshot_write.go (0600 temp file, CRC-32C records, trailer, fsync, atomic rename, deadline discards); Config.SnapshotPath and SnapshotTimeout; `CloseContext(ctx)`.
- Tests: TestSnapshotRoundTrip, TestSnapshotMainQueueFirst, TestSnapshotRespectsDeadline, TestSnapshotCloseUsesTimeout, TestSnapshotSkipsExpired, TestSnapshotOffAndConfig. Race tests, vet, gofmt, trace pass; lint unrun (Go 1.25 build).
- Deviations: LLD 13.3 said `Close(ctx)` but `store.Store` is `Close() error`; Ashwin chose an optional `CloseContext` plus `SnapshotTimeout`. LLD 13.3 and docs/05 5.1 updated.
- Review: card-reviewer must-fix: LLD claimed engine wiring that does not exist; reworded to say it lands later. Applied should-fix: best-effort directory sync, Close timeout test, expired-record test, per-record deadline comment. Left open: none.
- Follow-ups: wire Engine.Close to CloseContext (with M13-02).
- Context: low; size M was right.

## 2026-10-09 · M13-01 · review-fixes
- Branch / PR: claude/amazing-bohr-2fi9ve / https://github.com/AshwinSathian/weir/pull/68
- Done: adversarial review. Must-fix: the global hard epoch (kept outside the hard table) was not written, so a hard purge would degrade to soft-stale after restart; now written. Also: Close calls serialize and a failed snapshot can be retried; ctx re-checked before rename; epochs past retention skipped; FR-SNP-1 names SnapshotTimeout and CloseContext; Config field order matches docs/05; M13-02 card notes carry the engine wiring and failed-construction caveats.
- Tests: TestSnapshotWritesGlobalHardEpoch, TestSnapshotSkipsPrunableEpoch, TestSnapshotFailureAndRetry, TestSnapshotConcurrentClose; removed the real-clock sleep from TestSnapshotCloseUsesTimeout. Lint unrun.
- Deviations: docs/01 FR-SNP-1 wording extended with the approved timeout and CloseContext (no behavior change).
- Follow-ups: engine wiring and loader in M13-02. Waiting on Ashwin: none.
- Context: low.

## 2026-10-09 · M13-02 · in-progress (handoff pending)
- Branch / PR: claude/cool-maxwell-y4bqri / none yet
- Done: `memory.New` loads `SnapshotPath` (store/memory/snapshot_load.go): CRC/decode failures skipped and counted, expired dropped, MaxBytes respected in file order, hard epochs restored, global soft epoch at load, file removed (only when the magic matches). `Engine.Close` uses the store's `CloseContext` when it owns the store. `Set` and the loader share `put`.
- Tests: TestSnapshotLoadRoundTrip, TestSnapshotLoadIsSoftStale, TestSnapshotHardEpochSurvives, TestSnapshotCorruptRecordsSkipped, TestSnapshotIncompleteIgnored, TestSnapshotLoadRespectsMaxBytes, TestSnapshotLoadHostileLength, FuzzSnapshotLoad. Race tests pass; lint unrun (container golangci-lint is Go 1.25).
- Deviations: none. Load counts are unexported (`snapLoad`); a public accessor needs approval.
- Follow-ups: `/handoff` not yet run (review, card mark, PR). The engine passes no `SnapshotPath` today; the card that adds the Config field must guard `New`'s failure path that calls `Store.Close()`.
- Context: low; size M was right.

## 2026-10-09 · M13-02 · done
- Branch / PR: claude/cool-maxwell-y4bqri / https://github.com/AshwinSathian/weir/pull/69
- Done: snapshot loader (store/memory/snapshot_load.go) and `Engine.Close` via `closeStore` (optional `CloseContext`). Review must-fix: a lost hard-epoch record (bad CRC, bad length, trailer count mismatch) loaded entries without the purge; now the loader writes a global hard epoch (fail closed). Also clamped future RequestTime to load time.
- Tests: TestSnapshotLostRecordFailsClosed, TestSnapshotLoadClampsFutureRequestTime, TestCloseStorePrefersCloseContext, plus the loader tests from the earlier entry. Race tests, vet, gofmt, trace pass; lint unrun (Go 1.25 build).
- Deviations: docs/04 §13.3 gained the loader failure mode paragraph (no requirement change). Card note "New must not write a snapshot on failed construction" not implemented: the engine passes no SnapshotPath today.
- Follow-ups: whichever card adds the Config field for SnapshotPath must replace `_ = c.Store.Close()` on the MaxObjectBytes failure path in engine.go with a non-snapshotting close. Public accessor for load counts needs approval.
- Context: low; size M was right.

## 2026-10-09 · M13-02 · review-fixes
- Branch / PR: claude/cool-maxwell-y4bqri / https://github.com/AshwinSathian/weir/pull/69
- Done: independent adversarial review. Must-fix: loader wrote a soft epoch, so an entry invalidated before restart came back servable stale (FR-STL-5); now a global invalid epoch. Should-fix: failed `os.Remove` of the snapshot now fails closed; `Engine.Close` falls back to `Store.Close()` when the grace ctx is spent; failed `New` closes with a cancelled ctx (card note met); record length capped by bytes left; rejected epochs count as skipped. Reverted an unrelated edit to an older LOG entry.
- Tests: TestSnapshotLoadLengthBeyondFile, TestSnapshotRemoveFailureFailsClosed (skipped as root), TestCloseStoreWithCancelledContextWritesNoSnapshot; soft-stale tests now assert invalid. Race tests, vet, gofmt pass; lint unrun.
- Deviations: FR-SNP-2/3, T-33, LLD 13.3 and docs/05 5.5 updated (invalid instead of soft; fail-closed; counters internal; admission order only). No public API added: the load-count accessor stays deferred.
- Follow-ups: M14-01 note (loader honors quotas). Hot-first queue placement on load left as a ponytail.
- Context: low.

## 2026-10-09 · M14-01 · done
- Branch / PR: claude/peaceful-babbage-hrdup8 / https://github.com/AshwinSathian/weir/pull/70
- Done: per-host limiter cap (`limiter.Config.PerHost`, `Classified.HostH`, `Acquire` host argument, both pools); per-owner byte quota in the memory shard (`MaxBytesPerOwner`, own-entry eviction within a 64-node scan, decline otherwise); snapshot loader honors it without evicting.
- Tests: TestPerHostLimiterCap, TestOwnerQuotaIsolatesTenants, TestSnapshotLoadHonorsOwnerQuota, TestEnginePerHostCap, TestClassifyHostHash. Race tests, vet, gofmt, trace pass; lint 0 issues via the Go 1.27 build of v2.14.0.
- Deviations: LLD 13.4 updated (int map, Acquire signature, queue ceiling). FR-FAIR-2 scan wording left as is; question under "Waiting on Ashwin".
- Follow-ups: review should-fix 1 recorded as a ponytail (per-host queue share); should-fix 2 is the question above.
- Context: low; size M was right.

## 2026-10-09 · M14-01 · review-fixes
- Branch / PR: claude/peaceful-babbage-hrdup8 / https://github.com/AshwinSathian/weir/pull/70
- Done: independent adversarial review. Must-fix: the 64-node victim scan shared one budget across both queues, so foreign small-queue nodes hid an owner's main-queue entries and starved its quota turnover; now 64 per queue (FR-FAIR-2 and 05 §5.3 reworded). Should-fix: `node.owner` is stored only when a quota is set; `MaxBytesPerOwner` doc says per shard. Host-queue sharing kept as a ponytail and pinned by a test.
- Tests: limiter invariants now run with PerHost and random hosts; TestHostFloodFillsSharedQueue; main-queue victim behind a full foreign window; owner change on replace; size == quota; expired victim and OnEvict counts; TestEngineOwnerQuotaIsolatesHosts (Owner reaches the store). Race tests, vet, gofmt, trace pass; lint 0 issues via the Go 1.27 build.
- Deviations: FR-FAIR-2 and 05 §5.3 wording only (per-queue window), decided by delegation. STATUS.md restored: the first handoff overwrote it and dropped the older Decided sections and notes.
- Follow-ups: per-host queued cap if host floods shedding other hosts shows up in practice.
- Context: low.

## 2026-10-09 · M15-01 · done
- Branch / PR: claude/serene-volta-d9nvnj / https://github.com/AshwinSathian/weir/pull/76
- Done: `memory.Store.Scrub` (one shard lock at a time, exact tag match, response records only); `Engine.Purge` with `Eager` calls it through `storeGuard.scrub` after the epochs and emits the count as `EvPurge{hard}` `Status`; stores without `Scrubber` still get `ErrEagerUnsupported`.
- Tests: TestEagerHardPurgeDeletesAllPartitions, TestEagerSoftIsError, TestEagerUnsupportedStore, TestEagerScrubErrorKeepsEpochs, TestScrub. Race tests, vet, gofmt, trace pass; lint 0 issues via the Go 1.27 build of v2.14.0.
- Deviations: LLD purge section reworded (no store scrubs yet is no longer true). No requirement changed.
- Follow-ups: review should-fix on `hasTag` cost with very large URL lists left as a ponytail; engine test scrubs by `All` only, URI and group tags are covered at store level.
- Context: low; size S was right.

## 2026-10-09 · M15-01 · review-fixes
- Branch / PR: claude/serene-volta-d9nvnj / https://github.com/AshwinSathian/weir/pull/71
- Done: adversarial review (no must-fix). Scrub failures no longer count toward the breaker; EvStoreError `scrub` and `Status` on hard documented; scrub/request-time and part-way-failure behavior documented (LLD 13.5, 05 §5.3, `Scrubber` doc).
- Tests: TestEagerPurgeByURLAndGroup, TestEagerScrubFailuresDoNotOpenBreaker, TestStoreGuardScrubFailureNotCounted (fails if the failure is counted), TestScrubConcurrentWithWrites. Race tests, vet, gofmt, trace pass; lint 0 issues via the Go 1.27 build.
- Deviations: none beyond docs wording; no public signature changed.
- Follow-ups: none.
- Context: low.

## 2026-10-09 · M16-01 · blocked
- Branch / PR: claude/zen-rubin-hynjow / https://github.com/AshwinSathian/weir/pull/72
- Done: throwaway pointer-free prototype (one encoded record per entry in 1 MiB chunks, pointer-free index), saved as docs/benchmarks/m16-prototype.patch and not applied; GC at 1M entries measured for heap and prototype at GOGC 100 and 200; hit path measured with benchstat; docs/benchmarks.md section and a D36 note in docs/02 written.
- Tests: `TestGCAt1MEntries` x4, `BenchmarkServeHitSmall` and `BenchmarkMemoryStoreGetParallel` x12 samples each. No code merged, so `make check` is not affected; docs only.
- Deviations: the AC asks to measure an allocation cut on the heap layout; it is a projection from the linear formula, not a run (stated in docs/benchmarks.md). D36 in 01 is unchanged and the card is not marked done, because the AC says the user decides.
- Review: card-reviewer, no must-fix. Fixed: prototype GOGC=200 cells (0.007 µs, 0.06%), headroom wording, gate margin note. Left: allocation cut stays a projection.
- Follow-ups: Ashwin's decision (STATUS, Waiting on Ashwin). The allocation-cut alternative is a projection, not a measurement. Reference-machine rerun still pending.
- Context: medium; size S was right apart from the 15 minutes of benchmark runs.

## 2026-10-09 · M16-01 · review-fixes
- Branch / PR: claude/zen-rubin-hynjow / https://github.com/AshwinSathian/weir/pull/72
- Done: decision taken by delegation: keep the heap layout, D36 rewritten around a 2 µs GC-per-request gate, M16-02 deferred to 20-later.md, M16-01 marked done; `gc_test.go` reports GC µs per request (GOGC-aware) instead of the 10% share line.
- Tests: `TestGCAt1MEntries` smoke run at reduced scale; vet, gofmt, race tests, trace.
- Deviations: D36 (a decision) and the M16-02 card changed, both by Ashwin's delegation.
- Follow-ups: reference-machine rerun to re-base the gate; allocation cut remains a projection.
- Context: low.

## 2026-10-09 · M16-01 · review-fixes
- Branch / PR: claude/zen-rubin-hynjow / https://github.com/AshwinSathian/weir/pull/72
- Done: adversarial review of the decision commit, no must-fix. Fixed: docs/07 and docs/02 stale lines; gate scope stated (per harness hit, `GOMEMLIMIT` unset); deferred cards use a `[~]` marker that `card.sh next` skips (README, script); M16-02 Tests/AC rewritten to the µs gate; `gogc()` guards GOGC=off; gate log only at 1M entries; AC notes the allocation cut as a projection.
- Tests: vet (also with the load tag), gofmt, trace 146/146; `card.sh next/list/<ID>` checked by hand.
- Deviations: scripts/card.sh and docs/cards/README.md gained the `[~]` marker.
- Follow-ups: reference-machine rerun to re-base the gate.
- Context: low.

## 2026-10-09 · P2-00 · done
- Branch / PR: card/P2-00-caddy-spec (remote branch claude/zen-shannon-l879he) / see STATUS
- Done: docs/08 is v1.0, verified against Caddy v2.11.7 with a file:line table (§11); Phase 2 cards P2-01 to P2-07 (plus P2-01b, P2-03b) in docs/cards/20-later.md; docs/09 Caddy facts refreshed.
- Tests: docs only. gofmt, vet and trace 146/146 pass; `make check` stops at lint (container golangci-lint is Go 1.25), CI must confirm.
- Deviations: none from normative requirements. Findings written into 08: admin routes outlive reloads (engine registry), metrics registry is per config load, Caddy's upgrade detector is unusable (adapter uses `internal/keys.IsUpgrade`).
- Review: card-reviewer, no must-fix. Fixed: UsagePool `Delete` wording and a `TestCleanupDeletesOnce` test on P2-02, P2-01 and P2-03 pre-split to fit size M, line cites, XFF fact restored in 09.
- Follow-ups: P2-01 must prove the `internal/keys` import compiles; fallback is an exported `weirhttp.IsUpgrade` (public API, ask first).
- Context: low; size S was right.

## 2026-10-09 · P2-00 · review-fixes
- Branch / PR: card/P2-00-caddy-spec (remote branch claude/zen-shannon-l879he) / https://github.com/AshwinSathian/weir/pull/73
- Done: adversarial review of PR 73 (agent), no decision was waiting on Ashwin. Must-fix applied: admin routers are rebuilt every load (false claim removed), registry as a per-name set removed by identity, per-load name/settings check, store-level settings and pool key, memory sizing per load. Should-fix applied: metrics eviction sink, placeholder unkeyed input, config key table and name charset, admin bounds, hard epoch for key-generation hash, P2-01 split, docs/README status. Nits: close time, per-block order, rate_limit unverified.
- Tests: docs only; trace 146/146, gofmt clean; lint unrun here, CI must confirm.
- Deviations: OQ-C1 amended (hard epoch instead of soft) by Ashwin's delegation; PLAN 2.2 reworded to match.
- Follow-ups: 06 T-45/R-6 gain the placeholder case at the next revision.
- Context: low.

## 2026-10-09 · P2-01 · done
- Branch / PR: card/P2-01-module-skeleton (remote branch claude/epic-maxwell-jkv0at) / see STATUS
- Done: `caddy/` module (`weircaddy`): `http.handlers.weir` with strict JSON config, required `name` charset check, `ByteSize` parsing, mapping to `weir.Config`, Provision/Validate/Cleanup (idempotent), interface guards, pass-through ServeHTTP. `go.work` includes `./caddy`; root `go.mod` still has no `require`. docs/08 §2 key table lists every block key.
- Tests: `TestConfigFromJSON`, `TestProvisionReportsBadConfig`, `TestInternalKeysImport`. `make check` passes with the pinned golangci-lint run under Go 1.27; trace 146/146.
- Deviations: none from normative text; five keys added beyond the 08 example, recorded in the 08 table. `max_bytes`/`snapshot_dir` parsed but applied by P2-02. `internal/keys.IsUpgrade` imports across modules, so no `weirhttp.IsUpgrade` fallback.
- Review: card-reviewer, no must-fix. Fixed: invalid T-45 citation dropped, exact int parsing for byte sizes (no exponent or hex forms), 08 says P2-02 owns `snapshot_dir` validation. Open: Cleanup clears `h.engine` without a lock, revisit in P2-03 when serving reads it.
- Context: low; size M was right.

## 2026-10-09 · P2-01 · review-fixes
- Branch / PR: card/P2-01-module-skeleton (remote branch claude/epic-maxwell-jkv0at) / https://github.com/AshwinSathian/weir/pull/74
- Done: adversarial review of PR 74 (agent), no must-fix, nothing was waiting on Ashwin. Applied: dot-leading names rejected (08 §2), 1 PiB cap on `max_bytes`, warning when `max_bytes`/`snapshot_dir` are set, second `Provision` refused, stricter byte-size grammar, `null` no-op, doubled error prefix, real newline test case.
- Tests: added cases in TestConfigFromJSON and TestProvisionReportsBadConfig; `make check` passes with the pinned lint under Go 1.27.
- Deviations: 08 §2 `name` rule gained "no leading dot" (delegated decision, recorded in STATUS).
- Follow-ups: xcaddy resolution of the root module (P2-01c), `Cleanup` vs `ServeHTTP` on `h.engine` (P2-03).

## 2026-10-09 · P2-01 · review-fixes
- Branch / PR: card/P2-01-module-skeleton / https://github.com/AshwinSathian/weir/pull/74
- Done: CI `make vuln` failed on caddy/: Caddy v2.11.7 pulls golang.org/x/net v0.59.0, which has 5 reachable advisories (HTTP/2, fixed in v0.60.0). Bumped x/net to v0.60.0 in caddy/go.mod and go.sum.
- Tests: caddy builds and tests pass with and without the workspace; govulncheck cannot reach vuln.go.dev from the container (403), CI must confirm.
- Deviations: none.
- Follow-ups: P2-01c CI should keep `make vuln` on caddy/; later Caddy bumps may need the same x/net floor.

## 2026-10-10 · P2-01b · done
- Branch / PR: claude/adoring-curie-pvckor (session branch, not card/*) / https://github.com/AshwinSathian/weir/pull/75
- Done: `caddy/caddyfile.go`: `UnmarshalCaddyfile` for every 08 §2 key, nested blocks, line-numbered errors; directive and order registered in `init`.
- Tests: `TestCaddyfileParse` (08 example and literal 08 §2 text vs hand-written JSON, bad input with line numbers), `TestDirectiveOrder` (site, handle, route, encode). Caddy lint under Go 1.27 clean, root checks and trace pass.
- Deviations: none. No docs/01 or docs/06 ID covers Caddyfile syntax; tests say so.
- Review: card-reviewer, no must-fix. Fixed: traceability comments, exact line asserts, JSON-level comparison, route negative test, intArg comment.
- Follow-ups: duplicate `name` across handlers (P2-02/P2-03).
- Context: low; size S was right.

## 2026-10-10 · P2-01b · review-fixes
- Branch / PR: claude/adoring-curie-pvckor / https://github.com/AshwinSathian/weir/pull/75
- Done: adversarial review of PR 75 (agent), no must-fix, nothing waiting on Ashwin. Applied: bare or empty sub-block keys rejected, precise `name` error line, tests for the three unpinned paths and the matcher form, 08 §2 gained the Caddyfile rules (one occurrence per key, matcher token, `{$VAR}` only, block required). 08 date bumped.
- Tests: new cases in TestCaddyfileParse; caddy lint (Go 1.27) clean, race tests pass with GOWORK=off.
- Deviations: 08 §2 text added; no requirement, default or signature changed.
- Follow-ups: P2-02 placeholders in `snapshot_dir` and duplicate-name test (in STATUS).

## 2026-10-10 · P2-01c · done
- Branch / PR: claude/friendly-bardeen-ufk3ke (session branch, not card/*) / https://github.com/AshwinSathian/weir/pull/76
- Done: `caddy-build` CI job (xcaddy with root and caddy module via `--with`, `caddy validate`, run a minimal Caddyfile and curl it); aggregate `check` now needs it. 15 minute timeout, Caddy log printed on failure, Go version from caddy/go.mod.
- Tests: built and smoke-ran locally with xcaddy v0.4.5 (Caddy v2.11.7). `make check` passes (lint pinned v2.14.0 under Go 1.27).
- Deviations: none.
- Review: card-reviewer, no must-fix. Applied timeout, log capture, go-version-file, comment on why Caddy is unpinned. Its note that the Go matrix was added by this commit is wrong: it predates it.
- Follow-ups: none.
- Context: low; size S was right.

## 2026-10-10 · P2-01c · review-fixes
- Branch / PR: claude/friendly-bardeen-ufk3ke / https://github.com/AshwinSathian/weir/pull/76
- Done: adversarial review (agent), no must-fix, nothing waiting on Ashwin. Applied: Caddy matrix (v2.11.7 required, `latest` continue-on-error) so an upstream release cannot block unrelated PRs; `kill -0` liveness check after the first served response; tolerant EXIT trap; corrected the comment on the root replace (a v0.1.0 root tag exists but predates the APIs `caddy/` uses).
- Tests: smoke step re-run locally under `bash -ex` (second poll served `ok`, process alive); YAML parses.
- Deviations: none. Declined: SHA-pinning actions (matches the other jobs), `[ ] && [ ]` instead of `-a` (works in bash and dash).
- Follow-ups: flip the root `=.` to the tag after the next root release (in STATUS).

## 2026-10-10 · P2-02 · blocked
- Branch / PR: claude/peaceful-mccarthy-b9uz60 / none
- Done: read 08 §3, §4b, FR-FAIR-3, FR-SNP-1, Caddy's UsagePool and memory.Config. No code written.
- Tests: none.
- Deviations: none.
- Follow-ups: question under "Waiting on Ashwin" in STATUS (multi-host detection source).
- Context: low.

## 2026-10-10 · P2-02 · done
- Branch / PR: claude/peaceful-mccarthy-b9uz60 (session branch, not card/*) / https://github.com/AshwinSathian/weir/pull/77
- Done: caddy/pool.go (UsagePool-backed registry, load-scoped name claims, superseded-store snapshot skip with rollback re-election), caddy/keygen.go (hash of forward mode, allow, set-cookie stripping), hard epoch on hash change, `multi_host` key (FR-FAIR-3 caps at 25%), snapshot_dir validation and mkdir.
- Tests: TestCleanupDeletesOnce, TestPoolSharesStoreAcrossReload, TestPoolSettingsMismatchFailsProvision, TestPoolResizeReloadAllowed, TestPoolDestructsOnLastRelease, TestSupersededStoreSkipsSnapshot, TestRolledBackReloadRestoresSnapshotWriter, TestKeyGenHashChangeWritesHardEpoch, TestKeyGenHashIgnoresHostAndKeyRules, TestMultiHostEnablesFairnessCaps, TestSingleToMultiHostStartsNewStore. Caddy race tests and caddy lint pass; root `make check` lint cannot run here (Go 1.25 build), trace 146/146.
- Deviations: new `multi_host` key approved by Ashwin in session (scanning the http app was rejected, see 08 §2); `snapshot_dir` added to the pool key; shard count left out (no setting). 08 §2, §3, §4b updated.
- Review: card-reviewer, no must-fix. Fixed: stale superseded flag after rollback, test name and cites. Documented: hash is in-process only, rollback keeps the new hash. Not done: Destruct timing assertion (store close is bounded by closeTimeout, not unit-tested).
- Follow-ups: see STATUS notes (host warning, MaxObjectBytes key, persisted hash).
- Context: medium; size M was right.

## 2026-10-10 · P2-02 · review-fixes
- Branch / PR: claude/peaceful-mccarthy-b9uz60 / https://github.com/AshwinSathian/weir/pull/77
- Done: adversarial review (agent), delegated decisions by Ashwin ("take decisions on all items"). Must-fix: key-generation hash is now recorded only after weir.New and the purge succeed (retry of a failed Provision still purges). Fixed: hash persisted beside the snapshot (`<name>.weir.keygen`, stale snapshot deleted on mismatch, covers restart and hash-plus-spec changes); `Cleanup` no longer nils `engine`; `max_bytes` under 160 MiB rejected in `Validate`; `snapshot_dir` must be absolute, is cleaned, and refused when group/other-writable; `Destruct` reads the superseded flag and retires under one lock; FR-FAIR-3 amended to the `multi_host` key.
- Tests: TestKeyGenHashRetryAfterFailedProvisionStillPurges, TestSnapshotKeyGenReconcile, TestKeyGenRecordFollowsLoad, TestMaxBytesBelowMinimumRejectedEarly, TestSnapshotDirSafety, TestRegistryConcurrentAcquireRelease; caddy race tests and lint pass.
- Deviations: docs/01 FR-FAIR-3 reworded (requirement change, delegated); 08 §2/§3 updated.
- Declined: warning on on-demand TLS without `multi_host` (needs the global scan already rejected); per-name closing gate in acquire (Caddy serializes loads; ponytail comment records it).
- Follow-ups: STATUS notes (host warning in P2-03, `Storable.MaxObjectBytes` key).

## 2026-10-10 · P2-02 · review-fixes
- Branch / PR: claude/peaceful-mccarthy-b9uz60 / https://github.com/AshwinSathian/weir/pull/77
- Done: round 3 adversarial review (agent) on the round 2 fixes and every open item. Fixed: a rolled-back reload no longer leaves its hash behind (holder list, hash returns to the older engine, so re-applying purges again); the hash record is rewritten by the final snapshot writer so it always matches the snapshot; snapshot_dir must be owned by the Caddy user (unix); `Destruct` split into `destruct(ctx)` with a deadline test; memory defaults the adapter copies are pinned by a test. Reverted stray `go.work.sum` churn from local runs.
- Tests: TestRolledBackReloadRestoresKeyGen, TestSnapshotRecordMatchesFinalWriter, TestDestructBoundedBySnapshotDeadline, TestMemoryDefaultsPinned; caddy race tests and lint pass.
- Deviations: none beyond 08 §2/§3 wording.
- Declined: warning for on-demand TLS without `multi_host`; per-name closing gate; docs/07 adapter rows (no adapter table exists, add with P2-03b); a crash-leftover temp sweep (bounded by crashes, safe side).
- Follow-ups: P2-03 card now has the multi-host warning test and the ErrClosed 503; P2-04 card has the 160 MiB floor AC.

## 2026-10-10 · P2-03 · done
- Branch / PR: claude/funny-bohr-vcgmor / https://github.com/AshwinSathian/weir/pull/78 (branch name set by the environment, not `card/*`)
- Done: caddy/module.go `ServeHTTP` (upgrade bypass, engine call, `serveError` with Retry-After, ErrClosed 503), caddy/origin.go `nextOrigin` on `weirhttp.HandlerOrigin`, caddy/warn.go (route scan on first request: X-Forwarded-For and per-client placeholder warnings; one-host memory for the multi_host warning). The card's Touch list named `serve.go`; the code lives in `module.go`, `origin.go` and `warn.go`.
- Tests: TestUpgradeAndConnectBypassEngine, TestNextOriginUsesDetachedContext, TestNextOriginForwardsKeyedRequest, TestNextOriginErrorIsOriginError, TestErrorsReturnHandlerError, TestServeCachesThroughNext, TestForwardedForWarning, TestPlaceholderHeaderWarning, TestRouteScanFindsHandler, TestSecondHostWithoutMultiHostWarnsOnce. Root race tests, caddy race tests and lint (via `go run` v2.14.0) pass; `make check` lint step cannot run here (Go 1.25 build); trace 146/146.
- Deviations: docs/08 §6 reworded, warnings run on the first request, not at provision time (the route does not hold the handler until the http app provisions). Not a requirement change.
- Review: card-reviewer, no must-fix. Fixed: client `GetBody`/`Pattern`/`Close`/`Response` cleared on the clone, port stripped in the host comparison, more placeholder prefixes, recovered panic logged at Debug, test cites. Left: error text of `next` reaches `{http.error.message}` (ponytail comment, 08 §6 note, P2-03b); no SWR test of a background Fetch with a live writer (`next` asserts it never gets the client writer instead).
- Follow-ups: P2-03b (status mapping for `next` errors, warnings against a real config).
- Context: medium; size M was right.

## 2026-10-10 · P2-03 · review-fixes
- Branch / PR: claude/funny-bohr-vcgmor / https://github.com/AshwinSathian/weir/pull/78
- Done: adversarial review (agent), decisions delegated by Ashwin ("take decisions on all items"; "Waiting on Ashwin" was empty). Must-fix: a 4xx from `next` was a 502 that tripped the breaker for any run of missing paths (now an empty-bodied response with that status); `rewrite`/`handle_path` before `weir` was undone because the engine read `RequestURI` (now `r.URL` is used when it differs from Caddy's saved original request; the forwarding rule change was approved under the delegation). Fixed: private copy of Caddy's vars map per fetch (unlocked map shared with the client goroutine); `next`'s error text no longer reaches `{http.error.message}` (fixed text, cause at debug); `AppIfConfigured` instead of `App`; 499 returns no error; more per-client placeholders (`uuid`, `tls.server_name`, `proto`, `regexp`); alias assertion made real.
- Tests: TestNextErrors4xxIsResponse, TestNextErrorTextIsHidden, TestNextOriginCopiesVars, TestRewriteBeforeWeirIsHonored, TestServeErrorClientGone, TestChainWarningsLoggedOnce, TestBackgroundRefreshNeverSeesClientWriter (synctest SWR); caddy race tests (x3) and lint pass.
- Deviations: docs/08 §6 note rewritten for the points above.
- Declined: none.
- Follow-ups: STATUS notes (5xx statuses from `next`, caddytest).

## 2026-10-10 · P2-03b · done
- Branch / PR: claude/lucid-wright-8y0epq / https://github.com/AshwinSathian/weir/pull/79
- Done: `caddy/e2e_test.go`: real in-process Caddy via caddytest in front of a counting origin; reload, coalescing, outage and purge-herd scenarios, `Retry-After` through `handle_errors`.
- Tests: TestReloadKeepsWarmKeys, TestE2ECoalesceColdKey, TestE2EOriginOutage, TestRetryAfterSurvivesHandleErrors, TestE2EPurgeHerd; `make check` passes (lint run with the pinned v2.14.0 under Go 1.27).
- Deviations: none in docs. T6.12 uses a POST group invalidation because the admin purge API is P2-05. Review (card-reviewer): no must-fix; fixed t.Fatal from goroutines, loosened the collapsed assertion to at least one (load-dependent), added the 05 E-7 citation, corrected two requirement citations.
- Follow-ups: end-to-end admin purge case in P2-05.
- Context: low; size S was right.

## 2026-10-10 · P2-03b · review-fixes
- Branch / PR: claude/lucid-wright-8y0epq / https://github.com/AshwinSathian/weir/pull/79
- Done: adversarial review (agent), decisions delegated by Ashwin ("take decisions on all items"; "Waiting on Ashwin" was empty). No must-fix. Fixed: silent skip of caddytest turned into a failure outside `-short`; site bound to 127.0.0.1; herd test fails with counts instead of 100 timeouts. Decided: real-clock caddytest files are a documented exception (CLAUDE.md rule 6, docs/07 §1) rather than a build tag.
- Tests: e2e tests unchanged in scope; `make check` passes.
- Deviations: CLAUDE.md rule 6 and docs/07 §1 amended (docs/07 date bumped); this changes a project rule under the delegation.
- Declined: subtest independence in the reload test (nit), a pinned reserve in the herd test (no config key; the comment ties it to the FR-LIM-4 default), the T6.6 t=80 s case (needs a clock; the engine test covers it).

## 2026-10-10 · P2-04 · done
- Branch / PR: claude/focused-dijkstra-cr32by / https://github.com/AshwinSathian/weir/pull/80
- Done: caddy/memory.go (budget, share, overcommit warning); `pooledStore.size` set at build; auto-sized stores take half of the unclaimed 40% budget, 256 MiB with one warning when no limit is set.
- Tests: TestMemorySizingSplit, TestMemoryShareFixedAfterBuild, TestMemoryOvercommitWarns, TestMemoryShareNewSiteFirst (added after review); lint, shuffled race tests and trace-strict pass (golangci-lint run with Go 1.27 per Blockers).
- Deviations: card said "evenly"; no look-ahead exists, so Ashwin chose the halving rule. FR-MEM-1 (01), 08 §7 and T-43 (06) reworded.
- Follow-ups: deployment guide sentence on single-site `max_bytes`. Review found an order dependence (new site listed before reused ones) and fixed it by counting all live auto-sized stores.
- Context: low; size S was right.

## 2026-10-10 · P2-04 · review-fixes
- Branch / PR: claude/focused-dijkstra-cr32by / https://github.com/AshwinSathian/weir/pull/80
- Done: adversarial review (agent), no must-fix; "Waiting on Ashwin" was empty. Fixed: a same-name replacement (changed pool key) no longer counts its predecessor, so it keeps the full first share; overcommit warning only when a store was built; "20%" wording corrected for limits above 20 GiB (budget cap 8 GiB).
- Tests: TestMemoryShareSameNameReplacement, TestMemoryReleasedStoreStopsCounting, TestMemoryBudgetClamps; TestMemoryShareNewSiteFirst now exact. Lint, shuffled race tests, trace-strict pass.
- Deviations: 08 §7 states that a renamed or removed site's store counts until destroyed, so a swap-sites reload can shrink the new ones (set `max_bytes`), and that sizing assumes Caddy provisions handlers one at a time (ponytail comment names the upgrade: reserve the grant under `r.mu`).
- Declined: reserving the grant under a lock now (no concurrent provisioning exists); skipping every superseded store (would let a swap exceed 40% while both live).

## 2026-10-10 · P2-05 · blocked
- Branch / PR: claude/wizardly-ride-guhrru / none yet
- Done: `caddy/registry.go` (engines per name, removal by identity, `purgeTap` observer), `caddy/admin.go` (`admin.api.weir`: purge, mode, stats; bodies bounded), Provision/Cleanup wiring, 08 §7 JSON shapes and status mapping.
- Tests: admin_test.go (all nine card tests plus invalid-input, closed-engine 503), `TestE2EAdminPurge` under caddytest. Race and shuffle tests, lint on Go 1.27 and `make trace-strict` pass; `make check` not run as one command (lint toolchain, see STATUS).
- Deviations: scrubbed count read from `EvPurge` Status through `purgeTap` because `Engine.Purge` returns only an error (P2-06 must chain its observer behind it); an empty purge is a 400 at the adapter; documented in 08 §7.
- Follow-ups: card not marked done and no PR opened, because `/handoff` can only be run by the user. Run `/handoff`.
- Context: medium; size M was right.

## 2026-10-10 · P2-05 · done
- Branch / PR: claude/wizardly-ride-guhrru / https://github.com/AshwinSathian/weir/pull/81
- Done: handoff of the earlier P2-05 work. card-reviewer must-fix fixed: `purgeTap` counts only `EvPurge` with reason `hard`, so origin `Cache-Group-Invalidation` events cannot inflate the scrubbed count. Mode errors other than invalid config now go through `purgeError`.
- Tests: added TestPurgeTapIgnoresGroupInvalidation, TestAdminPurgeSkipsClosedEngine. Race and shuffle tests, lint on Go 1.27 and trace-strict pass; `make check` fails only at the Go 1.25 golangci-lint binary (known).
- Deviations: 08 §7 states how the scrubbed count is derived (reason `hard` only).
- Follow-ups: P2-06 chains its observer behind `purgeTap`.
- Context: medium; size M was right.

## 2026-10-10 · P2-05 · review-fixes
- Branch / PR: claude/wizardly-ride-guhrru / https://github.com/AshwinSathian/weir/pull/81
- Done: adversarial review (agent); "Waiting on Ashwin" was empty. Fixed M1: my earlier PR-link substitution had touched three older LOG entries, restored from main. Fixed M2: a purge now reaches every live engine (a reload that changes a pool-key setting builds a second store, and the old purge-through-one-engine left the new one serving purged entries). S1: purge serialization is a context-aware semaphore, not a held mutex. S2: a scrub failure says the epochs are written and reports the count so far. S3: stronger tests. S4 and N4/N5: mode comment fixed, ttl ignored for `normal`, duplicate keys documented.
- Tests: TestAdminPurgeReachesEveryStoreInOverlap, TestAdminPurgeSkipsClosedEngine (now checks fwd=stale), TestPurgeTapWaitHonoursContext, TestAdminConcurrentEagerPurgeCounts, TestAdminRacesWithCleanup, TestPurgeErrorWordsPartialScrub, TestAdminBodyEdges, TestAdminModeNormalIgnoresTTL; e2e now checks the cached entry survives a site-listener purge attempt. Race and shuffle tests and lint on Go 1.27 pass.
- Deviations: 08 §7 reworded (purge through every engine, semaphore, partial-failure wording, ttl for normal).
- Declined: checking count limits before decode (bounded by the 1 MiB cap); hiding which names exist from a prober (admin API is operator-only); an integration test for an origin group invalidation during an eager purge (the tap is unit-tested on the event reason).

## 2026-10-10 · P2-06 · done
- Branch / PR: claude/inspiring-galileo-j1jrdh / https://github.com/AshwinSathian/weir/pull/82
- Done: `caddy/metrics.go`: collector set per (registry, name) wrapping `observe/prom` with a constant `name` label, aggregating gauges over same-name engines, `evictSink` that fans the pooled store's evictions to the newest live set; `fanout` observer after `purgeTap`; Provision/Cleanup wiring; `caddy/go.mod` requires `observe/prom` and `client_golang`.
- Tests: TestMetricsNamesAndNameLabel, TestMetricsRegisteredOncePerRegistry, TestEvictionSinkRepointsOnReload, TestMetricsSurviveReload, TestMetricsGaugesAcrossEngines, TestSiblingCleanupKeepsEvictionSink, TestFailedLoadCleanupKeepsOlderSink, TestProvisionFailureReleasesMetricSetOnce. Race and shuffle tests pass in all three modules, `make trace-strict` passes; `make check` stops at the Go 1.25 golangci-lint binary (known), lint not run.
- Deviations: 08 §8 records the wrap decision and the sink/set design.
- Review (card-reviewer): must-fix fixed (a sibling Cleanup cleared the shared sink; the sink now keeps all live sets, which also covers a failed load). Stats() now calls engines outside the lock; nil sink is safe; package var renamed `metricSets`.
- Follow-ups: none. P2-07 is next.
- Context: medium; size S was right.

## 2026-10-10 · P2-06 · review-fixes
- Branch / PR: claude/inspiring-galileo-j1jrdh / https://github.com/AshwinSathian/weir/pull/82
- Done: adversarial review (agent); "Waiting on Ashwin" was empty, no must-fix. Fixed S1-S5: evictions now count in every attached set (overlapping loads and a not-yet-cleaned failed load all expose the one store), so the older serving load no longer undercounts (08 §8 states it); `removeEngine` runs before `Close`; garbled comment fixed.
- Tests: TestStoreEvictionReachesMetric (a real store fill reaches `weir_evictions_total`), TestPoolKeepsTheSinkItBuiltWith, TestProvisionFailureInEngineReleasesMetricSet (weir.New failure), stronger TestMetricsGaugesAcrossEngines (max not sum, survives sibling Cleanup), every `weir_*` series carries `name`. Race and shuffle tests (count=3), vet, gofmt and lint on Go 1.27 (`GOTOOLCHAIN=go1.27.0 ... golangci-lint@v2.14.0`) pass.
- Deviations: 08 §8 reworded (count in all attached sets).
- Declined: asserting all 19 names of 04 §9.3 (labelled families appear only after an event; `observe/prom`'s own tests cover the names).

## 2026-10-10 · P2-06 · review-fixes
- Branch / PR: claude/inspiring-galileo-j1jrdh / https://github.com/AshwinSathian/weir/pull/82
- Done: CI `caddy-build` failed because xcaddy cannot resolve `observe/prom` (an unreleased module; a `replace` in `caddy/go.mod` is ignored by importers). The job now passes `--with github.com/AshwinSathian/weir/observe/prom=./observe/prom`. Verified locally with xcaddy v0.4.5 against Caddy v2.11.7 (build complete).
- Follow-ups: any later module the `caddy` module requires needs its own `--with` until root and sub-modules have release tags.

## 2026-10-10 · P2-07 · done
- Branch / PR: claude/nice-rubin-sw5o8p (session-designated, not card/*) / not opened yet
- Done: docs/runbook.md section 7 (single-node Caddy deployment guide, all AC items); 08 §5 now records that `rate_limit` is ordered before `basic_auth` (checked in caddy-ratelimit's caddyfile.go) and Caddy v2.11.7's default order puts `encode` ahead of `weir`.
- Tests: none (docs only); `make check` stops at lint (container golangci-lint built with Go 1.25), CI must confirm.
- Deviations: none. Card marked and PLAN 2.4 ticked at /handoff.
- Review: card-reviewer found one must-fix (inner `encode` example listed `br` that `encode` cannot produce; now `zstd gzip` both sides) and six should-fix (shutdown budget wording, `.keygen` file, `max_bytes` below 160 MiB is rejected, `bypass` wording, metrics wiring, ratelimit source version); all fixed.
- Follow-ups: run the runbook's Caddyfile examples through `caddy adapt`; Phase 2 cards are done, next is P25-00.
- Context: low; size S was right.

## 2026-10-10 · P2-07 · review-fixes
- Branch / PR: claude/nice-rubin-sw5o8p / https://github.com/AshwinSathian/weir/pull/83 (corrects the earlier entry: it was opened at /handoff; the branch is not `card/*`)
- Done: adversarial review, built a real Caddy v2.11.7 and ran `adapt`/`validate` and the curl examples. Fixed: the inner `encode` example used a one-line `key { }` block (invalid Caddyfile); the group-purge example needed `origin` as `scheme://host` (was a 400); `caddy validate` builds the store and touches `snapshot_dir` (runbook 7.8, 08 §2, comment in caddy/pool.go were wrong or silent); outer `encode` stores compressed bodies when the origin compresses (runbook, 08 §5); admin API has no auth and `Origin` is checked only when sent (unix socket advice); packaged `TimeoutStopSec=5s`; purge check waits 2 s; `StripSetCookie` has no Caddy key; extra placeholders; bold limit; GOMEMLIMIT cross-reference.
- Tests: none (docs and one comment); `go vet` for caddy module passes.
- Deviations: 08 §2 and §5 reworded to match observed behavior.
- Follow-ups: 06 T-45/R-6 per-client placeholder case at its next revision; the 5xx mapping from P2-03; STATUS still says Phase 1 (unchanged).
- Context: low

## 2026-10-10 · P25-00 · done
- Branch / PR: claude/nice-davinci-rh3zjh (session-designated, not card/*) / https://github.com/AshwinSathian/weir/pull/84
- Done: client chosen with Ashwin via question (valkey-go v1.0.78); 05 §7 and §4.3 updated (Lua epoch scripts, pruned sorted set for the hard-epoch cap, `SpreadEntries`, Unix-second epochs, server-side sketch seed, vary CAS mechanism left to P25-05); 04 §6.7 note aligned; cards P25-01 to P25-07b written in docs/cards/20-later.md.
- Tests: none (docs only); `go vet`, gofmt, `make trace-strict`, short race tests pass; `make check` stops at lint (container golangci-lint built with Go 1.25), CI must confirm.
- Deviations: none.
- Review: card-reviewer found two must-fix (a counter cannot track hard-epoch expiry; P25-03 needed a storetest option) and seven should-fix (card sizes, hash-tag contradiction, vary CAS wording, dangling 4.3 reference, AC/Out of scope gaps). All fixed.
- Follow-ups: P25-05 and P25-07/07b need Ashwin's approval first; P25-06 decides epoch-key eviction policy.
- Context: low; size S was right.

## 2026-10-10 · P25-00 · review-fixes
- Branch / PR: claude/nice-davinci-rh3zjh / https://github.com/AshwinSathian/weir/pull/84
- Done: adversarial review (agent) of PR 84; "Waiting on Ashwin" was empty, decisions in STATUS. 05 §7 rewritten (non-evictable epoch state, hard epochs in a sorted set, loss detection, lazy connect, error mapping, Scrub contract, cluster and replication notes); 05 §8 gets `EpochModes` and `Parallel`; cards P25-01 to P25-07b re-cut.
- Tests: none (docs only); vet, gofmt, trace-strict pass.
- Deviations: reverted three old LOG entries that my earlier global sed pointed at PR 84; the P25-00 entry's branch deviation is the session-designated branch.
- Follow-ups: P25-05 and P25-07/07b need Ashwin first.
- Context: low

## 2026-10-10 · P25-00 · review-fixes
- Branch / PR: claude/nice-davinci-rh3zjh / https://github.com/AshwinSathian/weir/pull/84
- Done: second adversarial pass. Fixed: loss repair is now a write (the global hard epoch path) and reads stay read-only; empty `hardidx` is not loss; seed from `crypto/rand` with SHA-256 in Go (Lua has neither); `NoClockSkew`; cluster-wide policy check; lazy-connect rules; key declaration; skew formula; card sizes. Earlier LOG entries named `SpreadEntries`; the field is `CoLocateEntries`.
- Tests: none (docs only); vet and trace-strict pass.
- Deviations: none.
- Follow-ups: P25-05, P25-07/07b need Ashwin first.
- Context: low

## 2026-10-10 · P25-01 · done
- Branch / PR: claude/nice-goodall-atnlor / https://github.com/AshwinSathian/weir/pull/85
- Done: `store/valkey` module (valkey-go v1.0.78): `Config` with defaults, `Validate` (returns a filled copy), redaction in `String`/`GoString`/`LogValue`, `mapError` wrapping every client error with `store.ErrUnavailable`.
- Tests: TestConfigValidate, TestConfigRedacts, TestMapError; module lint, vet and race tests pass with go1.27 toolchain; root `make check` stops at lint (container golangci-lint is Go 1.25), CI must confirm.
- Deviations: empty `HashTag` means the default `e` (so it cannot be rejected); `NoClockSkew` with non-zero `MaxClockSkew` is an error; `0 < HardEpochWait < 1ms` is rejected (WAIT 0 blocks forever); `mapError(valkey.Nil)` returns Nil unchanged. `*ValkeyError` in tests is a zero value (no public constructor). Branch is the session-designated one, not `card/*`.
- Review: card-reviewer, no must-fix; should-fix (sub-ms wait, `go mod tidy`, Nil contract) fixed. Left: no cap on `MaxRetention`/`MaxClockSkew` magnitude (P25-03 clamps).
- Follow-ups: P25-01b next.
- Context: low; size M was right.

## 2026-10-10 · P25-01 · review-fixes
- Branch / PR: claude/nice-goodall-atnlor / https://github.com/AshwinSathian/weir/pull/85
- Done: adversarial review (agent) of PR 85; decisions in STATUS. `Validate` wraps `weir.ErrInvalidConfig`, caps durations, `MaxHardEpochs` and key-part length, checks `Addrs`, clones `Addrs`/`TLS`, returns the zero Config on error; `Config` JSON is redacted; `mapError` leaves wrapped `valkey.Nil` and `ErrNotFound` alone.
- Tests: new rows in TestConfigValidate, JSON case in TestConfigRedacts, wrapped Nil in TestMapError; module lint, vet, race tests and trace-strict pass.
- Deviations: 05 §7 gets a config-validation bullet; card P25-01 test list reworded for the empty-means-default rule.
- Follow-ups: P25-01b adds `./store/valkey` to `go.work`.
- Context: low

## 2026-10-10 · P25-01b · blocked
- Branch / PR: claude/brave-hamilton-yomx12 / none yet
- Done: `store/valkey` Store skeleton (store.go, client.go): `New` validates only, lazy single-flight connect at most once per second, `CallTimeout` default, closed/ctx checks before dialing, policy check on every node, `go.work` entry. Get/Set/Delete/epochs are placeholders returning `ErrUnavailable`.
- Tests: TestNewBadConfigFails, TestNewUnreachableServerSucceeds, TestReconnectRateLimited, TestInfo, TestCloseTwice, TestCloseDuringConnect, TestNoDialAfterClose, TestCanceledContextDoesNotDial, TestPolicyCheck and others; module lint, vet, race tests pass (go1.27 toolchain); trace-strict passes.
- Deviations: `New` returns `*Store` (as `memory.New`), not `store.Store`; errors use the `store: valkey:` prefix, not `weir:`.
- Follow-ups: waiting on the user to run `/handoff` (review, card mark, PR); the skill is user-invocable only.
- Context: low; size M was right.

## 2026-10-10 · P25-01b · done
- Branch / PR: claude/brave-hamilton-yomx12 / https://github.com/AshwinSathian/weir/pull/86
- Done: `store/valkey` Store skeleton (store.go, client.go): `New` validates only, lazy single-flight connect at most once per second, shared dial under `CallTimeout`, closed/ctx checks before dialing, fail-closed `maxmemory-policy` check on every node, `go.work` entry. Get/Set/Delete/epochs are placeholders returning `ErrUnavailable` until P25-02/03.
- Tests: card list plus TestCloseClosesClientBuiltDuringConnect, TestShortDeadlineCallerDoesNotCancelSharedDial, empty-node and empty-policy rows; submodule lint, vet, race tests, root vet/tests and trace-strict pass. Root lint and `make check`'s lint/modules steps cannot run here (Go 1.25 binary); ran with the go1.27 toolchain.
- Deviations: `New` returns `*Store`; prefix `store: valkey:` (card note and 05 §7 updated). 05 §7 Connection bullet gained fail-closed policy, shared dial, Close bound.
- Review: card-reviewer, no must-fix. Fixed: shared dial cancelled by first caller, fail-open policy, Close-during-connect test, attempt gap from end, sorted policy error. Left: watcher goroutine in `dialChecked` is not in `wg` (exits with its dial context); real `Nodes()` with replicas is untested until the integration job (P25-02).
- Follow-ups: none.
- Context: low; size M was right.

## 2026-10-10 · P25-01b · review-fixes
- Branch / PR: claude/brave-hamilton-yomx12 / https://github.com/AshwinSathian/weir/pull/86
- Done: adversarial review (agent) of PR 86; decisions in STATUS. Watcher goroutine in `wg`; single-client dial error closes the client; allowlist policy check; `Validate` rejects several `Addrs` unless `Cluster`; second `Close` waits; `lastErr` set when closed; 05 §7 corrected (Close bound, standalone address, allowlist).
- Tests: TestConcurrentCallersShareFailingDial, TestSecondCloseWaitsForDial, TestStandaloneRejectsSeveralAddrs, unknown-policy row, stricter TestCloseDuringConnect; race, shuffle, count=20 pass; submodule lint 0 issues.
- Deviations: 05 §7 as above.
- Follow-ups: none.
- Context: low

## 2026-10-10 · P25-02 · done
- Branch / PR: claude/blissful-pascal-4l6ag2 / https://github.com/AshwinSathian/weir/pull/87
- Done: `Get`, `Set`, `Delete` for `store/valkey` (entries.go); key layout `<prefix>:<hex>` or `<prefix>:{tag}:<hex>`; `Set` clamps to RequestTime + MaxRetention (E-11); `make test-valkey`; CI job `valkey` (valkey/valkey:8.1, volatile-lfu), added to the aggregate `check`.
- Tests: TestKeyLayout, TestSetClampsToMaxRetention, TestGetDecodeFailureIsUnavailable, TestEntryCommandErrors; integration `TestStoreConformance` (WithoutEpochs) passes against redis-server 7.0.15 with ExpiredIsNotFound run, not skipped; `make check` passes with the go1.27 lint workaround.
- Deviations: none. An unencodable record is declined with nil (S-4); the card was silent.
- Review: card-reviewer, no must-fix. Fixed: CI policy step selects the container by published port, not image string; TestKeyLayout cites IDs; `.PHONY` order. Left: extra copy in `Value(string(val))` (nit); server-size error on a near-512 MiB value feeds the breaker (depends on the engine body cap).
- Follow-ups: CI job unproven until the PR runs.
- Context: low; size M was right.

## 2026-10-10 · P25-02 · review-fixes
- Branch / PR: claude/blissful-pascal-4l6ag2 / https://github.com/AshwinSathian/weir/pull/87
- Done: adversarial review (agent) of PR 87; decisions in STATUS. Unencodable `Set` deletes the old record; `Get` checks `Expires`; 05 §7 gained the entry-read/write bullet.
- Tests: TestGetPastExpiresIsNotFound, unencodable-replaces row, tightened future-RequestTime row, integration TestSetTTLIsClamped; module lint 0 issues, race tests pass, integration passes on redis-server 7.0.15.
- Deviations: 05 §7 as above.
- Follow-ups: CI job still unproven until the PR runs; a `MaxValueBytes` config field needs approval if wanted.
- Context: low

## 2026-10-10 · P25-03 · done
- Branch / PR: claude/vigilant-goodall-bdmimx / https://github.com/AshwinSathian/weir/pull/88
- Done: hard and global epochs for `store/valkey` (epochs.go, scripts.go, meta.go): one write script (prune by server TIME, cap, max), one read-only script, `GET newest` fast path, loss repair by global hard write, one retry on network errors, `HardEpochWait` via `EVAL`+`WAIT` on a dedicated connection. `storetest.EpochModes` added.
- Tests: TestSaturatingWrite, TestSoftEpochBeforeSketchIsUnavailable, TestHardEpochCap, TestAbsentNewestIsNotNoEpochs, TestAbsentNewestWithMetaPresent, TestMetaLossRepairs, TestEmptyHardidxIsNotLoss, TestSkewAddsConservatively, TestGlobalTagKeepsAllModes, TestHardEpochWaitWaitsForReplica, fake-client unit tests; storetest TestRunEpochModes(None); integration conformance passes on redis 7.0.15; module lint 0 issues; root `make check` lint needs the go1.27 workaround.
- Deviations: 05 §7 note: any write that finds `meta` absent repairs the loss; `HardEpochWait` mechanism; refusals. docs/07 Conformance row mentions `EpochModes`.
- Review: card-reviewer, one must-fix, fixed: `WAIT` ran on a different connection and never waited (shown with a paused replica); now one dedicated connection. Also fixed: `EpochModes()` with no modes meant all modes. Left: `isNetworkError` also retries `ErrNoSlot` (harmless, scripts are idempotent).
- Follow-ups: replica wait test does not run in CI; P25-03b next.
- Context: medium; size M was right.

## 2026-10-10 · P25-03 · review-fixes
- Branch / PR: claude/vigilant-goodall-bdmimx / https://github.com/AshwinSathian/weir/pull/88
- Done: adversarial review (agent) of PR 88; decisions in STATUS. Corrected the WAIT-with-no-replica claim in code and 05 §7; documented identical-settings, stale-persistence, far-future-At and pre-P25-03b wiring limits; `ponytail:` note on the dedicated connection wait.
- Tests: TestSetEpochWaitPathRetriesOnce, tightened and un-vacuous cancel case, healthy-replica timing and link wait in TestHardEpochWaitWaitsForReplica; unit and integration (master plus replica, redis 7.0.15) pass, module lint 0 issues.
- Deviations: 05 §7 as above.
- Follow-ups: none.
- Context: low


## 2026-10-10 · P25-03b · done
- Branch / PR: claude/compassionate-pasteur-k8ueay / https://github.com/AshwinSathian/weir/pull/89
- Done: soft and invalid sketch for `store/valkey` (sketch.go, scripts.go, meta.go): two 2 MiB planes created full-size, positions `SHA-256(seed || tag)` in Go, `crypto/rand` seed shared through `HSETNX meta seed`, `SEED_CHANGED` retry once, plane loss repaired by the global hard write, `NewestEpochShared`. `storetest.Parallel` added.
- Tests: TestSketchStrlenAtMost2MiB, TestSharedTagsSkipInvalidPlane, TestSeedSharedAcrossStores, TestSeedChangedAfterFlushRetries, TestSketchPlaneLossRepairs, TestSketchPositions, TestSeedChangedRetriesOnce and fake-client argument tests; the full conformance suite now runs without `EpochModes` under `Parallel(64)`. Root tests pass; module lint 0 issues; `make check` stops at the caddy module lint (container's Go 1.25 golangci-lint), CI must confirm. Integration passes on redis 7.0.15, not Valkey.
- Deviations: 05 §7 (sketch writes, loss definition, upgrade note) and §8 (`Parallel`, property wording); docs/07 Conformance row. `EpochNeverUnderInvalidates` loosened: a more severe colliding mode is valid, time is checked only for the tag's own mode (clock skew can place it before `since`).
- Review: card-reviewer, no must-fix. Fixed both should-fix (the BITFIELD `SET` now uses the same `at` it compared; requirement IDs on the new tests) and the upgrade note. Left: O(n²) duplicate check in `readArgs` (bounded by the caller).
- Follow-ups: none. The CI Valkey job has still not run.
- Context: medium; size M was right.

## 2026-10-10 · P25-03b · review-fixes
- Branch / PR: claude/compassionate-pasteur-k8ueay / https://github.com/AshwinSathian/weir/pull/89
- Done: adversarial review (agent) of PR 89; decisions in STATUS. Strict single-mode phases for `EpochNeverUnderInvalidates`; nil seed no longer leaks `valkey.Nil`; docs 05 E-7, §8 and the flood-cost note.
- Tests: TestSeedWithoutVersionIsLoss, TestSharedLookupAfterPlaneLoss, nil-seed unit case, plane-only deletion in TestSketchPlaneLossRepairs; unit and integration (redis 7.0.15) pass, module and storetest lint 0 issues.
- Deviations: 05 §7 and §8 as above.
- Follow-ups: measure the two-round-trip lookup under a flood in P25-04; the CI Valkey job has still not run.
- Context: low

## 2026-10-10 · P25-04 · done
- Branch / PR: claude/optimistic-mendel-k6iji6 / https://github.com/AshwinSathian/weir/pull/90
- Done: `store/valkey/engine_integration_test.go` (tag `integration`): the engine with a Valkey store on the real clock, parallel tests on fresh prefixes. Helper primes the store and waits 2.1 s because a fresh prefix writes a repair epoch at `ceil(server now)` (05 §7).
- Tests: TestEngineCoalesceColdKey, TestEngineStaleWhileRevalidate, TestEngineStaleIfErrorOnOriginDown, TestEngineSoftPurge, TestEngineHardPurge, TestEngineGroupPurge, TestEngineNegativeCache, TestEngineBreaker. They pass with `-race -count=2 -shuffle=on` on redis 7.0.15 (about 15 s per run); module lint 0 issues; root `make check` passes except the `modules` target, which stops on the container's Go 1.25 golangci-lint (CI must confirm).
- Excluded and why: flight-table and store-size bounds, `TestStoreOutageStillCoalescedAndLimited` and `TestStoreSlowRemote` (store wrappers, not the Valkey store), `TestLimiterCap5000Keys` and the other limiter, partition and miss-rate engine tests (synctest timing, no store dependence beyond what is covered), `TestBatchWriteExpirySpread` (needs fake time over 300 s).
- Deviations: none. Review: card-reviewer, no must-fix; fixed the timing-margin should-fixes (breaker, negative TTL, stale-if-error windows, longer coalesce delay, per-test prefix).
- Follow-ups: flood measurement of the two-round-trip lookup (P25-03b note) is unowned; the CI Valkey job has still not run on Valkey 8.1.
- Context: low; size M was right.

## 2026-10-10 · P25-04 · review-fixes
- Branch / PR: claude/optimistic-mendel-k6iji6 / https://github.com/AshwinSathian/weir/pull/90
- Done: adversarial review (agent) of PR 90; decisions in STATUS. Suite sets `Timeouts.Store` to 2 s (store breaker opened under starvation); SWR test refresh gets `max-age=60`; store closed when `weir.New` fails; card P25-04b added for the skipped scenarios; PLAN 2.5.2 unticked.
- Tests: TestEngine* pass with `-race -count=2 -shuffle=on` on redis 7.0.15; module lint 0 issues.
- Deviations: none. The earlier excluded-scenario list was incomplete: Invalid epoch, global soft, Vary variants, MustRevalidate, Warm and creator cancel could run on Valkey; they are P25-04b.
- Follow-ups: P25-05 API decision waits on Ashwin; Valkey 8.1 CI job still not run.
- Context: low

## 2026-10-10 · P25-04b · done
- Branch / PR: claude/amazing-galileo-2z89q7 / https://github.com/AshwinSathian/weir/pull/91
- Done: `store/valkey/engine_scenarios_integration_test.go` (tag `integration`): unsafe-method and group invalidation (invalid mode, shared tags), invalidation flood with a logged lookup-cost measurement, global soft epoch, soft after hard, purge during an in-flight fetch, Vary followers through the codec, must-revalidate 504, creator cancel, Warm.
- Tests: TestEngineUnsafeMethodInvalidates, GroupInvalidationIsSoft, InvalidationFlood, GlobalEpochSoft, SoftAfterHardStaysHard, PurgeDuringInflightFetch, VaryFollowersRecoalesce, MustRevalidate504, CoalesceCreatorCancel, Warm. All TestEngine* pass 3 of 3 runs with `-race` on redis 7.0.15 (about 32 s). Root `make check` passes except `lint`/`modules`, which stop on the container's Go 1.25 golangci-lint (CI must confirm).
- Deviations: the card names `TestSharedTagsKeepURIInvalidation` with `NewestEpochShared` on Valkey; the Valkey sketch size is not configurable, so a flood cannot saturate it and `TestEngineInvalidationFlood` cannot tell the shared path from the plain one (storetest `SharedTagEpochs` pins the rule). It still checks that a POST invalidates its own URI. Review: card-reviewer found `TestEngineGlobalEpochSoft` failing (refresh started before the epoch second, so purged again); fixed with a 2.1 s wait and a 5 s window. Also set Warm.Concurrency explicitly and asserted 2..4 in flight, renamed the flood test to claim only what it shows (the shared-tag rule is pinned by storetest).
- Follow-ups: CI Valkey 8.1 run still pending; PLAN 2.5.2 unticked until then. P25-05 waits on Ashwin.
- Context: low; size S was right.

## 2026-10-10 · P25-05 · done
- Branch / PR: claude/amazing-galileo-2z89q7 / https://github.com/AshwinSathian/weir/pull/91 (added to the P25-04b PR at Ashwin's request)
- Done: `store.VarySetter` (SetVarySpec compare-and-set), memory store implementation (one shard lock hold), engine `setVariantCAS`/`nextSpec` (16 attempts, delete of an orphaned variant), guard methods; docs 04 §6.7, 05 V-1, 07.
- Tests: TestVaryCASConcurrentWriters (fails without the capability: 64 of 64 variants reachable), TestVaryCASFallsBackWithoutCapability, TestVaryCASStoreError, TestSetVarySpec, TestSetVarySpecOneWinner; `lazyStore` test wrapper got its own SetVarySpec. Root and memory race tests pass.
- Deviations: 04 §6.7 and 05 §7 note rewritten for the capability; the decision (capability over `Entry` field) was delegated to the agent by Ashwin.
- Follow-ups: P25-05b (Valkey script); the Valkey store keeps the old bound until then.
- Context: medium

## 2026-10-10 · P25-04b + P25-05 · review-fixes
- Branch / PR: claude/amazing-galileo-2z89q7 / https://github.com/AshwinSathian/weir/pull/91
- Done: adversarial reviews (two agents) of the scenarios and of the compare-and-set. Scenarios: `PurgeDuringInflightFetch` waits 3 s (the old pause let a request time taken at completion pass, confirmed by mutation), flood test warms up, Warm lower bound dropped. CAS: an unlisted variant is deleted on every failure exit with a context detached from the caller; docs 04 §6.7 and 05 V-1 say best effort and name the same-variant race; `TestVaryCASStoreError` counts calls (an error is not retried); the quota-refusal caveat is in V-1; a `ponytail:` note on the reclaim reads.
- Tests: TestVary*, TestSetVarySpec*, TestEngine* pass with `-race`; no must-fix from either review.
- Deviations: none beyond the notes above.
- Follow-ups: P25-05b (Valkey script); CI Valkey 8.1 run.
- Context: medium

## 2026-10-10 · P25-05b · done
- Branch / PR: claude/youthful-ride-2vosqo / https://github.com/AshwinSathian/weir/pull/92
- Done: `store/valkey/vary.go` `SetVarySpec` (store.VarySetter) over one Lua script on the spec's entry key (`GET`, byte compare with the encoded prev, `SET PXAT`); `expiryMillis` shared with `Set`; a nil prev that loses swaps over a record past its Expires or undecodable; docs 05 §7 rewritten for the mechanism.
- Tests: TestSetVarySpec, TestSetVarySpecConcurrentWritersLoseNothing (fake); integration TestVaryCASConcurrentWriters (64 writers, cap 8, with and without CoLocateEntries), TestEngineVaryCapHoldsAcrossWriters. Pass with `-race` on redis 7.0.15; root race tests and trace pass. golangci-lint cannot run here (Go 1.25 build), so CI must confirm lint.
- Deviations: bytes compared instead of a digest (05 §7 updated). Review: card-reviewer, no must-fix; the one should-fix (undecodable record) is fixed with a test.
- Follow-ups: CI Valkey 8.1 run still pending.
- Context: low; size S was right.

## 2026-10-10 · P25-05b · review-fixes
- Branch / PR: claude/youthful-ride-2vosqo / https://github.com/AshwinSathian/weir/pull/92
- Done: adversarial review (agent), no must-fix. Decisions (delegated by Ashwin): the undecodable-record fallback is unreachable from the engine (`Get` returns `ErrUnavailable`), so it is documented as serving direct callers and `Get` is unchanged; a SHA-1 digest compare was declined (gosec flags SHA-1, the bound is stated instead); 05 §7 now lists the wire cost, round trips, skew and rolling-upgrade limits. New tests: vanished key, re-read error, second script losing to a concurrent writer (hooks on the fake), a real-server stale-record test; the unit concurrency loop is capped; the integration test comment says what it proves. The card's Touch list was short by `client.go`, `entries.go`, `scripts.go` and `vary_integration_test.go`.
- Tests: Vary tests pass with `-race -count=2` on redis 7.0.15; `store/valkey` race tests and vet pass; lint cannot run here.
- Deviations: none beyond 05 §7.
- Follow-ups: whether `Get` should treat an undecodable spec as a miss so the engine can overwrite it (T-21 trade-off) is not decided; CI Valkey 8.1 run.
- Context: low
