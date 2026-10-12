---
title: Tuning and trade-offs
description: The settings that take a protection back. What each one gives up, when it is the right call, and the threat it reopens.
---

Weir's defaults fail in one direction: toward the origin doing more work, never toward a user receiving a response that was not meant for them. Most sites change a few of these settings, and that is fine when you know which protection you are trading and why.

Each row below returns a risk the defaults remove, or trades one function for another. The IDs link to the threat or residual risk it reopens.

| Setting | What you take back | Use it when |
|---|---|---|
| `Forward.Mode: weir.ForwardAll` | strict forwarding: every request header reaches the origin, unkeyed, on cacheable requests too. `New` logs a warning. Cookie bypass rules then match names as sent, so an origin that rewrites cookie names (PHP turns `.` and a space into `_`) needs each spelling in `Bypass.Cookies` | the origin cannot be audited for header use and you accept the poisoning risk; a long `Forward.Allow` is usually better. A flight led by a request with unkeyed input shares a 500, 502, 503 or 504 answer with the followers that joined it, although nothing is stored (R-7) |
| `Forward.Allow` with many names | the same, one header at a time. `New` logs a warning for `Authorization` and `Proxy-Authorization`; `Cookie` is rejected, so list cookie names in `Key.Cookies` | a header cannot change the response but the origin requires it |
| `Storable.StripSetCookie: true` | the rule that a response with `Set-Cookie` is not stored: Weir removes the field, stores the rest and still sends the cookie to the client whose request triggered the fetch (FR-STO-6) | the origin sets a cookie on every response (analytics) and the body does not depend on it |
| `Key.VaryAllow: ["Cookie"]` (or `Authorization`) | the rule that a response varying on `Cookie`, `Authorization` or `Proxy-Authorization` is not stored (FR-KEY-9) | a cookie really selects a small set of public variants |
| `Freshness.DefaultTTL`, `DefaultStaleWhileRevalidate`, `DefaultStaleIfError` | origin authority over freshness and staleness (D6): a response with no explicit lifetime or `Last-Modified` becomes cacheable, and stale content is served that the origin never allowed | the origin sends no freshness headers and cannot be changed |
| `Client.HonorRevalidation: true` | the default of ignoring client `Cache-Control: no-cache`, `max-age`, `min-fresh`, `max-stale` and `Pragma: no-cache` (D5): `no-cache`, `max-age=0` and `Pragma: no-cache` (when there is no `Cache-Control`) then force validation of a stored entry; `min-fresh`, `max-stale` and other `max-age` values stay ignored. Validation is still coalesced and limited, and is a cheap 304 when the entry has a validator. Anyone who sends those headers can trigger it | you want browser reloads to bypass the cache |
| `Breaker.Disable`, `Negative.Disable`, `MissRate.Disable`, `Freshness.NoJitter`, `Freshness.NoEarlyRefresh` | the matching defense: circuit breaker, short negative caching, miss-rate detection, TTL jitter, early refresh | a test, or a measured reason |
| `TransportOrigin.Rewrite` adding headers | the same, from the adapter side: a header it adds to a cacheable request is unkeyed input (T-4). Proxies in front of Weir that add `X-Forwarded-For` have the same effect (R-1) | the origin needs `Via` or origin auth and never varies on it |
| `HandlerOrigin` behind your own middleware | an in-process origin that varies its response on context values set by middleware in front of Weir (R-6): the key does not see them. Put that middleware behind Weir or route the path around it | never, unless the context values cannot change the response |
| Raising limits: `Limiter.*`, `Limits.*`, `Storable.MaxObjectBytes`, `Timeouts.*` | the bounds behind T-12, T-20, T-21 and NFR-4: a higher `MaxPerPartition` weakens the flood cap, a higher `MaxObjectBytes` raises buffering in proportion | the origin and the process have measured headroom |
| `Storable.Statuses` | the default status set; 206, 304, 500, 502, 503 and 504 are never storable whatever you list | you cache an unusual status on purpose |
| `MissRate.Throttle: true` | the busy path's own misses: a client that floods it with distinct queries can keep it capped at one origin fetch for as long as it sends (R-8). Off by default | after a run without it where you read the warnings, see the runbook |
| `CacheGroups.Ignore: true` | RFC 9875 group invalidation (`Cache-Group-Invalidation` is no longer acted on) | the origin's group headers should not purge other entries |

## Three that come up first

**`Freshness.DefaultStaleIfError`.** Most origins send no `stale-if-error`. Without it, an origin outage becomes errors as soon as entries expire. Setting this to a few minutes or hours is usually the first production change. You are then serving content past the lifetime the origin gave it, so pick a window your content can tolerate being wrong for.

`Key.QueryDrop: weir.TrackingParams()` is not on the list above because it adds a protection. It is here because it has a cost: dropped parameters do not reach the origin. If server-side code reads `utm_source`, remove it from the list first.

`Storable.StripSetCookie` matters when an origin sets an analytics or load-balancer cookie on every response, which would otherwise make nothing cacheable. Turn it on only after checking that no response body depends on the cookie being set.

## A rule for changing defaults

Write the reason down. The project holds itself to that: a change to a default that weakens security or stale serving needs explicit approval and happens only in a major release.
