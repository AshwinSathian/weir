---
title: Changelog
description: Release notes, with the versioning and support policy.
source: CHANGELOG.md
---

## Policy

- The root module and the `observe/prom`, `store/valkey` and `caddy` modules are tagged together and carry the same version. Use one version for all of them.
- Versions follow [semantic versioning](https://semver.org). From `v1.0.0` the exported Go API, the metric families and labels, the `Cache-Status` parameter names and the Caddy configuration names are stable within a major version.
- Defaults that protect (strict forwarding, `Vary` handling, stale serving) change only in a major release.
- The latest `v1.x` minor release gets security fixes.
- The minimum Go version is {{go_min}}. The project supports the two latest Go releases and CI tests each.

Subscribe to releases with the [Atom feed](/feed.xml).

{{changelog}}
