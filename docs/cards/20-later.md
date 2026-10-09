# Phase 2, 2.5 and 3 entry cards

These phases start from draft specs. Each begins with one planning card that verifies the spec against current upstream code, asks the user any new questions, and writes that phase's implementation cards into this file (replacing the placeholder list). Implementation cards are not pre-written here because they would be planned against APIs that may change.

## Phase 2: Caddy adapter

### [ ] P2-00 Finalize the Caddy adapter spec and write its cards
- Plan: 2.1 · Size: S · Depends on: M15-01
- Read: 08 whole; 10 §2 (E7 affects the adapter's response path later); Caddy source at the latest release tag
- Touch: docs/08-caddy-adapter-spec.md (v1.0), docs/cards/20-later.md (Phase 2 cards), docs/09-research-notes.md (verified facts)
- AC: every Caddy API named in 08 checked at the pinned release with file and line; 08 marked v1.0; Phase 2 split into S/M cards (expected: module skeleton and Caddyfile, store pool and key-generation hash, nextOrigin and upgrades, errors and memory split, admin API purge/mode/stats, metrics, deployment guide)

## Phase 2.5: Valkey store
- Notes: 06 T-45 and R-6: the adapter spec must say where `weir` sits relative to authentication and variable-setting handlers, and the deployment guide repeats it.

### [ ] P25-00 Plan the Valkey store and write its cards
- Plan: 2.5.x · Size: S · Depends on: Phase 2 cards done
- Read: 05 §7, §8; valkey-go docs via Context7
- Touch: docs/05-storage-interface-spec.md, docs/cards/20-later.md
- AC: client library and version chosen with the user; cards written (expected: connection and codec, Get/Set/Delete, epochs sketch in Lua with `SharedTagEpochs` in the same one round trip (05 E-12; without it the 4% group residual of T-29 returns), conformance in CI with `-tags integration` (05 §8, else `ExpiredIsNotFound` skips), engine suite re-run, vary CAS, multi-node guide)

## Phase 3: Experiment dimensions

### [ ] P3-00 Finalize the experiments spec and write its cards
- Plan: Phase 3 · Size: S · Depends on: Phase 2.5 cards done
- Read: 10 whole; 06 T-35, T-36
- Touch: docs/10-experiments-spec.md (v1.0), docs/cards/20-later.md
- AC: open items in 10 §6 resolved with the user; cards written

## Deferred

### [ ] M16-02 Pointer-light memory store layout
- Plan: M10.5b follow-up · Size: M · Depends on: M16-01
- Read: M16-01's recommendation; 05 §3, §4; store/memory/
- Touch: store/memory/, docs/05-storage-interface-spec.md, loadtest/gc_test.go (gate), docs/benchmarks.md
- Tests: the store conformance suite unchanged; `Get` and `Set` allocation benchmarks; `TestGCAt1MEntries` asserts a saturated GC share of at most 10% at 1M entries
- AC: the share target holds on the box that measured 13.6% to 15.4%; `BenchmarkMemoryStoreGetParallel` does not regress over 20%; stored entries stay immutable (P4); `make check` passes
- Notes: deferred 2026-10-09. M16-01 recommended keeping the heap layout (prototype fails NFR-5 without a serve-from-encoded-bytes path). Reopen only if a deployment exceeds the D36 gate (2 µs of GC per request at 1M entries) or needs more than 1M entries, and then scope it as a design change first: the Tests and AC below assume the 10% share line, which D36 replaced with the µs gate.
