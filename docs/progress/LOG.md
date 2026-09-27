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
