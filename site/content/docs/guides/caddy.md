---
title: Caddy guide
description: Build Caddy with the weir handler, configure it in a Caddyfile, and purge through the admin API.
---

The Caddy module puts a `weir` directive in front of `reverse_proxy`. Its import path is `github.com/AshwinSathian/weir/caddy` and its Go package name is `weircaddy`. It is built against Caddy {{caddy_version}}.

## Build

```sh
xcaddy build --with github.com/AshwinSathian/weir@{{version}} --with github.com/AshwinSathian/weir/caddy@{{version}}
./caddy list-modules | grep weir
```

Pin both modules to the same version. The four Weir modules are released together, and the Caddy module is tested only against the root module at its own version. Upgrade by rebuilding.

## A site

{{include docs/runbook.md "### 7.2 A minimal site"}}

## Directive reference

Every key is optional except `name`. Unset keys take the engine default from the [configuration reference](/docs/reference/config/).

| Key | Meaning |
|---|---|
| `name` | Required. 1 to 64 characters of `A-Z a-z 0-9 . _ -`. Store identity across reloads, metrics label, admin URL segment, snapshot file name |
| `max_bytes` | Memory store size, for example `512MiB`. At least 160 MiB when set. Unset: sized from `GOMEMLIMIT` |
| `snapshot_dir` | Absolute directory for the snapshot file. Unset: every restart is a cold cache. Set: entries come back, and each is checked with the origin on first use |
| `snapshot_keep_fresh` | Next release, not in `{{version}}`. With `snapshot_dir`: entries come back as fresh as they were, so a restart costs the origin nothing. A purge sent while Caddy was down is not applied to them; purge again after a restart if one may have been missed |
| `multi_host` | Set it when the site serves more than one host. Turns on the per-host limiter cap and store quota at 25% |
| `store valkey { ... }` | Shared store for several nodes: `addrs`, `username`, `password`, `tls`, `tls_ca_file`, `tls_server_name`, `max_object_bytes`, `cluster`, `prefix`. Write credentials as `{env.VAR}`, which stays a placeholder in the adapted JSON and in `GET /config/`; `{$VAR}` is expanded into both |
| `key { ... }` | `query_drop`, `query_keep`, `query_sort`, `normalize_path`, `headers`, `cookies`, `accept_encoding` |
| `forward { allow ... }` | Headers the origin may see unkeyed. The mode is always strict: `ForwardAll` has no Caddy form |
| `bypass { ... }` | `cookies`, `headers`: requests that skip the cache |
| `limiter { ... }` | `max_concurrent`, `max_queue`, `max_queue_wait`, `max_per_partition`, `partition_headroom`, and, in the next release, `adaptive_max`: above `max_concurrent`, the limit may grow to it while requests wait and the origin answers well |
| `stale { ... }` | `while_revalidate`, `if_error`: operator defaults, off unless set |
| `experiments { ... }` | A/B bucketing; see the [experiments guide](/docs/guides/experiments/) |

A fuller example:

```text
example.com {
	weir {
		name       site-a
		max_bytes  512MiB
		key {
			query_drop utm_* fbclid gclid
			query_sort
			headers    Accept-Language
			cookies    currency
			accept_encoding br gzip
		}
		bypass {
			cookies session_id
		}
		limiter {
			max_concurrent 128
		}
		stale {
			if_error 5m
		}
	}
	reverse_proxy app:8080
}
```

Each key may appear once per block, so list values go on one line. A repeated key is an error. The JSON config uses the same names in snake_case.

## Where weir sits among handlers

{{include docs/runbook.md "### 7.3 Order of handlers"}}

## Purges, modes and stats

Start with the safe setup, then the commands.

{{include docs/runbook.md "### 7.7 Admin API and purges"}}

## Before you go live

> [!CAUTION]
> `caddy validate` is not a dry run for the `weir` block. It builds the store for real, the way a start does: it reads and rewrites the snapshot in `snapshot_dir`, and when the `forward` block changed it discards that snapshot and, with a `store valkey` block, purges the shared cache on every node. Caddy gives a module no way to tell a validation from a start. Validate a copy of the Caddyfile whose `snapshot_dir` is a scratch directory and whose Valkey `addrs` (or `prefix`) point somewhere else.

{{include docs/runbook.md "### 7.8 Before you go live"}}

## Several nodes

One Caddy node per cache with the memory store. For several nodes, add a `store valkey` block and follow the runbook's [multi-node checklist](https://github.com/AshwinSathian/weir/blob/main/docs/runbook.md#8-deploying-on-caddy-several-nodes-one-valkey): one prefix per cache, the same `forward`, `key` and `stale` settings on every node, an eviction policy that cannot evict purge records, and clocks in step.

## Limits

> [!LIMITS]
> The directive order (`weir` directly before `reverse_proxy`) is registered for Caddy {{caddy_version}}; check `caddy adapt --pretty` on your build. `caddy validate` builds the store for real: it creates `snapshot_dir`, rewrites the snapshot, and on a Valkey site can spend a pending purge, so validate against a scratch server. There is no purge route on site listeners, on purpose. `ForwardAll` cannot be configured from Caddy.
