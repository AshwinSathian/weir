# Runbook

Updated: 2026-10-11

For the person running Weir in front of an origin. It covers first deployment, the signals to watch and what to do when each one fires. Setting names are in [01 §6](01-technical-spec.md); the reasoning behind the defaults is in [06](06-threat-model.md). This is not a design document: when it disagrees with 01 to 07, they win.

## 1. Before the first deployment

1. Set `GOMEMLIMIT` (plain library use; the `caddy` binary derives its own limit, see 7.5). The default memory store takes 40% of it, clamped to 16 MiB to 8 GiB. Without it the store takes 256 MiB and `New` logs a warning. Weir does not look at cgroup limits for you.
2. List what the origin reads from the request. Strict forwarding sends the origin only keyed, allowed and required headers (README, "Strict forwarding"). An `Accept-Language`, device header or cookie that is not in `Key.Headers`, `Key.Cookies` or `Forward.Allow` is invisible to the origin, so a page built from it comes out wrong, and the same wrong page is cached.
3. Run with the defaults for a day and read the warnings. The log line `weir: cookies stripped from cacheable requests` (Info, once, after the first 5 minutes of `Bypass.ReportStrippedCookies`) lists cookie names the origin never saw. A name you recognize as session or login state belongs in `Bypass.Cookies` (those requests skip the cache) or in `Key.Cookies` (each value is its own entry).
4. Decide what happens to requests with `Authorization` or a session cookie. A response to a request with `Authorization` is stored only when it carries `public`, a valid `s-maxage` or `must-revalidate` (FR-STO-5), and those requests are never coalesced with other users' requests.
5. Put a rate limiter in front for distinct-path floods (06 R-2). Weir bounds origin concurrency; it does not rate-limit clients.
6. Decide the stale policy. Defaults serve stale only inside the origin's `stale-while-revalidate` and `stale-if-error` windows. Setting `Freshness.DefaultStaleIfError` is the usual first change; it trades freshness for survival of an outage (D6).

## 2. What to watch

Each response carries `Cache-Status: Weir; ...` (RFC 9211). `hit; ttl=N` is a fresh hit. `fwd=uri-miss` (no entry), `fwd=vary-miss` (entry exists, not for these `Vary` values), `fwd=stale`, `fwd=request` (the client asked), `fwd=method` and `fwd=bypass` say why the origin was asked. `; collapsed` marks a request that joined another's fetch. `detail=negative` marks a negative-cache entry. A stale hit shows a negative `ttl=`. `detail=` names the stale reason: `stale-while-revalidate`, `stale-if-error`, `shed`, `circuit-open`, `coalesce-timeout`. The metric label `reason` uses short names for the first two: `swr` and `sie`.

With `observe/prom` (a separate module):

| Metric | Read it as |
|---|---|
| `weir_requests_total{outcome}` | the hit ratio and the mix of outcomes |
| `weir_origin_fetches_total{class,result}`, `weir_origin_fetch_seconds` | origin load and latency, split foreground, background and warm |
| `weir_origin_inflight`, `weir_limiter_queue_depth` | the limiter: sustained `inflight` at `MaxConcurrent` with a growing queue means the origin is the limit |
| `weir_shed_total{reason}` | requests refused (clients get 503 with `Retry-After`) |
| `weir_breaker_state` (0 closed, 1 half-open, 2 open), `weir_breaker_transitions_total{to}` | origin health as Weir sees it |
| `weir_stale_served_total{reason}` | how often staleness is hiding a problem |
| `weir_coalesce_timeouts_total{fallback}`, `weir_coalesced_total` | flights that ran long, and how much coalescing saves |
| `weir_store_errors_total{op}`, `weir_store_bytes`, `weir_evictions_total{queue}` | the store: errors mean it is unavailable, evictions are normal when full |
| `weir_not_stored_total{reason}`, `weir_key_rejections_total{reason}` | responses refused by storability rules, and requests refused by validation |
| `weir_miss_rate_anomalies_total{action}` | see 4.4 |
| `weir_purges_total{mode}`, `weir_negative_served_total` | purge activity and negative-cache use |

Labelled series appear on their first event, so alert on `absent()` as well as `rate()`. Without Prometheus, implement `weir.Observer` (it runs on the request path: keep `Observe` fast) and poll `Engine.Stats()` for in-flight, queued, breaker state and store bytes. Request-derived strings such as `Event.Partition` are never metric labels.

## 3. Alerts worth having

- The breaker is open for more than a few minutes (`weir_breaker_state == 2`).
- `weir_shed_total` is nonzero for more than a minute: the origin cannot keep up, or `MaxConcurrent` is set too low for it.
- `weir_stale_served_total{reason="sie"}` or `reason="circuit-open"` is rising: you are serving through an outage.
- `weir_store_errors_total` is rising (remote stores only).
- Hit ratio falls by half against the same hour last week.

## 4. Incidents

### 4.1 The origin is down or slow

The breaker counts origin-health failures over a 10 s window (500 and 4xx are not failures unless `Breaker.CountStatus500`), opens at a 50% failure ratio of at least 20 outcomes, and stays open 5 s (±20%), doubling to 60 s on each consecutive reopen. Meanwhile entries inside their stale-if-error window are served with `detail=stale-if-error` or `circuit-open`; everything else gets 503.

If entries have no stale-if-error window and you want to ride out the outage: `Engine.SetMode(weir.ModeStaleOnError, ttl)` serves any stored entry stale (up to 24 h past expiry, but see the retention note below), except hard-purged, invalidated and `must-revalidate`, `proxy-revalidate`, `no-cache` or `s-maxage` ones. `ttl` is required, at most 24 h, and the mode reverts by itself. Entries are kept only for their stale windows plus `Freshness.Keep` (5 min) after expiry, and only when they carry `ETag` or `Last-Modified`, so the practical reach is minutes unless you raised `Freshness.Keep` or `DefaultStaleIfError` before the incident. Modes are not persisted across restarts. Set it from an admin handler, not from request data.

### 4.2 The cache itself is suspect

Wrong content is being served, or you changed the key configuration. `SetMode(weir.ModeBypass, ttl)` makes every request a pass-through (still through the limiter and breaker, nothing stored, existing entries untouched; an unsafe request that succeeds still invalidates its target as usual). Then purge (4.3) and leave bypass when it is clean. Check `weir_not_stored_total` and the origin's `Vary` and `Cache-Control` first: most wrong-content reports are an origin that varies on something Weir does not key (R-1, R-5, R-6).

### 4.3 Purging

`Engine.Purge(ctx, weir.Purge{...})` takes `All`, `URLs` (absolute, rewritten like requests) and `Groups` (with `Origin`), in `PurgeSoft` (default) or `PurgeHard` mode.

- Soft: matching entries become stale at the purge time (with the memory store it can land up to 1 s late) and are revalidated with conditional requests. Inside the entry's `stale-while-revalidate` window clients keep getting the stale copy while a background refresh runs; with no such window (the default) the next request revalidates in the foreground and waits. Either way a purge of thousands of keys is bounded by coalescing per key and the limiter, and background refreshes never use the foreground reserve. Use it for content changes.
- Hard: matching entries are unusable at once, and clients wait for the origin. Use it for content that must not be served again (a leak, a takedown).

A same-origin 2xx or 3xx response to an unsafe method (`POST`, `PUT`, `DELETE`) also invalidates its target URI and `Location` and `Content-Location` URIs. `Cache-Group-Invalidation` from the origin soft-purges groups unless `CacheGroups.Ignore` is set. Purge errors reach you; a store that refuses a hard purge at its cap is not an outage.

### 4.4 Miss-rate anomaly

The log line `weir: miss-rate anomaly` and `weir_miss_rate_anomalies_total` mean that one partition (origin plus path) had at least 500 misses in a 10 s window at a miss ratio of 0.9 or more. Causes, in order of how often they happen:

1. A busy path whose responses are uncacheable (`private`, `no-store`) or always revalidated (`max-age=0`, `no-cache`). Every request is a miss by design. Route that path around Weir in your server (mount it on the mux beside the Weir handler); `Bypass` handles cookies and headers, not paths. It is not an attack and the warning will not go away until you do.
2. A cold path: a fresh deploy, a purge, a crawler walking many distinct URLs.
3. Cache busting with unique query strings (T-11). The per-path cap (`Limiter.MaxPerPartition`, 16) already bounds the damage; the flood cannot fill the shared queue.

`MissRate.Throttle` is off by default. Turn it on only after running without it and finding the warnings are cases 2 or 3. With it, an anomalous partition is capped at one origin fetch for the next window, for as long as it stays over both thresholds. Read the cost before you do (06 R-8):

- A cold path with far more distinct keys than a single fetch at a time can fill in a window stays capped. Measured: 300 000 distinct keys at 1 000 requests a second were capped in all 30 windows of the run.
- A client that sends max(500, 9 × the path's hits) distinct-query requests per window keeps that path capped for as long as it sends. In a review experiment, 60 requests a second let 390 of 1 200 legitimate misses through, against all 1 200 without `Throttle` (not a repo test).
- Sheds from a full global limiter count as misses, so a slow origin can get a busy, miss-heavy path capped in the next window.
- An attacker who floods every other window is never throttled.

`Throttle` trades the path's own misses for origin protection. If case 1 applies, fix it with a route, not with `Throttle`. `MissRate.MinMisses` and `MinRatio` tune detection, and `TopK` (64) bounds memory; a partition with under 1/`TopK` of a window's requests may go untracked.

### 4.5 Memory

`weir_store_bytes` should sit near the configured size once the cache is warm. If the process nears `GOMEMLIMIT`, look at `Storable.MaxObjectBytes` (1 MiB, headers included) and at large streamed bodies before you blame the cache: buffering for storage is bounded by `MaxConcurrent × MaxObjectBytes` (NFR-4, 64 MiB at defaults). A stream that exceeds `MaxObjectBytes` is not stored, but it holds up to `MaxObjectBytes + 1` bytes of read-ahead after its fetch slot is released, until the client reads it. Many slow clients on large responses therefore cost about one `MaxObjectBytes` each (1 MiB at defaults), beyond the bound above. The hit path's GC cost at 1M entries is in [benchmarks.md](benchmarks.md).

### 4.6 A remote store fails (Phase 2.5)

After 5 consecutive `ErrUnavailable` results the store breaker opens for 1 s, doubling to 30 s. While it is open, lookups are treated as misses and nothing is stored, so origin load rises to the uncached rate. The limiter and breaker still protect the origin. Epoch (purge) state may be lost with a store restart; run a hard purge of everything (`All: true`) after one if stale content matters (06 R-4).

## 5. Planned operations

- Warm start: `Engine.Warm(ctx, requests, origin)` fetches a list of requests at background priority (4 at a time, never using the foreground reserve) and skips keys that are already fresh. It returns counts of fetched, skipped, not-stored and failed. Run it after a deploy that empties the cache, from a goroutine that you own.
- Shutdown: stop the server first (`srv.Shutdown`), then `Engine.Close(ctx)`. `Close` cancels background work after the grace period in `ctx`, and requests that arrive later get `weir.ErrClosed` (503).
- Config changes: `weir.New` rejects an invalid `Config` with `ErrInvalidConfig`, naming the field. A running engine's config is fixed; build a new engine and swap it at your handler.

## 6. Errors clients see

| Status | Cause |
|---|---|
| 400 | request failed validation (bad host, path, escape, oversized target); no store or origin cost |
| 501 | CONNECT or a protocol upgrade reached `weirhttp.Handler` (`Middleware` passes them on) |
| 502 | origin error with no usable stale entry |
| 503 | shed (with `Retry-After`), breaker open with no usable stale entry, or the engine closed |
| 499 | the client went away; a log hint only, adapters write nothing |
| 504 | origin timeout, `must-revalidate` entry that could not be validated, or `only-if-cached` with nothing stored |

## 7. Deploying on Caddy (single node)

The `caddy` module ([08](08-caddy-adapter-spec.md)) puts the `weir` directive in front of `reverse_proxy`. This section is the checklist for one node. Setting names are in 08 §2.

### 7.1 One node per cache

With the default memory store, run exactly one Caddy node per cache (D17, T-38). Each node has its own memory store, so behind a load balancer a purge reaches only the node it was sent to and the others keep serving the old content. To run several nodes, put the cache on Valkey and follow section 8. Otherwise pin one node per site, or accept a different cache on every node and do not rely on purges.

### 7.2 A minimal site

```
{
	grace_period 15s
	servers {
		timeouts {
			read_body 10s
		}
	}
}

example.com {
	request_body {
		max_size 10MB
	}
	weir {
		name         site-a
		max_bytes    512MiB
		snapshot_dir /var/lib/caddy/weir
	}
	reverse_proxy app:8080 {
		header_up -X-Forwarded-For
	}
}
```

- `name` is required. It is the store identity across reloads, the `name` metric label, the admin URL segment and the snapshot file name, so pick it once and keep it.
- `request_body { max_size }` and the server `read_body` timeout are required on Weir routes (FR-LIM-7, T-39). Weir bounds origin concurrency, but a slow or oversized upload still holds a slot from the upload pool until it finishes. Size the limit to your largest legitimate upload.
- `snapshot_dir` must be absolute, owned by the Caddy user and not writable by group or others. Weir creates it with mode 0700 and refuses a shared one (a planted snapshot would be served as trusted cache content). The file is `<snapshot_dir>/<name>.weir`, written when the store closes and read at the next start. Without `snapshot_dir` every restart is a cold cache.
- The snapshot is written during shutdown, so give Caddy time: `grace_period` is the time Caddy waits for requests to drain before it stops, and the engine close and the store close can each take up to the 5 s snapshot timeout after that, so about 10 s more. A service manager that kills the process sooner (`TimeoutStopSec` in systemd, `terminationGracePeriodSeconds` in Kubernetes) loses the snapshot. The packaged `caddy.service` sets `TimeoutStopSec=5s`, which is too short: raise it above `grace_period` plus 10 s with a drop-in. Without `grace_period` Caddy waits for connections indefinitely, so the unit's kill timer is the only bound. A reload that replaces a large store waits about as long for the old config to stop (08 §3).
- `snapshot_dir` also holds `<name>.weir.keygen`, a 32-byte record of the forwarding settings the snapshot was stored under. Do not delete it as debris: without it the next start discards the snapshot. A start after a change to the `forward` block discards it too, on purpose (08 §3).

### 7.3 Order of handlers

Caddy sorts directives inside a site block by a fixed order, not by where you wrote them. In v2.11.7 `weir` sits directly before `reverse_proxy` and after `encode`, `request_header`, `basic_auth`, `forward_auth`, `handle` and `route`. Anything that must see a request before the cache does is therefore already outside it. Anything you want inside it needs a `route` block with an explicit order. If `reverse_proxy` is inside `handle { }` or `route { }`, put `weir` in the same block.

- **Auth before `weir`.** `basic_auth`, `forward_auth` and any identity handler must run before the cache, and a route that needs per-user responses should list its session cookie or header under `bypass { cookies ... }` (or `headers`) or rely on `Authorization` handling (FR-STO-5). The handler behind Weir sees the creating request's context values (T-45), so a variable that an auth handler put there can shape a response that is then stored and shared. Weir cannot key what it cannot see. Derive per-user output only from the forwarded request, or keep the route out of the cache (06 R-6).
- `rate_limit` before `weir`. This is the answer to distinct-path floods (06 R-2). The third-party `caddy-ratelimit` module registers its directive with the default order `before basic_auth` (its `caddyfile.go` on `master`, read 2026-10-10; check the version you build), so it is outside `weir` without any setting. Do not move it with a global `order` directive to after `weir`: a limiter behind the cache never sees hits and so cannot count them, and a flood of misses would be limited only after Weir had queued them. Verify the result on your build, not on this page:

  ```sh
  caddy adapt --config Caddyfile --pretty | grep -n '"handler"'
  ```

  The `rate_limit` handler must be listed above `weir`, and `reverse_proxy` below it. A site that uses `route { }` follows the order written inside the block.
- `encode`. Preferred: let the origin compress and leave `encode` out of cached routes. If the origin cannot, put `encode` outside `weir` (the default order does this, so a plain `encode` line works):

  ```
  example.com {
  	encode zstd gzip
  	weir {
  		name site-a
  	}
  	reverse_proxy app:8080
  }
  ```

  Weir sends the origin the normalized `Accept-Encoding` (one coding from `accept_encoding`, default `gzip`), so an origin that can compress will return compressed bodies and Weir stores those, one per bucket. Weir caches uncompressed bytes only when the origin does not compress, and then `encode` compresses every response, hits included, which costs CPU on each hit. The other arrangement puts `encode` inside, between `weir` and `reverse_proxy`, so Weir stores one compressed variant per `Accept-Encoding` bucket:

  ```
  example.com {
  	route {
  		weir {
  			name site-a
  			key {
				accept_encoding zstd gzip
			}
  		}
  		encode zstd gzip
  		reverse_proxy app:8080
  	}
  }
  ```

  Use it only when the compression CPU matters. Set `accept_encoding` to the encodings `encode` produces (the default is `gzip` only). Weir sends `encode` the single preferred listed coding, or `identity` when none qualifies, so an unlisted `br` is never used and `Accept-Encoding` reaches the entry key only through the origin's `Vary` (01 §5.2.3). List `Accept-Encoding` under `key { headers }` if storability differs by coding.

### 7.4 Per-client inputs behind the cache (T-4, T-45)

`reverse_proxy` adds `X-Forwarded-For` with the client address. If the origin changes a cacheable response by client address (geo pages), that is an input the key does not contain, and the first client's variant is served to everyone. On cached routes write `header_up -X-Forwarded-For` as above, or move the decision into the key.

The same holds for Caddy placeholders in any handler after `weir`. The request context's replacer was built from the original client request, so `header_up X-Real-IP {remote_host}` or `header_up X-Foo {http.request.header.Cookie}` sends the first client's data to the origin, on background refreshes too, and the response is stored for others. Do not use per-client placeholders (`{remote_host}`, `{http.request.header.*}`, `{http.request.cookie.*}`, `{http.auth.*}`, `{http.vars.*}`) in handlers after `weir` on cached routes. On its first request the handler logs a one-time warning for a `reverse_proxy` after `weir` that keeps `X-Forwarded-For`, and for a per-client placeholder in a later request header (best effort, same route only).

### 7.5 Memory

Stores without `max_bytes` share 40% of the memory limit (`GOMEMLIMIT`, which the `caddy` binary derives from the cgroup or system memory at start). Caddy provisions handlers one at a time and gives no look-ahead, so the split is uneven: each new auto-sized store takes half of what the live auto-sized stores have not claimed, with a floor of 160 MiB and a warning. One auto-sized site gets half of the budget (20% of the limit). The share is fixed for the store's life. A reload that adds an auto-sized site can push the total above 40% until the next restart, and `Provision` logs a warning when it does.

- Single-site node that wants more than 20%: set `max_bytes`.
- Several sites, or a BYOD control plane that adds sites by reload: set `max_bytes` on every site (T-43). The floor is 160 MiB, because the largest cacheable object is 10% of a shard. An explicit `max_bytes` below it is not clamped: `caddy validate` rejects it.
- A custom `main` that skips the memory-limit setup falls back to 256 MiB per store with a warning.

### 7.6 Hosts that share a site

A site that serves more than one host, or uses on-demand TLS, needs `multi_host` in the `weir` block. It turns on the per-host origin and store caps at 25% (FR-FAIR-3), so one tenant cannot fill the queue or the cache. Caddy does not tell a handler its route's host list, so Weir cannot detect this; a second distinct `Host` while `multi_host` is off logs one warning. Tenants on different upstreams are different sites with different `name`s, or one failing upstream opens the shared breaker for all.

`multi_host` is part of the store identity. Going from one host to two therefore starts a new, empty store once, a deliberate flush that makes sure the cap is never missing. Plan the change for a quiet hour. Adding a third host, or any host to a site that already has `multi_host`, changes nothing, and neither does a reload that adds a domain. Changing `max_bytes` or `snapshot_dir` also starts a new store. Tightening `forward` settings writes a hard epoch (a cold flush) on purpose (08 §3).

### 7.7 Admin API and purges

Purge, mode and stats are on Caddy's admin endpoint (`admin.api.weir`); no purge route is exposed on site listeners. The admin API has no authentication of its own. It listens on `localhost:2019`, which every local user and every container sharing the network namespace can reach; Caddy always checks the `Host` header, and checks `Origin` only when the request carries one. On a shared host listen on a unix socket instead (`admin unix//run/caddy/admin.sock|0660` in the global options) and give the group only to the operators. If you must call it from another machine, put it behind an SSH tunnel or a reverse proxy that authenticates (it must send a `Host` that matches the admin `origins` list, or Caddy answers 403), and never expose it unauthenticated (T-26): anyone who can reach it can flush the cache or switch the engine into bypass.

```sh
# soft purge of two URLs (stale, revalidated on next request)
curl -s -X POST localhost:2019/weir/site-a/purge \
  -H 'Content-Type: application/json' \
  -d '{"urls":["https://example.com/a","https://example.com/b"]}'

# hard purge of everything, deleting the records now
curl -s -X POST localhost:2019/weir/site-a/purge \
  -H 'Content-Type: application/json' \
  -d '{"mode":"hard","all":true,"eager":true}'

# purge by group tag (origin is scheme://host[:port] and is required with groups)
curl -s -X POST localhost:2019/weir/site-a/purge \
  -H 'Content-Type: application/json' \
  -d '{"groups":["product-42"],"origin":"https://example.com"}'

# ride out an origin outage for 30 minutes, then go back
curl -s -X POST localhost:2019/weir/site-a/mode \
  -H 'Content-Type: application/json' -d '{"mode":"stale-on-error","ttl":"30m"}'
curl -s -X POST localhost:2019/weir/site-a/mode \
  -H 'Content-Type: application/json' -d '{"mode":"normal"}'

# in-flight, queue depth, breaker state, store bytes
curl -s localhost:2019/weir/site-a/stats
```

A purge body that names nothing is a 400. A reply of 202 means the epochs are written and the entries are unreachable; `eager` adds the number of records scrubbed. A name with no live engine is a 404. Soft and hard are explained in 4.3, and modes in 4.1 and 4.2. The `caddy` module registers the `observe/prom` metrics (section 2) on Caddy's own registry (08 §8), with a `name` label on every `weir_*` series. They are served by Caddy's `metrics` handler or the admin endpoint's `/metrics`.

### 7.8 Before you go live

1. `caddy validate --config Caddyfile --adapter caddyfile` on the final Caddyfile (it runs `Provision`, so a bad `weir` block fails here). `validate` builds the store for real: it creates `snapshot_dir`, loads and rewrites the snapshot and its `.keygen` record, and a changed `forward` block discards the snapshot. Run it as the user Caddy runs as (as root it creates a root-owned directory that Caddy then refuses), and on a node that is already serving, validate a copy whose `snapshot_dir` is a scratch directory.
2. `caddy adapt --pretty` and check the handler order (7.3).
3. One request twice with `curl -si`: the second shows `Cache-Status: Weir; hit`.
4. A purge on a test URL (7.7), wait 2 s (a soft purge on the memory store can land up to 1 s late), then a request showing `fwd=stale` or `fwd=uri-miss`.
5. Stop and start the service and confirm the snapshot file `<snapshot_dir>/<name>.weir` exists and the first request after the start is a hit.

## 8. Deploying on Caddy (several nodes, one Valkey)

Several Caddy nodes share one cache when each site has a `store valkey` block ([08](08-caddy-adapter-spec.md) §2) pointing at the same server and the same `prefix`. Entries and purge epochs live on the server, so a purge sent to any node is seen by all of them. `TestE2ETwoNodePurge` runs two Caddy processes against one Valkey and checks exactly that. Section 7 still applies to every node; this section adds what sharing changes. Setting names for the store are in 05 §7.

```
example.com {
	weir {
		name    site-a
		store valkey {
			addrs  valkey.internal:6379
			prefix site-a
		}
	}
	reverse_proxy app:8080 {
		header_up -X-Forwarded-For
	}
}
```

### 8.1 One Valkey and one prefix per cache

- All nodes of a cache use the same `addrs` and the same `prefix`. Set `prefix` explicitly. It defaults to the site `name`, which keeps two sites on one server apart, but then `name` must be identical on every node or the nodes share nothing. Two caches that must not see each other's purges need two prefixes (or two servers).
- Every node that shares a `prefix` must also agree on `max_retention` and `max_clock_skew` (05 §7): the prune margin comes from the node that writes, so a node with a shorter retention prunes hard epochs that another node's entries still depend on.
- Give the cache its own server. Epoch keys have no TTL, and `maxmemory` is server-wide, so another application that writes TTL-less keys to the same server fills it with keys the eviction policy cannot remove.

### 8.2 Eviction policy

The server's `maxmemory-policy` must be `volatile-lfu` (recommended), another `volatile-*` or `noeviction`. Every entry has a TTL, so a `volatile-*` policy evicts entries only. `allkeys-*` could evict an epoch key, and a lost epoch is a purge that silently did not happen (T-29). The store checks the policy on connect, on every node the client knows, and stays unavailable (the store breaker opens, requests go to the origin) on `allkeys-*`.

`skip_policy_check` turns the check off. It is for managed services that disable `CONFIG`. If you set it, you own the guarantee: set the policy in the provider's console and verify it there, because Weir can no longer notice a wrong one.

### 8.3 Clocks and the purge window

Epochs compare the clock of the node that purged with the clock of the node that fetched, stored as Unix seconds rounded up. Keep node clocks within `max_clock_skew` (default 1 s) with NTP or chrony. The store adds the skew to its comparisons, which can only purge a little too much, never too little. With rounding the window is up to `max_clock_skew` plus 1 s, which with the default is the 2 s of 05 §4.3: a response fetched within 2 s after a purge is purged again, so a URL purged every second never stays cached. If every node has one clock source, `no_clock_skew` removes the skew and leaves only the rounding second. The first lookup on a prefix that has no epoch state yet (a new cache, or one whose server lost its data) writes a global hard epoch at the server's time, so entries fetched in the first 2 s of a new cache are refetched once. A purge itself is seen by the other nodes at once (a future-dated epoch counts as now, 05 E-7); the window only re-purges responses fetched just after it. So when you test a cache, wait 3 s after the first request on a new prefix, and after a purge do not refill the URL and expect it to stay cached for the next 2 s.

If clocks drift past `max_clock_skew`, a node whose clock runs behind can serve an entry that a faster node purged. Alert on clock offset; it is not visible in Weir's metrics.

### 8.4 Hard purges, replication and failover

Valkey replicates asynchronously, so a failover to a lagging replica can lose an acknowledged purge, and a restart from an old RDB or AOF can lose all purges since the last sync (05 §7, T-29). A soft or invalid purge cannot be protected. For hard purges, `hard_epoch_wait` (default off) issues `WAIT 1 <ms>` after each hard-epoch write, so the purge is acknowledged by a replica before `Purge` returns. The cost: with no replica, or the replica down, `WAIT` blocks for the full time, and a hard purge over N tags takes N times that. Set it only on a server that has a replica, to a few tens of milliseconds, and keep the admin client timeout above `N * hard_epoch_wait`.

After a failover, or after restoring Valkey from a backup, assume purges were lost and purge the cache by hand (`{"mode":"hard","all":true}` on any node).

### 8.5 Cluster mode and slot concentration

With `cluster`, entry keys spread over all slots by default and only the epoch keys share one hash tag (`hash_tag`, default `e`), so one script touches one slot. All epoch traffic, which every lookup reads, therefore lands on the primary that owns that slot. `co_locate_entries` puts the entries in the same slot: the cache's keys then share one slot, but the whole cache then lives on one primary, which limits its size to that node's memory and its throughput to that node. Leave it off for caches that outgrow one primary. Whichever you pick, set it identically on all nodes. Changing it later makes the old entries unreachable (they age out).

### 8.6 Settings that must match on every node

The adapter cannot compare nodes. A reload on one node that changes `forward` writes a hard epoch to the server, which flushes the cache for all nodes. A restart does too, because the server keeps a key-generation record at `<prefix>:keygen` (08 §3): the first node that starts with the new rules replaces the record and writes the hard epoch, and the nodes that start later find an equal record and write nothing. A node still on the old rules that restarts or reloads afterwards replaces the record again and purges again, and the warning in its log says so. A start with the server unreachable skips this check (the log says `key-generation record not checked`), so purge by hand if you tightened `forward` while the server was down. So, when you tighten `forward` on a shared cache:

1. Roll the new config to every node (a mixed fleet stores entries under two different rules, and the looser node can serve what the tighter one would never have stored).
2. The first restarted node purges for you. Purge by hand only if that node started while the server was down, or to be sure. Purge by hand once: `{"mode":"hard","all":true}` on any node. The hard purge alone makes old entries unservable. `eager` only reclaims their memory and, on Valkey, scans the whole keyspace (05 §7), so add it only off-peak.

`stale` and key settings that change which entry a request maps to (query rules, key headers and cookies, normalization) must also match. A node with different key settings misses on, or worse, reads a different bucket of, the same URL, but it never serves another key's entry, because the forwarded request equals the keyed request (INV-1).

### 8.7 When Valkey is down

The store has a breaker (FR-STF-2): after 5 consecutive store errors it opens for 1 s, doubling to 30 s. While it is open every lookup is a miss and nothing is stored, so the node behaves as a cache-less proxy in front of the origin. Request coalescing, the origin limiter and the origin breaker stay on (FR-STF-3), so a Valkey outage is not an origin stampede, but hit ratio and stale-while-revalidate protection are gone until the store recovers. A purge sent while the server is down fails with an error (the operator sees it and retries). A node starting with the server down does not fail its config load. It serves as a cache-less proxy; the first requests each wait for the dial (up to `call_timeout`) until 5 failures open the breaker. A config reload that changes `forward` needs the server up (the epoch must be written). A start skips the key-generation record check and logs a warning (08 §3).

Watch the `weir_store_*` series (section 2) and alert on a store breaker that stays open.

### 8.8 Shutdown order

A node closes its store when Caddy stops it, and it does not touch other nodes' data: a restart of one node must not flush the shared cache, and it does not. Stop or drain Caddy nodes first, then Valkey. Stopping Valkey first only opens the breakers while the nodes drain, and requests in that time go to the origin. On a rolling restart, take nodes out of the load balancer, restart one at a time and check `Cache-Status: Weir; hit` before the next. There is no snapshot with a Valkey store: the cache is the server's.

### 8.9 Before you go live

1. `caddy validate` on every node's config. It builds the store lazily: a wrong address passes validation and shows up as an open breaker (8.7), so also run the next step.
2. On a new cache, wait 3 s after the first request (8.3). Then request a URL once through node A; the store write happens after the response, so repeat until A shows `Cache-Status: Weir; hit`, and only then request it through node B. B shows `hit` too.
3. A purge on node A (7.7), then a request through node B showing `fwd=stale` or `fwd=uri-miss`.
4. `CONFIG GET maxmemory-policy` on the server shows a `volatile-*` policy or `noeviction`.
