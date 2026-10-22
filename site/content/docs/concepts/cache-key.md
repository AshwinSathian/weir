---
title: The cache key
description: Why the key is a security boundary, what strict forwarding does to your origin, and how to key or allow what it needs.
---

A cache key decides which requests are "the same". Two requests with the same key get the same stored response. So anything about a request that can change the origin's answer has to be in the key. When it is not, one user's request can decide what every other user receives. That is cache poisoning, and most of its published forms come down to a request header the origin read and the cache did not key (T-1).

## The rule Weir enforces

Weir does not try to list dangerous headers. It turns the question around:

> For a cacheable request, the request Weir forwards is the request it keyed.

On a `GET` or `HEAD` that is not bypassed, the origin receives only (FR-FWD-1):

- headers you named in `Key.Headers`, which are in the key;
- cookies you named in `Key.Cookies`, which are in the key, and no other cookie;
- `Accept-Encoding`, reduced to the one coding it prefers from `Key.AcceptEncoding` (`gzip` by default), so a thousand spellings of that header reach the origin as one;
- headers you named in `Forward.Allow`;
- `Authorization`, `Cache-Control` and `Pragma`, which HTTP requires and which get their own handling;
- the trace headers `traceparent`, `tracestate` and `X-Request-Id`, validated and bounded.

Everything else is dropped before the fetch. An attacker can send `X-Forwarded-Host: evil.example` all day; the origin never sees it, so it cannot reflect it into a response that gets stored. The same applies to the path and query: normalization rewrites the request, never only the key (FR-FWD-5). This is the cache's central invariant, and a fuzz target (`FuzzForwardEqualsKey`) checks it on generated requests.

## What this costs you

If your origin builds pages from a header or a cookie, it stops seeing that input the day you put Weir in front, until you say so. You have three choices for each input:

| Choice | Setting | Effect |
|---|---|---|
| Key it | `Key.Headers`, `Key.Cookies` | the origin sees it, and each distinct value gets its own entry |
| Allow it | `Forward.Allow` | the origin sees it, the key does not. Only for a header that cannot change the response |
| Lose it | nothing | the origin does not see it |

A page that varies on `Accept-Language` wants `Key.Headers: ["Accept-Language"]`. A request ID the origin only logs wants `Forward.Allow`. A session cookie usually wants neither: put it in `Bypass.Cookies`, and requests that carry it skip the cache.

> [!PROTECTION]
> Every name in `Forward.Allow` is input the origin can see and the key cannot. If the origin ever varies its response on one, you have rebuilt the poisoning bug by hand (R-3). `New` rejects `Cookie` there and logs a warning for `Authorization`.

To find what you forgot, read the log after the first five minutes. Weir counts the cookie names it dropped from cacheable requests (names, never values) and logs the most common ones once: `weir: cookies stripped from cacheable requests`.

## What goes into the key

The primary key is SHA-256 over a tagged, length-prefixed encoding of exactly these inputs (FR-KEY-1, FR-KEY-2):

- method class (`GET` and `HEAD` share one);
- scheme, lowercased;
- host, lowercased, default port and trailing dot removed;
- path, byte for byte unless `Key.NormalizePath` is set;
- query, after `Key.QueryDrop`, `Key.QueryKeep` and `Key.QuerySort`;
- the values of `Key.Headers` and `Key.Cookies`.

Length prefixes matter. A key built by joining strings with a delimiter lets `a|b` + `c` collide with `a` + `b|c`.

For a first deployment, `Key.QueryDrop: weir.TrackingParams()` is the one setting most sites want. It keeps `utm_*`, `gclid`, `fbclid` and similar from minting a new key for every ad click. Dropped parameters do not reach the origin either, so remove from the list any name your origin reads.

## Vary

When a stored response carries `Vary`, Weir keeps a small record of which headers it named and stores each variant under a second key built from those headers' forwarded values (FR-KEY-7). Three defaults keep this from becoming a hole or a memory leak:

- `Vary: *` is never stored (FR-KEY-8).
- A response that varies on `Cookie`, `Authorization` or `Proxy-Authorization` is not stored unless you list the name in `Key.VaryAllow` (FR-KEY-9).
- At most 8 `Vary` names and 8 variants per URL (FR-KEY-10). A ninth variant is served and not stored.

## Malformed input

A request that fails validation is rejected with 400 before the store or the origin is touched (FR-VAL-1). Odd-but-valid values, such as a strange `Accept-Encoding` q-value, collapse to a safe default and stay inside the key. The alternative, treating odd input as "uncacheable, pass it through", gives an attacker a guaranteed miss for the price of one malformed header (T-13).

## Limits

> [!LIMITS]
> Weir keys what it can see. It cannot see what your origin reads from outside the request: the client IP, the time of day, or a value some other proxy adds between Weir and the origin (R-1). It cannot see context values that middleware in front of Weir hands to an in-process origin (T-45, R-6). And if the origin marks a per-user page as publicly cacheable, no cache can tell (R-5). Request `Cache-Control` and `Pragma` reach the origin unkeyed, in a restricted form; a flight led by such a request can share a 5xx with its followers, though nothing is stored (R-7).
