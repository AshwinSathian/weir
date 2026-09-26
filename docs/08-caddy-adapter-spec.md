# Weir Caddy adapter specification (draft)

Status: draft v0.95. Phase 2. The seed says this document is "written once Phase 1 is stable"; this draft captures the constraints already known so Phase 1 does not paint Phase 2 into a corner. It is finalized at the start of Phase 2 against the Caddy version current then.
Date: 2026-09-27
Depends on: [01-technical-spec.md](01-technical-spec.md), [04-lld.md §10](04-lld.md)
Seed name: `04-caddy-adapter-spec.md` (renumbered, see [docs/README.md](README.md))
Verified against: Caddy v2.11.4 (released 2026-06-03, `go 1.25.1` in its `go.mod`). Weir requires Go 1.27, so `xcaddy` builds that include it need a Go 1.27 toolchain; Go's toolchain directive downloads it automatically when `GOTOOLCHAIN=auto`.

## 1. Shape

- Module: `github.com/AshwinSathian/weir/caddy`, its own `go.mod`, requiring `github.com/caddyserver/caddy/v2` and the root `weir` module.
- Built into Caddy with `xcaddy build --with github.com/AshwinSathian/weir/caddy`.
- Module ID: `http.handlers.weir`. Caddyfile directive: `weir`.
- Implements `caddy.Module`, `caddy.Provisioner`, `caddy.Validator`, `caddy.CleanerUpper`, `caddyhttp.MiddlewareHandler`, `caddyfile.Unmarshaler`, with interface guards.
- Registered with `httpcaddyfile.RegisterHandlerDirective("weir", parse)` and `httpcaddyfile.RegisterDirectiveOrder("weir", httpcaddyfile.Before, "reverse_proxy")` so the directive works without a global `order` option. `RegisterDirectiveOrder` is marked EXPERIMENTAL in Caddy's source; if it changes, the fallback is documenting `order weir before reverse_proxy` in the global options block.

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

Every field is optional and defaults to the engine default. `Validate` calls `weir.New` on a copy of the config and reports its error, so invalid configs fail at `caddy validate` time.

## 3. Engine lifetime across config reloads

Caddy starts new module instances before stopping old ones on every config change ("multiple loaded instances of your module may overlap", Caddy's extending guide). A naive adapter would build a fresh engine and store on every reload, which is a full cold flush (seed T6.4) every time the config changes. For the BYOD custom-domain project that shares the Caddy instance, config changes can be frequent.

Rule: the expensive state is the store, so the store is what survives reloads. The adapter keeps memory stores in a package-level `caddy.UsagePool` keyed by `name` plus the store settings (`max_bytes`, shard count). `Provision` calls `LoadOrNew` on that pool and builds a new `weir.Engine` around the shared store (engines are cheap: a limiter, a breaker and some maps). `Cleanup` closes the old engine, which does not close a store it did not create, and calls `Delete` on the pool, which closes the store only when the last user releases it.

Consequences, accepted:

- A reload that changes the store size starts a new, empty store. That is rare and deliberate.
- During the overlap window both the old and new engine have their own limiter, so origin concurrency can briefly reach twice `max_concurrent`. The old engine receives no new requests after the switch, so the overlap is bounded by its in-flight fetches.
- In-flight flights do not transfer: a request arriving at the new engine for a key the old engine is fetching starts its own fetch. At most one duplicate fetch per key per reload.
- Key-generation hash (OQ-C1, resolved): the adapter hashes the settings that change what an unchanged key means, namely `Forward.Mode`, `Forward.Allow` and `Storable.StripSetCookie`, and stores the hash beside the pooled store. When a reload changes it, the new engine writes one global soft epoch, so reachable entries revalidate (under SWR where the origin allows it) instead of serving content fetched under different forwarding rules. Settings that only change which key a request maps to (query rules, key headers and cookies, path normalization, experiments) are excluded: their old entries become unreachable and age out without a purge. Host lists, on-demand TLS domains and everything else a BYOD control plane changes on reload are excluded, so adding a domain never purges the cache.
- `name` is required (OQ-C2, resolved). Two site blocks with the same `name` share a store on purpose; the same `name` with different store settings fails validation.

## 4. Origin implementation

`ServeHTTP(w, r, next)`:

1. `req := weirhttp.RequestFrom(r)`.
2. `origin := nextOrigin{next: next, base: r}`.
3. `resp, err := engine.Serve(r.Context(), req, origin)`.
4. On error: `weirhttp.WriteError(w, err)` (status from `weir.StatusCode`, `Retry-After` from `weir.RetryAfter`), return nil.
5. Otherwise write `resp` with `weirhttp.WriteResponse` and return nil.

`nextOrigin.Fetch(ctx, fwd)` clones `base` with `base.Clone(ctx)`, replaces method, URL path and raw query, headers and body with the forwarded request's, and calls `next.ServeHTTP` with a response writer that streams into an `io.Pipe` (the `weirhttp.HandlerOrigin` shape). It returns as soon as the downstream handler writes headers.

Constraints this places on Phase 1, all already met:

- `Fetch` may run after the triggering request finished (SWR, early refresh). `ctx` carries the original request's values but not its cancellation (FR-COA-9), so Caddy's replacer and vars remain available through `ctx.Value`. The clone must not touch `base`'s original `ResponseWriter`.
- `reverse_proxy` adds `X-Forwarded-For`, `X-Forwarded-Proto` and `X-Forwarded-Host` when proxying. `X-Forwarded-For` is per-client. See §6.

## 4a. Deployment topology

Phase 2 supports exactly one Caddy node per cache (D17, T-38). Purges, snapshots and the memory store all assume it. Running several nodes behind a load balancer before the Valkey store (Phase 2.5) exists means each node has its own cache and a purge reaches only the node it is sent to; the adapter docs state this as unsupported.

## 4b. Multi-host sites (BYOD)

One engine serves every host of a site (OQ-C3, resolved): the tenants share one origin, so they share the breaker and the global limiter. Fairness comes from two caps that the adapter turns on at 25% when the site has more than one host or uses on-demand TLS (FR-FAIR-3): `Limiter.MaxPerHost` and the memory store's `MaxBytesPerOwner`. Tenants that run on different upstreams must be configured as different sites with different `name`s, or a failing tenant upstream would open the breaker for all.

## 5. Ordering with other handlers

- `encode` (compression): if `encode` runs before `weir` (outer), Weir caches uncompressed bytes and `encode` compresses every response, including hits. Simple and CPU-bound. If `encode` runs after `weir` (inner, between Weir and `reverse_proxy`), Weir caches compressed variants keyed by the `Accept-Encoding` bucket. Default recommendation: let the origin compress and leave `encode` out of Weir-cached routes, or place it outside `weir` when the origin cannot. Documented with both examples.
- `rate_limit` (third-party `caddy-ratelimit`): place before `weir` to answer residual risk R-2 (distinct-path floods).
- `forward_auth` or other auth: must run before `weir`, and routes that need per-user responses should use `bypass` rules or rely on `Authorization` handling.

## 6. Per-client headers added downstream (T-4)

Weir forwards a sanitized request, but `reverse_proxy` then adds `X-Forwarded-For` with the client address. If the origin changes cacheable responses based on client address (geo pages), that is an unkeyed input. The adapter documentation must say this plainly and give two options: configure `reverse_proxy` with `header_up -X-Forwarded-For` on cached routes, or move the decision into a key dimension (Phase 3). The adapter logs a one-time warning at provision time when it detects `reverse_proxy` as the next handler and no `header_up -X-Forwarded-For` (best effort; the handler chain is not always introspectable).

## 7. Purge over HTTP

Purge is available only through the admin API (decision confirmed 2026-09-27). An admin API module `admin.api.weir` registers routes under Caddy's admin endpoint (local-only by default):

- `POST /weir/<name>/purge` with a JSON body matching `weir.Purge` (including `eager` for hard purges). Returns 202 once epochs are written, with the scrubbed count when `eager` was set.
- `GET /weir/<name>/stats` returning `EngineStats`.

No purge endpoint is exposed on site listeners. Operators who need remote purges expose Caddy's admin API with its own access controls (T-26).

## 8. Observability

The adapter implements `weir.Observer` by incrementing metrics registered on Caddy's Prometheus registry (Caddy exposes `/metrics` through its `metrics` handler and admin endpoint), using the metric names in [04-lld.md §9.3](04-lld.md) with an added `name` label for the engine. `Logger` is `ctx.Slogger()`, which Caddy's `caddy.Context` provides (present in v2.11.4), so no zap-to-slog bridge is needed.

## 9. Tests

- Caddy's `caddytest` harness (`caddytest.NewTester`) with a Caddyfile that fronts an in-process origin; the T6.2, T6.6 and T6.12 engine scenarios re-run end to end.
- Reload test: load config, warm 100 keys, reload with a changed limiter setting, assert all 100 are still hits.
- `xcaddy build` in CI to catch Caddy API drift.

## 10. Resolved questions

- OQ-C1: keep the store; global soft purge only when the key-generation hash changes (§3).
- OQ-C2: `name` is required (§3).
- OQ-C3: one engine per site with per-host fairness caps (§4b).

Remaining for Phase 2 kickoff: re-verify every Caddy API named here against the then-current release.
