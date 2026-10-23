# Session log

One entry per working session, newest at the bottom. The SessionStart hook shows the last entry.

The history from the start of the project to the v1.2 launch preparation is condensed to one line per session below; the full entries are in this file's git history.

Entry template for new sessions:

```
## <date> · <card ID> · <outcome: done | split | blocked | review-fixes>
- Branch / PR: card/<id>-<slug> / #<n>
- Done: what now exists (files, behavior), in 1-4 bullets
- Tests: names added; `make check` result
- Deviations: doc changes made and why, or "none"
- Follow-ups: new cards or open questions, or "none"
- Context: rough share of the session budget used (low / medium / high) and whether the card size was right
```

## History

- 2026-09-27 · planning
- 2026-09-27 · P0-01 · Public request, response and error types
- 2026-09-27 · P0-02 · Store types, observer and remaining public types
- 2026-09-27 · P0-03 · Config, defaults and validation
- 2026-09-27 · P0-04 · Test origin
- 2026-09-27 · P0-05 · Engine skeleton and the single fetch function
- 2026-09-27 · P0-06 · Store conformance suite
- 2026-09-27 · M1-01 · Cache-Control and request directive parsing
- 2026-09-27 · M1-02 · Dates, lifetime, heuristic, age, jitter math
- 2026-09-27 · M1-03 · Entry evaluation
- 2026-09-27 · M1-04 · Request validation and host normalization
- 2026-09-27 · M1-05 · Query rewrite, cookies, Accept-Encoding
- 2026-09-27 · M1-06 · Canonical key encoding, tags, path normalization
- 2026-09-27 · M1-07 · Classification and forwarded request
- 2026-09-27 · M1-08 · Entry codec
- 2026-09-28 · M1-09 · Memory store: sharding and S3-FIFO
- 2026-09-28 · M1-10 · Memory store: epochs
- 2026-09-28 · M1-11 · Storability and entry building
- 2026-09-28 · M1-12 · Serve: lookup, hit, miss and store
- 2026-09-28 · M1-13 · Revalidation and client conditionals
- 2026-09-28 · M1-14 · HEAD, Range and request directives
- 2026-09-28 · M1-15 · Invalidation, epochs by request time, newest wins, store sizing
- 2026-09-28 · M1-16 · Pass-through, upgrades, event streams, trace headers (engine)
- 2026-09-28 · M1-17 · weirhttp adapter and example proxy
- 2026-09-28 · open questions
- 2026-09-28 · M1-17b · weirhttp HandlerOrigin
- 2026-09-28 · M1-17c · Key-boundary decisions: h2c, fragments in the target, keyed cookie size
- 2026-09-28 · M1-17d · Storability decisions: s-maxage permission, markers after unkeyed input, remote-store clock
- 2026-09-28 · M1-17e · Response decisions: hop-by-hop in fetch, 304 metadata, Pragma
- 2026-10-01 · M1-18 · RFC table test and first benchmarks
- 2026-10-01 · M2-01 · Flight table
- 2026-10-01 · M2-02 · Coalesced fetch with detached context
- 2026-10-01 · M2-03 · Markers, re-entry, Authorization, oversized streams
- 2026-10-01 · M3-01 · Jitter wiring and early refresh
- 2026-10-01 · M4-01 · Limiter component
- 2026-10-01 · M4-02 · Limiter in the fetch path
- 2026-10-01 · M4-03 · Upload pool and timeout split
- 2026-10-01 · M4-04 · Store guard
- 2026-10-01 · M4-05 · Warm
- 2026-10-01 · M5-01 · Breaker component
- 2026-10-01 · M5-02 · Stale-while-revalidate and background refresh
- 2026-10-01 · M5-03 · Stale-if-error, error table and breaker wiring
- 2026-10-01 · M5-04 · Incident modes
- 2026-10-01 · M6-01 · Negative caching
- 2026-10-02 · M7-01 · Vary spec and variant records
- 2026-10-05 · M7-02 · Vary policies: strict, sensitive, overflow, reclaim
- 2026-10-05 · M7-03 · Keyed headers and cookies, allow list, bypass
- 2026-10-05 · M7-04 · Tracking preset and stripped-cookie report
- 2026-10-05 · M7-05 · Security review of the key boundary
- 2026-10-05 · M8-01 · Space-Saving tracker
- 2026-10-06 · M8-02 · Miss-rate wiring and throttle
- 2026-10-06 · M9-01 · Structured-field list parser
- 2026-10-06 · M9-02 · Purge API: soft, hard, all, URLs
- 2026-10-06 · M9-03 · Cache-Groups tags and group invalidation
- 2026-10-06 · M10-01 · Event catalog and Stats
- 2026-10-07 · M10-08 · Group tags skip the invalid plane
- 2026-10-07 · M10-02 · Prometheus exporter module
- 2026-10-07 · M10-03 · Load-test harness and scenarios
- 2026-10-07 · M10-04 · Nightly fuzz and cache-tests workflows
- 2026-10-08 · M10-07 · Date on forwarded responses, empty absolute-form path
- 2026-10-08 · M10-09 · Cache-tests deviations: strict dates, Cache-Control spacing, conditional dates
- 2026-10-08 · M10-05 · Performance budgets and GC measurement
- 2026-10-08 · M10-06 · README guide, runbook, strict trace
- 2026-10-09 · M10-10 · Client preconditions after revalidation (optional)
- 2026-10-09 · M11-01 · Single-range responses from cache
- 2026-10-09 · M11-02 · Range miss background fill
- 2026-10-09 · M12-01 · Structured-field dictionary parser
- 2026-10-09 · M12-02 · Targeted cache-control precedence
- 2026-10-09 · M13-01 · Snapshot writer
- 2026-10-09 · M13-02 · Snapshot loader
- 2026-10-09 · M14-01 · Per-host limiter cap and per-owner quota
- 2026-10-09 · M15-01 · Eager hard purge
- 2026-10-09 · M16-01 · Pointer-light layout: prototype and decision (D36)
- 2026-10-09 · P2-00 · Finalize the Caddy adapter spec and write its cards
- 2026-10-09 · P2-01 · Module skeleton and JSON config
- 2026-10-10 · P2-01b · Caddyfile parsing and directive order
- 2026-10-10 · P2-01c · CI for the Caddy module
- 2026-10-10 · P2-02 · Store pool and key-generation hash
- 2026-10-10 · P2-03 · nextOrigin, errors and upgrades
- 2026-10-10 · P2-03b · End-to-end scenarios under caddytest
- 2026-10-10 · P2-04 · Memory budget split and memory sizing
- 2026-10-10 · P2-05 · Admin API: purge, mode, stats
- 2026-10-10 · P2-06 · Prometheus metrics on Caddy's registry
- 2026-10-10 · P2-07 · Single-node deployment guide
- 2026-10-10 · P25-00 · Plan the Valkey store and write its cards
- 2026-10-10 · P25-01 · Config and error mapping
- 2026-10-10 · P25-01b · Store skeleton, lazy connect and policy check
- 2026-10-10 · P25-02 · Get, Set, Delete and the CI Valkey job
- 2026-10-10 · P25-03 · Hard and global epochs
- 2026-10-10 · P25-03b · Soft and invalid sketch, shared tags
- 2026-10-10 · P25-04 · Engine suite against Valkey
- 2026-10-10 · P25-04b · Remaining engine scenarios against Valkey
- 2026-10-10 · P25-05 · Vary-spec compare-and-set: interface and memory store
- 2026-10-10 · P25-04b + P25-05 · Remaining engine scenarios against Valkey
- 2026-10-10 · P25-05b · Vary-spec compare-and-set in Valkey
- 2026-10-10 · P25-06 · Eviction-storm review and Scrubber via SCAN
- 2026-10-11 · P25-07 · Adapter wiring for the Valkey store
- 2026-10-11 · P25-07b · Two-node test and the multi-node guide
- 2026-10-11 · P25-07c · Server-side key-generation record for the Valkey store
- 2026-10-11 · P25-07d · Retry the key-generation check after a start with the server down
- 2026-10-11 · P3-00 · Finalize the experiments spec and write its cards
- 2026-10-11 · P3-01 · Experiment config and signed assignment cookie
- 2026-10-11 · P3-02 · Variant key dimension and forwarded header
- 2026-10-11 · P3-03 · Set-Cookie and downstream cache rewrite
- 2026-10-11 · P3-04 · EvExposure
- 2026-10-11 · P3-05 · Caddy adapter: experiments block
- 2026-10-11 · release-prep (R-02 to R-06)
- 2026-10-11 · release-review (R-07)
- 2026-10-11 · reference-run
- 2026-10-11 · hit-path
- 2026-10-12 · hit-path
- 2026-10-12 · release-gate
- 2026-10-12 · local-checks
- 2026-10-12 · rc-rehearsal
- 2026-10-12 · rc-hardening
- 2026-10-12 · rc3-rehearsal
- 2026-10-12 · release-v1.0.0
- 2026-10-12 · surfaces-and-site
- 2026-10-12 · site-consistency-check
- 2026-10-12 · threat-rows-experiments
- 2026-10-12 · v1.0.1-prep
- 2026-10-13 · release-v1.0.1
- 2026-10-13 · HRD-01 to HRD-17, HRD-19 · Followers outwait a healthy flight
- 2026-10-13 · HRD-21 · Improvements from the first comparison round
- 2026-10-13 · CMP-01 to CMP-05, CMP-08 (partial) · Comparison spec, harness and subject configs
- 2026-10-13 · comparison rerun · Improvements from the first comparison round
- 2026-10-13 · HRD-18 · Release v1.1.0
- 2026-10-13 · after v1.1.0
- 2026-10-13 · site results presentation
- 2026-10-13 · CMP-03b and CMP-04b · Remaining protection scenarios
- 2026-10-13 · HRD-23 · Improvements from the second comparison round
- 2026-10-14 · site charts for round 2

## 2026-10-22 · v1.2 launch preparation · done
- Branch / PR: launch/* / #134 to #143
- Done: Go 1.26 support and a newer safe patch floor; engine, adapter and store fixes from a pre-launch review; HRD-22 (over-size responses shared among waiting clients); HRD-20 (coalescing across nodes and a local tier for the Valkey store); CMP-06 (the speed and memory pillar harness); site and documentation fixes; this file condensed and the progress files rewritten.
- Tests: new tests with each pull request; `make check`, `make test-valkey` and `make site-check` pass.
- Deviations: each pull request lists the documents it changed.
- Follow-ups: the v1.2.0 release and the comparison rerun on a quiet machine.
- Context: long session, several cards by agreement with the owner.

## 2026-10-23 · v1.2.0 release · done
- Branch / PR: launch/gate-chunks / #145; launch/post-v1.2.0 and launch/results-v1.2.0 / open
- Done: release gate (the root package group now runs two race runs per process, after the runner ran out of memory twice); load, benchmarks, `make vuln` and `make cache-tests` on a quiet machine; `v1.2.0-rc.1` rehearsal, then `v1.2.0` for all four modules; runbook 7.8 on Caddy built from the tags; GitHub release v1.2.0, and the internal IDs removed from the v1.0.0 and v1.1.0 release notes; comparison round 3 against the published v1.2.0.
- Tests: `release-gate` runs 39753002745 and 39796966321; `make release-smoke` for rc.1 and v1.2.0.
- Deviations: speed pillar latency not published (drift gate, see STATUS); docs/07 §12 and the release-gate workflow run the 100 race runs in processes of two.
- Follow-ups: Dependabot #137 and #138; pkg.go.dev pages for v1.2.0 requested; site deploy after the results pull request.
- Context: same long session as the launch preparation.
