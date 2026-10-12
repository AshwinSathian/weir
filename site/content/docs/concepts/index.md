---
title: Concepts
description: The model behind Weir, starting from the failures it exists to prevent.
---

A shared cache sits between clients and an origin and answers some requests itself. Most of the time that makes the origin's life easier. The pages here are about the times it does not: the moments when a cache sends the origin more work, all at once, than it would have had with no cache at all.

## Start from the failure

| When this happens | The origin sees | Read |
|---|---|---|
| Entries stored together reach the end of their lifetime together | the whole batch again, in the same second | [Freshness and staleness](/docs/concepts/freshness-and-staleness/) |
| One popular entry expires | every waiting request for it | [Coalescing](/docs/concepts/coalescing/) |
| A purge, a restart or a cold start empties many keys | thousands of different requests that share nothing | [Origin protection](/docs/concepts/origin-protection/) |
| The origin slows down or fails | a queue of requests, each paying the full timeout | [Origin protection](/docs/concepts/origin-protection/) |
| A request header the cache does not key changes the response | nothing unusual; the damage is a poisoned entry served to everyone | [The cache key](/docs/concepts/cache-key/) |
| You need to remove content now | a stampede, if the purge deletes entries outright | [Purging](/docs/concepts/purging/) |

## Then the machinery

[Request flow](/docs/concepts/request-flow/) walks one request through the nine stages every `Serve` call runs. [Stores](/docs/concepts/stores/) covers where entries live, the memory store's eviction policy, and what changes when several nodes share a Valkey server.

## Words these pages use

| Term | Meaning |
|---|---|
| Origin | The server Weir fetches from. Anything with a `Fetch` method. |
| Shared cache | A cache whose stored responses are reused across users, in the RFC 9111 sense. Weir is never a private cache. |
| Coalescing | Several concurrent requests for one key sharing one origin fetch. Other projects call it request collapsing or single-flight. |
| Flight | One coalesced fetch and the requests waiting on it. The first requester is the leader, the rest are followers. |
| Soft purge and hard purge | Soft marks entries stale so they can still be served and revalidated. Hard makes them unusable. |
| Stale-while-revalidate (SWR) and stale-if-error (SIE) | The two windows, after an entry expires, in which it may still be served: SWR always, with a refresh in the background, and SIE only when the origin is failing. |
| Partition | The unit of fairness in the limiter: scheme, host and path, without the query. A flood of query strings on one path is one partition. |

Each concept page ends with a "Limits" section. If a page does not tell you where the feature stops, that is a bug in the page; [report it](https://github.com/AshwinSathian/weir/issues/new).
