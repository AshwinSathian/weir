# Weir threat model

Status: v1.0
Date: 2026-10-06
Depends on: [01-technical-spec.md](01-technical-spec.md), [02-architecture.md](02-architecture.md)

The seed's §7.3 makes the cache key a security boundary. This document says what that boundary protects, from whom, how each known attack class is answered, and what remains the operator's problem. Every threat has an ID so tests and code comments can cite it (`// T-3: ...`).

## 1. Assets

| Asset | Harm if lost |
|---|---|
| Integrity of stored responses | one attacker request changes what every later visitor sees (poisoning) |
| Confidentiality of per-user responses | one user's private page is served to others (deception, leakage) |
| Origin availability | the origin is overloaded through the cache (busting, stampede) |
| Engine availability | memory or CPU exhaustion in the process hosting Weir |
| Purge correctness | content that must disappear (legal takedown, security fix) keeps being served |

## 2. Trust boundaries and actors

| Actor | Trust | Capabilities |
|---|---|---|
| A1 anonymous client | untrusted | any request bytes the adapter accepts, high rate, many source addresses |
| A2 authenticated user | untrusted for other users' data | as A1 plus valid credentials and session cookies |
| A3 tenant on a shared origin | untrusted for other tenants | controls some responses' headers, including `Cache-Groups`, `Cache-Group-Invalidation`, `Vary` |
| Operator | trusted | configuration, `Purge` calls (through an adapter that authenticates them) |
| Origin | trusted to mean what it says in headers; may be buggy | response headers and bodies |
| Store backend | trusted infrastructure; may fail or be slow | availability only |

Out of scope: network attackers (TLS is the adapter's job), HTTP request smuggling and desync (the HTTP server's parser is the defense; Weir never parses wire bytes), compromised origins, compromised stores.

## 3. Threats

Each row: the attack, where it comes from, Weir's answer, the requirement or ADR, and the test that proves it.

### Poisoning and deception

| ID | Threat | Answer | Refs | Test |
|---|---|---|---|---|
| T-1 | Unkeyed header poisoning: origin reflects or reacts to a header (for example `X-Forwarded-Host`, `User-Agent`, `X-Forwarded-Port`, `Accept`, `Origin`) that the cache does not key (Kettle 2018, 2019) | strict forwarding: unkeyed headers never reach the origin on cacheable requests | D4, P2, FR-FWD-1, ADR-4 | `TestForwardEqualsKey`, `TestKettleUserAgent`, `TestUnkeyedHeaderNotForwarded`, `TestKeyedInputsSplitEntries` |
| T-2 | Unkeyed or cloaked query parameters: parameter excluded from the key but forwarded; `;` versus `&` parsing differences (Kettle 2020) | excluded parameters are removed from the forwarded query; kept segments are forwarded byte-for-byte, so any origin parse of them is also a parse of keyed bytes | P2, FR-KEY-5 | `TestQueryDropRemovesFromForward`, `TestParameterCloakingSemicolon` |
| T-3 | Key injection by delimiter collision (Akamai `__` example, Kettle 2020) | tagged, length-prefixed canonical encoding hashed with SHA-256 | FR-KEY-1, ADR-3 | `TestKeyEncodingInjective` (property), `FuzzKeyEncodingInjective` |
| T-4 | Per-client transport headers: adapters or proxies add `X-Forwarded-For` (or similar) to forwarded requests, and the origin varies content on it (geo, A/B) | Weir cannot see headers added after it. Documented adapter rule ([03-hld.md §5](03-hld.md)); Caddy spec requires origins not to vary unkeyed on these, or to key a derived dimension (Phase 3) | 08 §6 | manual review item |
| T-5 | Fat GET: body on a GET changes the response but not the key (Kettle 2020) | the forwarded request for a cacheable `GET`/`HEAD` never has a body | FR-FWD-1 | `TestFatGETBodyDropped` |
| T-6 | Path normalization discrepancies and web cache deception: cache and origin disagree on what `/account;x.css`, `/a/..%2fhome`, `%2F` mean (Doyhenard 2024) | Weir keys and forwards the exact path bytes (optional RFC 3986 normalization applied to both). Weir has no "cache by file extension" or "cache by directory" rules: storability comes only from origin headers. A raw `#` in path or query, or `?` in path, is rejected, so no origin reads a fragment or query the key did not see | FR-KEY-4, FR-STO-8, FR-VAL-1 | `TestPathForwardedByteExact`, `TestNoExtensionBasedCaching`, `TestFragmentInTargetRejected`, `TestAbsoluteFormFragmentRejected`, `FuzzRequestFrom` |
| T-7 | Error-page poisoning: attacker makes the origin return an error (WAF 403, `Range: bytes=cow` 400, `Transfer-Encoding` 501) that is stored for everyone (Kettle 2019) | 400 and 403 are not in the default storable set. A cacheable fetch never carries `Range`: a `Range` request with no usable entry is passed through with its `Range`, and its response is neither stored nor turned into a marker. `Transfer-Encoding` and other hop-by-hop fields are stripped, so a client cannot provoke the 501 (which is storable by default, as RFC 9110 lists it). Unkeyed headers are not forwarded at all | FR-STO-2, FR-FWD-1, FR-FWD-2, FR-SRV-5 | `TestRangeGarbageNotPoisoning`, `TestErrorStatusesNotStored`, `TestUnforwardable`, `TestClassifyPassForward` |
| T-8 | Private data leakage: `Set-Cookie`, `Authorization`-bearing, or `private` responses stored and shared | RFC 9111 §3.5 enforced, and only a valid `s-maxage`, or `public` or `must-revalidate` in a well-formed field (no argument, no unclosed quote), grants its permission; `Set-Cookie` responses not stored unless stripped by explicit config; `private` never stored; `Vary: Cookie` and `Vary: Authorization` not stored without explicit allow; requests with `Authorization` do not coalesce; bypass cookie rules match as widely as a lenient origin parser, so a reshaped session cookie cannot turn a personalized response into a shared one under `ForwardAll` | FR-STO-4..6, FR-KEY-9, FR-COA-8, FR-BYP-1 | `TestSetCookieNotStored`, `TestAuthorizationRules`, `TestAuthorizationNeedsValidSMaxage`, `TestAuthorizedNotCoalesced`, `TestBypassNeverStored`, `TestBypassCookieEvasion`, `FuzzBypassed`, `TestBypassedCookieShapes`, `TestAuthorizationNeedsBarePublic`, `TestFreshenRefusesUnstorable304` |
| T-9 | Stale purge: an entry that should be purged is served because the epoch lookup failed | memory store epoch lookups cannot fail; remote stores fail open with an event. Chosen because failing closed would turn a store blip into a full miss storm. Operators who need fail-closed purges should also hard-purge by `All` after an incident | [04-lld.md §6.3](04-lld.md) | `TestEpochLookupErrorEmitsEvent` |
| T-10 | Purge race: a fetch in flight during a purge stores pre-purge content afterwards | epochs compare against `RequestTime`, not store time | FR-PRG-7 | `TestPurgeDuringInflightFetch` |
| T-45 | In-process origin reads unkeyed context: `HandlerOrigin` (and the Caddy `nextOrigin`) hands the handler the flight creator's context values (FR-COA-9), so an identity or variable that upstream middleware put there can shape a response that is then stored and shared | Weir cannot see context values, so it cannot key them. A handler behind Weir must derive per-user output only from the forwarded request, or the route must bypass the cache; the same rule as R-1. `TransportOrigin` sends only the forwarded request and is not affected | FR-COA-9, INV-1 | none (operator rule; the weirhttp and Caddy guides state it, R-6) |

### Availability

| ID | Threat | Answer | Refs | Test |
|---|---|---|---|---|
| T-11 | Cache busting by unique query strings on one path (Ferretti and Ghini 2012) | per-partition cap bounds concurrent origin work for that path, and the queued waiters for it (at most a quarter of the queue), so the flood cannot fill the shared queue; miss-rate anomaly event; S3-FIFO keeps one-hit entries out of the main queue | FR-LIM-3, FR-MR-*, ADR-6, ADR-8 | `TestRandomQueryFloodBounded`, `TestPartitionFairness`, `TestLimiterPartitionQueueCap`, `TestMissRateAnomaly`, `TestMissRateThrottle`, `TestMissRateThrottleHoldsAcrossWindows`, `TestMissRateThrottleSkipsLateReport`, `TestMissRateIgnoresFreeRequests`, `TestMissRateCountsOwnFetchesOnly`, `TestS3FIFOScanResistance` |
| T-12 | Cache busting across many distinct paths | global cap bounds origin load; legitimate misses on other paths may shed. Residual: this is rate-limiting territory (for example `caddy-ratelimit` in front of Weir) | ADR-8 | `TestPathFloodOriginBounded` |
| T-13 | Malformed keyed header forcing bypass (CVE-2024-35296, `Accept-Encoding`) | normalizers map any malformed value to one canonical bucket, both in key and forward; never a bypass, never an error. The `Cookie` limit counts keyed pairs only, so a large unkeyed cookie cannot push a keyed one to absent | FR-VAL-3, §5.2.3 | `TestCVE202435296`, `FuzzAcceptEncoding`, `TestKeyedCookieLimitCountsKeyedPairs`, `TestNormalizeHeader`, `TestClassifyKeyedHeaders` |
| T-14 | Client revalidation directives as a bypass (`Cache-Control: no-cache`, `Pragma: no-cache`) | ignored by default | D5, FR-SRV-8 | `TestClientNoCacheIgnored` |
| T-15 | Vary explosion: origin varies on `User-Agent`, attacker cycles values. Variant confusion: two renderings of a Vary field that the origin answers differently (two lines vs one, a comma in a quoted string, a non-canonical name) share a variant | variants capped per primary key; over the cap new variants are not stored (answered, not cached). Vary values keyed exactly as forwarded and request names canonicalized before keying, so the variant always reflects what the origin saw | FR-KEY-10, FR-KEY-11, INV-1 | `TestVaryOverflow`, `TestVaryKeysExactLines`, `TestVaryNonCanonicalRequestKey` |
| T-16 | Breaker tripping: attacker triggers origin errors to open the breaker site-wide | only gateway failures count, by ratio with minimum volume; 500 excluded | ADR-7, FR-CB-2 | `TestBreaker500DoesNotTrip`, `TestBreakerNeedsVolume` |
| T-17 | Negative-cache poisoning: attacker-induced failure cached for everyone | negative entries only for gateway failures, status-only, 2 s, and the failing request's inputs are all keyed (strict forwarding), so the negative entry sits on the attacker's own key | FR-NEG-*, D4 | `TestNegativeScopedToKey` |
| T-18 | Slow-reader slot pinning on streamed responses | streamed fetches release their slot at headers; stream bounded by origin timeout | FR-LIM-1 | `TestSlowReaderDoesNotPinSlots` |
| T-19 | Stuck leader stalls a herd | leader aging, follower wait bound, stale fallback | FR-COA-3, FR-COA-4 | `TestCoalesceStuckLeader` |
| T-20 | Background refresh amplification | refresh only on request, one per key, background class cannot use the foreground reserve and never queues | FR-STL-6, FR-LIM-4 | `TestRefreshNeverExceedsReserve` |
| T-21 | Memory exhaustion via large bodies, many headers, many groups | object size cap including headers; group count and length caps; transient buffering bounded by `MaxConcurrent × MaxObjectBytes` | FR-STO-9, FR-STO-10, NFR-4 | `TestOversizedStreamedNotBuffered` |
| T-22 | Shard grinding: attacker chooses inputs so keys land in one memory-store shard | shard index from `maphash` with a per-process seed | ADR-3, 05 §5.2 | `TestShardDistributionAdversarial` |
| T-23 | Epoch growth via `Cache-Group-Invalidation` (A3) | groups bounded per response; soft and invalid epochs live in a fixed-size sketch, so volume cannot grow memory | 05 §4.4 | `TestInvalidationFloodBounded` |
| T-31 | Marker and negative-entry poisoning: a client sends `Authorization: junk` or `Cache-Control: no-store` on a cold URL; the origin's response (401, or anything under request `no-store`) is not storable, and a hit-for-miss marker or negative entry would then switch coalescing or caching off for that URL for everyone | markers and negative entries are written only for response-driven failures on requests without `Authorization` or request `no-store`, and never after a forwarded `Forward.Allow` field, an unkeyed `Cache-Control` or `Pragma` field (forwarded as sent, so a 9000-byte value that the origin answers with 431 or 503 would otherwise land on the shared key), or under `ForwardAll`; neither ever replaces a stored response | FR-STO-12, FR-NEG-4 | `TestMarkerNotFromAuthorizedRequest`, `TestMarkerNotFromRequestNoStore`, `TestNoMarkerAfterUnkeyedInput`, `TestNegativeNotFor500`, `TestClassifyKeyedHeaders` |
| T-32 | Tenant cache pollution (A3): a tenant requests its own unique URLs twice each so they reach the S3-FIFO main queue and evict other tenants' hot entries | per-owner byte quota per shard; over-quota inserts evict only the same owner's entries or are declined | FR-FAIR-2 | `TestOwnerQuotaIsolatesTenants` |
| T-33 | Snapshot resurrection: a purge issued while a node was down (or a stale snapshot file) brings purged content back after restart | loaded entries are soft-stale as of load time; hard epochs persist; the file is deleted after a successful load; incomplete files are ignored. Tampering needs write access to the host, which is out of scope; CRC only detects corruption | FR-SNP-1..3 | `TestSnapshotLoadIsSoftStale`, `TestSnapshotHardEpochSurvives`, `TestSnapshotCorruptRecordsSkipped` |
| T-34 | Targeted-field leak: origin sets `CDN-Cache-Control` globally and marks per-user pages `private` only in `Cache-Control`; RFC 9213 would have the cache ignore `private` | `private`, `no-store`, `no-cache` in `Cache-Control` still apply (stricter than RFC 9213) | FR-TCC-3 | `TestTargetedFieldKeepsPrivate` |
| T-35 | Variant spoofing: client sends `Weir-Variant` or edits the assignment cookie to pick a variant | header always replaced; cookie HMAC-signed with rotation | [10 §2](10-experiments-spec.md) E2, §5 | Phase 3 tests |
| T-36 | Downstream variant mixing: a CDN in front of Weir caches variant responses (or the assignment `Set-Cookie`) under a key without the variant | experiment responses rewritten to `private` for downstream unless the operator declares variant-aware downstream caches | [10 §2](10-experiments-spec.md) E7 | Phase 3 tests |
| T-37 | Range amplification: range requests on many URLs trigger full-object fetches | range requests never cause foreground full fetches; background fill only when the origin's 206 declares a total size within `MaxObjectBytes`, one flight per key, background class (no queueing, reserve respected) | FR-RNG-4 | `TestRangeMissBackgroundFillBounded` |
| T-38 | Multi-node purge gap: with per-node memory stores, a purge sent to one node leaves others serving the content | Phase 2 is single-node (D17); multi-node requires the Valkey store (shared entries and epochs) | D17 | deployment rule, checked at Phase 2 kickoff |
| T-39 | Slow-upload slot pinning: many requests with slowly streamed bodies hold origin slots | bodies use a separate upload pool, so cacheable misses and bodyless requests are unaffected; adapters must bound body size and read time. Residual: an upload flood can still shed other uploads | FR-LIM-7 | `TestSlowUploadsDoNotStarveMisses` |
| T-40 | Trace-header echo: an origin reflects `X-Request-Id` or `traceparent` into a cacheable body, making it an unkeyed input | forwarded by default for tracing; validated format, length and byte set (letters, digits and `-_.:/=+@,;*~!|`), at most 32 `tracestate` lines, none empty, no `..`, so neither field can carry a payload or a field count that a WAF or origin would reject onto the shared key (T-31); documented as origin misuse; operators can set `Forward.NoTraceHeaders` | FR-FWD-6 | `TestTraceparentValidated` |
| T-41 | Drip-feeding origin: an origin (or something in front of it) sends a cacheable body one byte at a time to hold limiter slots | buffered body reads stay under the total origin timeout | FR-TMO-1 | `TestDripOriginReleasesSlot` |
| T-42 | Forgotten incident mode: `ModeStaleOnError` or `ModeBypass` left on for days | mandatory expiry of at most 24 h; modes not persisted; events and logs on every change | FR-MODE-1 | `TestModeExpires` |
| T-43 | Memory overcommit: several default-sized stores in one process each take 40% of `GOMEMLIMIT` | the Caddy adapter splits the budget across stores; library users constructing several engines are warned in docs | FR-MEM-1 | `TestMemorySizingSplit` (Caddy) |
| T-44 | Upgrade smuggling: a WebSocket over HTTP/2 extended CONNECT carries no `Upgrade` header and slips past upgrade detection | `CONNECT` in any form is rejected by `Serve` and routed around by adapters. The one `Upgrade` served normally is a lone `h2c` token; `Upgrade` and `HTTP2-Settings` are hop-by-hop, so the origin sees a plain request and key and forward stay equal. `h2c` with any other token is still an upgrade | FR-UPG-1 | `TestConnectRejected`, `TestH2CUpgradeServedNormally`, `TestH2CKeyedLikePlainRequest` |
| T-24 | Unbounded host keyspace: arbitrary `Host` values create entries | adapter routes by host before Weir (Caddy site blocks); inside Weir, new keys land in the small S3-FIFO queue and churn only it | ADR-6 | covered by T-11 tests |
| T-28 | Group-invalidation amplification: attacker makes cheap unsafe requests whose responses carry `Cache-Group-Invalidation` for a large, hot group, forcing every entry in it to revalidate before it can be served | grouped invalidation is applied as a soft purge, so SWR and SIE windows still apply and revalidation is background where the origin allows it; revalidations are coalesced and limiter-bound | FR-INV-2 | `TestGroupInvalidationIsSoft` |
| T-29 | Invalidation flood: many unsafe requests to distinct URIs (`POST /x?r=<random>` answered with 2xx) each create a MUST-invalidate epoch (RFC 9111 §4.4). An exact epoch table either grows without bound or, if capped with a global fallback, lets ~100 000 cheap requests invalidate the whole cache | fixed-size max-timestamp sketch for soft and invalid epochs: memory constant, never under-invalidates, over-invalidation degrades gradually with volume (about 4% of entries at 60 000 invalidations per entry lifetime at defaults) | 05 E-7..E-9 | `TestInvalidationFloodBounded`, `EpochNeverUnderInvalidates` |
| T-30 | Origin clock skew: an origin whose `Date` runs behind makes responses look old on arrival (`apparent_age`), turning the cache off; one running ahead is harmless | age uses `age_value + response_delay` only (RFC 9111 §4.2.3 permits it); `Date` is used for `Expires` arithmetic and response ordering, both of which compare origin time with origin time | FR-FRS-4 | `TestOriginClockSkewDoesNotStale` |

### Multi-tenant and operational

| ID | Threat | Answer | Refs | Test |
|---|---|---|---|---|
| T-25 | Tenant A invalidates tenant B's groups (RFC 9875 §5) | groups are scoped by origin (scheme, host, port); tenants on distinct hosts cannot touch each other. Tenants sharing one host share a group namespace; operators who host multiple parties on one host should set `CacheGroups.Ignore` | FR-PRG-6 | `TestGroupsScopedByOrigin` |
| T-26 | Unauthenticated purge | `Purge` is a Go call; exposing it over HTTP is the adapter's job and must be authenticated (Caddy: admin API, which is local-only by default) | 08 §7 | manual review item |
| T-27 | Timing and Cache-Status side channel: learning whether a URL was recently requested (RFC 9111 §7.2) | acceptable for a shared reverse-proxy cache of public content; `NoCacheStatus: true` disables the header; Weir never emits the `key` parameter | FR-SRV-9 | `TestCacheStatusNoKey` |

## 4. Security invariants

These hold for every build. Each has at least one test that fails if it breaks.

- INV-1 (key-forward consistency). For a cacheable, non-bypassed request, every header, path byte, query byte and body byte in the forwarded request is either a key input or was allowed by `Forward.Allow` or is one of `Authorization`, `Cache-Control`, `Pragma`, `traceparent`, `tracestate`, `X-Request-Id` (FR-FWD-6, D29), or the `Accept-Encoding` bucket, which enters the key through `Vary` unless `Key.Headers` names it (01 §5.2.3), or a validator Weir added. Property test: random requests, random config; recompute the key from the forwarded request alone and compare.
- INV-2 (injective key). Distinct keyed-input tuples produce distinct canonical encodings.
- INV-3 (malformed means absent). A keyed header whose normalizer rejects the value is absent from both key and forwarded request.
- INV-4 (no unsafe storage). No stored entry came from a response with `no-store` (without `must-understand`), `private`, `Vary: *`, unstripped `Set-Cookie`, or from an `Authorization` request without `public`, `s-maxage` or `must-revalidate`.
- INV-5 (bounded). Every structure in NFR-3 stays within its bound under the adversarial load tests.
- INV-6 (no panic). No input panics the engine (fuzzing).
- INV-7 (budgeted origin). The number of concurrent `Origin.Fetch` calls holding slots never exceeds `MaxConcurrent`, and never exceeds `MaxPerPartition` for one partition (checked by the test origin on every call).

## 5. Residual risks

- R-1. Origins that vary on something outside the request (client IP seen through `X-Forwarded-For`, time of day) without `Vary`. Weir cannot see these. Mitigation is origin discipline and T-4 guidance.
- R-2. Distinct-path floods consume the origin budget (T-12). Use a rate limiter in front.
- R-3. Operators who set `ForwardAll`, `StripSetCookie`, `VaryAllow: [Cookie]`, or large `Forward.Allow` lists take back risks the defaults remove. `New` logs a warning for `ForwardAll`; the README lists the others. Under `ForwardAll`, bypass cookie rules match names as sent: percent-encoding, letter case, and a leading or trailing encoded space or `+` are covered, but an origin that rewrites cookie names in other ways before reading them (PHP turns `.` and space into `_`) needs each spelling listed in `Bypass.Cookies`. Strict forwarding has no such gap, because no unkeyed cookie reaches the origin.
- R-4. Epoch fail-open with remote stores (T-9).
- R-5. Web cache deception still works if the origin itself marks a per-user page as publicly cacheable. No cache can fix that.
- R-6. An in-process origin that varies its response on context values set by middleware in front of Weir (T-45). Put that middleware behind Weir, or bypass the route.
- R-7. A flight led by a request with unkeyed input (`Cache-Control`, `Pragma`, a `Forward.Allow` field, anything under `ForwardAll`) shares a 500, 502, 503 or 504 answer with the followers that joined it (FR-COA-5), although nothing is written to the store (T-31). An attacker needs an origin that answers 5xx to client-chosen bytes and must win the leader race on a key with no fresh entry; the reach is one flight, bounded by the origin timeout. Not coalescing such requests would switch coalescing off under `ForwardAll` and for every browser reload, which costs more than it saves.
- R-8. With `MissRate.Throttle`, a client that sends max(500, 9 × the path's hits) distinct-query requests per window to one path keeps it capped at one origin fetch for as long as it sends (60 requests a second served 390 of 1 200 legitimate misses on that path in test, against all 1 200 without `Throttle`). Sheds from a full global limiter count too, so a slow origin or a distinct-path flood (T-12) can get a busy, miss-heavy path capped in the next window. An attacker who floods every other window is never throttled at all. `Throttle` trades the path's own misses for origin protection; it is off by default, and `MaxPerPartition` is the bound either way.

## 6. Review checklist for security-sensitive code

Required for any change under `internal/keys`, to storability rules, or to forwarding (P1). The reviewer, human or agent, answers each line in the pull request.

1. Does any new request input reach the forwarded request? If so, is it keyed, or explicitly operator-allowed, and does INV-1's property test cover it?
2. Does any new input reach the key? Is it length-prefixed and tagged, with a new tag byte, and is the key version bumped if old keys would now collide or change meaning?
3. What does the code do with an empty value, a 10 KiB value, non-ASCII bytes, duplicate lines with different values, and a value made of separators only? Is there a fuzz seed for each?
4. Can the new code allocate in proportion to attacker input beyond the limits in `LimitsConfig`?
5. Does the change add a path that reaches `Origin.Fetch` without going through the fetch function (P3)?
6. Does the change add a way for an entry to be stored that bypasses one of the INV-4 checks?
7. Are threat IDs cited in comments where the code exists because of a threat?
