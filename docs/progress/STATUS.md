# Status

Updated: 2026-09-27
Phase: 0
Current card: none
Card state: awaiting-merge
Branch: card/P0-01-public-types
PR: pending
Next card: P0-02

## Blockers

none

## Waiting on Ashwin

- Review and merge the P0-01 PR. It is the first CI run.

## Notes for the next session

- errors.go and request.go exist in the root package. `Origin`/`OriginFunc` are not defined yet; they belong with the engine skeleton (P0-05) unless P0-02 needs them first.
- `FwdReason` and `StaleReason` have no `String` methods. Add them with the Cache-Status rendering (FR-SRV-9), not before.
- `StatusCode` checks context errors before `ErrOrigin` (recorded in 04 §1.3). Engine code that wraps fetch failures can rely on that.
- P0.2 in PLAN-weir.md stays unticked until P0-02 is done.
