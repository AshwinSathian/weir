# Status

Updated: 2026-10-09
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: claude/busy-ramanujan-mhd5kg
PR: #64 https://github.com/AshwinSathian/weir/pull/64
Next card: M11-02 per `scripts/card.sh next`; M16-01 is approved too (Phase 1 release gate comes first, your call)

## Waiting on Ashwin

none

## Blockers

none

## Notes for the next session

- M11-01: Range on a stored 200 (hit, SWR) is sliced in `fromEntry` (respond.go); the client's 304 check runs first. FR-SRV-5 in docs/01 now points at FR-RNG-1..3.
- M11-02: remove `FR-RNG-4` from the `later` allowlist in scripts/trace.sh in the PR that adds its first citing test. docs/06 maps T-37 only to FR-RNG-4, so cite T-37 there only.
- M11-02: Range plus a client precondition on a miss still goes to the pass-through, which drops preconditions (FR-FWD-1); revisit if the fill makes this matter.
- `ParseRange` accepts whitespace before `=` (`bytes =0-1`); lenient, harmless on a hit.
- golangci-lint cannot run in cloud sessions (built with Go 1.25); CI must confirm lint.
- Run the nightly workflow once after the M10-04 merge; PLAN M10.4 stays unticked until it is green.
