---
title: Cache-Status
description: The grammar of the Cache-Status header Weir writes, and what every parameter and value means.
---

Every response carries a `Cache-Status` header in the RFC 9211 format. The member name is `Weir`; change it with `Config.CacheStatus` or drop the header with `Config.NoCacheStatus`.

## Shape

```text
Cache-Status: Weir; hit; ttl=43
Cache-Status: Weir; fwd=uri-miss; fwd-status=200; stored
Cache-Status: Weir; fwd=uri-miss; fwd-status=200; stored; collapsed
Cache-Status: Weir; hit; ttl=-10; detail=stale-while-revalidate
Cache-Status: Weir; hit; ttl=1; detail=negative
```

A response is either a `hit` with a `ttl`, or was forwarded with a `fwd` reason. `collapsed` and `detail` can follow either.

## Parameters

| Parameter | Meaning |
|---|---|
| `hit` | answered from the store without waiting for the origin |
| `ttl=N` | seconds of freshness left. Negative on a stale hit: `ttl=-10` is ten seconds past expiry |
| `fwd=REASON` | the origin was asked, and why (values below) |
| `fwd-status=N` | the status the origin answered with |
| `stored` | the forwarded response was stored |
| `collapsed` | this request joined another request's fetch ([coalescing](/docs/concepts/coalescing/)) |
| `detail=TEXT` | more about the decision (values below) |

## `fwd` values

{{fwd_table}}

## `detail` values

On a stale hit, `detail` names why stale content was served:

{{stale_table}}

Other values: `negative` marks a [negative-cache](/docs/concepts/origin-protection/#negative-caching) answer, and `hit-for-miss` marks a request sent to the origin without coalescing because this URL recently produced a response that could not be shared.

`detail` and `fwd` are free-text values that may gain members in a minor release. Parse them with a default case.

## What is never in the header

The `key` parameter that RFC 9211 allows is never emitted (FR-SRV-9, T-27). A cache key in a response header tells an attacker exactly which inputs are keyed.

The value lists on this page are read from `cachestatus.go` at build time.
