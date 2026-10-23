# Weir comparison benchmark specification

Status: v0.5. Design approved by the project owner on 2026-10-13; the 160 MiB store of §5.5 on 2026-10-22. Two rounds ran on 2026-10-13 and a third, against `v1.2.0`, on 2026-10-22; the results of the third are in [benchmarks.md](benchmarks.md).
Date: 2026-10-23 (v0.5: round 3 and what it published; v0.4: the speed pillar's harness, CMP-06)
Depends on: [07-testing-strategy.md §9](07-testing-strategy.md) (the in-process load scenarios this mirrors over HTTP), [06-threat-model.md](06-threat-model.md) (the threats the security probes exercise)

## 1. What this is for

Before this work the site said Weir had no head-to-head benchmark against any other cache. This document specifies one. It measures Weir and four other caches as black boxes over HTTP, with the same origin and the same load for each.

The aim is adversarial in both directions. Scenarios where Weir is expected to lose are included on purpose, every competitor runs with its own documented tuning as well as its defaults, and a result that embarrasses Weir is a finding to record, then either fix or publish.

This document does not change Weir's behavior. It is normative only for the harness in `bench/compare/`.

## 2. Subjects

| Subject | What it is | Version | Role |
|---|---|---|---|
| `caddy-weir` | Caddy with the Weir handler, memory store | Caddy 2.11.7, Weir v1.1.0 | under test |
| `weirproxy` | `examples/weirproxy` on `net/http` | Weir v1.1.0 | under test; shows what Caddy adds |
| `caddy-weir-next`, `weirproxy-next` | the same two, built from the working tree | the commit in the run's `.next.txt` | under test, unreleased; not run in round 2 |
| `varnish` | Varnish Cache, official image | 9.1.0 | incumbent |
| `nginx` | nginx `proxy_cache`, official image | 1.30.5 (stable) | incumbent |
| `caddy-souin` | Caddy with cache-handler (Souin) | Caddy 2.11.7, cache-handler v0.17.0, Otter storage v0.0.20 (tuned tier) | the same host as Weir |
| `caddy-nocache` | Caddy `reverse_proxy` only | 2.11.7 | control |
| `origin` | the harness origin, direct | | control |

`caddy-weir` and `weirproxy` are built from Weir's published modules, the way a user gets them. A fix that is not released yet is measured by the labeled subjects `caddy-weir-next` and `weirproxy-next`: `run.sh` builds them from the working tree, records the commit beside the results, and they run the same configuration files as the published subjects. A result from a `-next` subject is always reported with its commit and never called a release. All three drivers build them through `lib.sh`, so no driver can use a binary another run left behind.

Round 3 measured the published `v1.2.0`, and neither it nor round 2 ran the `-next` subjects. Round 2 measured the published `v1.1.0`: `main` differed from the `v1.1.0` tag in one documentation comment (`caddy/doc.go`), so they would have measured the same code twice. `v1.0.1` is no longer a subject. Its round 1 figures stay in the before-and-after table of [benchmarks.md](benchmarks.md), where they show what the release fixed; a `v1.0.1` column in every table would describe a version nobody should install and would have had to be rerun with the corrected harness to sit beside the other columns.

Out of the first round: Apache Traffic Server and a Valkey-against-Redis pairing (second round, only if the first is worth publishing), Squid, and Pingora. Pingora would need a proxy written by us in Rust, and the numbers would describe our code.

## 3. Config tiers and fairness rules

Every cache runs in two tiers, and the three other caches in a third for the probes and conformance.

- `default`: the smallest configuration that turns caching on. It answers "what does someone who follows the quickstart get".
- `tuned`: the project's own documented stampede and stale settings. Each line of a tuned config cites the documentation it comes from, in a comment.

- `hardened`, for the probes and conformance only (§5.3): the default tier plus that project's own documented way to key or strip request input. Varnish, nginx and cache-handler have one. Weir does not, because its default already hides unkeyed request input from the origin.

The controls and `weirproxy` have one configuration and run once.

Rules:

- R1. Limits that are plumbing, not cache policy, are raised the same way for every subject in both tiers: file descriptors, listen backlog, nginx `worker_connections`, the ephemeral port range (`ip_local_port_range` 1024 to 65535) and `tcp_tw_reuse`. The first run without the last two gave nginx and the no-cache control 502 responses from port exhaustion ("cannot assign requested address"), which would have been reported as their behavior. Room to store a scenario's keys is plumbing in the same sense: nginx gets `max_size=1g`, Varnish 512 MiB, and cache-handler's tuned tier an Otter size of 1,000,000, because at Otter's default of 10,000 the 5,000 keys of `sync-expiry` were already being evicted and the evictions were counted as origin calls (found in round 2 and reproduced by hand).
- R2. Where a competitor documents an origin concurrency cap with a queue, the tuned tier sets it to Weir's default limiter numbers (64 concurrent, 8192 queued, 5 s wait since HRD-21; 1024 and 2 s in the first pass). Varnish has this since 7.6 (`.max_connections`, `.wait_limit`, `.wait_timeout`). Open source nginx has `max_conns` but no queue (`queue` is commercial), so a cap would turn overflow into 502 responses; its tuned tier leaves the cap off and the report says so.
- R3. Weir's tuned tier turns on stale serving (`while_revalidate 60s`, `if_error 5m`), which is off by default (D6). Varnish gets `grace 60s` and `keep 5m`, nginx `proxy_cache_use_stale` with `background_update`, Souin `stale 5m`.
- R3a. cache-handler logs that its default storage is "not optimized and for development purpose", so its tuned tier uses the Otter storage its documentation recommends. The default tier keeps the default storage, because that is what the quickstart gives.
- R4a. Every published page that shows a figure about another project says that it comes from our own runs and has not been verified by that project's owners.
- R4. Config files are committed and published with any result. Before publication, each competitor config is checked once more against that project's documentation, and a maintainer's correction is accepted and rerun.
- R5. A 503 from load shedding is counted as a client-visible failure, for Weir as for anyone else. Protecting the origin by refusing clients is reported as both numbers, never as a win alone.

## 4. Harness

`bench/compare/` is its own Go module, standard library only, with one binary.

- `compare origin` serves `/o/<profile>/<key>` on one port and a control API on another port that no cache proxies. A profile sets delay, status, `Cache-Control` and body size for every path under it. The origin counts calls and peak in-flight requests per profile. The table holds at most 64 profiles and a body is at most 128 MiB.
- `compare run` installs a scenario's profile, sends the load through one cache, reads the origin's counters, and appends one JSON line per repetition. Each repetition uses a fresh key prefix, so no cache needs purging between repetitions. Each subject and tier starts from a new container, so the cache is cold.
- `compare report` prints the median of the repetitions per scenario, subject and tier.
- `run.sh` drives Docker Compose: one subject at a time, the origin shared, container logs kept beside the results.

- A profile can carry a capacity: the origin refuses with 503 beyond that many concurrent requests and counts the refusals. Counters survive a profile replacement, so a scenario can change the origin's behavior midway.
- A profile can name another as its pool and then shares that profile's capacity while its calls are counted apart (`busting-flood`). A replaced profile keeps its counters and its in-flight count.
- Before each scenario the runner polls the `ready` profile through the subject until it answers 200. Without this gate a breaker opened by one scenario failed the first requests of the next.
- A 2xx response whose body is not the profile's length is recorded as `bad-body:<code>` and counted as a failure. Round 1 counted status codes only, and cache-handler's empty 200s passed as successes.
- A purge is one line per subject, `subjects/<name>/purge` (`METHOD URL [BODY]`, with `{path}` in the URL for one request per key). No file means the subject has no purge, and its row says `not available` with no figures.
- The runner is a container and cannot restart another one. For `restart` it writes `restart.request` into the results directory and `run.sh` performs a graceful `docker compose restart` of the same container, so whatever the cache keeps on its own disk is still there.
- `compare probe` runs the security probes of §5.3; `probes.sh` and `conformance.sh` drive it and the cache-tests suite across subjects.

With no cache in between, the origin must count exactly the requests the runner sent. Every row records both numbers (`sent`, `seen`). `TestRunCountsAgainstOriginDirect` runs every scenario the origin can run against the origin alone, in a synctest bubble over an in-memory transport, and holds each to that and to its request counts (`purge-herd` needs a cache and has its own test against one that purges and one that only says so); the `origin` control row repeats the check in every real run, and `compare report` fails when one of its rows disagrees.

Timing runs (pillar 5) use an established open-loop load generator, so that latency numbers do not rest on a load tool we wrote: vegeta v12.13.0 (`github.com/tsenart/vegeta`), built into the `load` image (`docker/load.Dockerfile`). Its `attack` sends at a fixed rate and starts more workers when responses are slow, so a slow subject does not lower the offered load while vegeta keeps up; a run whose achieved rate is under 95% of the rate asked for does not count as sustained, because a lagging generator would hide queueing again. Its reports give p50 and p99, and p99.9 comes from its `hdrplot` output (t-digest quantiles printed in HDR-plot form), printed only for runs of 10 000 requests or more. `compare targets` writes the request lists (uniform, Zipf, Zipf with a scan, fill) from a fixed seed, so every subject gets the same one, and `compare speed` reads the reports back. `speed.sh` runs the whole pillar from a plain terminal.

## 5. Pillars

Count-based pillars run first. Their results are origin-side counts and status codes, which the Docker Desktop virtual machine does not distort.

### 5.1 Origin protection

Each wave sends its requests at one instant. Three repetitions; the median is reported.

| Scenario | Origin profile | Load | Reported | Card |
|---|---|---|---|---|
| `cold-herd` | 200 ms, `max-age=60`, 1 KiB | 1 key, 1000 clients | origin calls (1 is ideal), p99 | CMP-01 |
| `cold-keys` | 200 ms, `max-age=60`, 1 KiB | 1000 keys, 2 clients each | peak origin concurrency, failures | CMP-01 |
| `sync-expiry` | 20 ms, `max-age=5`, 1 KiB | 5000 keys primed, 6 s wait, counters zeroed, then two waves of 1 client each 2 s apart; counters read 2 s after the second | origin calls after each wave, peak origin concurrency, failures | CMP-01, CMP-03b |
| `uncacheable-herd` | 200 ms, `private`, 1 KiB | 1 key, 500 clients | time to drain, failures (the hit-for-pass trap) | CMP-01 |
| `error-herd` | 100 ms, status 500 | 1 key, 200 clients, 3 waves 1 s apart | origin calls | CMP-01 |
| `cold-keys-capped` | as `cold-keys`, origin capacity 128 | as `cold-keys` | failures, origin refusals | CMP-03 |
| `sync-expiry-capped` | as `sync-expiry`, origin capacity 128 | as `sync-expiry` | failures, origin refusals | CMP-03 |
| `slow-origin-herd` | 15 s, `max-age=60`, 1 KiB | 1 key, 100 clients | origin calls, failures | CMP-03 |
| `leader-abort` | 2 s, `max-age=60` | the first client disconnects after 500 ms, 50 followers join at 100 ms | follower failures | CMP-03 |
| `turned-private` | 200 ms, `max-age=2`, then `private` | 1 key, stored, then 3 waves of 100 | origin calls, p50 of the third wave | CMP-03 |
| `bad-route` | 20 ms, status 504 | 60 requests with distinct queries on one path, then 200 clients on healthy paths | bystander failures | CMP-03 |
| `origin-down` | 5 ms, `max-age=2`, then status 503, then healthy | 200 keys stored, 3 waves of 200 while down, 200 clients 1 s and 10 s after recovery | failures and origin calls while down, 200s after recovery | CMP-03 |
| `post-flood` | `max-age=600`, 1 KiB | 2000 keys stored, 300 000 `POST` requests to other paths, then the keys read again | stored entries lost (one repetition) | CMP-03 |
| `oversize-herd` | 200 ms, `max-age=60`, 2 MiB | 1 key, 50 clients, 3 waves | origin calls | CMP-03 |
| `busting-flood` | 100 ms, `max-age=60`, capacity 128 shared by the flood and the bystanders | for 5 s, every 100 ms: 200 requests for one path with a fresh query, 20 for other paths nobody has asked for | bystander failures and origin calls, flood failures, all failures | CMP-03b |
| `brownout` | 5 ms, `max-age=2`; in a burst, 503 after 100 ms | 200 stored keys, 200 requests a second for 60 s: 5 s healthy, 10 s failing, 5 s healthy, 10 s failing, 30 s healthy | failures in the bursts and outside them, origin calls in the bursts, seconds from recovery to the last failure | CMP-03b |
| `purge-herd` | 200 ms, `max-age=600` | 1000 stored keys read once, purged, read at once and 1 s later, read again after 3 s more | failures, origin calls, peak origin concurrency, answers from before the purge, keys not refreshed at the last read; "not available" where a cache has no purge | CMP-03b |

`bad-route` waits 11 s before it starts, so a failure window left by an earlier scenario cannot count against the subject. The two `-capped` scenarios exist because an origin with no limit makes every cache that forwards the whole burst look free; with a capacity, a low origin call count shows up as fewer failed clients.

`sync-expiry` uses 5000 keys, not the 10 000 of the in-process scenario in 07 §9: 10 000 simultaneous connections would measure connection handling more than expiry.

Notes on the CMP-03b scenarios:

- `busting-flood` reports the flood's failures beside the bystanders'. A cache that saves the bystanders by refusing the flood has refused clients (R5), and the page gives the total. The capacity of 128 is the number the `-capped` scenarios already use, not Weir's limiter value. No tier sorts or strips query strings, Weir's included: the scenario is about how origin capacity is shared.
- `brownout` uses 10 s bursts, not the 30 s of 07 §9. Ten seconds is five lifetimes of the keys, and three repetitions of a 60 s scenario over eleven subject-tiers is what one session can run. The last healthy phase is 30 s so that a breaker that backed off has time to close. Failure is a 503, not added latency: a black box cannot be given the same origin timeout in every cache.
- `purge-herd` purges each subject the way its own configuration documents (Weir: a soft purge of everything through Caddy's admin API; Varnish default: `return (purge)` per URL; Varnish tuned: `purge.soft()` of the bundled `vmod_purge` per URL, so its grace applies as Weir's stale window does; cache-handler: its flush API, which answered on Caddy's admin endpoint and not on the site in our build, though its README routes it on the site; nginx: none, `proxy_cache_purge` is commercial). The last two figures exist because a cache that ignored the purge would otherwise show the best origin numbers.

### 5.2 Scenarios Weir is expected to lose

- `large-body-herd`: a 50 MiB body, 200 ms origin, 20 clients on one key, one repetition (the virtual machine has 8 GiB). Weir stores at most 1 MiB by default and passes the rest through, so before `v1.2.0` each client reached the origin. Since `v1.2.0` clients that wait together share one download, and round 3 measured one origin call. Varnish streams one fetch to all.
- `restart`: store 2000 keys with `max-age=600`, stop and start the cache's container gracefully, read the keys again 64 at a time. The origin calls of the replay are the entries lost. 2000 keys in chunks, not 10 000 at once, for the reason given for `sync-expiry`. nginx keeps its disk cache. Weir's tuned tier sets `snapshot_dir`; its snapshot reloads entries as stale ones (D15), so against an origin with no validators the replay still fetches every key, and the scenario reports that. Varnish's `deprecated_persistent` storage is marked deprecated and experimental in its own storage guide and is not used; neither cache-handler tier uses one of its disk storages.
- Raw hit throughput against the C proxies, in pillar 5.

### 5.3 Security probes

The origin gets deliberately unsafe routes under `/p/<probe>/`, and each probe sends an attacker request followed by a victim request. The result per subject and tier is `unsafe` or `safe`, and the results file keeps the two responses that decided it. An `unsafe` verdict says what the cache does when the origin is wrong. It is reported that way, never as a vulnerability of the project: each of them documents how to key or strip the input.

Probes: an unkeyed header reflected by the origin (`X-Forwarded-Host`, `X-Original-URL`), a GET with a body, parameter cloaking, a path the cache and the origin normalize differently, cache deception (`/account/x.css`), a cached `Set-Cookie`, a cached response to a request with `Authorization`, cache-poisoned denial of service (oversized header, method override, meta character), a partial response stored as a full one, HEAD and GET confusion, client `Cache-Control: no-cache` as an origin amplifier, and memory growth under high `Vary` cardinality.

All of them have run. Round 2 added parameter cloaking, path normalization, the meta character and `Vary` cardinality, and made the "origin error served as a success" probe concurrent (three waves of 20 clients), because the case it exists for, cache-handler's empty 200 to the waiters of a failed fetch, needs clients that overlap.

- Parameter cloaking: the origin splits the query on `;` as well as `&` and the last value wins. The attacker sends four spellings of a second `q`; the victim asks for `?q=page`.
- Path normalization: the origin serves its page only for the path exactly as spelled. The attacker asks for `//page`, `/./page`, `/x/../page` and `/%70age`; the victim for `/page`.
- Meta character: a request written by hand with a header value holding a control byte (0x07), then one holding 0xFF; the origin rejects both with a 400 it marks cacheable.
- `Vary` cardinality: 20 000 values of a header the origin varies on, for one URL; then the first and last 100 are asked for again, and a value never sent. The verdict is whether anyone was served another value's variant. The figure is how many of the 200 came from the cache. A cache that does not forward the header has one variant and reports that instead. `probes.sh` also reads each container's memory before and after; that reading is approximate and is published with the control's beside it.

`probe_test.go` holds every probe to two things: against the origin alone no probe is unsafe, and through a deliberately naive cache (path-only key, cleaned path, everything stored) every probe is, except the error probe, which has its own stand-in: a cache that replays a stored 500 is not serving a success, and one that hands waiters an empty 200 is.

The hardened tier (§3) is each project's own documentation applied to the request headers the round 1 probes use: `hash_data()` in `vcl_hash` for Varnish, `proxy_set_header` with an empty value plus `proxy_no_cache` and `proxy_cache_bypass` for nginx, `key.headers` and `default_cache_control` for cache-handler. Each line of those files cites its page. The tier was written after the default results and is fitted to them, in the other projects' favor; the page says so. A header the list does not name (the meta character probe's) is not covered, and the page reports that too, because it is the difference between listing inputs and hiding them by default.

### 5.4 Conformance

The cache-tests revision pinned in `scripts/cache-tests.sh`, run against every subject in every tier it has. `conformance.sh` starts the suite's own server under the name `origin` on the Compose network, so no subject's configuration changes, except that Weir is told to forward the suite's request headers (`subjects/caddy-weir/conformance/<tier>/`), as in its baseline run. Per subject and tier: tests passed, and the rest split into assertion failures and setup failures as the suite reports them, since a test whose setup failed says nothing about the behavior under test. The script compares Weir's default row with `testdata/cache-tests-baseline.json` test for test. The suite mixes required and optional behavior, so the counts are published with that caveat and without a per-test reading of the other projects' failures.

### 5.5 Speed and memory

Run last, on a quiet machine, and reported with the caveats of §6.

- CPU time per request, from each container's cgroup `cpu.stat`, is the primary speed figure.
- Latency at fixed open-loop rates (p50, p99, p99.9) and the saturation rate, for 1 KiB, 64 KiB and 960 KiB bodies, with the cache held to 2 cores.
- Added latency on a miss, against the origin direct.
- Memory per object (anonymous plus shared memory) at 100 000 and 1 000 000 entries.
- Hit ratio at a fixed 160 MiB store under a Zipf workload (200 000 keys of 3 000 bytes, exponent 1.1) and under a scan that pollutes the cache (one never-seen key in five). The spec first said 64 MiB; the owner chose 160 MiB for every subject on 2026-10-22, because the Caddy adapter accepts no smaller `max_bytes`. 3 000-byte bodies because nginx's `max_size` counts file-system blocks: with key and headers such an object still fits one 4 KiB block, so the stores hold about the same number of objects; Varnish's and Weir's byte accounting differ in smaller ways (Weir charges 48 bytes per header name), which the page states. cache-handler's default storage has no verified size setting, so it has no hit-ratio or memory rows.

How `speed.sh` measures (CMP-06): the subject runs pinned to virtual CPUs 0 and 1 of the Docker Desktop VM, the load generator to 2 to 5 and the origin to 6 and 7 (`speed.compose.yaml`; 8 CPUs or more). These are the VM's CPUs, not particular performance or efficiency cores of the M4 Pro. Bodies are 1 KiB, 64 KiB and 960 KiB, the largest every subject stores at its defaults (Weir's 1 MiB limit counts headers), over 1 000 keys, or 50 for the largest, so the working set fits the 160 MiB store and no shard of Weir's store (16 of 10 MiB, about ten such objects each) comes near full. Every key is fetched twice before the hit runs, because a cache with an admission filter (Weir's S3-FIFO) keeps an object seen once on probation and the hit rows would otherwise begin with misses. Every timed run records the origin's calls, so a hit run that reached the origin shows it, and the body size every response must have: a run with responses of another size is a failure (`bad body`), as in §4. CPU time per request is the difference in the container's cgroup `cpu.stat` `usage_usec` over a run, divided by the requests sent, read through a helper container with the host's cgroup namespace. Memory per object is the difference in `memory.stat` `anon` plus `shmem` across a fill of N entries of 100 bytes in a tier sized so nothing is evicted (`mem`), divided by N: anonymous and shared memory (nginx keeps its keys in a shared `keys_zone`), without the page cache that `memory.current` adds. nginx keeps its bodies in files on disk, which this does not count, and the page says so beside its row; for Go subjects the figure includes heap not yet collected. Hit ratio is one minus the origin's calls over the requests sent. Rates per body size: 2 000 and 8 000 per second for 1 KiB, 1 000 and 4 000 for 64 KiB, 100 and 400 for 960 KiB, then the rate is raised by half for up to four more steps while the run is sustained: 99.9% successes, every success of the right size (errors such as timeouts count against the 99.9%, not as wrong bodies), p99 within ten times the first rate's, and at least 95% of the rate asked for. The report gives the highest sustained rate per subject and size, with "≥" when the climb never failed. Those rates belong to this machine: per §6 they are published only as a comparison between subjects of the same run, never as a throughput claim. Latency ratios compare each subject with the control measured just before it, percentile to percentile. Memory and hit ratio are counts, so they run in the first repetition only and the drift gate does not apply to them.

### 5.6 Differential fuzzing

A later card. Random sequences of `Cache-Control`, `Vary` and validator headers go through every cache; where Weir disagrees with the majority, the case is checked against RFC 9111. Its purpose is finding Weir bugs.

## 6. What one Mac can and cannot show

Everything runs in Docker Desktop on the reference machine (Apple M4 Pro, 12 cores, 24 GiB): one Linux virtual machine, a bridge network, cores shared with macOS.

- Counts and verdicts (5.1 to 5.4) do not depend on timing precision and can be published as measured.
- Latency is published only as a ratio to the `caddy-nocache` control from the same run. The control is measured again between subjects, and a run whose control moved by more than 10% is discarded.
- Five repetitions for timing runs, subject order randomized. `compare speed` prints medians over the repetitions the drift gate kept and names each one it discarded; the gate checks the control's p50 and p99.
- No absolute throughput claim is made from this machine. The speed pillar's highest sustained rates are shown only beside each other, subject against subject from one run, with the machine named.

## 7. Scorecard

For each scenario and tier, Weir is marked win, tie or loss against the best competitor in the tuned tier, on the scenario's reported figures. A tie is within 10%, or equal counts. Every Weir bug or surprising default found on the way is listed with the scorecard, whatever the marks say.

The first round published no marks. Its tables carry too many caveats for a win or a loss per cell (unreviewed configurations, undercounted cells), so [benchmarks.md](benchmarks.md) lists in prose where Weir is ahead and where it is behind. Marks wait for the maintainers' review of R4 and for pillar 5.5.

## 8. Findings in Weir

A finding becomes a card of its own. It is never fixed inside a comparison card. If a fix ships before publication, the page reports the result before and after.

First round: three defects of v1.0.1 were already fixed on the v1.1 branch (`slow-origin-herd`, `bad-route`, `post-flood`). Four more findings, CMP-F1 to CMP-F4 in [cards/21-comparison.md](cards/21-comparison.md), were fixed in card HRD-21 with the owner's go-ahead, and the Weir v1.1 subjects were rerun. All seven are reported before and after in [benchmarks.md](benchmarks.md).

Second round: four findings, CMP-F5 to CMP-F8, none fixed in the comparison cards. The per-path cap refuses a busy path's clients while the origin has room (`busting-flood`); a soft purge with stale serving leaves most URLs unrefreshed (`purge-herd`); without stale serving the breaker keeps failing clients after the origin recovers from repeated bursts (`brownout`); a snapshot does not save origin calls after a restart (`restart`). Each is on the page as a loss and has a card for the owner's decision. They were addressed for `v1.2.0` (card HRD-23), and round 3 reports each before and after.

Third round (`v1.2.0`): no new defect. Three figures are new losses or got worse, and are on the page as such: Weir holds the most memory per small object of the measured caches (3,692 B behind Caddy at 100 000 entries, Varnish 1,080 B), its hit ratio under the scan workload is the lowest (0.666, Varnish 0.672), and its tuned tier calls a dead origin once per stale key in `origin-down` (200, where `v1.1.0` made 144), a side effect of the queued refreshes. None has a card yet; that is the owner's decision.

## 9. What is published

Decided on 2026-10-13: run first, then decide what to publish. After the first round the owner decided, the same day, to publish the count-based pillars in the documentation and on the site.

- Raw results are written to `bench/compare/results/`, which git ignores. The published tables in [benchmarks.md](benchmarks.md) are medians taken from them.
- The harness, the configs and this document go through normal pull requests.
- Whatever is published names every pillar that was run, including any left out, so the page does not hide its selection.

## 10. Open items

- Image digests are pinned in `compose.yaml` (2026-10-13). The two builder images in `docker/` are pinned by tag only.
- R4 is not done: no competitor configuration has been reviewed by that project's maintainers. The page says so and invites corrections. Our own review has found three mistakes against other projects so far (port exhaustion, Varnish's rule that abandons a failed background fetch, Otter's default size), each fixed and rerun before publication.
- The published data of round 2 came from two harness commits that differ in one subject's configuration and one probe's verdict logic. Round 3 ran each script once at one commit (`0147a80`).
- cache-handler: the `stale` setting has no visible effect in our tuned tier, with or without stale directives from the origin; a `GET` with a body, a cached `Set-Cookie` and a percent-encoded path stay unsafe in its hardened tier because we found no documented option for them; its Otter storage grew by more than 1 GiB for 10 000 small objects. Each may be our configuration. They need its maintainers' reading (R4) before more is said. The empty 200 is reported upstream as darkweak/souin#876 (open, no reply on 2026-10-13); the percent-encoded path is not reported yet.
- Latency was measured but not published: rounds 1 and 2 did not run under the drift gate of §6. Round 3 ran pillar 5.5 on a quiet machine, and all five timing repetitions failed the gate (the control's p50 held within 5%, its p99 moved 12 to 20% within each repetition). The owner decided on 2026-10-22 to publish the pillar's counts only (memory per object, hit ratio, origin calls) and keep its latency, CPU and saturation figures unpublished until a run passes the gate.
- Differential fuzzing (§5.6) has not run.
- Varnish's tuned tier carries Weir's limiter numbers (R2), so `cold-keys` and the capped scenarios compare two configurations of the same idea there, not two designs.
- `restart` does not show what Weir's snapshot is for. An origin profile with a validator, and a restart while the origin is down, would.
