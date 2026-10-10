# Weir Caddy adapter specification

Status: v1.0. Phase 2. Finalized by P2-00 against the Caddy release current at the start of Phase 2; every Caddy API named here is checked with file and line in §11.
Date: 2026-10-11
Depends on: [01-technical-spec.md](01-technical-spec.md), [04-lld.md §10](04-lld.md)
Seed name: `04-caddy-adapter-spec.md` (renumbered, see [docs/README.md](README.md))
Verified against: Caddy v2.11.7 (released 2026-10-03, `go 1.26.0` in its `go.mod`). Weir requires Go 1.27, so `xcaddy` builds that include it need a Go 1.27 toolchain; Go's toolchain directive downloads it automatically when `GOTOOLCHAIN=auto`.

## 1. Shape

- Module: `github.com/AshwinSathian/weir/caddy`, its own `go.mod`, requiring `github.com/caddyserver/caddy/v2` and the root `weir` module.
- Built into Caddy with `xcaddy build --with github.com/AshwinSathian/weir/caddy`.
- Module ID: `http.handlers.weir`. Caddyfile directive: `weir`.
- Implements `caddy.Module`, `caddy.Provisioner`, `caddy.Validator`, `caddy.CleanerUpper`, `caddyhttp.MiddlewareHandler`, `caddyfile.Unmarshaler`, with interface guards.
- Registered with `httpcaddyfile.RegisterHandlerDirective("weir", parse)` and `httpcaddyfile.RegisterDirectiveOrder("weir", httpcaddyfile.Before, "reverse_proxy")` so the directive works without a global `order` option. `RegisterDirectiveOrder` is marked EXPERIMENTAL in Caddy's source; if it changes, the fallback is documenting `order weir before reverse_proxy` in the global options block. In Caddy's default order `encode` comes before `reverse_proxy` (§11), so `weir` lands between them: `encode` is outer by default, the first case in §5.

## 2. Configuration surface

JSON mirrors `weir.Config` with snake_case names. Durations use Caddy's `caddy.Duration`. Example Caddyfile:

```
example.com {
	weir {
		name       site-a          # required; store identity across reloads (§3)
		max_bytes  512MiB          # memory store size
		key {
			query_drop utm_* fbclid gclid
			query_sort
			headers    Accept-Language
			cookies    currency
			accept_encoding br gzip
		}
		forward {
			allow X-Request-Id
		}
		bypass {
			cookies session_id
		}
		limiter {
			max_concurrent    128
			max_per_partition 16
			max_queue_wait    2s
		}
		stale {
			if_error 5m        # operator default, off unless set (D6)
		}
	}
	reverse_proxy app:8080
}
```

Every field is optional and defaults to the engine default, except `name` (and `addrs` inside a `store` block). `Provision` builds the engine with `weir.New`, so an invalid config fails at provisioning, which `caddy validate` also runs (validate therefore builds the store: it creates `snapshot_dir` and may rewrite the snapshot and its `.keygen` record, P2-07) (Caddy calls `Validate` after `Provision`, `context.go:423`, so `Validate` only re-checks the adapter's own fields). A failed `weir.New` leaves nothing to close.

Keys the cards implement (a card that needs another key adds it here in the same PR):

| Key | Meaning |
|---|---|
| `name` | required; `[A-Za-z0-9._-]{1,64}`, not starting with a dot (`.` and `..` would collapse in the admin URL); used in the admin URL, as a metrics label and in the snapshot file name, so nothing else is accepted |
| `max_bytes` | store size; unset means auto-sized (§7, Memory). At least 160 MiB when set: the largest cacheable object is 10% of a shard (05 §5.1) and there is no key for `Storable.MaxObjectBytes` yet (16 shards x 10 x the 1 MiB default) |
| `snapshot_dir` | optional directory; the snapshot file is `<snapshot_dir>/<name>.weir` (FR-SNP-1) |
| `multi_host` | boolean, off by default; set it when the site serves more than one host or uses on-demand TLS. It turns on `Limiter.MaxPerHost` and the store's `MaxBytesPerOwner` at 25% (FR-FAIR-3) and is part of the store identity (§3). The handler cannot detect this itself: Caddy gives a module no view of its route's host matchers (`ctx.ancestry` is private and sibling routes are not provisioned yet), and a scan of the whole http app would switch the caps on for unrelated single-host sites and flush their stores. The operator or control plane states it (P2-02) |
| `store` | optional; `store valkey { ... }` replaces the memory store with a shared Valkey store (05 §7). Absent means the memory store. Keys: `addrs` (one `host:port`, or several seed nodes with `cluster`; anything else, URLs and `user:pass` included, is refused and reported by index only), `username`, `password` (both accept `{env.VAR}`, resolved at `Provision`, so the stored config can hold the placeholder; `{$VAR}` is expanded into the adapted JSON, `GET /config/` and Caddy's autosave file, which then contain the secret), `tls`, `cluster`, `prefix` (defaults to the site `name`, so sites on one server never share a keyspace or an epoch; the library default `weir` is not used), `hash_tag`, `co_locate_entries`, `max_retention`, `max_clock_skew`, `no_clock_skew`, `max_hard_epochs`, `call_timeout`, `hard_epoch_wait`, `skip_policy_check`; each is the `valkey.Config` field of the same name and defaults to it. JSON is `"store": {"type": "valkey", ...}`. `max_bytes` and `snapshot_dir` size and persist the memory store, so they are an error beside a `store` block. `tls` turns TLS on with system roots, the server name taken from each address and TLS 1.2 or later; there is no key for a private CA yet. The secrets appear in no error or log line (bad addresses are reported by index, and Caddyfile errors for `username`, `password` and the flags never quote a token). A bad block fails `Provision`, so `caddy validate` fails; an unreachable server does not, because the store connects on first use and an outage opens the store breaker (FR-STF-2) |
| `key` | `query_drop`, `query_keep`, `query_sort`, `normalize_path`, `headers`, `cookies`, `accept_encoding` (as `weir.KeyConfig`) |
| `forward` | `allow`; the mode stays strict, `ForwardAll` has no JSON form (D4) |
| `bypass` | `cookies`, `headers` |
| `limiter` | `max_concurrent`, `max_queue`, `max_queue_wait`, `max_per_partition` |
| `stale` | `while_revalidate`, `if_error` (operator defaults, off unless set; D6) |

`max_bytes` takes a number of bytes or a string such as `512MiB` (P2-01 parses it, up to 1 PiB; P2-02 applies it). `snapshot_dir` may not contain braces or NUL (placeholders are not expanded) and is must be absolute, is created with mode 0700 when the store is built, and is refused when group or others can write it or when it belongs to another user (a planted snapshot would be decoded as trusted cache content; the owner check is unix only); `/data` and `/data/` are one store. `max_bytes` below 160 MiB is rejected by `Validate`: the largest cacheable object is 10% of a shard (05 §5.1) and the adapter has no key for `Storable.MaxObjectBytes`, so smaller stores cannot hold the default 1 MiB object. Unknown keys fail the load, because Caddy decodes module config strictly.

Caddyfile rules (P2-01b): the `weir` directive takes a block and no arguments, and an optional matcher token (`weir /api/* { … }`) limits which requests go through the cache. Each key may appear once per block, so list values go on one line; a repeat is an error, never a silent overwrite. `key`, `forward`, `bypass`, `limiter` and `stale` need a block with at least one key (a bare key or empty braces is a typo, not a no-op). Values are read as written: `{$VAR}` is expanded by Caddy's lexer before parsing, but runtime placeholders such as `{env.X}` or `{host}` are not expanded, so `name` rejects them and `snapshot_dir` would keep them literally (P2-02 either rejects braces there or expands them). Errors name the Caddyfile line; a missing `name` is reported at the directive, an invalid one at the `name` line.

Keys and blocks map through an adapter-side struct, not straight onto `weir.Config`, whose `Rand`, `Observer`, `Logger` and `Store` fields have no JSON form. The adapter sets those itself.

## 3. Engine lifetime across config reloads

Caddy starts new module instances before stopping old ones on every config change ("multiple loaded instances of your module may overlap", Caddy's extending guide). A naive adapter would build a fresh engine and store on every reload, which is a full cold flush (seed T6.4) every time the config changes. For the BYOD custom-domain project that shares the Caddy instance, config changes can be frequent.

Rule: the expensive state is the store, so the store is what survives reloads. The adapter keeps memory stores in a package-level `caddy.UsagePool` keyed by `name` plus the store settings (`max_bytes`, shard count). `Provision` calls `LoadOrNew` on that pool and builds a new `weir.Engine` around the shared store (engines are cheap: a limiter, a breaker and some maps). `Cleanup` closes the old engine, which does not close a store it did not create, and calls `Delete` on the pool, which closes the store only when the last user releases it. The pooled value must implement `caddy.Destructor` (`Destruct() error`, the constructor returns one); `Delete` calls it outside the pool lock, so a snapshot written by `Store.Close` does not block other `LoadOrNew` calls. `Cleanup` and `Destruct` take no context (modules.go, usagepool.go), so they bound the close with the store's `SnapshotTimeout` (5s by default) and the engine close with the same duration. An extra `Delete` is a silent no-op after full release, and while another instance still holds a reference it takes that instance's reference, which can close the store under a live engine during a reload overlap. `Cleanup` therefore deletes exactly once, and only after a successful `LoadOrNew`.

Consequences, accepted:

- A reload that changes the store size starts a new, empty store. That is rare and deliberate.
- During the overlap window both the old and new engine have their own limiter, so origin concurrency can briefly reach twice `max_concurrent`. The old engine receives no new requests after the switch, so the overlap is bounded by its in-flight fetches.
- In-flight flights do not transfer: a request arriving at the new engine for a key the old engine is fetching starts its own fetch. At most one duplicate fetch per key per reload.
- Key-generation hash (OQ-C1, amended by P2-00): the adapter hashes the settings that change what an unchanged key means, namely `Forward.Mode`, `Forward.Allow` and `Storable.StripSetCookie`, and stores the hash beside the pooled store. When a reload changes it, the new engine writes one global hard epoch. OQ-C1 first chose a soft epoch, but a soft-purged entry stays servable inside its SWR and stale-if-error windows (FR-PRG-2), so tightening forwarding (for example `ForwardAll` to a strict mode, where cookies reached the origin and personalized content may have been stored, R-3) would keep serving it. These edits are rare and security relevant, so the safe side (rule 12) is a cold flush. Entries the old engine stores during the overlap, after the epoch, are reachable again; that window is bounded by the old engine's in-flight and routed requests and is accepted. Sites that share a `name` must agree on the hash (next bullet). The hash is kept in memory beside the pooled store (with the list of engines that committed one) and, when `snapshot_dir` is set, in `<snapshot_dir>/<name>.weir.keygen` (32 bytes, mode 0600). Building a store deletes `<name>.weir` first when that record is missing or differs, and the store that writes the final snapshot rewrites the record just before, so the record always describes the snapshot beside it. A restart, a site that was removed and re-added, or a hash change together with the store settings therefore never loads entries stored under the old forwarding rules. The hash is recorded only after the new engine is built and the hard epoch is written, so a `Provision` that failed halfway does not make a retry of the same config skip the purge. If a reload that changed the hash is rolled back (its engine is cleaned up while the older one keeps the store), the store returns to the older engine's hash, so re-applying the tighter config purges again; entries the rolled-back engine stored during the overlap are purged with it. Settings that only change which key a request maps to (query rules, key headers and cookies, path normalization, experiments) are excluded: their old entries become unreachable and age out without a purge. Host lists, on-demand TLS domains and everything else a BYOD control plane changes on reload are excluded, so adding a domain never purges the cache.
- `name` is required (OQ-C2, resolved). Two site blocks with the same `name` share a store on purpose. Within one config load they must agree on the store settings and on the key-generation hash, or the second `Provision` fails. The check is scoped to a load (a map from name to settings, keyed by that load's metrics registry pointer, dropped with the last handler of the load) because `Validate` takes no context and a resize reload legitimately has two settings for one name alive at once.
- A site with a `store valkey` block pools its store the same way, keyed by `name` and a SHA-256 digest of every effective store setting (defaults filled in, secrets resolved, so an explicit default equals an unset field), so a changed address, credential or option builds a new store (a new client; the server's data is untouched) and the key never holds a secret. The key-generation hash also covers the digest when a store block is present (a memory site keeps the hash it had). `max_bytes`, `snapshot_dir` and the owner cap are memory-store settings and are not part of such a key. A newly built Valkey store does not write an epoch by itself: the server's state belongs to every node that uses it, so a restart of one node must not flush the cache of the others. The exception is a reload in this process: when a store setting changes (a new pool entry on the same server) and the forwarding hash differs from the engine it replaces, the new engine writes the hard epoch (R-3). Nodes that share a `prefix` must therefore be configured with the same `forward`, `stale` and key-generation settings; the adapter cannot check that across nodes (P25-07b documents it). There is also no key-generation record on the server (the memory store keeps one beside its snapshot), so tightening `forward` and then restarting every node, instead of reloading, leaves entries stored under the old rules servable; purge the cache by hand after such a change. A reload that changes the forwarding hash writes the hard epoch (R-3), so it needs the server: with Valkey down `Provision` fails on that one change, the safe side, while every other reload succeeds.
- Store-level settings are fixed when the store is built: `max_bytes`, `snapshot_dir`, `MaxBytesPerOwner` and the eviction callback. The pool key is `name`, `max_bytes` as configured (zero means auto), `snapshot_dir` (so a changed directory builds a store that loads from it) and one boolean, "owner cap on", which is the `multi_host` key (FR-FAIR-3: more than one host or on-demand TLS). A site that goes from one host to two therefore starts a new store once, deliberately, so the fairness cap is never silently missing; further hosts change nothing. Auto-sized stores keep the size computed when they were built (§7, Memory).
- Snapshots: the new store loads `<name>.weir` at `Provision`. A store that a newer store with the same `name` has superseded (a resize reload) skips its snapshot in `Destruct` (its old file in a different `snapshot_dir`, if any, stays as it was), so the final writer is the live store at shutdown. If the newer store is torn down first (the reload rolled back), the older one is the writer again.
- `Cleanup` can take up to the snapshot timeout twice (engine, then store, 5s each by default) inside Caddy's config-change path, so a reload that replaces a large store may wait about 10s for the old config to stop. The two closes may run in parallel; the card decides.

## 4. Origin implementation

`ServeHTTP(w, r, next)`:

0. If the request is `CONNECT` or a protocol upgrade (FR-UPG-1), call `next.ServeHTTP(w, r)` and return; WebSockets never touch the engine. Caddy's own detector (`upgradeType` in `reverseproxy.go`) is unexported and ignores `CONNECT`, so the adapter uses `internal/keys.IsUpgrade`, the function `weirhttp.Middleware` uses. Go's `internal` rule is path-based, so `github.com/AshwinSathian/weir/caddy` may import it although it is a separate module; the module skeleton card (P2-01) proves it compiles.
1. `req := weirhttp.RequestFrom(r)`.
2. `origin := nextOrigin{next: next, base: r}`.
3. `resp, err := engine.Serve(r.Context(), req, origin)`.
4. On error: set `Retry-After` from `weir.RetryAfter` on `w`, then return `caddyhttp.Error(weir.StatusCode(err), err)` so the operator's `handle_errors` routes apply (D34). Verified in v2.11.7 source (§11): the error path writes the status on the same `ResponseWriter`, so headers set before returning survive.
5. Otherwise write `resp` with `weirhttp.WriteResponse` and return nil.

`nextOrigin.Fetch(ctx, fwd)` clones `base` with `base.Clone(ctx)`, replaces method, URL path and raw query, headers and body with the forwarded request's, and calls `next.ServeHTTP` with a response writer that streams into an `io.Pipe` (the `weirhttp.HandlerOrigin` shape). It returns as soon as the downstream handler writes headers.

Constraints this places on Phase 1, all already met:

- `Fetch` may run after the triggering request finished (SWR, early refresh). `ctx` carries the original request's values but not its cancellation (FR-COA-9), so Caddy's replacer and vars remain available through `ctx.Value`. The clone must not touch `base`'s original `ResponseWriter`.
- `reverse_proxy` adds `X-Forwarded-For`, `X-Forwarded-Proto` and `X-Forwarded-Host` when proxying. `X-Forwarded-For` is per-client. See §6.

## 4a. Deployment topology

Phase 2 supports exactly one Caddy node per cache (D17, T-38). Purges, snapshots and the memory store all assume it. Running several nodes behind a load balancer before the Valkey store (Phase 2.5) exists means each node has its own cache and a purge reaches only the node it is sent to; the adapter docs state this as unsupported.

## 4b. Multi-host sites (BYOD)

One engine serves every host of a site (OQ-C3, resolved): the tenants share one origin, so they share the breaker and the global limiter. Fairness comes from two caps that the adapter turns on at 25% when the site sets `multi_host`, meaning more than one host or on-demand TLS (FR-FAIR-3; §2 says why the operator states it): `Limiter.MaxPerHost` and the memory store's `MaxBytesPerOwner`. With a `store valkey` block only the limiter cap applies and `Provision` logs a warning. Tenants that run on different upstreams must be configured as different sites with different `name`s, or a failing tenant upstream would open the breaker for all.

## 5. Ordering with other handlers

- `encode` (compression): if `encode` runs before `weir` (outer), Weir forwards the normalized `Accept-Encoding`, so an origin that compresses returns compressed bodies that Weir stores per bucket; it caches uncompressed bytes only when the origin does not compress, and `encode` then compresses every response, including hits. Simple and CPU-bound. If `encode` runs after `weir` (inner, between Weir and `reverse_proxy`), Weir caches compressed variants keyed by the `Accept-Encoding` bucket. Default recommendation: let the origin compress and leave `encode` out of Weir-cached routes, or place it outside `weir` when the origin cannot. Documented with both examples.
- `rate_limit` (third-party `caddy-ratelimit`): place before `weir` to answer residual risk R-2 (distinct-path floods). Verified by P2-07 against `caddyfile.go` on `master` of `mholt/caddy-ratelimit` (2026-10-10, no tag pinned): it registers `rate_limit` ordered `before basic_auth`, so it is outside `weir` by default; the runbook (§7.3) gives the `caddy adapt` check.
- `forward_auth` or other auth: must run before `weir`, and routes that need per-user responses should use `bypass` rules or rely on `Authorization` handling.
- Directive order applies inside each block: a `reverse_proxy` inside `handle { }` or `route { }` needs `weir` in the same block. `intercept`, `templates` and `request_header` come before `weir` in the default order, which is the safe side for T-45.

## 6. Per-client headers added downstream (T-4)

Weir forwards a sanitized request, but `reverse_proxy` then adds `X-Forwarded-For` with the client address. If the origin changes cacheable responses based on client address (geo pages), that is an unkeyed input. The adapter documentation must say this plainly and give two options: configure `reverse_proxy` with `header_up -X-Forwarded-For` on cached routes, or move the decision into a key dimension (Phase 3). The same rule covers Caddy placeholders on any later handler. The request context's replacer was built from the original client request, and `base.Clone(ctx)` keeps it, so `header_up X-Real-IP {remote_host}` or `header_up X-Foo {http.request.header.Cookie}` behind `weir` sends the original client's data to the origin on a cacheable route, including on background refreshes. The docs tell operators not to use per-client placeholders in handlers after `weir` on cached routes; threat T-45 and risk R-6 gain this case when 06 is next revised (a follow-up, not part of P2-00). On the first request the adapter finds its own route in the http app (by handler identity, subroutes included) and logs one-time warnings when a `reverse_proxy` follows `weir` without `header_up -X-Forwarded-For`, and when a `reverse_proxy` or `header` handler after `weir` sets a request header from a per-client placeholder (best effort; it cannot run at provision time, because the route does not hold the handler until the http app has provisioned, and it sees only handlers in the same route). A second distinct `Host` while `multi_host` is off logs one warning naming `multi_host`; the handler remembers only the first host. An error returned by `next` with a 4xx status (`file_server`'s 404) becomes an ordinary empty-bodied response with that status, so missing paths cannot open the breaker; any other error reaches the engine as an origin error (502) whose message is the fixed text `next handler failed`, with the cause logged at debug level, so `{http.error.message}` never shows upstream addresses. The adapter keys and forwards `r.URL` instead of `RequestURI` when it differs from Caddy's saved original request (`rewrite`, `handle_path`, `uri strip_prefix` before `weir`), and hands each fetch a private copy of Caddy's variable table, an unlocked map. A client that is already gone (499) returns no error and runs no `handle_errors` route.

## 7. Purge over HTTP

Purge is available only through the admin API (decision confirmed 2026-09-27). An admin API module `admin.api.weir` registers routes under Caddy's admin endpoint (local-only by default):

- `POST /weir/<name>/purge` with a JSON body matching `weir.Purge` (including `eager` for hard purges). Returns 202 once epochs are written, with the scrubbed count when `eager` was set.
- `GET /weir/<name>/stats` returning `EngineStats`.
- `POST /weir/<name>/mode` with `{"mode": "stale-on-error"|"bypass"|"normal", "ttl": "30m"}` calling `SetMode` (D33).

JSON shapes (P2-05). Purge body: `{"mode":"soft"|"hard","all":bool,"urls":[...],"origin":"...","groups":[...],"eager":bool}`, `mode` defaults to soft, unknown fields are rejected, and a body that names nothing (no `all`, `urls` or `groups`) is a 400 although the engine would accept it as a no-op. The reply is 202 `{"status":"accepted"}`, plus `"scrubbed": n` when `eager` was set. `Engine.Purge` returns only an error, so the count comes from the `Status` of the `EvPurge` events with reason `hard` that the engine emits to a small per-engine observer (`purgeTap`), read around a call that is serialized per engine by a context-aware semaphore, so a request that is cancelled while it waits gives up (P8). Events with reason `group` (an origin's `Cache-Group-Invalidation`) carry a Status too and are ignored, so origin data cannot move the number; a non-eager hard purge reports Status 0 and only this endpoint issues an eager one. P2-06 must chain its own observer behind `purgeTap`, not replace it. A store that cannot scrub answers 202 with a `warning` and no count (the epochs are written). Mode body: `{"mode":"...","ttl":"30m"}`; `ttl` is required except for `normal`, where it is ignored. Duplicate JSON keys follow Go's decoder (last wins). Stats reply: `[{"inflight":n,"queued":n,"breaker_state":"closed"|"half-open"|"open","store_bytes":n}]`, oldest engine first, `store_bytes` -1 when the store reports none. Status mapping: invalid input 400, body over 1 MiB 413, unknown name or action 404, wrong method 405 with `Allow`, `ErrClosed`, `store.ErrUnavailable` and a cancelled request 503 (a purge skips a closed engine and tries the next), anything else 500. A failure inside an eager purge's scrub happens after the epochs are written, so its message says the epochs are written, the entries are unreachable, and how many records were deleted before the failure; repeating the call is safe.

Registration (verified, §11): `admin.api.weir` is an `AdminRouter`. Caddy replaces the admin server on every config load: `provisionContext` calls `replaceLocalAdminServer`, which builds and provisions every `admin.api` module again with the new load's context before the apps (including `weir`) are provisioned. A router therefore cannot hold an engine, because the engines of its own load do not exist yet. The adapter keeps a package-level registry from `name` to the set of live engines with that name. `Provision` adds its engine; `Cleanup` removes that engine by identity. A failed load (a later module fails, Caddy cancels the half-built config and runs `Cleanup`) therefore removes only its own engine and leaves the serving one registered. A request for a `name` with no live engine gets 404.

Semantics for several live engines under one `name` (shared store, or a reload overlap): `purge` goes through every live engine and skips a closed one (a reload that changes `max_bytes`, `multi_host` or `snapshot_dir` builds a second store under the same name, so a purge sent through one engine would leave the other serving what was purged; epochs are idempotent, and on a shared store the second eager scrub finds nothing; counts are summed, and a purge on which every engine is closed is a 503); `mode` is applied to every live engine of the name, so the old and new engine of an overlap agree; `stats` returns a JSON array with one `EngineStats` per live engine. An engine mid-cleanup answers `ErrClosed`, which the admin handlers map to 503.

Bounds (rule 5, NFR-3): the request body is capped with `http.MaxBytesReader` at 1 MiB, a purge may carry at most 1000 URLs and 100 groups, and anything over is a 413 or 400 before any epoch is written. Caddy's admin origin and host enforcement applies to every method, so the GET endpoint is covered as well.

Memory: stores without `max_bytes` share 40% of `GOMEMLIMIT` (FR-MEM-1). Caddy provisions handlers one at a time and offers no look-ahead, so no handler knows how many auto-sized sites the load has; the split is not even. Each new auto-sized store takes half of the part of the budget that the live auto-sized stores have not claimed (stores reused from an earlier load count at their built size, a new site listed before the reused ones still sees them, `max_bytes` stores not at all), raised to the 160 MiB floor with a warning when the half is smaller (Ashwin, P2-04: chosen over a new config key). A lone auto-sized site gets half of the budget (20% of the limit, at most 4 GiB), not all of it. Stores of the name being built are not counted (a changed pool key builds a replacement while the old store lives until `Cleanup`), but a renamed or removed site's store is counted until it is destroyed, so a reload that swaps sites can leave the new ones smaller than the old ones were (set `max_bytes` to avoid it). The sizing assumes Caddy provisions handlers one at a time, which it does; the deployment guide tells single-site operators to set `max_bytes` for more. With no limit set each store takes 256 MiB and one warning per process recommends `GOMEMLIMIT`. The share is fixed for that store's life, because the store interface has no resize and a size in the pool key would flush every auto-sized store whenever a site is added. A reload that adds an auto-sized site can therefore push the total above 40% until the next restart; `Provision` logs a warning when the sum of live auto-sized stores exceeds it, and operators with several sites or a BYOD control plane set `max_bytes` explicitly (T-43 residual, stated in the deployment guide). The `caddy` binary sets the memory limit from the cgroup or system memory at startup (`cmd/main.go`, `memlimit.Set`), so `debug.SetMemoryLimit(-1)` is normally finite under Caddy; a custom main that skips it falls to the 256 MiB branch of FR-MEM-1 with its warning. Request bodies: the adapter docs require `request_body { max_size ... }` and server `timeouts { read_body ... }` on Weir routes (FR-LIM-7).

No purge endpoint is exposed on site listeners. Operators who need remote purges expose Caddy's admin API with its own access controls (T-26).

## 8. Observability

The adapter implements `weir.Observer` by incrementing metrics registered on Caddy's Prometheus registry (Caddy exposes `/metrics` through its `metrics` handler and admin endpoint), using the metric names in [04-lld.md §9.3](04-lld.md) with an added `name` label for the engine. Caddy creates a new Prometheus registry for every config load (`Context.GetMetricsRegistry`, pedantic mode, so a duplicate registration is an error). The adapter therefore keeps one collector set per registry, created by the first `Provision` of a load and shared by every `weir` handler in that load (looked up by registry pointer, guarded by a mutex, dropped in `Cleanup` when the last user of that registry goes). Counters restart at zero on reload; Prometheus treats that as a counter reset. The set of registries is bounded by the number of live loads (one or two, three if a failed load has not cleaned up yet). Collectors are not attached to a pooled store, which outlives the registry. The one value a store feeds is its eviction counter (`memory.Config.OnEvict`, fixed at construction): the pooled value owns an atomic pointer to the current sink, and each `Provision` repoints it at the new load's collector, so evictions never write to a dead registry. P2-06 chose to wrap `observe/prom`: the metric names, buckets and reason sanitizing stay in one place, and `prometheus.WrapRegistererWith(Labels{"name": name}, registry)` adds the `name` label as a constant label. The `caddy` module requires `observe/prom` and `client_golang` in its own `go.mod`. The set is keyed by (registry pointer, `name`), so site blocks that share a `name` in one load share one set (they share a store, and a second registration of the same descriptors would fail on the pedantic registry); the gauges then add in-flight fetches and queue depth over that name's engines, take the worst breaker state, and report the largest store size (one shared store). The last `Cleanup` of a key unregisters the collectors. Each engine's observer is the purge tap followed by the set's `Observer`. The pooled store's `OnEvict` calls an `evictSink` that keeps every live set using the store (at most one per live load) and counts each eviction in all of them, because while loads overlap (or a failed load has not cleaned up) every live registry still exposes the one store; `Provision` attaches its set and the last `Cleanup` of a set detaches it, so a sibling handler or a failed load cleaning up never silences the load that still serves. `Logger` is `ctx.Slogger()`, which Caddy's `caddy.Context` provides (present in v2.11.7), so no zap-to-slog bridge is needed.

## 9. Tests

- Caddy's `caddytest` harness (`caddytest.NewTester`) with a Caddyfile that fronts an in-process origin; the T6.2, T6.6 and T6.12 engine scenarios re-run end to end.
- Reload test: load config, warm 100 keys, reload with a changed limiter setting, assert all 100 are still hits.
- `xcaddy build` in CI to catch Caddy API drift.

## 10. Resolved questions

- OQ-C1: keep the store; global soft purge only when the key-generation hash changes (§3).
- OQ-C2: `name` is required (§3).
- OQ-C3: one engine per site with per-host fairness caps (§4b).

Closed by P2-00: every Caddy API named here was re-verified against v2.11.7 (§11). Findings that changed the text: admin routers are rebuilt on every load before the apps, so a registry of live engines is needed (§7); the metrics registry is per load (§8); Caddy's upgrade detection cannot be reused (§4 step 0); store-level settings, memory sizing and the snapshot owner are fixed per store (§3, §7); per-client placeholders are an unkeyed input (§6). Decision amended by P2-00 on Ashwin's delegation: OQ-C1's key-generation change writes a hard epoch, not a soft one (§3).

## 11. Verified Caddy APIs (v2.11.7)

Checked against the source at tag `v2.11.7` (commit 72dd0fb) on 2026-10-09. Paths are relative to the Caddy repository; lines are at that tag. The adapter's CI builds with `xcaddy` (§9), so drift after this tag shows up as a build failure, and the next re-verification updates this table.

| API or behavior | Location | Note |
|---|---|---|
| `caddy.Module` | `modules.go:54` | `CaddyModule() ModuleInfo` |
| `caddy.Provisioner`, `Validator`, `CleanerUpper` | `modules.go:296`, `:305`, `:315` | `Cleanup()` and `Validate()` take no context; `Validate` runs after `Provision`, and a failed load runs `Cleanup` on the half-built modules (`context.go:423` to `:447`, `caddy.go:505`) |
| `caddy.Context.Slogger()` | `context.go:612` | slog logger for the most recent module in the context |
| `caddy.Context.GetMetricsRegistry()` | `context.go:115` | registry is per config load, pedantic (`context.go:73`) |
| `caddy.UsagePool.LoadOrNew`, `Delete`, `Constructor`, `Destructor` | `usagepool.go:77`, `:171`, `:216`, `:220` | `Delete` runs `Destruct` outside the lock; a surplus `Delete` is a no-op or steals another instance's reference |
| `caddy.Duration`, `caddy.ParseDuration` | `caddy.go:866`, `:888` | the latter accepts a `d` (days) unit |
| `httpcaddyfile.RegisterHandlerDirective` | `caddyconfig/httpcaddyfile/directives.go:135` | |
| `httpcaddyfile.RegisterDirectiveOrder` | `directives.go:168` | still marked EXPERIMENTAL (`:167`); `Before` is `:644` |
| default directive order | `directives.go:47` | `request_body` (`:62`) < `encode` (`:78`) < `reverse_proxy` (`:95`) |
| `caddyfile.Unmarshaler` | `caddyconfig/caddyfile/adapter.go:106` | |
| `caddyhttp.MiddlewareHandler` | `modules/caddyhttp/caddyhttp.go:90` | `ServeHTTP(w, r, next Handler) error` |
| `caddyhttp.Error`, `HandlerError` | `modules/caddyhttp/errors.go:32`, `:56` | `Error` keeps an existing `HandlerError` and fills missing fields |
| error path | `modules/caddyhttp/server.go:735` to `:785`, `:1162` | `handle_errors` chain and the plain `WriteHeader` branch both use the request's `ResponseWriter`; `WithError` sets `{http.error.*}` placeholders |
| `caddy.AdminRouter`, `AdminRoute` | `admin.go:774`, `:779` | routers built in `newAdminHandler` (`:222`, loop at `:277`) with the load's context |
| admin server replacement | `caddy.go:565`, `admin.go:375` | `provisionContext` replaces the admin server on every load, before the apps are provisioned; routers are re-provisioned each time (`admin.go:277`) |
| existing admin module as a pattern | `modules/caddyhttp/reverseproxy/admin.go:45` | ID `admin.api.reverse_proxy` |
| `request_body` `max_size` | `modules/caddyhttp/requestbody/requestbody.go:35`, `:69` | wraps the body in `http.MaxBytesReader` |
| server `timeouts { read_body }` | `caddyconfig/httpcaddyfile/serveroptions.go:133` to `:146` | |
| `reverse_proxy` upgrade detection | `modules/caddyhttp/reverseproxy/reverseproxy.go:1647` | `upgradeType`, unexported; no `CONNECT` check |
| `reverse_proxy` `X-Forwarded-For` | `reverseproxy.go:939` to `:993` | removed from untrusted clients, then set to the client address; operators can strip it with `header_up -X-Forwarded-For` |
| `encode` and `Vary` | `modules/caddyhttp/encode/encode.go:522` | adds `Vary: Accept-Encoding` when it compresses |
| memory limit at startup | `cmd/main.go:484` | `memlimit.Set` from cgroup or system memory |
| `caddytest.NewTester`, `InitServer` | `caddytest/caddytest.go:68`, `:118` | |
