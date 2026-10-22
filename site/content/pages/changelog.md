---
title: Changelog
description: Release notes, with the versioning and support policy.
source: CHANGELOG.md
---

## Policy

- The root module and the `observe/prom`, `store/valkey` and `caddy` modules are tagged together and carry the same version. Use one version for all of them.
- Versions follow [semantic versioning](https://semver.org). From `v1.0.0` the exported Go API, the metric families and labels, the `Cache-Status` parameter names and the Caddy configuration names are stable within a major version.
- Defaults that decide what is shared and how safely (strict forwarding, `Vary` handling, which responses are stored, how long stale content may be served) change only in a major release. Tuning defaults (limits, timeouts, breaker timing) may change in a minor release; the changelog lists each such change with the way back.
- The latest `v1.x` minor release gets security fixes.
- The minimum Go version is {{go_min}}. The project supports the two latest Go releases and CI tests each.

Subscribe to releases with the [Atom feed](/feed.xml).

{{changelog}}
