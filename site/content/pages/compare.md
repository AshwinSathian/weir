---
title: Compare
description: Where Varnish, nginx, Souin, Pingora, singleflight or a CDN is the better choice, and where Weir is.
---

Most of this page is qualitative. We have not measured speed against any of these projects, so there are no speed claims here. One round of black-box tests against Varnish, nginx and the Caddy cache-handler counted origin calls, failed clients and poisoning verdicts; it is charted [below](#measured-side-by-side). Those measured results are ours alone: we wrote the configurations and ran the tests, and the owners of the other projects have not verified them. Every statement about another project comes from that project's own documentation, linked in each section and read on 2026-10-12. If a claim could not be checked there, it was left out. Corrections are welcome: [open an issue](https://github.com/AshwinSathian/weir/issues/new).

Each technique in Weir exists somewhere else. What Weir adds is the combination in one Go library, with a written threat model and a test for each claim.

## Choose it when

Six alternatives, each with the case for it and the case for Weir. The longer note under each card quotes that project's own documentation.

<div class="cards">
<section class="card">

### Varnish

<p class="kind">A standalone caching proxy, configured in VCL.</p>
<dl><dt>Choose it when</dt><dd>You want a dedicated cache tier you can program, with a long production history.</dd><dt>Choose Weir when</dt><dd>The cache has to live inside a Go server or Caddy, configured with a struct.</dd></dl>

<details>
<summary>What its documentation says, and what each does that the other does not</summary>

Varnish coalesces requests for one object automatically. Its documentation says: "When several clients are requesting the same page Varnish will send one request to the backend", and "In some products this is called request coalescing and Varnish does this automatically." It has `grace` (serve a stale object while fetching a new one) and `keep` (hold an object longer for revalidation), and VCL lets you program the cache's behavior per request. Source: [Grace mode and keep](https://github.com/varnishcache/varnish-cache/blob/master/doc/sphinx/users-guide/vcl-grace.rst).

**What Varnish does that Weir does not.** It is a complete proxy with its own configuration language. You can express logic in VCL that Weir has no setting for. It has been run in production for far longer than Weir has existed.

What Weir does that we could not find in the grace documentation: lifetime jitter, a concurrency budget shared across different keys, and a default that hides unkeyed request headers from the origin. Varnish may offer these through VCL or modules; we only read the page linked above, so treat this as "not built in as far as that page says".

Choose Varnish when you want a separate, programmable cache tier. Choose Weir when you want the cache inside a Go process or Caddy, configured with a struct.

</details>

</section>
<section class="card">

### nginx proxy_cache

<p class="kind">Caching built into the nginx proxy.</p>
<dl><dt>Choose it when</dt><dd>nginx is already your proxy and its cache lock covers your herds.</dd><dt>Choose Weir when</dt><dd>You need jitter, a breaker, a budget across keys or strict forwarding.</dd></dl>

<details>
<summary>What its documentation says, and what each does that the other does not</summary>

nginx has `proxy_cache_lock`: "only one request at a time will be allowed to populate a new cache element", with `proxy_cache_lock_timeout` (5 s by default, after which the request goes to the upstream and its response is not cached) and `proxy_cache_lock_age` (5 s by default). `proxy_cache_use_stale updating` and `proxy_cache_background_update` serve stale content while one request refreshes. Source: [ngx_http_proxy_module](https://nginx.org/en/docs/http/ngx_http_proxy_module.html).

**What nginx does that Weir does not.** It is a full web server and proxy, and caching is one module among many that you may already rely on.

What Weir does that the module page does not describe: the cache lock is per cache element, and the page documents no randomization of cache lifetimes, no circuit breaker and no global limit on concurrent cache-miss requests to the upstream. The default cache key is built from scheme, proxy host and request URI; which request headers reach the upstream is a separate setting you manage. `proxy_cache_purge` is documented as part of the commercial subscription.

Choose nginx when it is already your proxy and same-key locking is the problem you have. Choose Weir when the origin needs protecting across keys, or when you want the forwarded request tied to the key.

</details>

</section>
<section class="card">

### Souin and the Caddy cache-handler

<p class="kind">A Go HTTP cache with plugins for many servers.</p>
<dl><dt>Choose it when</dt><dd>You need its storage backends, ESI or its framework integrations.</dd><dt>Choose Weir when</dt><dd>Origin protection and key hardening matter more than backends.</dd></dl>

<details>
<summary>What its documentation says, and what each does that the other does not</summary>

Souin describes itself as "a new HTTP cache system suitable for every reverse-proxy", and the [Caddy cache-handler](https://github.com/caddyserver/cache-handler) is "a distributed HTTP cache module for Caddy based on Souin". Their READMEs list RFC 7234 compliance, the `Cache-Status` header, targeted cache control (RFC 9213), surrogate-key purging, ESI, request coalescing, storage providers including Badger, etcd, NutsDB, Olric, Otter and Redis, and plugins for more than a dozen Go frameworks and proxies. Source: [Souin README](https://github.com/darkweak/souin).

**What Souin does that Weir does not.** Many storage backends, where Weir has two. ESI. Surrogate keys in the Akamai and Fastly styles. Integrations with Traefik, Tyk, Echo, Gin, Fiber and others, where Weir has `net/http` and Caddy.

What Weir does that those READMEs do not mention: lifetime jitter, a circuit breaker, an origin concurrency limit, and cache-key hardening as a security topic. The cache-handler README documents the cached body size as "unlimited if omitted" and a management API at a default path; Weir's object size is bounded at 1 MiB by default and its purge API exists only on Caddy's admin endpoint.

Choose Souin or cache-handler when you need a backend or an integration Weir lacks, or ESI. Choose Weir when the origin's survival and poisoning resistance are the requirements.

</details>

</section>
<section class="card">

### Pingora

<p class="kind">A Rust framework for building proxies.</p>
<dl><dt>Choose it when</dt><dd>You are building a proxy in Rust.</dd><dt>Choose Weir when</dt><dd>You are working in Go and want cache policy, not a proxy framework.</dd></dl>

<details>
<summary>What its documentation says, and what each does that the other does not</summary>

Pingora is "a Rust framework to build fast, reliable and programmable networked systems". Its README says "Pingora proxy integration with caching should be considered experimental". Source: [Pingora README](https://github.com/cloudflare/pingora).

It is a different kind of thing: a framework for writing a proxy in Rust, where Weir is a policy library for Go that leaves the wire to an adapter. If you are building in Rust, use Pingora. If your stack is Go or Caddy, the comparison ends at the language.

</details>

</section>
<section class="card">

### singleflight and sturdyc

<p class="kind">Go libraries that deduplicate function calls.</p>
<dl><dt>Choose it when</dt><dd>You are caching function or data-source results.</dd><dt>Choose Weir when</dt><dd>You are caching HTTP responses and need HTTP semantics.</dd></dl>

<details>
<summary>What its documentation says, and what each does that the other does not</summary>

`golang.org/x/sync/singleflight` "provides a duplicate function call suppression mechanism": one execution in flight per key, with other callers receiving the same result. Its documentation says nothing about timeouts, panics or storing results. Source: [pkg.go.dev](https://pkg.go.dev/golang.org/x/sync/singleflight). [sturdyc](https://github.com/viccon/sturdyc) is "a caching library with advanced concurrency features" with stampede protection, early refreshes and batching for data-source results.

**What they do that Weir does not.** They cache anything: a database row, an API result, a computed value. sturdyc batches and refreshes records individually. Weir only caches HTTP responses.

What Weir adds for HTTP: `Cache-Control` and `Vary` semantics, validation with 304, a wait bound for followers and an age bound for leaders, recovery of a panicking fetch with the error delivered to every waiter, and the budget across keys. See [Coalescing](/docs/concepts/coalescing/).

Choose singleflight or sturdyc for function results. Choose Weir for HTTP responses.

</details>

</section>
<section class="card">

### CDN request collapsing

<p class="kind">A managed edge feature.</p>
<dl><dt>Choose it when</dt><dd>You want caching run for you, close to users.</dd><dt>Choose Weir when</dt><dd>You need the behavior at your own origin tier, in code you control.</dd></dl>

<details>
<summary>What its documentation says, and what each does that the other does not</summary>

Fastly defines request collapsing as "the practice of combining multiple requests for the same object into a single request to origin", and documents its edge cases: an uncacheable response sends the queued requests one after another unless a hit-for-pass marker exists, which "can create extreme response times of several minutes" in the worst case. Source: [Fastly: request collapsing](https://www.fastly.com/documentation/guides/concepts/edge-state/cache/request-collapsing/).

**What a CDN does that Weir does not.** It runs in many locations close to users, absorbs traffic before it reaches your network, and is operated by someone else. Weir is one process next to your origin.

They are not alternatives so much as layers. A CDN in front and Weir at the origin tier is a reasonable setup: collapsing at the edge is per location, so a purge or a cold start can still arrive at your origin from many locations at once.

</details>

</section>
</div>

## Measured side by side

One round on one machine on 2026-10-13: Varnish 9.1.0, nginx 1.30.5, the Caddy cache-handler v0.17.0 and Weir, as containers behind one origin. These results are based on our own runs and have not been verified by the owners of the other projects.

### Poisoning probes

Does a victim get served what an attacker caused? The origin in these probes has deliberately unsafe routes, and each probe sends an attacker's request and then a victim's. Weir's result comes from its default of hiding unkeyed request headers, cookies and bodies from the origin on cacheable requests.

{{figure results-probes}}

<details>
<summary>Full table and notes, with Weir v1.0.1 and the probes that were not run</summary>

{{include docs/benchmarks.md "### Comparison: security probes"}}

</details>

### cache-tests

How much of the public cache-tests suite does each cache pass in its default tier?

{{figure results-conformance}}

<details>
<summary>Full table and how the suite was run</summary>

{{include docs/benchmarks.md "### Comparison: conformance"}}

</details>

Origin calls and failed clients under herds, expiry and outages are on the [benchmarks page](/benchmarks/#against-other-caches), with the scenarios Weir loses.

## Where Weir is wrong or unproven

- It is new. `v1.0.0` is the first stable release and there are no public production deployments to point to.
- It has no speed or memory benchmark against any project above, and the one measured round ran on a single machine with configurations no other project's maintainer has reviewed.
- It does not store responses over 1 MiB by default, and nothing is served stale unless you or the origin allow it. See the list on the [benchmarks page](/benchmarks/#what-the-numbers-say-about-weir).
- 81 of the 365 cache-tests fail, most by design; read [the reasons](/conformance/) before deciding they suit you.
- Two store implementations, two adapters, no scripting.
- The residual risks listed on the [security page](/security/#residual-risks-in-plain-language) are real.
