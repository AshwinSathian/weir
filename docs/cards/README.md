# Task cards

A card is one Claude Code session of work: read a short list of doc sections, write tests, write code, pass `make check`, hand off a PR. Cards are ordered; `make next` prints the first open one. [PLAN-weir.md](../../PLAN-weir.md) keeps the milestone view; cards are the unit of work inside it.

## Card format

```
### [ ] M1-04 Title
- Plan: M1.2 · Size: M · Depends on: M1-01
- Read: 01 §5.1; 04 §3.1, §3.7; 07 §5
- Touch: internal/keys/validate.go (new), internal/keys/validate_test.go (new)
- Tests: FuzzValidateRequest, TestValidateRejects
- AC: binary statements; all must hold
- Out of scope: what this card must not do (it belongs to a named later card)
- Notes: traps, decisions, hints
```

Reading references use document numbers: `04 §6.4` means `scripts/section.sh docs/04-lld.md 6.4`. `01 FR-COA-4` means `grep -n 'FR-COA-4' docs/01-technical-spec.md` (read that requirement line). `02 ADR-6` and `02 P8` mean the matching heading or paragraph in docs/02. `06 T-13` means the T-13 row: `grep -n '| T-13 |' docs/06-threat-model.md`. `07 T6.2` means `scripts/section.sh docs/07-testing-strategy.md T6.2`. Read only what the card lists, plus the code of the packages it touches. If the listed reading is not enough to proceed, that is a card defect: say so in the LOG entry.

## Sizing rules (200K-class context, one card per session)

Budget per session is roughly 60–90K tokens of real work, leaving headroom so the session never compacts.

| Size | Production code | Test code | Reading | Files touched |
|---|---|---|---|---|
| S | up to ~200 lines | up to ~300 lines | up to ~8K tokens | up to 4 |
| M | up to ~450 lines | up to ~600 lines | up to ~15K tokens | up to 6 |

There are no L cards. A card that grows past M during the session is split (see CLAUDE.md "Session protocol", overrun rule). Test output is part of the budget: run focused packages (`go test ./internal/keys/...`) while iterating and `make check` once at the end.

## Rules that apply to every card

- Observability as you go: when a card adds a behavior that has an event in 04 §9.2, the card emits it (through the `emit` helper from P0-02) and its tests assert it. M10-01 only audits.
- Events never carry request data except the truncated partition string (04 §9.2).
- Tests named on a card but absent from docs/07 are allowed; add them to docs/07 only if a later card or the threat model refers to them.

## Status marks

`### [ ]` open, `### [x]` done (set by `/handoff` through `scripts/card.sh done <ID>`). A split card keeps its ID for the finished part and gets a new card `<ID>b` inserted right after it for the remainder.

## Files

| File | Scope |
|---|---|
| [00-phase0.md](00-phase0.md) | Phase 0 skeleton |
| [01-m1.md](01-m1.md) | M1 RFC 9111 core and key hardening |
| [02-m2-m3.md](02-m2-m3.md) | M2 coalescing, M3 jitter and early refresh |
| [04-m4.md](04-m4.md) | M4 limiter, timeouts, storage failure, warm |
| [05-m5-m6.md](05-m5-m6.md) | M5 stale serving, breaker, modes; M6 negative caching |
| [07-m7.md](07-m7.md) | M7 Vary, keyed inputs, bypass, security review |
| [08-m8-m9.md](08-m8-m9.md) | M8 miss-rate, M9 purge and Cache Groups |
| [10-m10.md](10-m10.md) | M10 observability, performance, conformance |
| [11-phase1x.md](11-phase1x.md) | M11 to M16 |
| [20-later.md](20-later.md) | Phase 2, 2.5 and 3 entry cards |
