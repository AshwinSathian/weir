# Status

Updated: 2026-09-27
Phase: 0
Current card: none
Card state: awaiting-merge
Branch: card/P0-02-store-observer-types
PR: https://github.com/AshwinSathian/weir/pull/2
Next card: P0-03

## Blockers

none

## Waiting on Ashwin

- Review and merge the P0-02 PR.
- Approve the `EvMode` row added to 04 §9.2: FR-MODE-1 requires the event but the catalog omitted it. The reason vocabulary (`normal`, `stale-on-error`, `bypass`, the new mode) and "or it expired" trigger are new.

## Notes for the next session

- store/store.go holds every 04 §2 type, `Store`, `Scrubber`, `Sizer`, `ErrNotFound`, `ErrUnavailable` and `Entry.Size`. Tag computation (`TagGlobal` etc.) belongs to internal/keys, not yet written.
- observer.go: `EventKind` constants end with an unexported `evCount` sentinel; add new kinds before it and give each a name in `eventKindNames` (TestEventKindString enforces it). `emit(obs, ev)` is the nil-safe helper the engine wraps in P0-05.
- types.go: `Purge`, `PurgeMode`, `WarmStats`, `EngineStats`, `BreakerState`, `Mode`. `Engine.Stats` and `Config` are still missing; PLAN P0.2 is ticked because its mapped cards are done, and they land in P0-03 and P0-05.
- `store.Kind` and `store.EpochMode` start at 1 so an unset value is invalid; `PurgeSoft`, `BreakerClosed`, `ModeNormal` are zero values.
- deps_test.go runs `go list` with `GOWORK=off`, so it keeps checking only the root module once go.work exists (M10-02).
