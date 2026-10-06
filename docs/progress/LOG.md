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
- Branch / PR: card/M9-02-purge-api / pending
- Done: `(*Engine).Purge` in purge.go: soft, hard, `All`, URLs and group tags, validated in full before the first epoch; `classifyURL` cuts purge URLs by hand so the tag equals the request's `URITag`. `Eager` follows FR-PRG-8 for a store without `Scrubber`.
- Tests: TestPurge5000KeysBounded, TestSoftPurgeServesStaleWhileRevalidating, TestHardPurgeIsMiss, TestSoftAfterHardStaysHard, TestGlobalEpochSoft, TestPurgeRejectsInvalidInput, TestPurgeGroupsAndStoreErrors, TestPurgeDuringInflightFetchPurgeAPI; `make check` passes, trace 126/152.
- Deviations: 04 §7 names `e.classifyURL` (the `keys.ClassifyURL` and `keys.NormalizeOrigin` it cited never existed), lists the purge reasons, the partial-failure event, `ErrClosed` and the 1 s soft delay. 07 T6.12 names the URL form (here) and the group form (M9-03) of the 5 000-key test; the M9-03 card lists it.
- Review: card reviewer found one must-fix (any `@` in a URL was rejected), fixed test-first. An adversarial agent then attacked ten open decisions: four changed (partial `EvPurge`, `Eager`, error text, sharper 48-refresh assertion), the rest kept; see STATUS "Decided 2026-10-06 (M9-02)".
- Follow-ups: two questions under "Waiting on Ashwin" (1 s soft purge delay; hard-epoch cap opening the store breaker). Group-name validation goes to M9-03.
- Context: medium; size M was right.
