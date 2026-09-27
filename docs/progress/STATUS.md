# Status

Updated: 2026-09-27
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: card/M1-02-lifetime
PR: none
Next card: M1-03

## Blockers

none

## Waiting on Ashwin

- Confirm the coalesce-default clamp: a zero `LeaderMaxAge`/`FollowerMaxWait` now defaults to min(10s, `Timeouts.Origin`) instead of failing validation when the origin timeout is under 10s (01 §6 and 04 §1.1 updated).
- Approve the storetest API: `Run(t, newStore, opts ...Option)` with `WithoutEpochs()` and `Synctest()` (05 §8). `Synctest()` replaces the card's `func(d time.Duration)` advance hook, which cannot work: `synctest.Test` forbids `t.Run` inside a bubble and stores read `time.Now` (D9).
- Pragma with Cache-Control: `ParseRequest` sets `NoCache` from `Pragma: no-cache` even when the request also has `Cache-Control` (docs say "plus Pragma: no-cache" unconditionally). RFC 7234 §5.4 ignored Pragma when Cache-Control was present. Only matters with `Client.HonorRevalidation`. Keep as is, or ignore Pragma when Cache-Control is present (FR-SRV-8 wording change)?
- Resolve a conflict between CLAUDE.md hard rule 6 (no real-clock sleeps outside the `load` tag) and 05 §8 (remote stores run `ExpiredIsNotFound` on the real clock, now a 3 s sleep). Proposal: exempt remote-store conformance runs from rule 6, or run them only under an integration build tag.

## Notes for the next session

- httpcc lifetime API: `Lifetime`, `Jitter`, `StaleWindows` in internal/httpcc/lifetime.go; `CorrectedInitialAge`, `CurrentAge` in age.go. The engine passes an `httpcc.Config` built from the defaulted `FreshnessConfig` (04 §4.2).
- All lifetimes and windows are clamped to 2147483648 s and ages saturate, so M1-03/M1-11 retention sums cannot overflow.
- Stale-window defaults are per directive (FR-STL-2 clarified, approved in the M1-02 session): origin `stale-if-error` alone still gets `DefaultStaleWhileRevalidate`.
- HTTP-dates outside GMT/UTC are invalid (`http.ParseTime` resolves abbreviations against host TZ).
- Doc wording drift, not yet fixed: 01 FR-SRV-1 and FR-STL-3 say "unqualified `no-cache`", while the 01 RFC table and 04 `FlagNoCache` treat qualified and unqualified the same. The parser and `StaleWindows` follow the table.
- engine.go: `Close` does not wait for foreground `Serve` calls; decide before M1-12 closes an engine-owned store under them.
