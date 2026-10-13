# Comparison benchmark cards

Scope: [docs/11-comparison-spec.md](../11-comparison-spec.md). Reading references to `11` mean that document. No card commits a file from `bench/compare/results/`; published tables are medians copied into docs/benchmarks.md (11 §9).

State after the second session of 2026-10-13, which ran CMP-03b and CMP-04b on one branch at the owner's request: the count-based pillars are complete. CMP-01 to CMP-05, CMP-03b and CMP-04b are done. CMP-06 (speed and memory) has not run. CMP-07's decision came early: the owner chose to publish the count-based pillars before CMP-06, so no scorecard with marks exists (11 §7), and CMP-08 stays open until CMP-06 is published or dropped. Findings CMP-F1 to CMP-F4 were resolved in card HRD-21; CMP-F5 to CMP-F8, from round 2, are open and wait for the owner's decision.

### [x] CMP-01 Comparison spec, harness and subject configs
- Plan: comparison · Size: M · Depends on: none
- Read: 11 whole; 07 §9; loadtest/harness_test.go (capOrigin)
- Touch: docs/11-comparison-spec.md (new), docs/cards/21-comparison.md (new), bench/compare/{go.mod,main.go,origin.go,run.go,compare_test.go,compose.yaml,run.sh} (new), bench/compare/docker/ (new), bench/compare/subjects/ (new), Makefile (`compare` target)
- Tests: `TestRunCountsAgainstOriginDirect`, `TestOriginControl`, `TestReportMedians`
- AC: the module builds, vets, lints and passes its race tests alone (`GOWORK=off`); `docker compose config` accepts the file; the five CMP-01 scenarios of 11 §5.1 run natively against `weirproxy`; the root module still imports only the standard library
- Out of scope: running anything in Docker (CMP-02); scenarios that need an origin failure cue (CMP-03)
- Notes: written in one session with the spec at the owner's request, without a Docker daemon. The competitor configs are therefore unverified until CMP-02.

### [x] CMP-02 First Docker run: verify every subject and pin images
- Plan: comparison · Size: S · Depends on: CMP-01, a running Docker daemon
- Read: 11 §2, §3, §10
- Touch: bench/compare/compose.yaml, bench/compare/subjects/, bench/compare/run.sh, docs/11-comparison-spec.md (§10)
- AC: every subject starts in both tiers and answers the `ready` profile; each cache serves a second request for a `max-age=60` response without an origin call (it caches at all); image digests are pinned in `compose.yaml`; the five CMP-01 scenarios complete for every subject with 3 repetitions; the `origin` control row shows origin calls equal to requests
- Out of scope: reading meaning into the numbers (CMP-07)
- Notes: check against the running containers, not memory: the Varnish 9.1 image's `VARNISH_HTTP_PORT` and `VARNISH_SIZE`, the tmpfs path, `.wait_limit` in VCL 4.1; that the Caddy builder image honors `GOTOOLCHAIN=auto`. The Weir and Souin Caddyfiles already validate and cache on native builds of the same versions; Varnish and nginx have not run at all. In the native smoke, Souin answered 2xx to 597 of 600 clients in `error-herd` although the origin only ever sent 500: confirm or clear that in Docker before CMP-07 reads it. A native run on macOS lost connections at 1000 simultaneous connects (listen backlog 128); if the Linux run shows `error` in the `origin` row, fix the plumbing (11 R1) before trusting any row.

### [x] CMP-03 Origin failure cues and the remaining protection scenarios
- Plan: comparison · Size: M · Depends on: CMP-02
- Read: 11 §5.1, §5.2; loadtest/scenarios_test.go (brownout and flood shapes)
- Touch: bench/compare/origin.go, bench/compare/run.go, bench/compare/compare_test.go, bench/compare/subjects/ (purge access per subject)
- Tests: one test per new origin cue (capacity, fail-on-cue, hang), against the origin direct
- AC: `busting-flood`, `brownout`, `slow-leader`, `leader-abort`, `purge-herd`, `large-body-herd` and `restart` run for every subject; a subject without a feature reports `not available`, never a zero
- Out of scope: security probes (CMP-04)
- Notes: split at `purge-herd` if the card overruns; purge differs per subject and is the costly part. Closed by CMP-03b: every scenario listed has run for every subject (`slow-leader` is `slow-origin-herd`), and each cue has a test against the origin direct.

### [x] CMP-04 Security probes
- Plan: comparison · Size: M · Depends on: CMP-02
- Read: 11 §5.3; 06 §6; 06 T-rows for key poisoning and deception (grep the probe names)
- Touch: bench/compare/origin.go (unsafe routes), bench/compare/probe.go (new), bench/compare/probe_test.go (new), bench/compare/subjects/ (hardened tier where a project documents one)
- Tests: each probe against the origin direct yields `not applicable` (nothing caches); against a deliberately naive in-test cache yields `poisoned`
- AC: every probe of 11 §5.3 returns `poisoned`, `safe` or `not applicable` for every subject and tier; each verdict keeps the two responses that decided it in the results file
- Out of scope: fixing anything found in Weir (new card, 11 §8)
- Notes: closed by CMP-04b. The verdicts are `unsafe` and `safe` (11 §5.3), not the `poisoned` and `not applicable` this card first named: against the origin direct every probe is `safe`.

### [x] CMP-05 Conformance across subjects
- Plan: comparison · Size: S · Depends on: CMP-02
- Read: 11 §5.4; scripts/cache-tests.sh; docs/cache-tests-expected-failures.md
- Touch: bench/compare/conformance.sh (new), scripts/cache-tests.sh only if a shared function is lifted out
- AC: the pinned cache-tests revision runs against every subject in both tiers; pass counts by category land in the results directory; Weir's row matches `testdata/cache-tests-baseline.json`
- Notes: closed by CMP-04b. Three tiers ran (default, tuned, hardened); `summary.md` splits failures into assertion and setup and lists passes by test group; the script itself compares Weir's default row with the baseline (equal, on Caddy and `weirproxy`).

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

### [x] CMP-03b Remaining protection scenarios
- Plan: comparison · Size: M · Depends on: CMP-03
- Read: 11 §5.1, §5.2; loadtest/scenarios_test.go
- Touch: bench/compare/{origin.go,run.go,scenarios.go,compare_test.go}, bench/compare/subjects/ (purge access per subject)
- AC: `busting-flood`, `brownout`, `purge-herd` and `restart` run for every subject; a subject without a feature reports `not available`, never a zero
- Also: the runner checks body length as well as status (cache-handler's empty 200s); `post-flood` records the status of its `POST` requests; counters are reset after the priming wait and a second wave shows deferred refreshes; a replaced profile keeps its in-flight count.
- Notes: CMP-03 ran `slow-origin-herd`, `leader-abort`, `turned-private`, `bad-route`, `origin-down`, `post-flood`, `oversize-herd`, `large-body-herd` and the two capped scenarios instead of these four. The new origin cues have no test of their own against the origin direct; add them here.
- Done 2026-10-13: the four scenarios ran for all eleven subject-tiers, three repetitions; nginx, `weirproxy` and the controls report `not available` for `purge-herd`. Every scenario was rerun with the corrected harness, not only cache-handler's two, because the body check and the second wave can change any cell. `brownout` uses 10 s bursts and `restart` 2000 keys (11 §5.1, §5.2 say why). Tests: every scenario against the origin direct in a synctest bubble with sent equal to seen, and `TestOriginCapacity`, `TestOriginStatusChangeKeepsCountersAndInFlight`, `TestOriginHang`.

### [x] CMP-04b Remaining probes and a hardened tier
- Plan: comparison · Size: S · Depends on: CMP-04
- Read: 11 §5.3
- Touch: bench/compare/probe.go, bench/compare/compare_test.go, bench/compare/subjects/
- AC: `probe_test.go` exists as CMP-04 asked; verdicts follow 11 §5.3 (`unsafe`, `safe`); the error-as-success probe is concurrent; parameter cloaking, path normalization, the meta character and `Vary` cardinality are probed; conformance runs in both tiers and reports setup failures apart from assertion failures (CMP-05); each competitor with documented hardening gets a hardened tier and the table of docs/benchmarks.md is rerun with it
- Done 2026-10-13. The first full run showed two artifacts, fixed before anything was published: the `Vary` probe called Weir unsafe for not forwarding the header, and cache-handler's tuned tier evicted stored URLs at Otter's default size. Container memory in the `Vary` probe is read from `docker stats` and is approximate; real memory figures are CMP-06.

### [x] CMP-F1 Default tier sheds load the origin could carry
- Resolved: card HRD-21 (22-hardening.md), D-H25: `MaxQueue` 8192, `MaxQueueWait` 5 s. Rerun: 0 failures in `cold-keys` and `sync-expiry`.
- Plan: comparison finding · Size: S · Depends on: owner decision
- Read: 01 FR-LIM; runbook 4.8; 06 R-14
- Notes: `cold-keys` 720 of 2000 and `sync-expiry` about 3850 of 5000 clients get 503 with the default limiter (64 concurrent, 2 s wait) and stale serving off (D6). Options: keep and document louder (quickstart turns stale serving on); raise `MaxQueueWait`; turn `while_revalidate` on by default. The last two change defaults (hard rule 12).

### [x] CMP-F2 Per-partition cap throttles one hot uncacheable URL
- Resolved: card HRD-21 (22-hardening.md), D-H26: `Limiter.PartitionHeadroom`. Rerun: 0 failures in `uncacheable-herd`.
- Plan: comparison finding · Size: M · Depends on: owner decision
- Read: 01 FR-LIM; 04 limiter section
- Notes: `Limiter.MaxPerPartition` 16 applies to pass-through and marker fetches. `uncacheable-herd`: 339 of 500 fail. `turned-private` and `oversize-herd`: each client waits about four times the origin's latency. The cap exists so one path cannot take the whole budget (busting flood). Options: keep; a separate, larger cap for fetches that a marker says cannot be stored; document the knob on the tuning page.

### [x] CMP-F3 Objects over 1 MiB reach the origin once per client
- Resolved: card HRD-21 (22-hardening.md), D-H28: default unchanged, Caddy `max_object_bytes` added; sharing one fetch deferred as HRD-22. Still a loss in the tables.
- Plan: comparison finding · Size: S · Depends on: owner decision
- Read: 01 FR-STO-9; 05 §2
- Notes: `oversize-herd` 150 origin calls, `large-body-herd` 20, where Varnish and cache-handler make 1. By design (bounded memory, T-21). Options: keep and document; coalesce over-size bodies by streaming one fetch to the waiting clients without storing it.

### [x] CMP-F4 A burst arriving with the breaker's probe fails
- Resolved: card HRD-21 (22-hardening.md), D-H27: requests wait for the probe. Rerun: 200 of 200 served 10 s after recovery.
- Plan: comparison finding · Size: S · Depends on: owner decision
- Read: 04 breaker section; 01 FR-CB-1 onward
- Notes: `origin-down`, default tier: 10 s after the origin recovered, 1 of 200 simultaneous clients is served and 199 get 503, because half-open admits one probe. Requests after the probe succeed. Options: keep; let requests that arrive during a probe wait for its result, bounded by `MaxQueueWait`.

### [ ] CMP-F5 The per-path cap refuses a busy path while the origin has room
- Plan: comparison finding · Size: M · Depends on: owner decision
- Read: 01 FR-LIM-3; 04 limiter section; D-H26
- Notes: `busting-flood`, both tiers: 8,520 of 10,000 clients of the flooded path fail, more clients in total (8,520 of 11,000) than any other subject, the no-cache control included (6,535), while the origin has about 50 of its 128 slots in use and Weir 14 of its own 64 free. Every bystander is served, which is what the cap is for. The cap cannot tell a flood from a search endpoint with many honest queries. Options: keep and say so on the tuning page; extend `PartitionHeadroom` (D-H26) to storable fetches, so a path may borrow slots that are idle, bounded and given back; queue longer for a partition whose misses are being stored. The last two change behavior under a real flood and need the busting-flood load test of 07 §9 to hold.

### [ ] CMP-F6 After a soft purge most URLs keep their old content
- Plan: comparison finding · Size: S · Depends on: owner decision
- Read: 01 FR-PRG-2, FR-STL; 04 background refresh
- Notes: `purge-herd`, tuned tier: 1,000 URLs purged softly, each then asked for three times in 4 s; 904 still served pre-purge content at the last read, and the origin was called 144 times. Background refreshes are capped at 48 at a time and the rest are dropped, not queued, so a URL is refreshed only when a later request finds a free slot. Varnish's `purge.soft()` had refreshed all 1,000. The concepts page says a soft-purged entry "is served stale while one background fetch renews it", which a reader takes to mean every entry. Options: correct the page and recommend a hard purge when content must change now; queue refreshes that a purge made due (bounded, FR-LIM); let a soft purge shorten the stale window.

### [ ] CMP-F7 The breaker fails clients after the origin has recovered
- Plan: comparison finding · Size: S · Depends on: owner decision
- Read: 01 FR-CB-3, FR-CB-4; 04 breaker section
- Notes: `brownout`, default tier (no stale serving): after two 10 s bursts of 503, requests keep failing once the origin is healthy, 120 in the median repetition and 1,980 in the worst on Caddy (last failure 9.6 s after recovery), 2,280 and 14.8 s on `weirproxy`. No other subject fails a client after recovery. `OpenFor` doubles on each failed probe (5 s, 10 s, up to 60 s), so the wait after recovery depends on where the burst ended. With stale serving on nobody fails. Options: keep and document the worst case (`MaxOpenFor`) beside D6; a lower default `MaxOpenFor` (a default change, hard rule 12); probe on a fixed short period once open, and keep the backoff for the closing decision only.

### [ ] CMP-F8 A snapshot does not save origin calls after a restart
- Plan: comparison finding · Size: S · Depends on: owner decision
- Read: 01 §13.3 (FR-SNP), D15; runbook 7.2
- Notes: `restart`, tuned tier with `snapshot_dir`: all 2,000 stored URLs are fetched again on first use, as with no snapshot, where nginx's disk cache makes no origin call. By design (D15 loads entries as stale), and the first request for each waits for the origin (`fwd=stale`) even with `while_revalidate` on. The harness origin sends no validator; with one, each call would be a 304. Options: keep, and say on the Caddy guide that the snapshot buys 304s and outage cover, not a warm cache; serve loaded entries stale-while-revalidate when that window is configured; an opt-in to trust remaining freshness after a clean shutdown.
- Also seen in round 2, no card proposed: the tuned tier passes 246 of 365 cache-tests where the default passes 284, because `while_revalidate` applies to every response. The conformance page should say what stale serving costs there.

### [~] CMP-09 Differential fuzzing across caches
- Plan: comparison · Size: M · Depends on: CMP-02
- Read: 11 §5.6
- Notes: deferred until the first round is decided.

### [~] CMP-10 Second round: Traffic Server, Valkey against Redis
- Plan: comparison · Size: M · Depends on: CMP-08
- Notes: deferred; only if the first round is published.
