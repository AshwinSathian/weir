# Status

Updated: 2026-10-10
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: card/P25-06-scrubber-scan (pushed as claude/serene-dirac-0yagok)
PR: none yet
Next card: P25-07

## Notes for the next session (P25-06)

- Review (card-reviewer): one must-fix (CI `docker ps --filter publish=` cannot tell the two Valkey services apart; the step now uses `job.services.<id>.id`), fixed but unproven until CI runs. Fixed too: busy-loop reader replaced by a channel, the poll names its real-clock exception, empty-tag `Scrub` no longer dials. Hot-key hit counts in the storm test are logged, not asserted (09 §7 says so).
- Open: the engine bounds the whole `Scrub` by `Timeouts.Store` (50 ms default, remote), so an eager purge on a real keyspace times out. Needs its own scrub deadline in the engine (new card or P25-07). In 05 §7 and 09 §7.
- A failed command inside a `getMulti`/`delMulti` pipeline (for example MOVED during resharding) fails the scrub with `ErrUnavailable`; P25-07 decides whether that needs handling.
- golangci-lint cannot run here (Go 1.25 build); CI must confirm lint, the second Valkey service on 6380, and the storm on Valkey 8.1. PLAN 2.5.4 is ticked on the strength of local redis 7.0.15 runs; PLAN 2.5.2 still waits on the CI run.

## Waiting on Ashwin

Nothing.

Earlier decisions: none open. Decided 2026-10-10 (P2-03b, review of PR 79; Ashwin delegated "take decisions on all items"): caddytest e2e files are a named exception to the real-clock rule (CLAUDE.md rule 6, docs/07 §1) instead of a build tag, because a tag would stop CI running them; a skip outside `-short` fails the test; the test site binds 127.0.0.1; the herd test uses a 2 s client timeout so a hard purge reports counts. Also decided (P2-02): multi-host is an explicit `multi_host` key; scanning the http app was rejected (handler cannot find its own route; global scan would cap unrelated sites and flush their stores). Documented in 08 §2/§3/§4b.

## Decided 2026-10-10 (P25-05, delegated: "take decisions on all items")

- Vary-spec compare-and-set is the optional capability `store.VarySetter` (05 V-1), not a version field on `Entry`: no change to the codec or the entry layout, and a store with no cross-node writers pays nothing. Signature `SetVarySpec(ctx, k, prev, next) (swapped, err)`, `prev` being the `*Entry` Get returned (a remote store compares bytes).
- Implemented in this PR with P25-04b, at Ashwin's request: interface, memory store, engine (`setVariantCAS`, 16 attempts, a writer that loses to a full spec deletes its variant), docs 04 §6.7 and 05 V-1. The old path showed 64 of 64 variants reachable with `MaxVariants` 8, because lookup finds a variant by key and the lost spec refs only hid the accounting.
- Valkey implementation stays P25-05b (a script comparing a digest of the stored spec). Until then the Valkey store has no capability and keeps the old bound.

## Decided 2026-10-10 (P25-05b, adversarial review of PR 92)

Ashwin delegated "take decisions on all items"; "Waiting on Ashwin" was empty. No must-fix. The undecodable-record swap stays but is documented as unreachable from the engine; no SHA-1 digest (gosec; the cost bound is in 05 §7); fallback tests and a real-server stale-record test added.

## Notes for the next session (P25-05b)

- The Valkey vary CAS compares the encoded `prev` bytes; a real server confirmed decode-then-encode reproduces the stored bytes. A nil `prev` that loses re-reads the key and swaps over a record past its `Expires` or one that does not decode (Get hides both).
- golangci-lint and the `modules` make target cannot run in this container (Go 1.25 build); CI must confirm. PLAN 2.5.2 still waits on the CI Valkey 8.1 run.

## Notes from P25-04b

- PLAN 2.5.2 stays unticked until the CI `valkey` job has run both engine files on Valkey 8.1 (card AC).
- Measured on redis 7.0.15: a hit costs about 0.27-0.32 ms before and 0.46-0.51 ms after 300 invalidations (lookup is GET newest plus a script). A newest cache is the upgrade only if this matters; no change made.
- Epoch tests must start a refresh after `ceil(purge time)`: a response fetched before the epoch second is purged again (E-7). `TestEngineGlobalEpochSoft` waits 2.1 s for that.

## Decided 2026-10-10 (P25-04, adversarial review of PR 90)

Ashwin delegated "take decisions on all items"; "Waiting on Ashwin" was empty. An independent agent attacked the PR; no must-fix. Decisions:

- The suite sets `Timeouts.Store` to 2 s: under CPU starvation 200 concurrent `Get`s exceeded the 50 ms default, opened the store breaker and failed `TestEngineCoalesceColdKey` (3 of 4 runs in the extreme case). The 50 ms default is not what this suite tests.
- SWR test: the refresh answers with `max-age=60`, so a stall cannot make it stale again. `engineWith` closes the store when `weir.New` fails. The header comment states why engine-side pauses are real-clock.
- New card P25-04b lists the scenarios P25-04 skipped (Invalid epoch, global soft, Vary through the codec, others). PLAN 2.5.2 unticked until it is done. Not added to this PR: the card said "new scenarios" are out of scope.
- The P25-05 API question moved to "Waiting on Ashwin"; not decided.

## Decided 2026-10-10 (P25-03b, adversarial review of PR 89)

"Waiting on Ashwin" was empty. An independent agent attacked the PR (including a 16-goroutine chaos run with FLUSHALL every 40 ms: no lost purge; at 3 ms it fails closed with `ErrUnavailable`); no must-fix. Decisions:

- `EpochNeverUnderInvalidates` was too loose for 3.8% of tags. It now runs a strict soft phase and a strict invalid phase (100 000 tags each, fresh stores, exact mode and time) plus a 20 000-tag mixed phase with the loosened rule. 05 §8 updated.
- `getSeed` maps a nil `HGET` (flush between `HSETNX` and `HGET`) to `ErrUnavailable` instead of leaking `valkey.Nil`.
- New tests: seed written without `meta.v` counts as loss (lookup and write), shared lookup after plane loss, nil seed; `TestSketchPlaneLossRepairs` now deletes only the plane.
- Documented, not coded: under an invalidation flood every lookup is `GET newest` plus a script (two round trips), a `newest` cache is the upgrade if P25-04 measures a problem; no single-flight on the seed fetch (bounded, convergent). 05 E-7 and the `Parallel` line corrected; stale breaker-hazard line below marked resolved.
- Declined: a flush-between-repair-and-reseed integration test (not deterministic; the unit test pins the one-retry caps).

## Decided 2026-10-10 (P25-03, adversarial review of PR 88)

Ashwin delegated "take decisions on all items"; "Waiting on Ashwin" was empty. An independent agent attacked the PR; no must-fix.

- `WAIT` blocks the full `HardEpochWait` when no replica acknowledges (single node, replica down), so a hard `Purge` over N tags costs N times that. The code comment and 05 §7 were wrong; both corrected, option stays off by default. Test now also checks a healthy replica returns well under the wait.
- Breaker hazard (resolved by P25-03b, which accepts invalid-mode writes on URI tags): the store used to refuse the engine's RFC 9111 4.4 invalidation writes and the guard counted them.
- 05 §7 now says: stores sharing a `Prefix` must use identical `MaxRetention` and `MaxClockSkew`; stale persistence (old RDB/AOF, lagging failover) is a known T-29 residual risk; a far-future hard `At` holds a cap slot.
- Declined as code: bounding `readArgs` (engine caller bounds it), a bounded wait for the dedicated connection (`ponytail:` note added). Test fixes: retry on the WAIT path, a vacuous cancel test now cancels mid-call, replica test waits for the link.

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

## Decided 2026-10-10 (P25-02, adversarial review of PR 87)

Ashwin delegated "take decisions on all items"; "Waiting on Ashwin" was empty. An independent agent attacked the PR; no must-fix. Decisions, written into 05 §7:

- A `Set` the codec rejects now deletes the record at the key and returns nil (S-4: new record or nothing, as the memory store does for oversized records). A past-expiry or clamped-away `Set` keeps the older record (05 §2.3 literal).
- `Get` returns `ErrNotFound` for a record past its `Expires` (05 §2.2), since the server clock differs.
- `Decode` stays bounded by the 512 MiB server limit; no new config field (needs approval). The residual download-per-`Get` from a planted value needs server write access and is documented. A `MaxValueBytes` field can be proposed later.
- Documented: same `Prefix` with different `HashTag` is a silent missed purge; the scrubber must read only 64-hex suffix keys; poisoned keys can open the store breaker.
- Tests: vacuous future-RequestTime case fixed (bounds the PXAT); integration `TestSetTTLIsClamped` checks PTTL on a real server.
- Kept: floating `valkey/valkey:8.1` tag (the minor was left to this card), `Value(string(val))` copy, error text prefixes.

## Decided 2026-10-10 (P25-01b, adversarial review of PR 86)

Ashwin delegated "take decisions on all items"; "Waiting on Ashwin" was empty. An independent agent attacked the PR; no must-fix. Decisions, written into 05 §7: standalone mode (`Cluster` false) takes exactly one address (valkey-go uses only the first; `Validate` rejects more, a config-behavior change inside the approved Config); the policy check is an allowlist (`noeviction`, `volatile-*`); `Close` waits for a real dial, and the doc no longer claims a single `CallTimeout` bound; a second `Close` waits for the first. Fixed: the watcher goroutine joins `wg`, a failed single-client dial is closed, `lastErr` is set when closed. Tests added for failing-dial fan-in, the end-of-attempt gap, second `Close`, standalone addresses. Kept: no recheck of the policy after connect (documented).

## Decided 2026-10-10 (P25-01, adversarial review of PR 85)

Ashwin delegated "take decisions on all items"; "Waiting on Ashwin" was empty. An independent agent attacked the PR; no must-fix. Decisions: empty `Prefix`/`HashTag` mean the default (card test list reworded); config errors wrap `weir.ErrInvalidConfig` (no new sentinel); upper bounds added (MaxRetention 10 y, MaxClockSkew 1 h, MaxHardEpochs 1e6, key parts 64 bytes); `HardEpochWait` whole ms and below `CallTimeout`; `Addrs` must be unique `host:port`. Fixed: `Validate` copy aliased `Addrs`/`TLS`, returns the zero Config on error; JSON marshalling leaked the password (now redacted); `mapError` leaves wrapped `valkey.Nil` and `ErrNotFound` alone. Written into 05 §7.

## Notes for the next session

- P25-04 done: `store/valkey/engine_integration_test.go` (tag `integration`, eight engine scenarios on the real clock, about 15 s). A fresh prefix starts with a repair epoch at `ceil(server now)`, and an entry fetched in that second is purged (05 §7, E-7), so `engineStore` primes the store and waits 2.1 s with `NoClockSkew`. Memory-store internal or wrapper-based scenarios are not repeated: flight-table bounds, the store-outage wrapper tests, the 5 000-key limiter cap, batch expiry spread, limiter/partition/miss-rate component tests.
- Still open from P25-03b: nobody has measured the two-round-trip lookup under an invalidation flood; P25-04 did not add a benchmark (not in its scope). The CI Valkey job has not run on Valkey 8.1; local runs use redis 7.0.15.
- P25-05 needs Ashwin's decision first (see Waiting on Ashwin).
- PR 90 review (2026-10-10): P25-04b added for the scenarios the suite skips; PLAN 2.5.2 is unticked until it is done.
- Work was done on `claude/optimistic-mendel-k6iji6`, not a `card/*` branch.

- P25-03b done: `store/valkey/sketch.go` (seed, positions, `NewestEpochShared`, `setSketch`). Loss is now `meta` without field `v` or a missing plane; the client writes `meta.seed` first with `HSETNX`, so `meta` alone no longer means "initialised". Scripts take the seed id and reply `SEED_CHANGED` (write: error reply mapped to `errSeedChanged`; read: `{-2}`); the read arguments are four per non-global tag. `SetEpoch` refuses only unknown modes now, so the store can be wired into an engine (P25-07 still depends on P25-04..06).
- `storetest.EpochNeverUnderInvalidates` now accepts a more severe colliding mode and checks time only for the tag's own mode (skew makes the other case legitimate); `Parallel(n)` added. The Valkey conformance run passes no `EpochModes` and takes about 10 s for 200 000 epochs locally (redis 7.0.15).
- A server upgraded from P25-03 has no planes: its first lookup purges everything once (05 §7).
- Container lint: `GOTOOLCHAIN=go1.27.0 go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` per module; `make -o lint check` stops at the caddy module with the Go 1.25 binary. CI must confirm.
- This work was done on `claude/compassionate-pasteur-k8ueay`, not a `card/*` branch.

- P25-03 done: `store/valkey/{epochs,scripts,meta}.go`. The client seam gained `evalWrite`, `evalRead`, `evalWriteWait`. Both scripts take the six epoch keys in `KEYS` (`Store.ekeys`, slots in meta.go); P25-03b fills `sketch:soft`/`sketch:invalid` and the `seed` field of `meta` (today `meta` holds only `v`), and must add the plane-absent loss check to the read script and `SEED_CHANGED` to both. `SetEpoch` refuses soft/invalid on non-global tags until then (`epochs.go`).
- Any write that finds `meta` absent also raises `global.hard` to server time (05 §7 note added). `HardEpochWait` sends `EVAL` + `WAIT` on one dedicated connection; the reviewer showed `WAIT` on the shared connection never waited. `TestHardEpochWaitWaitsForReplica` needs a pausable replica (`WEIR_VALKEY_REPLICA_ADDR`, `--enable-debug-command yes`) and skips in CI.
- `storetest.EpochModes` marks cases by needed modes; `EpochModes()` with none skips all epoch cases. P25-03b drops the option so all cases run.
- Local check: `redis-server --port 6390 --maxmemory-policy volatile-lfu --save "" --daemonize yes`, then `WEIR_VALKEY_ADDR=127.0.0.1:6390 go test -race -tags integration ./store/valkey/` (redis 7.0.15, not Valkey; the CI job has still not run).

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
