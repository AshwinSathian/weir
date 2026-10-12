---
title: Freshness and staleness
description: How long an entry lives, why its lifetime is shortened a little, and when Weir serves it after it expires.
---

## How long is an entry fresh?

The origin decides. Weir takes the first of these it finds on the response (FR-FRS-1): `s-maxage`, then `max-age`, then `Expires` minus `Date`, then a heuristic. The heuristic applies only to a response with `Last-Modified` and no explicit lifetime: 10% of the time since it was modified, at most an hour (FR-FRS-3).

A response with no lifetime and no `Last-Modified` is not cached, unless you set `Freshness.DefaultTTL`.

A malformed lifetime is treated as zero. `max-age=60, max-age=600` is not sixty or six hundred; two values that disagree mean Weir cannot know what the origin meant, so it does not guess long (FR-FRS-2).

## One entry, second by second

Take a response with `Cache-Control: max-age=60, stale-while-revalidate=30, stale-if-error=300` and default settings.

{{figure freshness-timeline}}

| When | What happens |
|---|---|
| 0 s | Stored. Jitter picks its real expiry: somewhere between 54 and 60 seconds |
| Up to expiry | Served as `hit`. Close to the end, a request may also start a background refresh |
| Expiry to +30 s | Served stale immediately, with one background fetch renewing it: `Weir; hit; ttl=-10; detail=stale-while-revalidate` |
| Up to +300 s | Not served in normal operation: a request waits for a fetch. If that fetch fails, the breaker is open, or the request is shed, the entry is served with `detail=stale-if-error` |
| 5 more minutes | Not served. Kept only if it has an `ETag` or `Last-Modified`, so the next request can revalidate with a 304 instead of a full fetch (`Freshness.Keep`) |

## Jitter

Ten thousand pages are stored within two seconds of a deploy, each with `max-age=60`. Sixty seconds later all ten thousand expire within two seconds. The cache has turned a deploy into a recurring load spike, once a minute, until traffic happens to spread it out.

Weir multiplies each lifetime by a random factor between 0.9 and 1 (FR-FRS-5):

```text
lifetime_eff = lifetime × (1 − Jitter × U)      U uniform in [0, 1), Jitter = 0.10
```

So the batch spreads over six seconds on its first expiry, and further on each one after. Jitter only ever shortens a lifetime, so Weir never serves something as fresh for longer than the origin allowed. It is skipped for lifetimes under 10 seconds (`Freshness.JitterMinLifetime`).

## Early refresh

Jitter spreads expiries. Early refresh removes some of them. On a fresh hit, Weir draws a random number and may start a background refresh before the entry expires (FR-FRS-6). The chance grows as expiry approaches and with how long the last fetch took: an entry that takes 2 seconds to regenerate starts refreshing earlier than one that takes 20 milliseconds. A hot key is usually renewed without any request seeing a miss. This is the XFetch algorithm from Vattani, Chierichetti and Lowenstein's "Optimal Probabilistic Cache Stampede Prevention" (2015).

Background refreshes never queue. If the limiter has no room outside the foreground reserve, the refresh is dropped and a later request tries again.

## Stale serving is the origin's call

By default Weir serves stale content only inside windows the origin granted with `stale-while-revalidate` or `stale-if-error` (RFC 5861). An origin that sends neither gets neither.

> [!PROTECTION]
> `Freshness.DefaultStaleWhileRevalidate` and `Freshness.DefaultStaleIfError` apply a window to responses that did not ask for one. `DefaultStaleIfError` is the usual first change in production, since it trades freshness for surviving an outage. You are then serving content the origin never agreed could be stale.

Some entries are never served stale, whatever you set: those marked `must-revalidate`, `proxy-revalidate` or `no-cache`, those with `s-maxage` and no explicit stale directive (FR-STL-3), and those invalidated by a `POST` or `DELETE` to the same URL (FR-STL-5). When a `must-revalidate` entry cannot be validated, the client gets a 504.

## What clients can ask for

By default Weir ignores `Cache-Control: no-cache` and `max-age=0` on requests (decision D5). Otherwise anyone with a browser's reload button, or a script, could turn hits into origin fetches. `Client.HonorRevalidation` changes that; validation is still coalesced and limited.

## Limits

> [!LIMITS]
> Refreshes are triggered by requests, never by timers (FR-STL-6). An entry nobody asks for is not kept warm. Use `Engine.Warm` for that. Age is computed from when Weir fetched the response, not from the origin's `Date` header, so a skewed origin clock cannot switch the cache off (FR-FRS-4, T-30); one cache-tests case fails because of it.
