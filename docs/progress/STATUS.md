# Status

Updated: 2026-09-27
Phase: 0
Current card: none
Card state: awaiting-merge
Branch: card/P0-05-engine
PR: pending
Next card: P0-06

## Blockers

none

## Waiting on Ashwin

- Review and merge the P0-05 PR.
- Confirm the coalesce-default clamp: a zero `LeaderMaxAge`/`FollowerMaxWait` now defaults to min(10s, `Timeouts.Origin`) instead of failing validation when the origin timeout is under 10s (01 §6 and 04 §1.1 updated).

## Notes for the next session

- engine.go: `goBackground(f)` is the only way to start an engine goroutine. It checks `closed` and calls `wg.Go` under `e.mu`, so `Close` never races a `wg.Add`. Flights and background refresh must use it.
- fetch.go: `(*Engine).fetch(ctx, req, origin)` is a skeleton that always streams. It takes a plain `*Request`; M1-07 and later cards change it to the `fetchSpec`/`fetchResult` shape in 04 §6.7 and add the limiter, breaker and buffered path. `timeoutOrOrigin` maps a done ctx to `ctx.Err()`; flights must map it to `ErrClosed` (04 §1.3).
- `Serve` forwards `*req` unchanged and leaves `Cache` zero until classification lands (M1-07). A nil `origin` panics inside `safeFetch` and comes back as `*OriginError` (502).
- `New` uses `nopStore` when `Config.Store` is nil (replace with the memory store in M1-09) and runs the `store.Sizer` MaxObjectBytes check. Typed-nil `Store` is rejected in `validate`.
- Engine tests are package `weir_test` (testorigin imports weir); `export_test.go` exposes `GoBackground`.
- Unchecked config ranges from P0-03 are still open for their component cards (limiter, breaker, miss-rate, heuristic fraction).
