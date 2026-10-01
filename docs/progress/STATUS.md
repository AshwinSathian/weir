# Status

Updated: 2026-10-01
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: card/M2-02-coalesced-fetch
PR: #31 https://github.com/AshwinSathian/weir/pull/31
Next card: M2-03

## Blockers

none

## Waiting on Ashwin

- M1-18 closes M1, which triggers PLAN P0.0: add `SECURITY.md`, flip the repo to public, enable private vulnerability reporting (D40), tag `v0.1.0` (D24). The card says to ask before flipping visibility. After merging #29, say whether to do P0.0 now (and in which session) or hold it.

## Decided 2026-09-28 (delegated by Ashwin after the #24 adversarial review)

Already built, now confirmed: the weirhttp default transport (compression off, no proxy), the coalesce-default clamp to min(10s, `Timeouts.Origin`), and the storetest `Run(t, newStore, opts...)` API with `Synctest()`. The other nine decisions are cards, and each card changes its spec text together with its code:

- M1-17c (merged), the key boundary: h2c is served normally; `#` is rejected in path and query; the cookie limit counts keyed pairs only.
- M1-17d (merged), storability: `s-maxage` must be valid to permit an `Authorization` response; markers are suppressed after unkeyed input; remote conformance tests move behind an `integration` tag.
- M1-17e (merged), responses: the engine strips hop-by-hop fields; a 304 keeps `Content-Encoding` and `Content-Type`; Pragma is ignored when `Cache-Control` is present.

The cards' Notes give the reasons and the options rejected. All three come before M1-18, because closing M1 makes the repo public.

## Notes for the next session

- M2-02: `flight.go` holds `fetchCoalesced`, `runFlight`, `leaveFlight`, `staleOnTimeout`, `fetchDirect`; `serve.go` splits `fetchStored` (fetch, freshen, store) from `respond`. Followers are served `fromEntry(fr.entry)`; a follower of an unshareable result calls `fetchDirect` (`ponytail:`). M2-03 replaces that with the FR-COA-5 re-entry rule.
- M2-02: `cacheable` sends `c.Authorized` and marker requests to `fetchDirect` (FR-COA-8, FR-STO-12), pinned by `TestCoalesceSkipsDirectRequests`. M2-03 adds its named tests (`TestAuthorizedNotCoalesced` etc.) and the marker rules on top.
- M2-02: on follower timeout without a stale-if-error entry, the creator keeps waiting on its own flight (04 §6.4 updated): the default `FollowerMaxWait` equals `Timeouts.Origin` below 10 s, so refetching doubled origin load.
- #31 adversarial review: followers share entries the store would keep but that need validation before reuse (`no-cache`, `max-age=0`), as Varnish and nginx do. A strict RFC 9111 §4 reading says they should not; changing it would stop coalescing for typical `no-cache` HTML. Decide in M2-03 whether to gate sharing on `Evaluate` being Fresh.
- M7-01: followers get `fr.entry` without `keys.VaryMatches`; safe only while storability refuses Vary. Add the check in `fetchCoalesced` (comment there).
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
- Unowned events: no card emits `EvRequest`, `EvFetchStart`, `EvFetchEnd`, or `EvStoreError{epoch}`; over-size and 5xx responses emit no `EvNotStored`. M1-16 decided not to fold `EvRequest` in (its "every hit/stale/miss/.../error" scope is bigger than a Size S card and depends on M6/M7 reason values); give it its own card. CONNECT/upgrade rejection stays event-less too: `EvKeyRejected`'s reason vocabulary is `RequestError.Reason` values only, and FR-UPG-1 has adapters intercept these before `Serve`.
- Carried: `New` must reject `Forward.Allow` entries naming keyed or hop-by-hop fields and compile query patterns; decide whether `Close` waits for foreground `Serve` calls; codec header-name case; hard-epoch prune and S3-FIFO walk to measure in M1-18.
- `invalidate` (purge.go) handles FR-INV-1 URI, `Location` and `Content-Location`; FR-INV-2 groups are M9-03. Failed `SetEpoch` writes are ignored with no event.
- Newest-wins lives in `storeResponse` (serve.go), not `fetch`; M2 moving the store into the flight must keep the found-record exemption (`sameRecord`). `TestNewerResponseWins` sends a second GET while the first is gated, so under M2 coalescing it must use a key that cannot join the flight.
- Event streams (FR-STR-1, M1-16): `fetch` checks `Content-Type` against `text/event-stream` or `Storable.StreamTypes` right after headers arrive, before the buffered `io.ReadAll`, and sets `fetchResult.stream`; `cacheable` (serve.go) treats it like `res.over` (skip `storeResponse`, leave `resp.Body` as fetch wired it) but never marks it `over`. M2 coalescing and M5 background refresh must keep a `stream` response out of the flight/refresh path (FR-COA-5: followers re-enter, not share it).
- M1-16 review nits left open (not must-fix): no test exercises the operator-configured `Storable.StreamTypes` branch of `isEventStream` (only the `text/event-stream` literal is covered); `TestConnectRejected`/`TestUpgradeRejected` check no origin call but not that no event fires.
- weirhttp (M1-17): `TransportOrigin.Fetch` sends `//` paths in absolute form, suppresses Go's default `User-Agent`, and relies on `DisableCompression` (04 §10). `Middleware` routes upgrades with `keys.IsUpgrade` (exported from `isUpgrade`).
- M1-17 review nits left open: an origin-form `//x` target through `RequestFrom`'s `RequestURI` branch is untested (needs a raw connection; the test's `//` case goes absolute-form); a nil `TransportOrigin.Target` panics (documented contract).
- weirhttp `HandlerOrigin` (M1-17b): enforces a declared `Content-Length` like net/http's server (short body reads fail with `io.ErrUnexpectedEOF`, so fetch.go never stores it), refuses 204/304 bodies, discards HEAD bodies, recovers panics and `runtime.Goexit` as `ErrOrigin`. A handler that ignores its context and never writes outlives `Close` (04 §10). The Caddy `nextOrigin` should reuse this writer rather than copy it.
- Threat-model gap for the Caddy card: an in-process origin sees the creator request's context values (FR-COA-9: Caddy vars, auth identity set by upstream middleware). Those are unkeyed input `TransportOrigin` never exposes; docs/06 has no row for it. Decide before the Caddy adapter lands.
