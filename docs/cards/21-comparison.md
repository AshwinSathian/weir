# Comparison benchmark cards

Scope: [docs/11-comparison-spec.md](../11-comparison-spec.md). Reading references to `11` mean that document. Results are private until CMP-07 (11 §9): no card commits a file from `bench/compare/results/`.

### [ ] CMP-01 Comparison spec, harness and subject configs
- Plan: comparison · Size: M · Depends on: none
- Read: 11 whole; 07 §9; loadtest/harness_test.go (capOrigin)
- Touch: docs/11-comparison-spec.md (new), docs/cards/21-comparison.md (new), bench/compare/{go.mod,main.go,origin.go,run.go,compare_test.go,compose.yaml,run.sh} (new), bench/compare/docker/ (new), bench/compare/subjects/ (new), Makefile (`compare` target)
- Tests: `TestRunCountsAgainstOriginDirect`, `TestOriginControl`, `TestReportMedians`
- AC: the module builds, vets, lints and passes its race tests alone (`GOWORK=off`); `docker compose config` accepts the file; the five CMP-01 scenarios of 11 §5.1 run natively against `weirproxy`; the root module still imports only the standard library
- Out of scope: running anything in Docker (CMP-02); scenarios that need an origin failure cue (CMP-03)
- Notes: written in one session with the spec at the owner's request, without a Docker daemon. The competitor configs are therefore unverified until CMP-02.

### [ ] CMP-02 First Docker run: verify every subject and pin images
- Plan: comparison · Size: S · Depends on: CMP-01, a running Docker daemon
- Read: 11 §2, §3, §10
- Touch: bench/compare/compose.yaml, bench/compare/subjects/, bench/compare/run.sh, docs/11-comparison-spec.md (§10)
- AC: every subject starts in both tiers and answers the `ready` profile; each cache serves a second request for a `max-age=60` response without an origin call (it caches at all); image digests are pinned in `compose.yaml`; the five CMP-01 scenarios complete for every subject with 3 repetitions; the `origin` control row shows origin calls equal to requests
- Out of scope: reading meaning into the numbers (CMP-07)
- Notes: check against the running containers, not memory: the Varnish 9.1 image's `VARNISH_HTTP_PORT` and `VARNISH_SIZE`, the tmpfs path, `.wait_limit` in VCL 4.1; that the Caddy builder image honors `GOTOOLCHAIN=auto`. The Weir and Souin Caddyfiles already validate and cache on native builds of the same versions; Varnish and nginx have not run at all. In the native smoke, Souin answered 2xx to 597 of 600 clients in `error-herd` although the origin only ever sent 500: confirm or clear that in Docker before CMP-07 reads it. A native run on macOS lost connections at 1000 simultaneous connects (listen backlog 128); if the Linux run shows `error` in the `origin` row, fix the plumbing (11 R1) before trusting any row.

### [ ] CMP-03 Origin failure cues and the remaining protection scenarios
- Plan: comparison · Size: M · Depends on: CMP-02
- Read: 11 §5.1, §5.2; loadtest/scenarios_test.go (brownout and flood shapes)
- Touch: bench/compare/origin.go, bench/compare/run.go, bench/compare/compare_test.go, bench/compare/subjects/ (purge access per subject)
- Tests: one test per new origin cue (capacity, fail-on-cue, hang), against the origin direct
- AC: `busting-flood`, `brownout`, `slow-leader`, `leader-abort`, `purge-herd`, `large-body-herd` and `restart` run for every subject; a subject without a feature reports `not available`, never a zero
- Out of scope: security probes (CMP-04)
- Notes: split at `purge-herd` if the card overruns; purge differs per subject and is the costly part.

### [ ] CMP-04 Security probes
- Plan: comparison · Size: M · Depends on: CMP-02
- Read: 11 §5.3; 06 §6; 06 T-rows for key poisoning and deception (grep the probe names)
- Touch: bench/compare/origin.go (unsafe routes), bench/compare/probe.go (new), bench/compare/probe_test.go (new), bench/compare/subjects/ (hardened tier where a project documents one)
- Tests: each probe against the origin direct yields `not applicable` (nothing caches); against a deliberately naive in-test cache yields `poisoned`
- AC: every probe of 11 §5.3 returns `poisoned`, `safe` or `not applicable` for every subject and tier; each verdict keeps the two responses that decided it in the results file
- Out of scope: fixing anything found in Weir (new card, 11 §8)

### [ ] CMP-05 Conformance across subjects
- Plan: comparison · Size: S · Depends on: CMP-02
- Read: 11 §5.4; scripts/cache-tests.sh; docs/cache-tests-expected-failures.md
- Touch: bench/compare/conformance.sh (new), scripts/cache-tests.sh only if a shared function is lifted out
- AC: the pinned cache-tests revision runs against every subject in both tiers; pass counts by category land in the results directory; Weir's row matches `testdata/cache-tests-baseline.json`

### [ ] CMP-06 Speed and memory
- Plan: comparison · Size: M · Depends on: CMP-02
- Read: 11 §5.5, §6
- Touch: bench/compare/speed.sh (new), bench/compare/compose.yaml (cpuset pins, load generator service), bench/compare/run.go (warm and fill helpers)
- AC: the load generator is an established open-loop tool, named with its version in 11 §4; CPU time per request comes from cgroup `cpu.stat`; the drift gate of 11 §6 discards a run and says so; one command runs the whole pillar from a plain terminal
- Notes: ask the owner before the run: quiet machine, Docker Desktop at 8 CPUs and 12 GiB, editor closed.

### [ ] CMP-07 Private scorecard and the publish decision
- Plan: comparison · Size: S · Depends on: CMP-03, CMP-04, CMP-05, CMP-06
- Read: 11 §7, §8, §9
- Touch: bench/compare/results/ (not committed), docs/progress/STATUS.md
- AC: a scorecard per 11 §7 and the list of Weir findings are handed to the owner; each finding has a proposed card; the owner's decision is recorded in STATUS
- Notes: ends blocked on the owner by design.

### [ ] CMP-08 Publish
- Plan: comparison · Size: M · Depends on: CMP-07 with a yes
- Read: 11 §9; site/README.md (include directives, claims register)
- Touch: docs/benchmarks.md, site/content/pages/{benchmarks,compare}.md, the claims register, the chosen result files
- AC: every published number has its config, command, machine and date; the page names every pillar that was run; `make site-check` passes

### [~] CMP-09 Differential fuzzing across caches
- Plan: comparison · Size: M · Depends on: CMP-02
- Read: 11 §5.6
- Notes: deferred until the first round is decided.

### [~] CMP-10 Second round: Traffic Server, Valkey against Redis
- Plan: comparison · Size: M · Depends on: CMP-08
- Notes: deferred; only if the first round is published.
