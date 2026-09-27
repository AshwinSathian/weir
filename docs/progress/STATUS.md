# Status

Updated: 2026-09-27
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: card/M1-08-entry-codec
PR: #14 https://github.com/AshwinSathian/weir/pull/14
Next card: M1-09

## Blockers

none

## Waiting on Ashwin

- Approve the codec API added in M1-08 (05 §6): `store.Encode(*Entry) ([]byte, error)` and `store.Decode(b []byte, maxBytes int64) (*Entry, error)`, decode errors wrapping `ErrUnavailable`, and time 0 meaning the zero time (a `Last-Modified` of exactly the Unix epoch decodes as absent). Review in PR.
- Confirm the coalesce-default clamp: a zero `LeaderMaxAge`/`FollowerMaxWait` now defaults to min(10s, `Timeouts.Origin`) instead of failing validation when the origin timeout is under 10s (01 §6 and 04 §1.1 updated).
- Approve the storetest API: `Run(t, newStore, opts ...Option)` with `WithoutEpochs()` and `Synctest()` (05 §8). `Synctest()` replaces the card's `func(d time.Duration)` advance hook, which cannot work: `synctest.Test` forbids `t.Run` inside a bubble and stores read `time.Now` (D9).
- Pragma with Cache-Control: `ParseRequest` sets `NoCache` from `Pragma: no-cache` even when the request also has `Cache-Control` (docs say "plus Pragma: no-cache" unconditionally). RFC 7234 §5.4 ignored Pragma when Cache-Control was present. Only matters with `Client.HonorRevalidation`. Keep as is, or ignore Pragma when Cache-Control is present (FR-SRV-8 wording change)?
- Resolve a conflict between CLAUDE.md hard rule 6 (no real-clock sleeps outside the `load` tag) and 05 §8 (remote stores run `ExpiredIsNotFound` on the real clock, now a 3 s sleep). Proposal: exempt remote-store conformance runs from rule 6, or run them only under an integration build tag.

- FR-VAL-1 and `#`: `Validate` accepts `#` (0x23) in `Path` and `RawQuery`, as FR-VAL-1 allows any byte in 0x21-0x7E. net/http passes a raw `#` through `RequestURI`, and `TransportOrigin` forwards it via `URL.Opaque`, so `GET /a#x?q` is keyed as path `/a#x`, query `q`, while nginx-style origins treat `#x?q` as a fragment and serve `/a`. Key and forward stay byte-equal, and browsers never send `#`, so this is cache fragmentation, not poisoning. Proposal: reject `#` in path and query, and `?` in path (RFC 9112 §3.2 origin-form), under the existing `path` and `query` reasons. Changes FR-VAL-1 wording, so it needs your approval.

- Keyed cookies and large Cookie headers: FR-VAL-3 applies `MaxKeyedHeaderBytes` (1 KiB) to all Cookie lines combined, so a 1.2 KiB analytics cookie makes a keyed `lang` absent, and the origin's default-language response is cached under the "no lang" key. Safe (no bypass) but wrong for common traffic. Options: measure only the keyed pairs' bytes, or give Cookie its own limit. Changes FR-VAL-3 wording.

- Trace fields in INV-1: docs/06 INV-1 lists `Authorization`, `Cache-Control`, `Pragma`, Allow and Weir validators as the only unkeyed forwarded fields, but FR-FWD-6 (D29) also forwards `traceparent`, `tracestate` and `X-Request-Id` on cacheable fetches, and M1-07 does. Proposal: add the three trace fields to INV-1's list (06 wording change).
- `tracestate` has no limit: T-40 says trace headers get "validated format and length", but FR-FWD-6 checks only `traceparent` and `X-Request-Id`, so up to the adapter's header limit of `tracestate` reaches the origin unkeyed. Proposal: forward it only as at most 512 bytes of visible ASCII in one or more lines combined (W3C limit), else drop it (FR-FWD-6 wording change).

## Notes for the next session

- `store.Encode`/`store.Decode` exist (05 §6). Remote stores and the FR-SNP-1 snapshot writer call `Decode(b, maxObjectBytes)`; its errors already wrap `ErrUnavailable`.
- `Decode` allocates at most about 16-24x its input (every repeated item carries at least one byte). `Entry.Size` does not charge per-item slice or map overhead; revisit if snapshot load feeds decoded entries into a byte-weighted store.
- Codec accepts header and vary names in any case form; add canonical-form validation when the Valkey store lands if the engine relies on it.
- Carried from M1-07: `keys.Classify` ready for the engine (04 §3.1); `New` must still reject `Forward.Allow` entries naming keyed or hop-by-hop fields and compile query patterns; `Close` does not wait for foreground `Serve` calls (decide before M1-12); TRACE/OPTIONS with `Max-Forwards: 0` and forwarding of `Proxy-Authorization` on pass-through are open spec questions.
