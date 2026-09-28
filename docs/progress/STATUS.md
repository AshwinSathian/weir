# Status

Updated: 2026-09-28
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: card/M1-14-head-range
PR: pending
Next card: M1-15

## Blockers

none

## Waiting on Ashwin

- Confirm the coalesce-default clamp: a zero `LeaderMaxAge`/`FollowerMaxWait` now defaults to min(10s, `Timeouts.Origin`) instead of failing validation when the origin timeout is under 10s (01 §6 and 04 §1.1 updated).
- Approve the storetest API: `Run(t, newStore, opts ...Option)` with `WithoutEpochs()` and `Synctest()` (05 §8). `Synctest()` replaces the card's `func(d time.Duration)` advance hook, which cannot work: `synctest.Test` forbids `t.Run` inside a bubble and stores read `time.Now` (D9).
- Pragma with Cache-Control: `ParseRequest` sets `NoCache` from `Pragma: no-cache` even when the request also has `Cache-Control` (docs say "plus Pragma: no-cache" unconditionally). RFC 7234 §5.4 ignored Pragma when Cache-Control was present. Only matters with `Client.HonorRevalidation`. Keep as is, or ignore Pragma when Cache-Control is present (FR-SRV-8 wording change)?
- Resolve a conflict between CLAUDE.md hard rule 6 (no real-clock sleeps outside the `load` tag) and 05 §8 (remote stores run `ExpiredIsNotFound` on the real clock, now a 3 s sleep). Proposal: exempt remote-store conformance runs from rule 6, or run them only under an integration build tag.

- FR-VAL-1 and `#`: `Validate` accepts `#` (0x23) in `Path` and `RawQuery`, as FR-VAL-1 allows any byte in 0x21-0x7E. net/http passes a raw `#` through `RequestURI`, and `TransportOrigin` forwards it via `URL.Opaque`, so `GET /a#x?q` is keyed as path `/a#x`, query `q`, while nginx-style origins treat `#x?q` as a fragment and serve `/a`. Key and forward stay byte-equal, and browsers never send `#`, so this is cache fragmentation, not poisoning. Proposal: reject `#` in path and query, and `?` in path (RFC 9112 §3.2 origin-form), under the existing `path` and `query` reasons. Changes FR-VAL-1 wording, so it needs your approval.

- Keyed cookies and large Cookie headers: FR-VAL-3 applies `MaxKeyedHeaderBytes` (1 KiB) to all Cookie lines combined, so a 1.2 KiB analytics cookie makes a keyed `lang` absent, and the origin's default-language response is cached under the "no lang" key. Safe (no bypass) but wrong for common traffic. Options: measure only the keyed pairs' bytes, or give Cookie its own limit. Changes FR-VAL-3 wording.

- Trace fields in INV-1: docs/06 INV-1 lists `Authorization`, `Cache-Control`, `Pragma`, Allow and Weir validators as the only unkeyed forwarded fields, but FR-FWD-6 (D29) also forwards `traceparent`, `tracestate` and `X-Request-Id` on cacheable fetches, and M1-07 does. Proposal: add the three trace fields to INV-1's list (06 wording change).
- `tracestate` has no limit: T-40 says trace headers get "validated format and length", but FR-FWD-6 checks only `traceparent` and `X-Request-Id`, so up to the adapter's header limit of `tracestate` reaches the origin unkeyed. Proposal: forward it only as at most 512 bytes of visible ASCII in one or more lines combined (W3C limit), else drop it (FR-FWD-6 wording change).

- FR-STO-5 and malformed `s-maxage`: a response to an `Authorization` request is stored as shareable when its only permission is an invalid (`s-maxage=abc`) or conflicting repeated `s-maxage`. Lifetime is 0, but an explicit `stale-if-error` or `ModeStaleOnError` could serve it stale to another user on origin error (T-8). Proposal: for FR-STO-5, count `s-maxage` only when valid and not duplicated (FR-STO-5 wording change).

- Markers from other unkeyed inputs: FR-STO-12 and T-31 block markers only for `Authorization` and request `no-store`. Trace headers (default), `Forward.Allow` headers and `ForwardAll` also reach the origin unkeyed, so an origin that answers them with `Set-Cookie`, `private` or a non-storable status lets one client plant a 30 s marker for everyone (coalescing off from M2). Proposal: no marker when the forwarded request carried any unkeyed header other than trace headers, or drop markers entirely under `ForwardAll` (FR-STO-12 wording change).

- 304 and Content-Encoding: FR-SRV-3 copies every 304 field except Content-Length into the stored entry, so a 304 that names a different `Content-Encoding` (or `Content-Type`) relabels the stored body, and every later hit serves bytes that do not match their coding. RFC 9111 §3.2 lets a cache keep fields the stored body depends on. Proposal: also keep the stored `Content-Encoding` on freshen (FR-SRV-3 wording change).

## Notes for the next session

- `cacheable` (serve.go) validates StaleSWR and NeedsValidation entries with validators via `fetch(..., prior)`; SWR still validates in the foreground (`ponytail:`, M5). Under M5, StaleSWR must be served before the `only-if-cached` and Range checks, which today reject or pass through stale SWR entries.
- A Range request that no entry answers (miss or stale) goes through `pass` via `keys.Classified.AsRangePass()` with Range and If-Range (FR-SRV-5 updated). HEAD with Range goes forward as GET and the body is dropped (FR-FWD-4). M11-01 adds 206 from entries; FR-RNG-4's background fill hooks into that `pass` call.
- With `HonorRevalidation`, `no-cache`/`max-age=0` turn Fresh into NeedsValidation (`forcesValidation`), except under `only-if-cached`.
- `fetch` retries a strong-ETag-mismatch 304 under the same timeout; M4 must keep the retry under the same limiter slot. After a validation whose response is unstorable and response-driven, `setMarker` relies on the read-before-write to skip the marker.
- Unowned events: no card emits `EvRequest`, `EvFetchStart`, `EvFetchEnd`, or `EvStoreError{epoch}`; over-size and 5xx responses emit no `EvNotStored`. Give them a card or fold into M1-16.
- Carried: newest-wins store rule (M1-15); `New` must reject `Forward.Allow` entries naming keyed or hop-by-hop fields and compile query patterns; decide whether `Close` waits for foreground `Serve` calls; codec header-name case; hard-epoch prune and S3-FIFO walk to measure in M1-18.
