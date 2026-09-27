# Status

Updated: 2026-09-27
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: card/M1-04-validate
PR: #10 https://github.com/AshwinSathian/weir/pull/10
Next card: M1-05

## Blockers

none

## Waiting on Ashwin

- Confirm the coalesce-default clamp: a zero `LeaderMaxAge`/`FollowerMaxWait` now defaults to min(10s, `Timeouts.Origin`) instead of failing validation when the origin timeout is under 10s (01 §6 and 04 §1.1 updated).
- Approve the storetest API: `Run(t, newStore, opts ...Option)` with `WithoutEpochs()` and `Synctest()` (05 §8). `Synctest()` replaces the card's `func(d time.Duration)` advance hook, which cannot work: `synctest.Test` forbids `t.Run` inside a bubble and stores read `time.Now` (D9).
- Pragma with Cache-Control: `ParseRequest` sets `NoCache` from `Pragma: no-cache` even when the request also has `Cache-Control` (docs say "plus Pragma: no-cache" unconditionally). RFC 7234 §5.4 ignored Pragma when Cache-Control was present. Only matters with `Client.HonorRevalidation`. Keep as is, or ignore Pragma when Cache-Control is present (FR-SRV-8 wording change)?
- Resolve a conflict between CLAUDE.md hard rule 6 (no real-clock sleeps outside the `load` tag) and 05 §8 (remote stores run `ExpiredIsNotFound` on the real clock, now a 3 s sleep). Proposal: exempt remote-store conformance runs from rule 6, or run them only under an integration build tag.

## Notes for the next session

- keys API (M1-04): `Validate(r *Request, c *Config) (host string, err error)` returns the normalized host; key and forwarded request must both use it (P2). Errors are `keys.ErrUpgrade` and `*keys.RequestError{Reason}`; M1-07 `Classify` (or the engine) maps them to `weir.ErrUpgradeNotSupported` and `*weir.RequestError`. `keys.Config` so far has only the path and query limits.
- httpcc API: `Lifetime`, `Jitter`, `StaleWindows`, `CorrectedInitialAge`, `CurrentAge`, `Evaluate`, `State` (zero `State` is invalid).
- `Evaluate` trusts `epOK`: the caller's `newestEpoch` applies FR-PRG-7 (04 §6.3). A zero or unknown `EpochMode` with `epOK` returns `Unusable`.
- FR-MODE-2 (`ModeStaleOnError`) must tell "stale forbidden" from "no SIE window" using `ep.Mode` and `e.Flags`; `sieOK` alone is false for both.
- Doc wording drift, not yet fixed: 01 FR-SRV-1 and FR-STL-3 say "unqualified `no-cache`", while the 01 RFC table and 04 `FlagNoCache` treat qualified and unqualified the same. The parser, `StaleWindows` and `Evaluate` follow the table.
- Memory store (M1-06 or wherever epochs land): keep `Epoch.At` as a `time.Now()` value with its monotonic reading. `Evaluate` computes `now.Sub(ep.At)`; a wall-only `At` lets a backward clock step shorten or cancel a soft purge.
- Codec (store/codec.go): `Evaluate` trusts `SWR`/`SIE` and does not re-check `FlagMustRevalidate`/`FlagProxyRevalidate`. The decoder should zero both windows when those flags are set, so corrupt bytes cannot enable stale serving (FR-STL-3).
- engine.go: `Close` does not wait for foreground `Serve` calls; decide before M1-12 closes an engine-owned store under them.
