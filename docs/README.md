# Weir design documents

Read in this order the first time. After that, go straight to the one you need.

| # | Document | Answers | Status |
|---|---|---|---|
| 00 | [design-doc](00-design-doc.md) | why Weir exists, prior art, failure-mode taxonomy (T6.1–T6.13) | seed, preserved verbatim |
| 01 | [technical-spec](01-technical-spec.md) | what Weir must do: requirements with IDs, public API, config, decision tables | normative |
| 02 | [architecture](02-architecture.md) | structure, principles P1–P8, packages, ADRs | normative |
| 03 | [hld](03-hld.md) | request flows and state machines | descriptive |
| 04 | [lld](04-lld.md) | exact types, algorithms, locking, pseudo-code, event catalog | normative for implementers |
| 05 | [storage-interface-spec](05-storage-interface-spec.md) | `store.Store` contract, epochs, memory store (S3-FIFO), codec, Valkey on paper, conformance suite | normative |
| 06 | [threat-model](06-threat-model.md) | threats T-1–T-44, invariants INV-1–INV-7, security review checklist | normative |
| 07 | [testing-strategy](07-testing-strategy.md) | test levels, harness, per-failure-mode tests with pass criteria, CI, definition of done | normative |
| 08 | [caddy-adapter-spec](08-caddy-adapter-spec.md) | Phase 2 adapter | draft |
| 09 | [research-notes](09-research-notes.md) | sources, verified facts, seed errata | living |
| 10 | [experiments-spec](10-experiments-spec.md) | Phase 3 experiment dimensions: locked decisions E1–E7 | draft |

Operators start with the [runbook](runbook.md) (deployment checklist, signals and incident steps) and the [benchmarks](benchmarks.md). The roadmap with milestones, tasks and acceptance criteria is [PLAN-weir.md](../PLAN-weir.md). Session-sized task cards are in [cards/](cards/README.md), and current progress in [progress/STATUS.md](progress/STATUS.md) with history in [progress/LOG.md](progress/LOG.md). Instructions for coding agents are in [CLAUDE.md](../CLAUDE.md).

## Mapping from the seed's planned document names

The seed (§11) planned four follow-on documents. They were renumbered to fit architecture, HLD, LLD and the threat model in logical reading order:

| Seed name | Now |
|---|---|
| `01-technical-spec.md` | [01-technical-spec.md](01-technical-spec.md) |
| `02-storage-interface-spec.md` | [05-storage-interface-spec.md](05-storage-interface-spec.md) |
| `03-testing-strategy.md` | [07-testing-strategy.md](07-testing-strategy.md) |
| `04-caddy-adapter-spec.md` | [08-caddy-adapter-spec.md](08-caddy-adapter-spec.md) |
| `CLAUDE.md` planning doc | [CLAUDE.md](../CLAUDE.md) and [PLAN-weir.md](../PLAN-weir.md) |

## Precedence

When documents disagree: 01 over everything; then 05, 06, 07 for their areas; then 02; then 04; then 03. The seed (00) is historical and loses to all of them. A disagreement is a bug: fix the losing document in the same change that notices it.

## Changing a normative document

Requirement IDs are never reused or renumbered. A removed requirement keeps its ID with the text "Removed (date): reason". A new requirement takes the next free number in its group. Every change to 01, 02, 04, 05, 06 or 07 updates the document's date line.
