# Status

Updated: 2026-09-27
Phase: 1
Current card: none
Card state: ready
Branch: card/M1-01-directives
PR: #7 merged
Next card: M1-02

## Blockers

none

## Waiting on Ashwin

- Confirm the coalesce-default clamp: a zero `LeaderMaxAge`/`FollowerMaxWait` now defaults to min(10s, `Timeouts.Origin`) instead of failing validation when the origin timeout is under 10s (01 §6 and 04 §1.1 updated).
- Approve the storetest API: `Run(t, newStore, opts ...Option)` with `WithoutEpochs()` and `Synctest()` (05 §8). `Synctest()` replaces the card's `func(d time.Duration)` advance hook, which cannot work: `synctest.Test` forbids `t.Run` inside a bubble and stores read `time.Now` (D9).
- Pragma with Cache-Control: `ParseRequest` sets `NoCache` from `Pragma: no-cache` even when the request also has `Cache-Control` (docs say "plus Pragma: no-cache" unconditionally). RFC 7234 §5.4 ignored Pragma when Cache-Control was present. Only matters with `Client.HonorRevalidation`. Keep as is, or ignore Pragma when Cache-Control is present (FR-SRV-8 wording change)?
- Resolve a conflict between CLAUDE.md hard rule 6 (no real-clock sleeps outside the `load` tag) and 05 §8 (remote stores run `ExpiredIsNotFound` on the real clock, now a 3 s sleep). Proposal: exempt remote-store conformance runs from rule 6, or run them only under an integration build tag.

## Notes for the next session

- httpcc: `ParseResponse`/`ParseRequest` in internal/httpcc/directives.go. `Seconds{V, Set, Invalid}`; `V` is 0 when Invalid. M1-02 zeroes the lifetime on `Invalid` or `Duplicates` (FR-FRS-2).
- `Duplicates` covers all four delta-seconds directives, SWR and SIE included (FR-FRS-2 says "a directive"; 04 §4.1 comment updated).
- Request `max-stale` without argument parses as 2147483648 (any staleness). Request duplicates keep the first value.
- Doc wording drift, not yet fixed: 01 FR-SRV-1 and FR-STL-3 say "unqualified `no-cache`", while the 01 RFC table and 04 `FlagNoCache` treat qualified and unqualified the same. The parser follows the table.
- storetest: in-process stores pass `storetest.Synctest()`; M1-09 adds `WithoutEpochs()` until M1-10. `New` uses `nopStore` until M1-09.
- engine.go: `Close` does not wait for foreground `Serve` calls; decide before M1-12 closes an engine-owned store under them.
