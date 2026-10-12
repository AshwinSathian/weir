---
title: Purging
description: Soft and hard purges by URL, by group, or everything, and why none of them walks the store.
---

You published a correction and the old page must go. On many caches a purge deletes entries, and the next requests all miss. Purge a whole site that way and you have built the cross-key stampede yourself.

## Soft and hard

| | Soft purge (the default) | Hard purge |
|---|---|---|
| Effect | matching entries become stale now | matching entries are unusable |
| Served again? | yes, inside their stale-while-revalidate and stale-if-error windows | never |
| Refetched how | with a conditional request, so an unchanged page costs a 304 | in full |
| Use it for | content that changed | content that must not be served again: a leak, a legal removal |

A soft purge moves an entry's expiry to the time of the purge (FR-PRG-2). Everything in [Freshness and staleness](/docs/concepts/freshness-and-staleness/) then applies from that moment: the entry is served stale while one background fetch renews it, and every refetch goes through the limiter. The origin sees a controlled refill.

A hard purge makes entries a miss: no stale serving, no revalidation (FR-PRG-3). The limiter still bounds the refill, but clients wait for it.

## What you can name

```go
// One or more URLs. They are validated and rewritten exactly like requests,
// so a purge reaches the key a request for that URL would use.
e.Purge(ctx, weir.Purge{URLs: []string{"https://example.com/pricing"}})

// A group: every response the origin tagged with Cache-Groups: "product-42".
e.Purge(ctx, weir.Purge{Groups: []string{"product-42"}, Origin: "https://example.com"})

// Everything, hard.
e.Purge(ctx, weir.Purge{Mode: weir.PurgeHard, All: true})
```

A URL purge reaches every variant of that URL and every keyed-header or keyed-cookie version of it. Groups are RFC 9875 cache groups: the origin sends `Cache-Groups: "product-42", "catalog"` on responses, and you purge by that name. Group names are compared byte for byte and scoped to one origin (FR-PRG-6), which is why `Origin` is required with them. The origin can also invalidate its own groups by answering an unsafe request with `Cache-Group-Invalidation`.

On Caddy the same calls are `POST` requests to the admin API; see the [Caddy guide](/docs/guides/caddy/).

## Why a purge is cheap

A purge does not search the store. It writes an epoch: a timestamp filed under the URL, the group, the origin, or "everything" (FR-PRG-4). At lookup, an entry fetched before an epoch that applies to it is stale or gone (FR-PRG-7). The cost of a purge depends on how many things you named and not on how many entries match, so purging a million-entry site is one write.

It also closes a race. A fetch that started before the purge and finished after it carries the earlier time, so its response does not survive the purge even though it was stored afterwards.

The purged records stay in the store until eviction or expiry removes them. If the bytes themselves must go now, add `Eager: true` to a hard purge: stores that support it scan and delete matching records at once (FR-PRG-8). The memory store does.

## Limits

> [!LIMITS]
> There is no prefix or wildcard purge: name URLs, name groups, or purge all. With the memory store, each node has its own cache and its own epochs, so a purge sent to one node leaves the others serving the old content (T-38); run one node, or use the Valkey store. A soft purge on the memory store can take effect up to 1 second late. With a remote store, if the epoch lookup itself fails, Weir serves the entry and emits an event; it does not fail closed, because that would turn a store blip into a full miss storm (T-9, R-4). After a store incident, a hard purge of everything restores certainty.
