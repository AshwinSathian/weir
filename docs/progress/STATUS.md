# Status

Updated: 2026-09-27
Phase: 0
Current card: none
Card state: awaiting-merge
Branch: card/P0-04-testorigin
PR: none
Next card: P0-05

## Blockers

none

## Waiting on Ashwin

- Review and merge the P0-04 PR.
- Confirm the coalesce-default clamp: a zero `LeaderMaxAge`/`FollowerMaxWait` now defaults to min(10s, `Timeouts.Origin`) instead of failing validation when the origin timeout is under 10s (01 §6 and 04 §1.1 updated).

## Notes for the next session

- config.go: `prepareConfig(Config) (Config, error)` copies slices, applies defaults, canonicalizes, validates. P0-05's `New` calls it, then builds the default store and runs the FR-LCY-1 store-size rule (`Storable.MaxObjectBytes` against the store's `MaxObjectBytes()`), which is not in prepareConfig.
- P0-05: a typed-nil `Config.Store` panics like a typed-nil observer did; reject it in `New` the same way (`reflect`, see the Observer check in `validate`).
- Config shape decided this session: `NoCacheStatus` bool (CacheStatus "" means "Weir"), negative `Bypass.ReportStrippedCookies` disables the report, `Forward.NoTraceHeaders`, `Limiter.MaxUpload` (0: max(1, MaxConcurrent/4)), `Timeouts.StreamIdle`.
- Unchecked ranges left for their cards: `ReserveForeground > MaxConcurrent` (limiter), `MaxOpenFor < OpenFor` and `Breaker.Window` under 10 buckets of 1ns (breaker), `MissRate.MinRatio > 1`, `HeuristicFraction > 1`. Reject or clamp them when the component lands.
- P0-05: `New` should log a warning when `Forward.Allow` names `Cookie`, `Authorization` or `Proxy-Authorization` (unkeyed forwarding, R-3), like it does for `ForwardAll`.
- Query patterns (`Key.QueryDrop`/`QueryKeep`) are stored as strings only; M1-05 compiles them.
- testorigin: `NewChecked(tb, maxConcurrent, maxPerPartition)` (07 §3 amended). In-flight counts cover the `Fetch` call only, not body reads; if the limiter holds a slot until the body closes, M4-02 may want the body's Close to release the count.
- P0-05's `TestServePassThroughStub` can use `testorigin.New()` with `Default(Behavior{...})` and `Requests()` for the forwarded request.
