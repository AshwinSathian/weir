---
title: Incidents
description: What to do when the origin is down, the cache is suspect, a path is flooded or memory is short.
---

Rendered from the repository's runbook. Commands that use the Caddy admin API are in the [Caddy guide](/docs/guides/caddy/#purges-modes-and-stats); from Go, the same operations are `Engine.SetMode` and `Engine.Purge`.

`SetMode` always takes a time limit, at most 24 hours, so a mode nobody remembers to undo ends on its own.

{{include docs/runbook.md "## 4. Incidents" demote=-1}}

## Errors clients see

{{include docs/runbook.md "## 6. Errors clients see"}}
