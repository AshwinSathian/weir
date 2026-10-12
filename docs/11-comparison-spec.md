# Weir comparison benchmark specification

Status: v0.1, draft. Design approved by the project owner on 2026-10-13; the scenario numbers below are fixed for the first round.
Date: 2026-10-13
Depends on: [07-testing-strategy.md §9](07-testing-strategy.md) (the in-process load scenarios this mirrors over HTTP), [06-threat-model.md](06-threat-model.md) (the threats the security probes exercise)

## 1. What this is for

The site says Weir has no head-to-head benchmark against any other cache ([compare](../site/content/pages/compare.md), [benchmarks](../site/content/pages/benchmarks.md)). This document specifies one. It measures Weir and four other caches as black boxes over HTTP, with the same origin and the same load for each.

The aim is adversarial in both directions. Scenarios where Weir is expected to lose are included on purpose, every competitor runs with its own documented tuning as well as its defaults, and a result that embarrasses Weir is a finding to record, then either fix or publish.

This document does not change Weir's behavior. It is normative only for the harness in `bench/compare/`.

## 2. Subjects

| Subject | What it is | Version | Role |
|---|---|---|---|
| `caddy-weir` | Caddy with the Weir handler, memory store | Caddy 2.11.7, Weir v1.0.1 | under test |
| `weirproxy` | `examples/weirproxy` on `net/http` | Weir v1.0.1 | under test; shows what Caddy adds |
| `varnish` | Varnish Cache, official image | 9.1.0 | incumbent |
| `nginx` | nginx `proxy_cache`, official image | 1.30.5 (stable) | incumbent |
| `caddy-souin` | Caddy with cache-handler (Souin) | Caddy 2.11.7, cache-handler v0.17.0, Otter storage v0.0.20 (tuned tier) | the same host as Weir |
| `caddy-nocache` | Caddy `reverse_proxy` only | 2.11.7 | control |
| `origin` | the harness origin, direct | | control |

Weir is built from its published modules, the way a user gets it, never from the working tree. A fix to Weir is measured by raising `WEIR_VERSION` after a release, or by a separate, labeled run.

Out of the first round: Apache Traffic Server and a Valkey-against-Redis pairing (second round, only if the first is worth publishing), Squid, and Pingora. Pingora would need a proxy written by us in Rust, and the numbers would describe our code.

## 3. Config tiers and fairness rules

Every subject runs in two tiers.

- `default`: the smallest configuration that turns caching on. It answers "what does someone who follows the quickstart get".
- `tuned`: the project's own documented stampede and stale settings. Each line of a tuned config cites the documentation it comes from, in a comment.

The controls and `weirproxy` have one configuration and run once.

Rules:

- R1. Limits that are plumbing, not cache policy, are raised the same way for every subject in both tiers: file descriptors, listen backlog, nginx `worker_connections`.
- R2. Where a competitor documents an origin concurrency cap with a queue, the tuned tier sets it to Weir's default limiter numbers (64 concurrent, 1024 queued, 2 s wait). Varnish has this since 7.6 (`.max_connections`, `.wait_limit`, `.wait_timeout`). Open source nginx has `max_conns` but no queue (`queue` is commercial), so a cap would turn overflow into 502 responses; its tuned tier leaves the cap off and the report says so.
- R3. Weir's tuned tier turns on stale serving (`while_revalidate 60s`, `if_error 5m`), which is off by default (D6). Varnish gets `grace 60s` and `keep 5m`, nginx `proxy_cache_use_stale` with `background_update`, Souin `stale 5m`.
- R3a. cache-handler logs that its default storage is "not optimized and for development purpose", so its tuned tier uses the Otter storage its documentation recommends. The default tier keeps the default storage, because that is what the quickstart gives.
- R4. Config files are committed and published with any result. Before publication, each competitor config is checked once more against that project's documentation, and a maintainer's correction is accepted and rerun.
- R5. A 503 from load shedding is counted as a client-visible failure, for Weir as for anyone else. Protecting the origin by refusing clients is reported as both numbers, never as a win alone.

## 4. Harness

`bench/compare/` is its own Go module, standard library only, with one binary.

- `compare origin` serves `/o/<profile>/<key>` on one port and a control API on another port that no cache proxies. A profile sets delay, status, `Cache-Control` and body size for every path under it. The origin counts calls and peak in-flight requests per profile. The table holds at most 64 profiles and a body is at most 128 MiB.
- `compare run` installs a scenario's profile, sends the load through one cache, reads the origin's counters, and appends one JSON line per repetition. Each repetition uses a fresh key prefix, so no cache needs purging between repetitions. Each subject and tier starts from a new container, so the cache is cold.
- `compare report` prints the median of the repetitions per scenario, subject and tier.
- `run.sh` drives Docker Compose: one subject at a time, the origin shared, container logs kept beside the results.

With no cache in between, the runner's request count and the origin's call count must be equal. `TestRunCountsAgainstOriginDirect` holds the harness to that, and the `origin` control row repeats the check in every real run.

Timing runs (pillar 5) use an established open-loop load generator, chosen and verified in CMP-06, so that latency numbers do not rest on a load tool we wrote.

## 5. Pillars

Count-based pillars run first. Their results are origin-side counts and status codes, which the Docker Desktop virtual machine does not distort.

### 5.1 Origin protection

Each wave sends its requests at one instant. Three repetitions; the median is reported.

| Scenario | Origin profile | Load | Reported | Card |
|---|---|---|---|---|
| `cold-herd` | 200 ms, `max-age=60`, 1 KiB | 1 key, 1000 clients | origin calls (1 is ideal), p99 | CMP-01 |
| `cold-keys` | 200 ms, `max-age=60`, 1 KiB | 1000 keys, 2 clients each | peak origin concurrency, failures | CMP-01 |
| `sync-expiry` | 20 ms, `max-age=5`, 1 KiB | 5000 keys primed, 6 s wait, then 1 client each | peak origin concurrency, failures, stale served | CMP-01 |
| `uncacheable-herd` | 200 ms, `private`, 1 KiB | 1 key, 500 clients | time to drain, failures (the hit-for-pass trap) | CMP-01 |
| `error-herd` | 100 ms, status 500 | 1 key, 200 clients, 3 waves 1 s apart | origin calls | CMP-01 |
| `busting-flood` | capacity-limited origin | random queries on one path, normal traffic on others | bystander p99 and failures | CMP-03 |
| `brownout` | origin fails in 30 s bursts | steady traffic over warm keys | client-visible errors, origin calls during the outage, recovery time | CMP-03 |
| `slow-leader` | one fetch hangs 30 s | 1 key, 100 clients | how long followers wait | CMP-03 |
| `leader-abort` | 2 s | the first client disconnects mid-fetch | follower failures | CMP-03 |
| `purge-herd` | 200 ms | purge under load | peak origin concurrency; "not available" where a cache has no purge | CMP-03 |

`sync-expiry` uses 5000 keys, not the 10 000 of the in-process scenario in 07 §9: 10 000 simultaneous connections would measure connection handling more than expiry.

### 5.2 Scenarios Weir is expected to lose

- `large-body-herd`: a 100 MiB body, slow origin, 50 clients on one key. Weir stores at most 1 MiB by default and passes the rest through, so each client reaches the origin. Varnish streams one fetch to all.
- `restart`: warm 10 000 keys, restart the cache, replay. nginx keeps its disk cache; Weir's memory store starts cold unless `snapshot_dir` is set (tuned tier).
- Raw hit throughput against the C proxies, in pillar 5.

### 5.3 Security probes

The origin gets deliberately unsafe routes, and each probe sends an attacker request followed by a victim request. The result per subject and tier is `poisoned`, `safe` or `not applicable`.

Probes: an unkeyed header reflected by the origin (`X-Forwarded-Host`, `X-Original-URL`), a GET with a body, parameter cloaking, a path the cache and the origin normalize differently, cache deception (`/account/x.css`), a cached `Set-Cookie`, a cached response to a request with `Authorization`, cache-poisoned denial of service (oversized header, method override, meta character), a partial response stored as a full one, HEAD and GET confusion, client `Cache-Control: no-cache` as an origin amplifier, and memory growth under high `Vary` cardinality.

The tuned tier for this pillar is each project's documented hardening, where it has any.

### 5.4 Conformance

The cache-tests revision pinned in `scripts/cache-tests.sh`, run against every subject. Pass counts by category, with Weir's existing baseline as its row.

### 5.5 Speed and memory

Run last, on a quiet machine, and reported with the caveats of §6.

- CPU time per request, from each container's cgroup `cpu.stat`, is the primary speed figure.
- Latency at fixed open-loop rates (p50, p99, p99.9) and the saturation rate, for 1 KiB, 64 KiB and 1 MiB bodies, with the cache held to 2 cores.
- Added latency on a miss, against the origin direct.
- Resident memory per object at 100 000 and 1 000 000 entries.
- Hit ratio at a fixed 64 MiB store under a Zipf workload and under a scan that pollutes the cache.

### 5.6 Differential fuzzing

A later card. Random sequences of `Cache-Control`, `Vary` and validator headers go through every cache; where Weir disagrees with the majority, the case is checked against RFC 9111. Its purpose is finding Weir bugs.

## 6. What one Mac can and cannot show

Everything runs in Docker Desktop on the reference machine (Apple M4 Pro, 12 cores, 24 GiB): one Linux virtual machine, a bridge network, cores shared with macOS.

- Counts and verdicts (5.1 to 5.4) do not depend on timing precision and can be published as measured.
- Latency is published only as a ratio to the `caddy-nocache` control from the same run. The control is measured again between subjects, and a run whose control moved by more than 10% is discarded.
- Five repetitions for timing runs, subject order randomized.
- No absolute throughput claim is made from this machine.

## 7. Scorecard

For each scenario and tier, Weir is marked win, tie or loss against the best competitor in the tuned tier, on the scenario's reported figures. A tie is within 10%, or equal counts. Every Weir bug or surprising default found on the way is listed with the scorecard, whatever the marks say.

## 8. Findings in Weir

A finding becomes a card of its own. It is never fixed inside a comparison card. If a fix ships before publication, the page reports the result before and after.

## 9. Results stay private until the owner decides

Decided on 2026-10-13: run first, then decide what to publish.

- Raw results are written to `bench/compare/results/`, which git ignores. The repository is public, so a pushed results file is a published one.
- The harness, the configs and this document go through normal pull requests.
- Whatever is published names every pillar that was run, including any left out, so the page does not hide its selection.

## 10. Open items

- Docker image digests are recorded into each run's `.env.txt`; pinning them in `compose.yaml` happens in CMP-02 after the first successful pull.
- The Varnish 9.1 image's environment variables, its VCL and the nginx configs are checked in CMP-02 against the running containers. The four Caddyfiles were validated on 2026-10-13 with native builds of the same module versions.
- The open-loop load generator for 5.5 is chosen in CMP-06.
