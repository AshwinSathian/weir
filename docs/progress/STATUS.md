# Status

Updated: 2026-09-27
Phase: 0
Current card: none
Card state: awaiting-merge
Branch: card/P0-06-storetest
PR: none
Next card: M1-01

## Blockers

none

## Waiting on Ashwin

- Review and merge the P0-06 PR.
- Confirm the coalesce-default clamp: a zero `LeaderMaxAge`/`FollowerMaxWait` now defaults to min(10s, `Timeouts.Origin`) instead of failing validation when the origin timeout is under 10s (01 §6 and 04 §1.1 updated).
- Approve the storetest API: `Run(t, newStore, opts ...Option)` with `WithoutEpochs()` and `Synctest()` (05 §8). `Synctest()` replaces the card's `func(d time.Duration)` advance hook, which cannot work: `synctest.Test` forbids `t.Run` inside a bubble and stores read `time.Now` (D9).
- Resolve a conflict between CLAUDE.md hard rule 6 (no real-clock sleeps outside the `load` tag) and 05 §8 (remote stores run `ExpiredIsNotFound` on the real clock, now a 3 s sleep). Proposal: exempt remote-store conformance runs from rule 6, or run them only under an integration build tag.

## Notes for the next session

- storetest: every store test calls `storetest.Run(t, newStore, storetest.Synctest())`; in-process stores must pass `Synctest()` or `ExpiredIsNotFound` sleeps 3 s on the real clock. M1-09 adds `WithoutEpochs()` until M1-10.
- `WithoutEpochs()` still calls SetEpoch/NewestEpoch in `ContextCanceled` and `ClosedStore`: stubs return nil, or ErrUnavailable after Close.
- `Run` registers `t.Cleanup(s.Close)` for every store it builds; stores may also register their own (Close is idempotent).
- M1-08 adds `CodecRoundTrip`; M1-10 adds `EpochNeverUnderInvalidates` and `EpochHardCap` to storetest.go's case table.
- engine.go: `goBackground(f)` is the only way to start an engine goroutine. `Close` does not wait for foreground `Serve` calls; decide before M1-12 closes an engine-owned store under them.
- `New` uses `nopStore` when `Config.Store` is nil (replace with the memory store in M1-09).
