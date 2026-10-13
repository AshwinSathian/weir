---
title: Compare
description: Where Varnish, nginx, Souin, Pingora, singleflight or a CDN is the better choice, and where Weir is.
---

Most of this page is qualitative. We have not measured speed against any of these projects, so there are no speed claims here. Two rounds of black-box tests against Varnish, nginx and the Caddy cache-handler counted origin calls, failed clients and poisoning verdicts; it is summarized [below](#measured-side-by-side). Those measured results are ours alone: we wrote the configurations and ran the tests, and the owners of the other projects have not verified them. Every statement about another project comes from that project's own documentation, linked in each section and read on 2026-10-12. If a claim could not be checked there, it was left out. Corrections are welcome: [open an issue](https://github.com/AshwinSathian/weir/issues/new).

Each technique in Weir exists somewhere else. What Weir adds is the combination in one Go library, with a written threat model and a test for each claim.

## At a glance

| | What it is | Choose it when | Choose Weir when |
|---|---|---|---|
| Varnish | a standalone caching proxy, configured in VCL | you want a dedicated cache tier you can program, with a long production history | the cache has to live inside a Go server or Caddy |
| nginx `proxy_cache` | caching built into the nginx proxy | nginx is already your proxy and its cache lock covers your herds | you need jitter, a breaker, a cross-key budget or strict forwarding |
| Souin, Caddy cache-handler | a Go HTTP cache with plugins for many servers | you need its storage backends, ESI or its framework integrations | origin protection and key hardening matter more than backends |
| Pingora | a Rust framework for building proxies | you are building a proxy in Rust | you are working in Go and want cache policy, not a proxy framework |
| singleflight, sturdyc | Go libraries that deduplicate function calls | you are caching function or data-source results | you are caching HTTP responses and need HTTP semantics |
| CDN request collapsing | a managed edge feature | you want caching run for you, close to users | you need the behavior at your own origin tier, in code you control |

## Varnish

Varnish coalesces requests for one object automatically. Its documentation says: "When several clients are requesting the same page Varnish will send one request to the backend", and "In some products this is called request coalescing and Varnish does this automatically." It has `grace` (serve a stale object while fetching a new one) and `keep` (hold an object longer for revalidation), and VCL lets you program the cache's behavior per request. Source: [Grace mode and keep](https://github.com/varnishcache/varnish-cache/blob/master/doc/sphinx/users-guide/vcl-grace.rst).

**What Varnish does that Weir does not.** It is a complete proxy with its own configuration language. You can express logic in VCL that Weir has no setting for. It has been run in production for far longer than Weir has existed.

What Weir does that we could not find in the grace documentation: lifetime jitter, a concurrency budget shared across different keys, and a default that hides unkeyed request headers from the origin. Varnish may offer these through VCL or modules; we only read the page linked above, so treat this as "not built in as far as that page says".

Choose Varnish when you want a separate, programmable cache tier. Choose Weir when you want the cache inside a Go process or Caddy, configured with a struct.

## nginx proxy_cache

nginx has `proxy_cache_lock`: "only one request at a time will be allowed to populate a new cache element", with `proxy_cache_lock_timeout` (5 s by default, after which the request goes to the upstream and its response is not cached) and `proxy_cache_lock_age` (5 s by default). `proxy_cache_use_stale updating` and `proxy_cache_background_update` serve stale content while one request refreshes. Source: [ngx_http_proxy_module](https://nginx.org/en/docs/http/ngx_http_proxy_module.html).

**What nginx does that Weir does not.** It is a full web server and proxy, and caching is one module among many that you may already rely on.

What Weir does that the module page does not describe: the cache lock is per cache element, and the page documents no randomization of cache lifetimes, no circuit breaker and no global limit on concurrent cache-miss requests to the upstream. The default cache key is built from scheme, proxy host and request URI; which request headers reach the upstream is a separate setting you manage. `proxy_cache_purge` is documented as part of the commercial subscription.

Choose nginx when it is already your proxy and same-key locking is the problem you have. Choose Weir when the origin needs protecting across keys, or when you want the forwarded request tied to the key.

## Souin and the Caddy cache-handler

Souin describes itself as "a new HTTP cache system suitable for every reverse-proxy", and the [Caddy cache-handler](https://github.com/caddyserver/cache-handler) is "a distributed HTTP cache module for Caddy based on Souin". Their READMEs list RFC 7234 compliance, the `Cache-Status` header, targeted cache control (RFC 9213), surrogate-key purging, ESI, request coalescing, storage providers including Badger, etcd, NutsDB, Olric, Otter and Redis, and plugins for more than a dozen Go frameworks and proxies. Source: [Souin README](https://github.com/darkweak/souin).

**What Souin does that Weir does not.** Many storage backends, where Weir has two. ESI. Surrogate keys in the Akamai and Fastly styles. Integrations with Traefik, Tyk, Echo, Gin, Fiber and others, where Weir has `net/http` and Caddy.

What Weir does that those READMEs do not mention: lifetime jitter, a circuit breaker, an origin concurrency limit, and cache-key hardening as a security topic. The cache-handler README documents the cached body size as "unlimited if omitted" and a management API at a default path; Weir's object size is bounded at 1 MiB by default and its purge API exists only on Caddy's admin endpoint.

Choose Souin or cache-handler when you need a backend or an integration Weir lacks, or ESI. Choose Weir when the origin's survival and poisoning resistance are the requirements.

## Pingora

Pingora is "a Rust framework to build fast, reliable and programmable networked systems". Its README says "Pingora proxy integration with caching should be considered experimental". Source: [Pingora README](https://github.com/cloudflare/pingora).

It is a different kind of thing: a framework for writing a proxy in Rust, where Weir is a policy library for Go that leaves the wire to an adapter. If you are building in Rust, use Pingora. If your stack is Go or Caddy, the comparison ends at the language.

## singleflight and sturdyc

`golang.org/x/sync/singleflight` "provides a duplicate function call suppression mechanism": one execution in flight per key, with other callers receiving the same result. Its documentation says nothing about timeouts, panics or storing results. Source: [pkg.go.dev](https://pkg.go.dev/golang.org/x/sync/singleflight). [sturdyc](https://github.com/viccon/sturdyc) is "a caching library with advanced concurrency features" with stampede protection, early refreshes and batching for data-source results.

**What they do that Weir does not.** They cache anything: a database row, an API result, a computed value. sturdyc batches and refreshes records individually. Weir only caches HTTP responses.

What Weir adds for HTTP: `Cache-Control` and `Vary` semantics, validation with 304, a wait bound for followers and an age bound for leaders, recovery of a panicking fetch with the error delivered to every waiter, and the budget across keys. See [Coalescing](/docs/concepts/coalescing/).

Choose singleflight or sturdyc for function results. Choose Weir for HTTP responses.

## CDN request collapsing

Fastly defines request collapsing as "the practice of combining multiple requests for the same object into a single request to origin", and documents its edge cases: an uncacheable response sends the queued requests one after another unless a hit-for-pass marker exists, which "can create extreme response times of several minutes" in the worst case. Source: [Fastly: request collapsing](https://www.fastly.com/documentation/guides/concepts/edge-state/cache/request-collapsing/).

**What a CDN does that Weir does not.** It runs in many locations close to users, absorbs traffic before it reaches your network, and is operated by someone else. Weir is one process next to your origin.

They are not alternatives so much as layers. A CDN in front and Weir at the origin tier is a reasonable setup: collapsing at the edge is per location, so a purge or a cold start can still arrive at your origin from many locations at once.

## Measured side by side

These results are based on our own runs and have not been verified by the owners of the other projects. Varnish, nginx `proxy_cache`, the Caddy cache-handler and Weir ran as containers behind one origin with deliberately unsafe routes. Each probe sends an attacker's request and then a victim's. "unsafe" means the victim was served what the attacker caused. That is what a cache does when the origin is wrong, not a vulnerability in the cache: each of these projects documents how to key or strip the input, and the "hardened" columns are that documentation applied to the headers our probes use. Weir's result comes from its default of hiding unkeyed request headers, cookies and bodies from the origin on cacheable requests.

{{include docs/benchmarks.md "### Comparison: security probes"}}

The same round ran the public cache-tests suite against each subject, in each configuration:

{{include docs/benchmarks.md "### Comparison: conformance"}}

Origin calls and failed clients under herds, expiry and outages are on the [benchmarks page](/benchmarks/#against-other-caches), with the scenarios Weir loses.

## Where Weir is wrong or unproven

- It is new. `v1.0.0` is the first stable release and there are no public production deployments to point to.
- It has no speed or memory benchmark against any project above, and the one measured round ran on a single machine with configurations no other project's maintainer has reviewed.
- It does not store responses over 1 MiB by default, and nothing is served stale unless you or the origin allow it. See the list on the [benchmarks page](/benchmarks/#against-other-caches).
- In the measured round it refused most clients of a flooded path while the origin had room, left most URLs unrefreshed seconds after a soft purge, kept failing clients for seconds after an origin recovered when stale serving was off, and refetched every URL after a restart. The numbers are on the benchmarks page.
- 81 of the 365 cache-tests fail, most by design, and more with stale serving turned on; read [the reasons](/conformance/) before deciding they suit you.
- Two store implementations, two adapters, no scripting.
- The residual risks listed on the [security page](/security/#residual-risks-in-plain-language) are real.
