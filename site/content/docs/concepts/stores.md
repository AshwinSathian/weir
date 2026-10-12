---
title: Stores
description: Where entries live. The memory store and its eviction policy, snapshots, and Valkey for several nodes.
---

The engine talks to storage through a small interface, `store.Store`: `Get`, `Set`, `Delete`, two epoch methods, `Info` and `Close`. Two implementations ship. Entries are immutable once stored; to change one, the engine builds a new one.

## The memory store

The default. It lives in the engine's process, so a hit costs no network round trip and cannot fail.

**Size.** It takes 40% of `GOMEMLIMIT`, clamped between 16 MiB and 8 GiB. With `GOMEMLIMIT` unset it takes 256 MiB and logs a warning. Weir does not read cgroup limits for you, so in a container set `GOMEMLIMIT` yourself.

Eviction is S3-FIFO (Yang et al., SOSP 2023). New entries enter a small queue; an entry requested again before it reaches the end moves to the main queue, and one that was not is dropped. A flood of one-off keys, which is what a cache-busting attack looks like to the store, flows through the small queue and out again without displacing the entries that get hits. The store is split into 16 shards, each with its own lock and budget. An entry's shard is chosen with a per-process random seed, so an attacker cannot work out offline which keys land together.

One object can be at most `Storable.MaxObjectBytes`, 1 MiB by default, body and headers together. Larger responses are streamed to the client and not stored.

### Snapshots

A restart with an empty cache is a cold start. With a snapshot path set (`snapshot_dir` on Caddy), the memory store writes its entries to a file on graceful shutdown and reads them at the next start. Loaded entries are revalidated once before they are served (FR-SNP-2), so the first request for each shows `fwd=stale` and costs a 304 when the origin sends validators.

A snapshot is not persistence. A crash or a `kill -9` writes nothing. For a planned cold start, `Engine.Warm` fetches a list of requests at background priority.

## The Valkey store

A separate module, `github.com/AshwinSathian/weir/store/valkey`. Use it when several nodes must share one cache, and therefore one set of purges.

What changes:

- A hit is a network round trip, bounded by `Timeouts.Store` (50 ms). The store can now fail, and [the store breaker](/docs/concepts/origin-protection/) exists for that.
- Eviction is Valkey's. Set `maxmemory-policy` to `volatile-lfu` (recommended), another `volatile-*` policy, or `noeviction`. Purge epochs are stored as keys without a TTL, and an `allkeys-*` policy could evict one, which would silently undo a purge. The store checks the policy on connect and refuses to run under `allkeys-*`.
- A stored value larger than `Config.MaxObjectBytes` (64 MiB) is never read whole and counts as a miss, so a value planted on a shared server cannot be used to exhaust a node's memory.
- Every node sharing a prefix needs the same forwarding, key and stale settings and clocks within the configured skew. The [runbook section](https://github.com/AshwinSathian/weir/blob/main/docs/runbook.md#8-deploying-on-caddy-several-nodes-one-valkey) is the checklist.

The store is written for Valkey 7 or later. The only version it is tested against is Valkey 8.1, in CI. Other versions are untested, so run the store's integration tests (`make test-valkey`) against yours before relying on it.

## Writing your own

Implement `store.Store` and run the conformance suite in `store/storetest` against it. Optional capability interfaces (eager scrubbing, size limits) are discovered by type assertion. The contract is in [the storage spec](https://github.com/AshwinSathian/weir/blob/main/docs/05-storage-interface-spec.md).

## Limits

> [!LIMITS]
> The memory store is one node's cache. Two nodes with memory stores have two caches and two sets of purges (T-38). At 1 million entries the Go garbage collector costs 1.08 µs per request on the reference machine; a layout that removes that cost exists as a prototype and is deferred until a deployment needs it. With Valkey, coalescing is still per node, so a cold key can cost one origin fetch per node. No store survives a crash with its contents: snapshots cover graceful restarts only.
