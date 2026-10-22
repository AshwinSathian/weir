---
title: Coalescing
description: Concurrent requests for one key share one origin fetch. What happens when that fetch is slow, fails or panics.
---

A popular entry expires. In the next 200 milliseconds, 1,000 requests arrive for it, find nothing fresh, and each one asks the origin. The origin was sized for a cache in front of it, and now does the same work 1,000 times. That is a hot-key stampede.

Coalescing is the fix: the first request starts a fetch, and the others wait for its result (FR-COA-1). One origin call, 1,000 answers. The first request is the leader of a flight and the rest are followers. Other projects call this request collapsing; Go programmers know it from `x/sync/singleflight`.

The basic idea is twenty lines of code. What separates implementations is the behavior when the shared fetch goes wrong, so that is most of this page.

## The leader disconnects

The fetch does not belong to the leader. It runs on its own goroutine with a context detached from every requester (FR-COA-2), so a leader that hangs up does not cancel the fetch out from under 999 followers. Each follower's own cancellation ends only its own wait.

## The fetch is slow

Two bounds, both 10 seconds by default and both capped by `Timeouts.Origin`:

- **At `Coalesce.FollowerMaxWait` a follower takes a stale entry if one is allowed** (the response says `detail=coalesce-timeout`). Without one it keeps waiting for the fetch in progress, which the origin timeout already bounds. Fetching for itself at that point would turn one slow fetch into sixteen and shed everyone else. It gives up on the flight and fetches for itself only if the flight is still running after `Timeouts.Origin` plus the queue wait, which takes an origin that ignores cancellation.
- A flight older than `Coalesce.LeaderMaxAge` stops accepting followers (FR-COA-3). The next request for that key starts a fresh flight. So one stuck fetch delays the requests that joined it, and nobody who arrives later.

Without these bounds a single hung origin connection holds every request for that key until the origin timeout, which is how coalescing turns a slow origin into an outage.

## The fetch fails

A 500, 502, 503 or 504, or a network error, goes to every waiter (FR-COA-5). Each one serves stale from its own lookup if stale-if-error permits, and gets the error otherwise. Followers do not retry against an origin that just failed, because that would be the stampede again, aimed at a server already in trouble.

A panic inside your `Origin.Fetch` is recovered, turned into an error, and delivered the same way. The limiter slot and the flight are released (FR-COA-6).

## The response cannot be shared

Sometimes the leader's response is not usable by a follower: it was not storable (`private`, `Set-Cookie`), it was too large to buffer, or its `Vary` does not match the follower's headers. The follower then goes back to lookup once (FR-COA-5). To stop this repeating on every request, Weir remembers for `Coalesce.HitForMissTTL` (30 seconds) that this key produces unshareable responses, and sends requests for it straight to the origin, through the limiter, without coalescing.

A stored response that is stale on arrival (`max-age=0`, or `no-cache` with a validator) is still shared with the followers that waited for it. They asked at the same moment as the leader, and the response is as fresh for them as it is for the leader; the next request after them revalidates it. Most shared caches collapse this way, and the origin otherwise gets one request per waiting client, which is the stampede coalescing exists to prevent.

## Requests that are never coalesced

A request carrying `Authorization` that finds no stored entry fetches on its own (FR-COA-8). A response fetched with one client's credentials reaches another client only through the store, and only when the origin marked it `public`, gave it `s-maxage` or `must-revalidate`.

## Seeing it

Followers get `; collapsed` in `Cache-Status`. With Prometheus, `weir_coalesced_total` counts them and `weir_coalesce_timeouts_total{fallback}` counts flights that ran past the wait bound.

The repository's `BenchmarkServeMissCoalesced` sends 8 callers at a cold key and reports 1.003 to 1.004 origin calls per round on a 4-vCPU Linux container.

## Limits

> [!LIMITS]
> Coalescing works per key. A thousand requests for a thousand different keys share nothing, which is what the [limiter](/docs/concepts/origin-protection/) is for. Coalescing is per process: two nodes sharing a Valkey store each run their own flights, so a cold key can cost one origin fetch per node. The flight table is in memory and keeps working when the store is down (FR-COA-7).
