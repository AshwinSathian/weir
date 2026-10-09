# Weir research notes

Status: living document
Date of this pass: 2026-09-27

This is the evidence behind the decisions in 01–08. Each claim lists where it was checked and when, so a later reader can tell a verified fact from something carried over from the seed. Claims marked "seed, not re-verified" were not independently checked in this pass.

## 1. Standards read for this design

| Document | Status (checked 2026-09-27) | Sections used |
|---|---|---|
| RFC 9111 HTTP Caching | Internet Standard, June 2022, obsoletes RFC 7234 | §3 storage, §3.1 header exclusions, §3.5 Authorization, §4 reuse and collapsing, §4.1 Vary, §4.2 freshness and age, §4.2.4 stale, §4.3 validation, §4.4 invalidation, §5.2 directives, §7 security |
| RFC 9110 HTTP Semantics | Internet Standard, June 2022 | §7.6.1 hop-by-hop fields, §12.5.3 Accept-Encoding, §12.5.5 Vary, §13.2.2 precondition order, §15.1 heuristically cacheable statuses (200, 203, 204, 206, 300, 301, 308, 404, 405, 410, 414, 501) |
| RFC 5861 stale-while-revalidate, stale-if-error | Informational, 2010 | §3, §4 (error = 500, 502, 503, 504), §5 (revalidation should be request-triggered to avoid amplification) |
| RFC 9211 Cache-Status | Proposed Standard, June 2022 | parameters `hit`, `fwd`, `fwd-status`, `ttl`, `stored`, `collapsed`, `key`, `detail` |
| RFC 9213 Targeted Cache-Control | Proposed Standard, June 2022 | reviewed; deferred (needs a Structured Fields dictionary parser) |
| RFC 9875 HTTP Cache Groups | Proposed Standard, October 2025 | `Cache-Groups`, `Cache-Group-Invalidation`, origin scoping, non-cascading, shared-hosting security note, "MUST support at least 32 groups ... at least 32 characters" |
| RFC 9651 Structured Field Values | Proposed Standard, September 2024, obsoletes RFC 8941 | List and String parsing for RFC 9875 |

Texts were downloaded from rfc-editor.org and read directly.

## 2. Findings that changed the seed's design

1. RFC 7234 is obsolete. The seed cites it throughout; the spec targets RFC 9111.
2. Additive TTL jitter serves content past its origin-declared lifetime, which RFC 9111 §4.2.4 forbids without explicit permission. Jitter must shorten.
3. RFC 9111 §4 explicitly anticipates collapsed requests and says that when the leader's response is not usable for some followers, "it will need to forward the requests". This is the hit-for-miss problem Varnish solves with hit-for-miss objects (default 120 s TTL in Varnish's built-in VCL). Weir's markers (FR-STO-12) and re-entry rule (FR-COA-5) come from here.
4. nginx's `proxy_cache_lock_timeout` (default 5 s) forwards the waiting request after the timeout "however, the response will not be cached" (since 1.7.8); `proxy_cache_lock_age` (default 5 s) lets "one more request" through when the lock holder is slow. Pingora's cache lock distinguishes a writer-age timeout from a reader-wait timeout (`LockStatus::AgeTimeout` and `WaitTimeout` in `pingora-cache/src/lock.rs`). Weir's `LeaderMaxAge` and `FollowerMaxWait` follow the same split. Unlike nginx, Weir stores the follower's own fetch result, because Weir's newest-response-wins store rule makes that safe.
5. RFC 9875 (October 2025) standardizes what the seed calls surrogate keys. Adopted.
6. RFC 9211 standardizes the staleness header the seed mentions as optional. Adopted.
7. Kettle's defense advice in "Web Cache Entanglement" (2020) is "avoid rewriting cache keys; rewrite requests instead". That became principle P2 and decision D4.
8. Go's `testing/synctest` (GA in Go 1.25) fakes time for every goroutine in a bubble and detects durable blocking. Go 1.27 adds `synctest.Sleep` and `httptest.NewTestServer` with an in-memory network that works inside bubbles. A `Clock` interface is unnecessary (D9). Verified with `go doc` against the local Go 1.27.1 toolchain.
9. Mutex waits are not durably blocking inside a synctest bubble (from the package docs: "locking a sync.Mutex or sync.RWMutex" is listed as not durably blocking). This constrains how the limiter and flights wait (P8).
10. Go 1.27 release notes add a `goroutineleak` profile in `runtime/pprof`; useful for the load tests' goroutine-leak check.
11. RFC 9111 §4.2.3 allows `corrected_initial_age = age_value + response_delay` and offers `max(apparent_age, ...)` only as a conservative option for paths with pre-HTTP/1.1 caches. The conservative form ties freshness to agreement between the origin's clock and ours; Weir uses the permitted simpler form (FR-FRS-4, T-30).
12. RFC 9111 §4.4 makes target-URI invalidation after unsafe methods a MUST, while `Location`/`Content-Location` (RFC 9111) and grouped invalidation (RFC 9875) are MAYs. That asymmetry decides which invalidations must be exact (target URI, invalid mode) and which Weir can apply in a weaker, cheaper form (groups, soft mode), and it forced the bounded epoch sketch (T-29): the MUST cannot be skipped under load, so its memory has to be fixed instead.

## 3. Algorithms

### 3.1 XFetch (probabilistic early expiration)

Vattani, Chierichetti, Lowenstein, "Optimal Probabilistic Cache Stampede Prevention", PVLDB 8(8), 2015. Read from the PDF. The algorithm (their Figure 3): recompute when `Time() - Δ·β·log(rand()) >= expiry`, where Δ is the last recomputation time and β defaults to 1 ("already provides effective prevention"; larger β favors earlier recomputation). The paper shows the exponential gap distribution is optimal in the trade-off between stampede size and how early recomputation happens. Weir uses it for early background refresh on fresh hits (FR-FRS-6); because Weir also coalesces, the stampede-prevention property matters less than the latency property (the refresh happens before any request sees an expired entry).

### 3.2 S3-FIFO

Yang, Zhang, Qiu, Yue, Vinayak, "FIFO queues are all you need for cache eviction", SOSP 2023. Three FIFO queues: small (10% of capacity), main (90%), ghost (metadata only, as many entries as main). Reference implementation checked in libCacheSim (`libCacheSim/cache/eviction/S3FIFO.c`): defaults `small-size-ratio=0.10`, `ghost-size-ratio=0.90`, `move-to-main-threshold=2`; main-queue reinsertion when `freq >= 1`; 2-bit frequency capped at 3; capacities in bytes. Known weakness per the authors: objects accessed exactly twice where the second access comes after the object left the small queue. Chosen over W-TinyLFU (Caffeine, and otter v2's "adaptive W-TinyLFU") for implementation simplicity; revisit with trace data.

### 3.3 Space-Saving

Metwally, Agrawal, El Abbadi, "Efficient Computation of Frequent and Top-k Elements in Data Streams", ICDT 2005. Fixed k counters; a new item replaces the minimum counter and inherits its count as overestimation error. Used for per-partition miss-rate tracking (ADR-9). Not re-read in this pass; the algorithm is standard.

### 3.4 Overload control

Ben Maurer, "Fail at Scale", ACM Queue 13(8), 2015: CoDel-style queue timeouts, adaptive LIFO, and client-side concurrency limits at Facebook. Weir uses a bounded FIFO with a queue wait deadline (the CoDel idea in its simplest form) and a concurrency cap. Adaptive LIFO and adaptive limits are recorded as OQ-3.

### 3.5 Circuit breaker

`sony/gobreaker`'s default trip condition is `counts.ConsecutiveFailures > 5` (`defaultReadyToTrip` in `v2/gobreaker.go`, checked 2026-09-27). ADR-7 departs from it to a failure ratio with minimum volume because of T-16.

## 4. Security research

| Source | Checked | What it contributed |
|---|---|---|
| CVE-2024-35296 (CVE record via cveawg.mitre.org API) | 2026-09-27 | "Invalid Accept-Encoding header can cause Apache Traffic Server to fail cache lookup and force forwarding requests." Affected 8.0.0–8.1.10 and 9.0.0–9.2.4; fixed in 8.1.11 and 9.2.5; CWE-20; published 2024-07-26. |
| James Kettle, "Responsible Denial of Service with Web Cache Poisoning", PortSwigger, 2019-10-24 | 2026-09-27 | WAF 403 pages cached; `X-Forwarded-Port` and `X-Forwarded-SSL` poisoning; invalid `Transfer-Encoding` caching 501s; `Accept-Encoding` variations; `Range: bytes=cow` caching 400s; Burp's IE9 User-Agent overwriting a homepage with an upgrade prompt; also `Accept`, `Upgrade`, `Origin`, `Max-Forwards`. Drives T-1, T-7. |
| James Kettle, "Web Cache Entanglement: Novel Pathways to Poisoning", PortSwigger, 2020-08-05 | 2026-09-27 | unkeyed query strings and parameters, parameter cloaking with `;`, fat GET, nginx decoding `%3f` in the key but not the forwarded request, Akamai key injection via `__`, internal (fragment) caches; defense: rewrite requests not keys, disable fat GET. Drives T-2, T-3, T-5, P2. |
| Martin Doyhenard, "Gotta cache 'em all: bending the rules of web cache exploitation", PortSwigger, Black Hat USA and DEF CON 32, August 2024 | 2026-09-27 | path delimiter discrepancies (`;` in Spring, `.` in Rails, `%00` in OpenLiteSpeed, `%0a` in nginx rewrites), normalization discrepancies (`%2F`, dot-segments), static-extension, static-directory and file-name caching rules as deception vectors. Drives T-6 and the decision to have no extension-based caching rules. |
| Ferretti and Ghini, "Mitigation of Random Query String DoS via Gossip", 2012 | seed, not re-verified | random query string attack (T-11). |
| RFC 9111 §7 | 2026-09-27 | cache poisoning via parser differences, timing attacks and double keying, `Set-Cookie` does not inhibit caching. |
| RFC 9875 §5 | 2026-09-27 | shared-hosting parties can group or invalidate each other's resources (T-25). |

## 5. Ecosystem facts

| Fact | Source | Checked |
|---|---|---|
| Souin latest release v1.7.9 (2026-09-16); 1 009 GitHub stars; storages include otter, badger, etcd, olric, redis, simplefs, nuts; config keys include `ttl`, `stale`, `default_cache_control`, `timeout.backend`, `timeout.cache`, key options (`disable_body`, `disable_host`, `disable_method`, `disable_query`, `disable_scheme`, `disable_vary`, `hash`, `hide`, `headers`, `template`) | GitHub API and README | 2026-09-27 |
| Souin has no TTL jitter: a code search for `jitter` in `darkweak/souin` finds only a NATS storage doc and a vendored TLS renewer | GitHub code search | 2026-09-27 |
| Caddy latest release v2.11.7 (2026-10-03, tag commit 72dd0fb); `go.mod` declares `go 1.26.0` | Go module proxy, source | 2026-10-09 |
| Caddy APIs named in 08 exist in v2.11.7 with the file and line in 08 §11; `RegisterDirectiveOrder` is still EXPERIMENTAL; admin routers are rebuilt and re-provisioned on every config load, before the apps; the metrics registry is created per config load and is pedantic; `reverseproxy.upgradeType` is unexported and has no CONNECT check; the `caddy` binary sets the memory limit at startup; `reverse_proxy` sets `X-Forwarded-For/-Proto/-Host` | source at tag v2.11.7 | 2026-10-09 |
| Caddy modules overlap during reloads; `caddy.UsagePool` is the recommended way to share state across loads | caddyserver/website extending guide via Context7 | 2026-09-27 |
| Go 1.27.1 is current; Go 1.26 added `errors.AsType`, `new(expr)`, Green Tea GC default; Go 1.27 added `synctest.Sleep`, `httptest.NewTestServer`, `maphash.Hasher`, `goroutineleak` profile, `Server.MaxHeaderValueCount` | go.dev release notes, local `go doc` | 2026-09-27 |
| golangci-lint latest v2.14.0 (2026-09-24); golangci-lint-action v9.3.0; actions/setup-go v7.0.0; actions/checkout v7.0.1 | GitHub API | 2026-09-27 |
| `debug.SetMemoryLimit(-1)` returns the current limit without changing it; the initial value is `math.MaxInt64` unless `GOMEMLIMIT` is set | local `go doc runtime/debug.SetMemoryLimit` (Go 1.27.1) | 2026-09-27 |
| Caddy's error path (`modules/caddyhttp/server.go`) writes the error status on the same `ResponseWriter`, so headers set before returning `caddyhttp.Error` survive; `handle_errors` routes run with it | source at tag v2.11.7 | 2026-10-09 |
| WebSockets over HTTP/2 and HTTP/3 use extended CONNECT (RFC 8441, RFC 9220) and carry no `Upgrade` header, so upgrade detection must include `CONNECT` | RFCs | 2026-09-27 |
| `http-tests/cache-tests` is active (last push 2026-09-18); runs against proxies via Docker or npm; authors state passing everything "means nothing" by itself | GitHub | 2026-09-27 |
| The open-source Varnish Cache project was renamed Vinyl Cache (announced September 2025, completed early 2026) after a trademark dispute; Varnish Software ships its own distribution under the old name | ma.ttias.be, Arch Linux news, HN | 2026-09-27 |
| `github.com/AshwinSathian/weir` does not exist yet; `tidb-incubator/weir` (a TiDB SQL proxy, 96 stars, last push 2022-01-16) and a few tiny unrelated repos use the name | GitHub API | 2026-09-27 |

## 6. Seed errata

Numbered against [00-design-doc.md](00-design-doc.md). None of these change the seed's thesis.

| Where | Seed says | Correction |
|---|---|---|
| §2.2, §3.4 | "correct under RFC 7234" | RFC 9111 (June 2022) obsoletes RFC 7234. |
| §4.2 | Souin "985 GitHub stars ... v1.7.8 shipped September 2025" | 1 009 stars; v1.7.9 shipped 2026-09-16. v1.7.8 was 2025-09-18, so the seed was accurate when written. |
| §4.2, §5 | "Varnish" | The open-source project is now Vinyl Cache; the behavior discussed is unchanged. |
| §4.1 | nginx, HAProxy, Envoy, Traefik versions | seed, not re-verified; they do not affect any decision. |
| §4.2 | "Its own documentation ... explicit that coalescing does not help against multi-URL stampedes" | The source listed is a Netdata guide, not Varnish's own documentation. The technical point stands: coalescing is per object. |
| §6.1 | jitter `base_ttl + random(0, jitter_window)` | must shorten, not lengthen (§2 finding 2). |
| §7.2 | `KeyBuilder.Build(req, varyHeaders)` | Vary is unknown until a response is stored; needs a two-level key (ADR-2). |
| §9 Phase 0 | "controllable `Clock`" | `testing/synctest` (ADR-10). |
| §9 M1 | key construction "with no adversarial hardening yet" | contradicts §7.3 "from the first line of code"; M1 now includes the structural hardening. |
| §10 title | "Appendix: Phase 2 — Experiment-aware routing" | the roadmap in §9 calls it Phase 3; Phase 2 is the Caddy adapter. It is Phase 3. |
| §4.5 | "relevant to the Phase 2 appendix, Part 10" | same: Phase 3. |
| §1 | "no obvious collision in the Go proxy space" | `tidb-incubator/weir` is a Go database proxy, inactive since January 2022. Low risk; noted. |
| §9 Phase 0 | license MIT | Apache-2.0 (D41), for the explicit patent grant; changed before any outside contribution. |
| §11 | document names `02-storage-interface-spec.md`, `03-testing-strategy.md`, `04-caddy-adapter-spec.md` | renumbered to make room for architecture, HLD, LLD and threat model; mapping in [README.md](README.md). |
