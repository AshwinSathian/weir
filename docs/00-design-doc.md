# Cache-Resilience Engine — Design Document

**Status:** Draft v0.1 — seed document
**Author:** Ashwin Sathian, with research and drafting assistance from Claude
**Date:** September 27, 2026
**Module path (placeholder):** `github.com/AshwinSathian/<name>`

> Repository note: this file is the original seed document, preserved verbatim. Where later documents correct or refine it, the correction is recorded in [09-research-notes.md](09-research-notes.md) under "Seed errata" and in the "Deviations from the seed" section of [01-technical-spec.md](01-technical-spec.md). When the two disagree, the numbered documents 01–09 win.

---

## 0. How to use this document

This document has three jobs, and they pull in slightly different directions, so it's worth being explicit about them up front:

1. **Textbook.** Part I assumes you know how to build software but have never had to think hard about HTTP caching specifically. It teaches the vocabulary and mechanics from first principles, so that Parts II–VII read as applications of that vocabulary rather than a wall of new jargon.
2. **Decision record.** Parts II–IV are the research trail: what already exists, why it isn't enough, and exactly which failure modes this project is answerable for. This is the part you point to later when someone (including future-you) asks "why does this exist when Varnish/Souin/Caddy already do caching?"
3. **Seed for what comes next.** This is not the technical spec. It's the document the technical spec, the storage interface RFC, and the Claude Code planning docs all get derived from. Section 11 lists exactly what those follow-on documents are and what each one owes the reader.

If you're re-reading this in six months with the project half-built, Parts I and II will feel unnecessary — skip to Part IV (the taxonomy) and Part V (the architecture), which are the load-bearing parts.

---

## 1. Naming (open item)

No name is fixed yet. Candidates, all built around the flood/water-control metaphor that the failure-mode taxonomy in Part IV makes literal (this project exists because caches "flood," "stampede," and get "poisoned" — the vocabulary already wants a water metaphor):

| Name | Rationale | Watch-outs |
|---|---|---|
| **Weir** | A weir is a low dam that regulates flow rate without stopping it entirely — exactly what request coalescing and rate-aware origin protection do. Short, pronounceable, no obvious collision in the Go proxy space. | Homophone of "weird" in some accents; check `pkg.go.dev` and GitHub before committing. |
| **Sluice** | A sluice gate controls water release under pressure — maps well to controlled cache-refresh under load. | Slightly harder to say/spell than Weir. |
| **Levee** | A levee holds back a flood entirely. Strong metaphor for stampede/flood protection specifically, less so for the caching half. | "Levee" is a common word; likely GitHub name collisions. |
| **Cistern** | An underground reservoir that stores water safely for controlled release — good caching metaphor, weaker flood-protection metaphor. | Less evocative of the *resilience* angle. |
| **Firebreak** | Inverts the metaphor (fire, not flood) but the concept — a deliberate gap that stops cascading spread — maps precisely onto circuit-breaking and bulkheading. | Mixed metaphor if paired with the flood language elsewhere. |
| **Aquifer** | A natural underground reservoir that buffers supply against demand spikes over long timescales. Evocative, memorable, thematically strong for "cache that survives outages." | Longer, and "Aquifer" has been used as a product name in unrelated spaces — check for collisions. |

**Recommendation:** Weir, if it's free on GitHub and `pkg.go.dev`. It's short, the metaphor holds up under scrutiny (regulates flow, doesn't just block it — which is exactly what coalescing and jitter do), and it doesn't overload a common English word the way Levee or Cistern do.

This document uses **Weir** as a working placeholder. Every occurrence should be treated as find-and-replace once a name is locked in.

---

## 2. Why this project exists

### 2.1 The origin story, stated precisely

At a previous job, Ashwin worked on a Go rewrite of a Node/TypeScript reverse-proxy engine sitting in front of a B2B SaaS website/funnel builder. That engine did three things at once: device fingerprinting to control split-test delivery and analytics attribution, request routing, and response caching. The caching side had three specific, named weaknesses:

- No mechanism to survive a **hard cache reset** gracefully (a full flush meant every subsequent request became a cold miss, all at once).
- No defense against **flooding** — many requests arriving faster than the origin could regenerate cache entries.
- No **TTL jitter** — cache entries set in a batch expired in a batch, recreating the same stampede on a schedule instead of once.

Those three gaps are not exotic. They are, as Part III shows, still open gaps in most of the software people actually run in production today. This project exists to close them properly, once, as reusable infrastructure — partly because Ashwin wants to actually understand this space rather than route around it, and partly because a genuine gap seems to exist.

### 2.2 What "done" looks like

A Go library that, given a request and a way to reach an origin, makes cache decisions that are correct under RFC 7234, coalesce duplicate work, survive origin and storage outages without amplifying them, resist adversarial cache-key manipulation, and never let a synchronized expiry or a bulk purge turn into a backend-crushing stampede — regardless of whether that stampede is caused by bad luck, a deploy, or a deliberate attacker. Wrapped first as a Caddy module, because Caddy already solves TLS and transport, and because it puts this project in the same runtime as the separate BYOD custom-domain project, which also sits on Caddy.

---

## 3. Part I — Foundations (read this if caching is new to you)

### 3.1 What a reverse proxy actually does

A **reverse proxy** sits in front of one or more backend servers ("origins") and intercepts requests before they reach them. Clients think they're talking to the origin; they're actually talking to the proxy, which decides whether to forward the request, transform it, or answer it directly. Load balancing, TLS termination, request routing, and — the subject of this document — caching, are all things a reverse proxy is positioned to do because it sees every request before the origin does.

### 3.2 What "caching" means at this layer

An HTTP cache stores a copy of a response and, on a later matching request, serves that copy instead of asking the origin to regenerate it. The entire discipline reduces to two questions, asked on every request:

1. **Does a stored response exist that matches this request?** (the **cache key** question)
2. **If one exists, is it still usable?** (the **freshness** question)

Everything else in this document is elaboration on how to answer those two questions correctly, cheaply, and safely.

### 3.3 Cache keys

A cache key is the subset of a request's attributes the cache uses to decide "is this the same request as one I've already answered?" Naively, a cache key might be just the URL. In practice it usually needs to include (or explicitly exclude) the HTTP method, the `Host` header, specific query parameters, and — critically — any header the response's content actually varies on. That last part is what the `Vary` response header exists to declare: `Vary: Accept-Encoding` tells the cache "responses differ by encoding, so key on that header too, or you'll serve gzipped content to a client that didn't ask for it."

Anything **not** included in the key is called **unkeyed**. Unkeyed inputs are, as Part IV.7 covers in depth, the single most common source of cache-layer security vulnerabilities.

### 3.4 Freshness: TTL, `Cache-Control`, and validation

RFC 7234 (HTTP Caching) defines the vocabulary here, and it's worth knowing the actual directives rather than reinventing informal versions of them:

- **`max-age=N`** — the response is fresh for N seconds from when it was generated. This is the origin's `Cache-Control` header, honored by any HTTP cache including browsers.
- **`s-maxage=N`** — like `max-age`, but only shared caches (proxies, CDNs) honor it; browsers ignore it. Useful when you want the proxy to cache longer or shorter than the end-user's browser does.
- **`no-cache`** — confusingly, this does *not* mean "don't cache." It means "cache this, but revalidate with the origin before serving it every time" (see conditional requests, below).
- **`no-store`** — this is the actual "don't cache" directive.
- **`must-revalidate`** — once stale, the cache must not serve this response without successfully revalidating; if the origin can't be reached, serve an error rather than stale content.
- **`stale-while-revalidate=N`** — for N seconds after becoming stale, the cache may serve the stale copy immediately while fetching a fresh one in the background. This is the single most important directive for this project, because it's the mechanism that turns "the cache expired" from a synchronous latency hit into an invisible background operation.
- **`stale-if-error=N`** — for N seconds after becoming stale, if the origin is unreachable or errors, serve the stale copy instead of propagating the failure. This is the mechanism that turns an origin outage into degraded-but-functional service instead of a cascading failure.

**Conditional requests** are the other half of freshness. When a cached response is stale but the cache wants to check whether it's still valid without re-fetching the whole body, it sends the origin a request with `If-None-Match` (matched against an `ETag` the origin previously supplied) or `If-Modified-Since`. If nothing changed, the origin replies `304 Not Modified` with no body, and the cache just extends the freshness window on its existing copy. This is cheap and worth supporting from day one — the whole point of caching is avoiding unnecessary work, and conditional requests avoid unnecessary *bytes* even on a cache miss for freshness.

### 3.5 The failure this project is actually about: stampedes

A **cache stampede** (also called the **thundering herd problem**) happens when a cache entry becomes unusable — through expiry, invalidation, or a cold start — while many concurrent requests want it. Naively, every one of those requests independently discovers the miss and independently asks the origin to regenerate the value. If the origin was sized to handle the trickle of traffic that normally causes cache misses, it now receives a synchronized burst instead, and can fall over.

The textbook single-object case is solved by **request coalescing** (sometimes called **single-flight**): the first request to notice a miss becomes the "leader," fetches from the origin, and every other concurrent request for the *same key* is held ("collapsed") and given a copy of the leader's result once it lands, rather than each issuing its own origin request. Both Varnish and Go's `singleflight` package implement variants of this.

But — and this is the point where most explanations of stampedes stop short and this project doesn't get to — coalescing only helps when the herd wants the *same* key. It does nothing when hundreds of *different* URLs expire, get purged, or go cold at the same moment, because coalescing collapses duplicates of one key; it can't collapse a burst across many distinct keys. That case needs a different set of tools entirely, covered exhaustively in Part IV.

---

## 4. Part II — Prior art (what already exists, researched adversarially)

This section is deliberately skeptical. The goal going in was to find reasons *not* to build this — to find something that already does the job — and report honestly on what was actually found.

### 4.1 The general-purpose reverse proxy layer

As of mid-to-late 2026, the mainstream options are all actively maintained: nginx (mainline 1.31.4 / stable 1.30.4), HAProxy (3.4.3), Envoy (1.39 series), Caddy (2.11.4), and Traefik (3.7.11). Notably, in February 2024 nginx's own security lead forked the project as **freenginx** after a governance dispute with F5 over how a security release was handled — a reminder that even mature, boring infrastructure carries organizational risk.

These tools split cleanly into two architectural families:

- **Servers you configure** — nginx, HAProxy, Caddy, Traefik. You describe desired behavior in a config file or via labels; the software executes it. None of them are meant to be programmed against for custom request-handling logic beyond what their plugin systems expose.
- **Frameworks you program** — Cloudflare's **Pingora** (Rust) is the clearest example. It's not a finished proxy; it's a library exposing a `ProxyHttp` trait that you implement to get a proxy doing exactly what you need. Cloudflare's own published numbers from replacing their Nginx-based edge with it: 70% less CPU, 67% less memory, and 80ms lower p95 time-to-first-byte, at over a trillion requests/day.

A production case study (Tako, a deployment platform) documented picking Pingora over Caddy and Traefik for precisely the reason this project cares about: their proxy needed to be a first-class participant in application lifecycle logic — not just forward HTTP, but make decisions that require deep integration with process state. That's the same shape of problem as cache-resilience logic that needs to reason about origin health, not just route bytes.

**Conclusion:** none of the general-purpose servers expose the kind of programmable cache-decision surface this project needs. Pingora proves the "framework, not server" model works, but committing to Rust and to owning the full request path is more scope than this project should take on for Phase 1 (see Part V.1 for the reasoning).

### 4.2 Purpose-built HTTP caching layers

- **Varnish** — the canonical caching reverse proxy. Has native request coalescing ("busy object" sleep/wake) and **grace mode** (serve stale while refetching). Its own documentation and third-party incident writeups are explicit that coalescing does not help against multi-URL stampedes — only against duplicate requests for one URL.
- **Apache Traffic Server (ATS)** — a mature, CDN-grade caching proxy. In 2024, ATS shipped **CVE-2024-35296**: a malformed `Accept-Encoding` header could break cache-lookup matching, forcing every request through to the origin and enabling a denial-of-service. This is the single most important data point in this entire research pass: *a caching proxy, mature enough to power large-scale CDN deployments, shipped a header-validation bug that turned cache lookups into an origin-overload vector.* This isn't a hypothetical risk this project is inventing — it's a demonstrated failure in best-in-class software, fixed only in versions 8.1.11 / 9.2.5.
- **`pingora-cache`** — Pingora's own in-memory cache module: LRU eviction plus "cache-lock" (its name for coalescing). A solid primitive, but explicitly a building block, not a policy layer — you still have to decide TTL strategy, staleness handling, and abuse resistance yourself.
- **Souin** (`darkweak/souin`, Go, 985 GitHub stars, actively released — v1.7.8 shipped September 2025) is the most complete general-purpose answer found. It's RFC 7234-compliant, supports request coalescing, `stale-*` directives, surrogate-key invalidation (Fastly/Akamai-style tag purging), and plugs into eleven-plus Go web frameworks plus Caddy, Traefik, and Tyk directly. It is genuinely excellent engineering.

**What Souin does not have**, based on its published configuration surface: no TTL jitter (nothing randomizes expiry to desynchronize a batch of keys set together), no explicit flood/abuse-rate awareness tied to caching decisions, and no documented hardening specifically against cache-key manipulation of the kind that produced ATS's CVE. It solves RFC-correctness and same-key coalescing thoroughly. It does not solve cross-key stampedes, adversarial cache-busting, or storage-backend-outage degradation.

### 4.3 The academic and security research on cache-layer denial-of-service

This is worth its own subsection because it's not folklore — it's documented, named, and still actively exploited:

- **Ferretti & Ghini, "Mitigation of Random Query String DoS via Gossip"** (2012) formally describes the **random query-string attack**: an attacker appends a novel, never-seen query string to a cacheable URL, forcing every CDN edge node to treat it as a fresh miss and forward it to the origin. Enough distinct attacker-generated query strings, spread across enough edge nodes, and the origin drowns in "cache misses" that were never legitimate traffic to begin with.
- **James Kettle (PortSwigger), "Responsible Denial of Service with Web Cache Poisoning"** documents taking down production websites with a *single crafted request*, by exploiting unkeyed inputs (a malformed `Accept-Encoding` value, an unexpected `Origin` header) to either poison a cached response with an error page or force a permanent cache-bypass condition. One of his examples used Burp Repeater's default user-agent to accidentally overwrite a live cached homepage with a "please upgrade your browser" page for every subsequent visitor.
- **Cache poisoning via unkeyed inputs** generally (PortSwigger's own taxonomy, and tools like the Web Cache Vulnerability Scanner) catalogs nine distinct poisoning techniques and three deception techniques, all rooted in the same underlying issue: something the origin's response depends on that the cache's key generation ignores.

**Conclusion for this project:** cache-key construction cannot be treated as an implementation detail. It has to be treated as a hardened, adversarial-input security boundary from the first line of code — the equivalent of treating SQL query construction as an injection risk rather than a string-concatenation convenience. This becomes a first-class design principle in Part V.3.

### 4.4 The adjacent, already-decided BYOD project

Ashwin's separate custom-domain control-plane project already established that the multi-tenant BYOD-domain space has thin but real open-source prior art (`ericls/certmatic`, `avashForReal/caddy-control`) sitting alongside mature commercial competitors (Approximated, SaaS Custom Domains), all built on Caddy's `on_demand_tls` "ask" endpoint pattern. That research isn't repeated here, but it's the reason Caddy is the natural first integration target for *this* project too: the same Caddy instance can eventually run both the custom-domain control plane and this cache-resilience engine, which was part of the original job's engine in the first place.

### 4.5 Fingerprinting and split-test routing (relevant to the Phase 2 appendix, Part 10)

Two unrelated markets both use the word "fingerprinting" at the proxy layer, and it matters not to conflate them:

- **Bot/scraper defense** — Anubis (`TecharoHQ`, ~20k GitHub stars, MIT, Go) issues a proof-of-work challenge before serving requests, and computes JA4H TLS/HTTP fingerprints (lazily, only when a policy references them, to keep the hot path fast). `go-away` is a newer, more configurable competitor in the same space. Both exist to say *no* to traffic.
- **Legitimate experiment bucketing** — Statsig, GrowthBook, LaunchDarkly, and Optimizely all document the same pattern: run the experiment-assignment SDK inside a CDN edge function (Cloudflare Workers, Fastly Compute, Akamai EdgeWorkers), fold the resulting variant ID into the cache key *before* the cache lookup, so A/B testing doesn't destroy cache-hit ratio. This is architecturally mature — but every implementation found is tied to a proprietary vendor SDK. No open-source reverse-proxy-layer equivalent exists.

This is the actual shape of Ashwin's original job's problem, and it's why Part 10 treats it as "a cache-key dimension," not a separate subsystem, once the engine's key-construction boundary is solid.

---

## 5. Part III — The gap, stated as a thesis

No individual technique in this document is novel. Jitter, coalescing, stale-while-revalidate, circuit breaking, and hardened cache-key construction are all independently well-understood, and most have at least one solid open-source implementation somewhere. What doesn't exist, based on everything found in Part II, is **all of them, in one coherent, opinionated engine, at the reverse-proxy layer, open source, designed against a named adversarial threat model from day one rather than bolted on after an incident.**

Concretely:

- Varnish solves single-key coalescing and grace-mode staleness, but is a large C codebase with its own configuration language (VCL), not something you extend in Go, and doesn't address cross-key stampedes or adversarial key construction as a first-class concern.
- Souin solves RFC-correctness and coalescing comprehensively, in Go, pluggable into everything — but has no jitter, no documented abuse-rate awareness, and no hardening story against the exact class of bug that produced CVE-2024-35296 in ATS.
- `pingora-cache` gives you primitives, not a policy layer, and commits you to Rust and to owning the whole proxy.
- Nothing surveyed treats cache-key construction as a security boundary the way this project's Part V.3 principle demands.

That combination — desynchronized expiry, cross-key stampede protection, storage/origin-outage degradation, and adversarial-hardened key construction, together, in Go, as a library first — is the actual gap. It is narrow. It is also exactly wide enough to be a real, finishable, well-scoped project, which is the better property for a from-scratch build than a wide, vague one.

---

## 6. Part IV — Failure-mode taxonomy

This is the specification, in the sense that every mode listed here needs an explicit answer somewhere in the engine before Phase 1 is "done." Each entry gives the mechanism, the evidence it's real, the mitigation this project commits to, and how to test for it.

### 6.1 Synchronized TTL expiry

**Mechanism:** Many cache entries written around the same time (a bulk warm, a deploy, a batch job) share the same TTL and therefore the same expiry instant. When it arrives, they all become misses simultaneously, even with no traffic spike at all — the expiry schedule itself creates the stampede.

**Mitigation:** TTL jitter — store each entry's effective TTL as `base_ttl + random(0, jitter_window)`. This is free (no added latency, trivial CPU cost) and should be the default, not an opt-in.

**Test strategy:** Write N entries with the same base TTL in a tight loop; assert their expiry timestamps are spread across the jitter window, not clustered.

### 6.2 Hot-key stampede

**Mechanism:** One popular key expires or is invalidated while many concurrent requests want it. Each independently misses and fetches, multiplying origin load by the concurrency of the herd.

**Mitigation:** Request coalescing / single-flight: the first miss becomes the leader; concurrent requests for the same key wait on the leader's result rather than issuing their own fetch.

**Failure mode within the mitigation (6.2a — lock starvation):** if the leader's fetch hangs (slow origin, network partition), every follower waits indefinitely, and a single stuck request has now stalled the entire herd instead of just itself. **Mitigation:** the coalescing lock must carry a timeout; on timeout, followers fall back to serving stale content if available, or to issuing their own bounded, independent fetch if not — never to waiting forever.

**Test strategy:** Fire 1,000 concurrent requests for a single cold key against a slow/instrumented origin; assert exactly one origin call was made, and assert followers unblock within the configured lock timeout even if the leader never returns.

### 6.3 Cross-key ("cardinality") stampede

**Mechanism:** Coalescing collapses duplicate requests for *one* key. It does nothing when a deploy, a bulk purge, or a cold cache start causes hundreds or thousands of *distinct* keys to become misses at once — the number of concurrent origin fetches equals the cardinality of the expiring set, not the request concurrency. This is explicitly the case Varnish's own documentation says coalescing cannot help with.

**Mitigation:** a **backend concurrency limiter** (a bulkhead / semaphore) independent of the cache layer, capping how many simultaneous origin fetches are in flight *regardless of how many distinct keys are asking*, with excess requests queued (bounded) or shed with a `stale-if-error`-style fallback rather than forwarded uncapped.

**Test strategy:** Invalidate 5,000 distinct keys simultaneously under load; assert concurrent origin fetches never exceed the configured limit, and assert queued/shed requests still resolve (from stale cache or a bounded wait) rather than timing out uncontrolled.

### 6.4 Cold full flush / restart

**Mechanism:** Cache process restarts, a node fails over, or an operator issues a full flush. Every subsequent request is a miss, and 6.3's mitigation alone still means every one of those misses queues for an origin slot — a legitimate but painful version of the same problem, self-inflicted rather than adversarial.

**Mitigation:** the concurrency limiter from 6.3 applies here too, but should be paired with an optional **warm-start** path — the engine can be handed a pre-population source (a snapshot, or a "replay the last N minutes of traffic against a cold cache before flipping it live") — deferred to a later phase, but the *interface* for it should exist from the start so it isn't bolted on.

### 6.5 Cache storage backend outage

**Mechanism:** If the cache store itself (in Phase 1, the in-process map; later, Redis/Valkey) becomes unavailable or slow, a naive implementation either blocks every request on a failing dependency, or fails open and forwards *every* request to the origin uncapped — which is 6.3's cross-key stampede, just triggered by infrastructure failure instead of a deploy.

**Mitigation:** the storage interface (Part V.2) must expose a fast, bounded-timeout failure signal, and the engine must treat "storage unavailable" as a first-class state that still routes through the 6.3 concurrency limiter — never as an implicit "just hit the origin" bypass.

### 6.6 Origin outage or degradation

**Mechanism:** The backend the engine is protecting is itself down or slow. Every miss now waits on (or fails against) a broken dependency.

**Mitigation:** `stale-if-error` semantics — if a fresh fetch fails and a stale copy exists, serve it (optionally with a response header indicating staleness, for observability) rather than propagating the failure. Paired with a **circuit breaker**: after a threshold of consecutive origin failures, stop sending new fetches for a cooldown window and serve stale-or-error immediately, rather than letting every new request pay the full timeout cost of a backend that's already known to be down.

**Test strategy:** Kill the mock origin mid-test; assert stale content is served for in-window keys, assert the circuit opens after N consecutive failures, and assert it attempts a "half-open" probe request after the cooldown rather than staying open forever.

### 6.7 Cache poisoning via unkeyed inputs

**Mechanism:** Documented exhaustively in Part II.3 (Kettle, PortSwigger). If a response varies based on some request attribute (a header, a cookie, a fragment of the URL) that the cache key doesn't include, an attacker can craft a request that causes the origin to generate a malicious or broken response, which the cache then serves to every subsequent legitimate user who shares that (incomplete) key.

**Mitigation:** the cache-key builder defaults to **deny-by-default** on header/cookie inclusion — nothing gets into the key unless explicitly declared, but more importantly, anything the origin's response is known to vary on (via `Vary`) is *mandatorily* folded into the key, with no override that silently drops it. The engine should also support an explicit **strict mode** that refuses to cache a response at all if its `Vary` header names something the engine wasn't configured to key on, rather than silently caching an incomplete key.

**Test strategy:** Serve responses with `Vary: X-Custom-Header` from the mock origin without configuring the engine to key on it; assert the engine either keys on it automatically or refuses to cache the response — it must never silently cache under an incomplete key.

### 6.8 Cache-busting denial of service

**Mechanism:** Formally described by Ferretti & Ghini (2012) and demonstrated at bug-bounty scale by Kettle: an attacker appends novel query strings (or otherwise-unkeyed-but-forwarded parameters) to a cacheable URL, guaranteeing a miss on every request while looking, to a naive cache, like ordinary traffic hitting ordinary misses.

**Mitigation:** this is where 6.3's concurrency limiter and 6.7's strict key discipline compose: even a flood of guaranteed-miss requests can only ever consume as many origin slots as the bulkhead allows. On top of that, the engine should support an explicit **normalized-key rate signal** — tracking miss rate *per URL path, ignoring the busting parameter*, when the operator explicitly configures certain parameters as "known to be excluded from the key" — so a spike in misses against the same normalized path can be flagged or throttled even though every individual request technically misses "legitimately."

**Test strategy:** Simulate 10,000 requests to the same path with randomized irrelevant query strings; assert origin load stays bounded by the concurrency limiter, and assert the normalized-path miss-rate metric spikes (for alerting), even though every individual key is technically unique.

### 6.9 Malformed-input cache bypass (the ATS CVE class)

**Mechanism:** CVE-2024-35296, concretely: a malformed `Accept-Encoding` header broke ATS's cache-lookup matching, causing every affected request to bypass the cache and hit the origin — a direct, demonstrated denial-of-service vector in mature software.

**Mitigation:** every header or input that participates in key construction must be **validated and normalized before use**, with a defined, tested fallback for malformed values (treat as absent, treat as a specific canonical bucket — never "pass the malformed value through and hope the matching logic degrades gracefully"). This is a direct, named lesson from a real CVE, not a hypothetical.

**Test strategy:** Fuzz every header the key builder consumes with malformed/edge-case values (empty, oversized, non-ASCII, duplicate headers with conflicting values); assert the engine never bypasses the cache as a result, and never panics.

### 6.10 Negative-caching gap

**Mechanism:** If the origin returns an error (5xx) and the engine doesn't cache that fact, every subsequent request retries the origin individually — during an origin outage, this is 6.6's problem again, but specifically for the "first few seconds after failure starts" window before the circuit breaker has seen enough failures to open.

**Mitigation:** short-TTL **negative caching** for defined error classes — cache a 502/503 for a few seconds so a burst of requests arriving in the same instant doesn't each independently retry a backend that just failed.

### 6.11 Eviction storms

**Mechanism:** Under memory pressure, an LRU (or similar) eviction policy can cascade — evicting entries that are about to be requested again, causing more misses, causing more memory pressure from newly-fetched entries, in a feedback loop that collapses hit rate exactly when the cache is needed most.

**Mitigation:** Phase 1 (in-memory, single-node) should use a sized, sharded LRU with admission control awareness (a newly-fetched entry doesn't necessarily evict a frequently-hit one just because it's more recent) rather than pure recency-based eviction. Full tiered storage is out of scope for Phase 1 but the storage interface (Part V.2) must not assume a single eviction policy.

### 6.12 Purge-triggered thundering herd

**Mechanism:** A tag-based or surrogate-key purge (invalidate everything tagged `product:123`) can invalidate a large key set atomically, recreating 6.3's cross-key stampede as a direct consequence of an intentional, correct cache-management operation.

**Mitigation:** support **soft purge** (mark entries stale but servable, rather than deleting them outright) as the default purge semantics, with hard/immediate purge as an explicit, named opt-in for cases that genuinely require it (legal takedown, security incident).

### 6.13 Distributed consistency during rolling deploys (forward-looking, Phase 1.5+)

**Mechanism:** Once storage is distributed (post-Phase-1), a rolling deploy can mean requests are served by a mix of old and new backend code simultaneously, and a cache entry generated by the old version may be served to a client interacting with the new version, or vice versa.

**Mitigation (deferred, noted for the storage-interface spec):** versioned or generation-tagged cache keys, so a deploy can be configured to bump a generation marker and treat the previous generation's entries as stale without a disruptive full flush.

---

## 7. Part V — Architecture

### 7.1 Why a library first, not a standalone proxy

Repeating the core argument from the chat discussion, stated here for the record because it's the single most consequential architectural decision in this document: a standalone proxy means months spent on HTTP/1.1, HTTP/2, HTTP/3, TLS termination, and connection pooling — all solved problems, none of them what this project is actually about. Caddy already does all of that correctly and is already the target runtime for the separate BYOD project. Building Weir as a Go library with no Caddy-specific types in its core, and writing a Caddy module as the first thin adapter, means Phase 1 effort goes entirely into the taxonomy in Part IV rather than into re-solving transport.

### 7.2 Core interfaces (sketch — the technical spec owns the real signatures)

```go
// Engine is the entry point. It knows nothing about Caddy, HTTP transport,
// or TLS — it operates on a normalized request/response abstraction so it
// can be adapter-wrapped for Caddy today and anything else later.
type Engine interface {
    // Decide inspects a request, consults the store, and returns a
    // Decision describing what the adapter should do next.
    Decide(ctx context.Context, req *NormalizedRequest) (Decision, error)

    // Complete is called by the adapter once an origin fetch resolves
    // (success or failure), so the engine can update storage, release
    // any coalescing lock, and update circuit-breaker state.
    Complete(ctx context.Context, req *NormalizedRequest, result FetchResult) error
}

// Decision tells the adapter what to do: serve from cache, serve stale,
// become the coalescing leader and fetch, wait on an in-flight leader,
// or reject/shed the request under load-shedding policy.
type Decision struct {
    Action       DecisionAction // ServeCached | ServeStale | Fetch | Wait | Shed
    CachedEntry  *Entry         // populated for ServeCached / ServeStale
    WaitOn       <-chan FetchResult // populated for Wait
}

// Store is the pluggable storage boundary (Part V.2 / 6.5). Phase 1 ships
// exactly one implementation: an in-process sharded LRU. Nothing above
// this interface may assume in-process semantics, so a Redis/Valkey
// implementation is a Phase 1.5 addition, not a rewrite.
type Store interface {
    Get(ctx context.Context, key Key) (*Entry, error)
    Set(ctx context.Context, key Key, entry *Entry, ttl time.Duration) error
    Delete(ctx context.Context, key Key) error
    // Store implementations must return a distinguishable error for
    // "unavailable" vs "not found" — see failure mode 6.5.
}

// KeyBuilder is the hardened boundary from failure modes 6.7-6.9. It is
// deny-by-default: nothing enters the key without being named, and it is
// mandatory-inclusive of anything the origin's Vary header names.
type KeyBuilder interface {
    Build(req *NormalizedRequest, varyHeaders []string) (Key, error)
}
```

The point of sketching this now, before the technical spec exists, is to fix the *shape* of the boundaries: `Engine` never touches raw `net/http` types, `Store` never assumes in-process semantics, and `KeyBuilder` is a named, separately-testable component rather than a helper function buried inside request handling. Getting these three boundaries right is most of what makes the Caddy adapter (7.4) thin instead of tangled.

### 7.3 Design principle: the cache key is a security boundary

Stated as a standing rule, not a suggestion: any code that reads a header, cookie, or query parameter and feeds it into key construction is security-sensitive code, reviewed and tested with the same seriousness as code that constructs a SQL query or a shell command. Part IV.7–IV.9 exist because every mature caching system surveyed in Part II treats this as an implementation detail somewhere, and it produces real, exploitable, sometimes CVE-worthy bugs when it does.

### 7.4 The Caddy adapter

A thin `xcaddy`-buildable module translating Caddy's request/response types into `NormalizedRequest`/`NormalizedResponse`, calling `Engine.Decide`, and executing the returned `Decision` (serve from its own store of the cached body, proxy through to the configured upstream on `Fetch`, block on the `Wait` channel, or return a shed response). The adapter owns nothing about caching *policy* — it's a translation layer, which is the entire point of keeping `Engine` Caddy-agnostic.

### 7.5 Observability from day one

Every `Decision` and every failure-mode trigger from Part IV should be a labeled metric and a structured log line, not an afterthought bolted on once something breaks in production. Specifically: coalescing hit/miss counts, circuit-breaker state transitions, concurrency-limiter queue depth and shed count, stale-serve counts (split by `stale-while-revalidate` vs `stale-if-error`), and key-builder rejections (6.7/6.9). This isn't scope creep — a resilience engine you can't observe under load is not meaningfully more trustworthy than one with no resilience features at all.

---

## 8. Part VI — Explicit non-goals

Worth stating plainly, because scope creep is the most likely way this project stalls:

- **Not a general-purpose reverse proxy.** It does not replace Caddy, nginx, or Traefik. It plugs into one.
- **Not a WAF or bot-detection tool.** Anubis and go-away already do proof-of-work/fingerprint-based bot gating well; this project's fingerprinting use (Part 10) is about routing legitimate traffic to experiment variants, not about saying no to traffic.
- **Not a CDN.** No edge-node distribution, no anycast, no geographic routing. Single-origin, single-deployment-target resilience.
- **Not initially distributed.** Phase 1 storage is in-process. Distributed storage (Redis/Valkey) is a deliberately deferred Phase 1.5, designed for from the interface level but not built until Phase 1's single-node behavior is proven correct under the full Part IV taxonomy.

---

## 9. Part VII — Phased roadmap

**Phase 0 — Skeleton.** Repo, module path, license (MIT), CI, the three core interfaces from 7.2 with no real logic behind them, a fake in-memory `Store`, and a test harness with a controllable `Clock` and a mock origin that can be told to be slow, to fail, or to return arbitrary headers.

**Phase 1 — The engine, failure mode by failure mode.** Each milestone below corresponds directly to a Part IV entry and should ship with the test strategy described there:

- **M1** — Correct RFC 7234 freshness (3.4) and basic key construction (3.3) with no adversarial hardening yet — get the boring case right first.
- **M2** — Request coalescing + lock-timeout fallback (6.2, 6.2a).
- **M3** — TTL jitter (6.1).
- **M4** — Backend concurrency limiter / bulkhead (6.3), reused for cold-start (6.4) and storage-outage (6.5) cases.
- **M5** — `stale-while-revalidate` and `stale-if-error` plus circuit breaker (6.6).
- **M6** — Negative caching (6.10).
- **M7** — Hardened `KeyBuilder`: deny-by-default, mandatory `Vary` inclusion, strict mode, header fuzz-testing (6.7, 6.9).
- **M8** — Normalized-path miss-rate signal for cache-busting detection (6.8).
- **M9** — Soft-purge-by-default semantics (6.12).
- **M10** — Observability (7.5) across everything above — not deferred to the end, but formalized once all the signals actually exist.

**Phase 1.5 — Storage pluggability proven.** A second `Store` implementation (Redis/Valkey) built strictly against the existing interface, specifically to validate that no Phase 1 assumption leaked in-process semantics into `Engine`. Eviction-storm hardening (6.11) belongs here once there's a real distributed store to reason about.

**Phase 2 — Caddy adapter (7.4).** Only after Phase 1 is stable enough to trust in a real request path.

**Phase 3 — Experiment-aware routing.** See Part X (appendix).

---

## 10. Appendix: Phase 2 — Experiment-aware routing

Kept as an appendix, not a co-equal section, deliberately: this phase should not start until the `KeyBuilder` boundary from 7.2/7.3 is proven solid, because this phase's entire design is "the experiment variant is just another mandatory key dimension."

**The pattern, borrowed from Statsig/GrowthBook's edge-function architecture (Part II.5):** a request arrives, a deterministic function maps some stable identifier (a fingerprint, a cookie-based visitor ID) to an experiment variant *before* the cache lookup happens, and that variant ID is folded into the cache key the same way a `Vary` header would be. The origin never needs to be involved in bucketing at all for cached content — the proxy layer owns the decision, and the cache naturally partitions by variant.

**What's different from Statsig/GrowthBook:** those are proprietary SDKs syncing config to a vendor-controlled edge runtime. This would be the same pattern, open source, running inside the same Caddy instance already doing caching and custom-domain routing — no external service dependency for the assignment decision itself.

**Deliberately unresolved here, to be answered in a future spec once Phase 1 ships:** how experiment configuration is authored and distributed (a config file? an admin API, echoing the BYOD project's control-plane shape?), what identifier is used for bucketing (a first-party cookie is simpler and more reliable than passive device fingerprinting, and worth defaulting to unless there's a concrete reason fingerprinting is required), and how bucketing interacts with the `stale-while-revalidate` path (a stale response cached under variant A should never be served to a visitor freshly bucketed into variant B).

---

## 11. What documents come next

This document is the seed. In order, the next artifacts should be:

1. **`01-technical-spec.md`** — the real Go interfaces (not the sketch in 7.2), package layout, and the exact `Decision`/`Entry`/`Key` types, with enough detail that Claude Code (or any contributor) can start implementing Phase 0 directly from it.
2. **`02-storage-interface-spec.md`** — the `Store` contract in full, including the exact error-distinguishing behavior required by 6.5, written *before* Phase 1.5's Redis implementation so the interface is designed against a second implementation on paper, not just imagined.
3. **`03-testing-strategy.md`** — formalizes the per-failure-mode test strategies scattered through Part IV into an actual test plan: what needs a real concurrent-load test versus a unit test, what the mock origin's fault-injection API looks like, and what "Phase 1 is done" means in terms of coverage against the taxonomy.
4. **`04-caddy-adapter-spec.md`** — written once Phase 1 is stable, detailing the `xcaddy` module structure and the Caddyfile/JSON configuration surface.
5. **A `CLAUDE.md` planning doc** — repo conventions, the milestone list from Part VII as literal tracked tasks, and pointers back to the relevant Part IV entry for each, so an agent picking up any milestone has the "why" one link away from the "what."

---

## 12. Glossary

- **Reverse proxy** — a server that sits in front of one or more backend origins and mediates client requests to them.
- **Cache key** — the subset of a request's attributes used to determine whether two requests should receive the same cached response.
- **Unkeyed input** — any request attribute the origin's response depends on that is *not* part of the cache key; the root cause of most cache poisoning.
- **TTL (time-to-live)** — how long a cached entry is considered fresh before it must be revalidated or refetched.
- **Jitter** — deliberate randomization added to a TTL so that entries set together don't expire together.
- **Cache stampede / thundering herd** — many requests simultaneously discovering a cache miss for the same or related keys, overwhelming the origin.
- **Request coalescing / single-flight** — collapsing concurrent requests for the same key into one origin fetch, sharing the result.
- **Grace mode / stale-while-revalidate** — serving a stale cached response immediately while refreshing it in the background.
- **stale-if-error** — serving a stale cached response when a fresh fetch fails, instead of propagating the failure.
- **Circuit breaker** — a mechanism that stops sending requests to a dependency after repeated failures, resuming only after a cooldown and a successful probe.
- **Bulkhead / concurrency limiter** — a hard cap on concurrent operations against a shared resource, preventing one overloaded path from exhausting it entirely.
- **Negative caching** — caching the fact that a request failed (e.g., a 502), briefly, to avoid repeated identical failures.
- **Surrogate key / cache tag** — a label attached to cached entries so a group of related entries can be invalidated together.
- **Soft purge** — marking cached entries stale-but-servable rather than deleting them outright.
- **Admission control** — deciding whether a new entry is allowed into a cache (or an eviction is allowed to proceed) based on more than pure recency.
- **Cache poisoning** — causing a cache to store and serve a malicious or incorrect response by exploiting unkeyed inputs.
- **Cache-busting DoS** — deliberately generating guaranteed-miss requests (e.g., via randomized query strings) to overload the origin behind a cache.

---

## 13. Sources

- Deepak Gupta, "Top 5 Reverse Proxies for 2026" — version floors and the freenginx fork: https://guptadeepak.com/tools/top-5-reverse-proxies-2026/
- Tako, "Pingora vs Caddy vs Traefik: Why We Built on Cloudflare's Proxy": https://tako.sh/blog/pingora-vs-caddy-vs-traefik/
- Cloudflare, Pingora project: https://github.com/cloudflare/pingora
- Netdata, "Varnish cache stampede / thundering herd" guide: https://www.netdata.cloud/guides/varnish/varnish-cache-stampede-thundering-herd/
- `darkweak/souin` — HTTP cache system: https://github.com/darkweak/souin
- SentinelOne, CVE-2024-35296 (Apache Traffic Server): https://www.sentinelone.com/vulnerability-database/cve-2024-35296/
- Ferretti & Ghini, "Mitigation of Random Query String DoS via Gossip" (2012): https://cris.unibo.it/handle/11585/115913
- James Kettle (PortSwigger), "Responsible Denial of Service with Web Cache Poisoning": https://portswigger.net/research/responsible-denial-of-service-with-web-cache-poisoning
- PortSwigger, "Web cache poisoning" reference (Academy)
- Hackmanit, Web Cache Vulnerability Scanner: https://github.com/hackmanit/web-cache-vulnerability-scanner
- `ericls/certmatic` and `avashForReal/caddy-control` (BYOD project prior art, referenced for the shared-runtime rationale in 4.4)
- `TecharoHQ/anubis`: https://github.com/techarohq/anubis and `WeebDataHoarder/go-away`: https://github.com/weebdatahoarder/go-away
- Statsig, "CDN Edge Testing for Cached Resources": https://docs.statsig.com/api/content/guides/cdn-edge-testing
- Caddy, "Automatic HTTPS" / on-demand TLS documentation: https://caddyserver.com/docs/automatic-https

---

*End of design document v0.1. Next: pick a name, confirm the working title throughout, then start `01-technical-spec.md`.*
