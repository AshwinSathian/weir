# Status

Updated: 2026-09-28
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: card/M1-10-review-fixes
PR: #17 https://github.com/AshwinSathian/weir/pull/17
Next card: M1-11

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

## Notes for the next session

- `store/memory` epochs exist (05 §4, §5.4): exact global tag (`store.TagGlobal()`, moved from `internal/keys`, which now delegates), capped hard map pruned after `MaxRetention`, two 2^19-cell sketch planes, `newest` fast path. `SetEpoch` with an invalid mode returns an unwrapped error (not a store sentinel).
- E-11 clamp counts from `RequestTime` (approved 2026-09-28); the clamped deadline lives on the node.
- `New` builds a fixed 256 MiB memory store when `Config.Store` is nil (`ponytail:` in engine.go); FR-MEM-1 sizing is M1-15. `Storable.MaxObjectBytes` vs `Entry.Size` overhead margin still open for M1-15.
- Hard-epoch prune walks up to `MaxHardEpochs` under the write lock when the table is full (`ponytail:` in epochs.go). Worst-case S3-FIFO eviction walk still to measure in M1-18.
- Carried from M1-07/M1-08: `New` must still reject `Forward.Allow` entries naming keyed or hop-by-hop fields and compile query patterns; `Close` does not wait for foreground `Serve` calls (decide before M1-12); codec accepts any header-name case (revisit with Valkey).
