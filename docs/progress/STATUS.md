# Status

Updated: 2026-10-10
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: claude/blissful-pascal-4l6ag2
PR: pending
Next card: P25-03

## Waiting on Ashwin

none. Decided 2026-10-10 (P2-03b, review of PR 79; Ashwin delegated "take decisions on all items"): caddytest e2e files are a named exception to the real-clock rule (CLAUDE.md rule 6, docs/07 §1) instead of a build tag, because a tag would stop CI running them; a skip outside `-short` fails the test; the test site binds 127.0.0.1; the herd test uses a 2 s client timeout so a hard purge reports counts. Also decided (P2-02): multi-host is an explicit `multi_host` key; scanning the http app was rejected (handler cannot find its own route; global scan would cap unrelated sites and flush their stores). Documented in 08 §2/§3/§4b.

## Decided 2026-10-10 (P25-00, adversarial review of PR 84)

Ashwin delegated "take decisions on all items"; "Waiting on Ashwin" was empty. An independent agent attacked the PR. Decisions, written into 05 §7/§8 and the cards:

- Epoch state must never be evictable: epoch keys have no TTL, the server policy is `volatile-lfu` (or `noeviction`); the store refuses `allkeys-*` on connect (`SkipPolicyCheck` for managed services). Hard epochs live in one pruned sorted set (no per-tag keys); an absent `newest` key is never "no epochs"; a lost `meta`/sketch reports a hard epoch at now. This replaces the earlier `allkeys-lfu` recommendation.
- Connection: `New` validates config but does not connect; an unreachable server opens the store breaker (FR-STF-2). `Cluster` is an explicit setting. Replica reads off.
- New config fields approved for `store/valkey`: `Prefix`, `HashTag`, `CoLocateEntries` (entries omit the hash tag by default), `Cluster`, `MaxRetention`, `MaxClockSkew`, `MaxHardEpochs`, `CallTimeout`, `HardEpochWait`, `SkipPolicyCheck`.
- Second review: loss repair is a write (global hard epoch), seed from `crypto/rand` hashed in Go, `NoClockSkew` bool, policy checked on every node.
- Skew: accept the up to `MaxClockSkew` + 1 s re-purge window and document it; single-clock operators set skew 0.
- Scrubber stays synchronous (SCAN per node, `ponytail:` ceiling O(keyspace)).
- Cards re-cut: P25-01 split into P25-01 and P25-01b, P25-05 into P25-05 and P25-05b; storetest gets `EpochModes` and `Parallel`. P25-05 (vary CAS mechanism) and P25-07/07b (Caddy `store valkey` block, lifting D17) still need Ashwin's approval when they start.
- Also fixed: a global sed in my earlier commit had rewritten three old LOG entries to point at PR 84; reverted.

## Decided 2026-10-10 (P2-04)

Ashwin chose "halve the remainder" (via question) because Caddy gives `Provision` no look-ahead for an even split. Each new auto-sized store takes half of the budget the live auto-sized stores have not claimed (20%, 10%, 5% of the limit), floored at 160 MiB with a warning. A lone site gets 20%, not 40%. FR-MEM-1, 08 §7 and T-43 reworded.

## Decided 2026-10-09 (M16-01)

Ashwin delegated "take decisions on all items" on PR 72. Approved the recommendation: keep the heap layout; D36 now gates on GC µs per request (at most 2 µs at 1M entries, 1 KiB, default `GOGC`; now 1.76), reported by `TestGCAt1MEntries`, not asserted; `GOGC=200` is the documented lever. M16-02 deferred (moved to `docs/cards/20-later.md`, reopens as a design change if the gate is exceeded or more than 1M entries are needed). M16-01 marked done.

## Decided 2026-10-09 (M15-01)

From the adversarial review of PR #71 (Ashwin delegated "take decisions on all items"). "Waiting on Ashwin" was empty. No must-fix.

- Scrub failures do not count toward the store breaker (same rule as `purgeEpoch`), emit `EvStoreError{scrub}`; pinned by `TestStoreGuardScrubFailureNotCounted`. LLD 5.2 and the event table updated.
- Scrub ignores request times and cannot stop an in-flight store: documented in LLD 13.5 and 05 §5.3 rather than changing `Scrubber`'s signature (public API; the epoch decides reachability). A part-way epoch failure scrubs nothing (documented; repeating is safe).
- Added engine tests by URI and group tag with a surviving URL, a cancelled-context case, and a concurrent Scrub/Set/Get/Delete race test. `Scrubber` doc comment and docs/07 row updated.
- Declined: keeping `context.Canceled` in the scrub error (nit; callers see `store.ErrUnavailable` and their own ctx).

## Blockers

none. golangci-lint in this container is built with Go 1.25 and cannot load the Go 1.27 config; run it with `GOTOOLCHAIN=go1.27.0 go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run`. CI must confirm.

## Decided 2026-10-09 (P2-00, review of PR 73)

Ashwin delegated "take decisions on all items"; "Waiting on Ashwin" was empty. An independent agent attacked the PR. Decisions, all written into 08:

- Admin routers are rebuilt on every load (my earlier claim that they survive reloads was wrong). The registry stays but holds a set of live engines per name, removed by identity, so a failed load keeps the serving engine reachable. `mode` applies to all live engines of a name, `stats` returns an array, `purge` goes through any one. Admin bodies are bounded (1 MiB, 1000 URLs, 100 groups).
- Same `name` with different settings fails `Provision` within one load only; across loads (resize reload) it is allowed.
- Store-level settings are fixed at build. Pool key adds an "owner cap on" boolean: one host to two starts a new store once, so the FR-FAIR-3 cap is never silently missing. Auto-sized stores keep their size; no cross-load 40% guarantee (store has no resize), warning plus explicit `max_bytes` advice.
- OQ-C1 amended: a key-generation hash change writes a hard epoch, not soft (soft entries stay servable in SWR and stale-if-error windows, FR-PRG-2). This changes a decision from 2026-09-27; PLAN 2.2 reworded.
- Per-client placeholders (`{remote_host}` and header placeholders) on handlers after `weir` are an unkeyed input like X-Forwarded-For; 08 §6 and P2-03/P2-07 cover it. Follow-up: add to 06 T-45/R-6 at its next revision.
- Snapshots: the superseded store skips its snapshot; `name` is restricted to `[A-Za-z0-9._-]{1,64}`; config keys table added to 08 §2.
- Cards: P2-01 split into P2-01, P2-01b (Caddyfile), P2-01c (xcaddy CI); `TestRetryAfterSurvivesHandleErrors` moved to P2-03b (needs caddytest).

## Decided 2026-10-09 (P2-01, review of PR 74)

Ashwin delegated "take decisions on all items"; "Waiting on Ashwin" was empty. An independent agent attacked the PR; no must-fix. Decisions:

- `name` may not start with a dot (admin URL `/weir/../purge` collapses). Changes the 08 §2 charset rule; 08 updated.
- `max_bytes` is capped at 1 PiB at parse time (later budget sums cannot overflow); `Provision` warns when `max_bytes` or `snapshot_dir` is set, since P2-02 applies them.
- A second `Provision` on a provisioned `Handler` fails instead of leaking the first engine. Byte sizes accept digits and one decimal point only; JSON `null` is a no-op; doubled `weir:` error prefix removed.
- Risk for P2-01c: `caddy/go.mod` uses `replace => ..`, which importers ignore; `xcaddy build --with .../weir/caddy` outside the repo may not resolve the root module until it has a tag. P2-01c must test this.
- P2-03: `Cleanup` clears `h.engine` without a lock; `ServeHTTP` must read it safely (atomic pointer or the engine registry).

## Decided 2026-10-10 (P2-01b, adversarial review of PR 75)

"Waiting on Ashwin" was empty. No must-fix. Decided: sub-block keys (`key`, `forward`, `bypass`, `limiter`, `stale`) with no block or empty braces are an error; repeated keys stay an error and 08 §2 now says so; the `weir <matcher>` form is supported and documented; runtime placeholders (`{env.X}`, `{host}`) are not expanded, only parse-time `{$VAR}`, documented in 08 §2; the `name` error points at the `name` line. Tests added for repeated sub-block keys, negative durations, directive arguments, bare and empty sub-blocks, the matcher form. Kept: duplicate `name` handling in P2-02, the non-`card/*` branch, `RegisterDirectiveOrder` (TestDirectiveOrder fails loudly on a Caddy bump).

## Decided 2026-10-10 (P25-01b, adversarial review of PR 86)

Ashwin delegated "take decisions on all items"; "Waiting on Ashwin" was empty. An independent agent attacked the PR; no must-fix. Decisions, written into 05 §7: standalone mode (`Cluster` false) takes exactly one address (valkey-go uses only the first; `Validate` rejects more, a config-behavior change inside the approved Config); the policy check is an allowlist (`noeviction`, `volatile-*`); `Close` waits for a real dial, and the doc no longer claims a single `CallTimeout` bound; a second `Close` waits for the first. Fixed: the watcher goroutine joins `wg`, a failed single-client dial is closed, `lastErr` is set when closed. Tests added for failing-dial fan-in, the end-of-attempt gap, second `Close`, standalone addresses. Kept: no recheck of the policy after connect (documented).

## Decided 2026-10-10 (P25-01, adversarial review of PR 85)

Ashwin delegated "take decisions on all items"; "Waiting on Ashwin" was empty. An independent agent attacked the PR; no must-fix. Decisions: empty `Prefix`/`HashTag` mean the default (card test list reworded); config errors wrap `weir.ErrInvalidConfig` (no new sentinel); upper bounds added (MaxRetention 10 y, MaxClockSkew 1 h, MaxHardEpochs 1e6, key parts 64 bytes); `HardEpochWait` whole ms and below `CallTimeout`; `Addrs` must be unique `host:port`. Fixed: `Validate` copy aliased `Addrs`/`TLS`, returns the zero Config on error; JSON marshalling leaked the password (now redacted); `mapError` leaves wrapped `valkey.Nil` and `ErrNotFound` alone. Written into 05 §7.

## Notes for the next session

- P25-02 done: `Get`/`Set`/`Delete` in `store/valkey/entries.go`; the `client` seam gained `get`, `set`, `del` (P25-03 adds script calls). `Set` declines (nil) a record `store.Encode` rejects. `Decode` is bounded by `maxValueBytes` (512 MiB, the server's limit), not a Weir cap; confirm the engine's body cap is far below it. Integration tests need `WEIR_VALKEY_ADDR` (`make test-valkey`); only `redis-server` 7.0 was available here, so the CI job on `valkey/valkey:8.1` has not run. The job sets `volatile-lfu` with `docker exec ... valkey-cli config set` (service containers take no command); it is a new required job in the aggregate `check`.

- P25-01b done: `valkey.New` returns `*Store` (as `memory.New`); `acquire(ctx)` hands out the `client` seam (`policies`, `close`) that P25-02 extends with the real commands. The shared dial runs under `CallTimeout`, not the caller's deadline. Policy check fails closed. `go.work` now includes `./store/valkey`. Errors use `store: valkey:` (card note reconciled).
- Container lint workaround for the root `make check`: `make -o lint check` after running golangci-lint with `GOTOOLCHAIN=go1.27.0`; the `modules` target also hits the Go 1.25 binary, so run each submodule by hand. CI must confirm.

- P25-01 done: `store/valkey` module (Config, Validate, redaction, `mapError`). `Validate` returns a defaults-filled copy; `mapError(valkey.Nil)` returns Nil unchanged, so `Get` (P25-01b onward) maps it to `ErrNotFound`. `store/valkey` is not in `go.work` yet (the card left it out); add it with P25-01b if tooling needs it.
- Lint for submodules: run golangci-lint v2.14.0 with `GOTOOLCHAIN=go1.27.0` and `GOWORK=off` inside the module.
- `Validate` now caps `MaxRetention` (10 y), `MaxClockSkew` (1 h), `MaxHardEpochs` (1e6), key parts (64 bytes) and errors wrap `weir.ErrInvalidConfig` (05 §7). P25-01b must add `./store/valkey` to `go.work` and wrap the policy-check error as `ErrUnavailable`.

- P2-07 done: runbook section 7. Not yet run through `caddy adapt`; the examples were read against Caddy v2.11.7 source only. caddy-ratelimit order was read on `master` (no tag).

- CI's xcaddy step needs one `--with <module>=<path>` per unreleased module the `caddy` module requires (root, `caddy`, `observe/prom`); add one when another sub-module becomes a dependency.

- P2-06 done: `cfg.Observer` is `fanout{purgeTap, metric set Observer}` (caddy/metrics.go). Any further observer joins that fanout; do not replace the tap. Metrics wrap `observe/prom` (decision in 08 §8).
- P2-05 admin tests call the router handler directly; the only real-Caddy admin case is `TestE2EAdminPurge` (admin port 2999, one named 1.1 s pause).

- P2-04: the deployment guide (P2-09 or later) must tell single-site operators to set `max_bytes` if they want more than 20% of the limit. Tests that depend on the memory budget call `isolateStores` because the caddytest instance and earlier tests leave stores live in the global registry.
- P2-03b ran the T6.12 scenario through a POST with `Cache-Group-Invalidation`; the admin purge endpoint does not exist yet, so P2-05 should add an end-to-end purge case (`fwd=stale` on the next `Cache-Status`).
- caddytest tests use the real clock (two named pauses: 300 ms for followers, 1.1 s for the epoch second) and skip under `-short`. Ports 2999 (admin) and 9080 are fixed by the harness; do not run two caddytest packages in parallel.
- Still open from P2-03: 5xx statuses chosen by `next` (a dial error from `reverse_proxy`) become 502 with no body. The outage test confirms 502 reaches the client and trips the breaker; no custom mapping was added.
- Follow-up for 06 T-45/R-6: add the per-client placeholder case at its next revision.
- P2-01c: root `=.` replace stays until the next root release tag.
- `caddy/go.mod` gained indirect requirements from `caddytest` only.
