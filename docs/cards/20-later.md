# Phase 2, 2.5 and 3 entry cards

These phases start from draft specs. Each begins with one planning card that verifies the spec against current upstream code, asks the user any new questions, and writes that phase's implementation cards into this file (replacing the placeholder list). Phase 2 cards were written by P2-00 against Caddy v2.11.7; the Phase 2.5 and 3 cards are written by their planning cards.

## Phase 2: Caddy adapter

### [x] P2-00 Finalize the Caddy adapter spec and write its cards
- Plan: 2.1 · Size: S · Depends on: M15-01
- Read: 08 whole; 10 §2 (E7 affects the adapter's response path later); Caddy source at the latest release tag
- Touch: docs/08-caddy-adapter-spec.md (v1.0), docs/cards/20-later.md (Phase 2 cards), docs/09-research-notes.md (verified facts)
- AC: every Caddy API named in 08 checked at the pinned release with file and line; 08 marked v1.0; Phase 2 split into S/M cards (expected: module skeleton and Caddyfile, store pool and key-generation hash, nextOrigin and upgrades, errors and memory split, admin API purge/mode/stats, metrics, deployment guide)
- Notes: 08 is v1.0 against Caddy v2.11.7 (§11 has file and line for each API). Cards P2-01 to P2-07 (with P2-01b, P2-01c and P2-03b) below replace the placeholder; the plan items 2.2 to 2.4 are split across them.

### [x] P2-01 Module skeleton and JSON config
- Plan: 2.2 · Size: M · Depends on: P2-00
- Read: 08 §1, §2, §11; 04 §1 (Config); 01 FR-LCY-2
- Touch: caddy/go.mod (new, requires caddy v2.11.7 and the root module with a `replace` for local work), caddy/module.go, caddy/config.go (adapter-side config struct, name validation, byte sizes, mapping to `weir.Config`), caddy/module_test.go, go.work (add `./caddy`)
- Tests: `TestConfigFromJSON` (table: every key in the 08 §2 table, unknown key, missing `name`, bad `name` charset or length), `TestProvisionReportsBadConfig`, `TestInternalKeysImport` (compiles `keys.IsUpgrade` from the adapter module)
- AC: `http.handlers.weir` loads from JSON; `name` is required and matches `[A-Za-z0-9._-]{1,64}`; a bad engine config fails `Provision` (and so `caddy validate`); interface guards present; the module builds with `GOWORK=off`; the root module still has no `require` block
- Out of scope: Caddyfile parsing (P2-01b), CI job (P2-01c), the store pool (P2-02), serving (P2-03)
- Notes: go.work exists since M10-02; extend it. The Makefile finds submodules itself, so `make check` covers `caddy/` as soon as its `go.mod` exists. The `internal/keys` import compiles across modules by path (checked with a throwaway two-module build at P2-00); if it fails here, stop and ask: the fallback is an exported `weirhttp.IsUpgrade`, a public API change.

### [x] P2-01b Caddyfile parsing and directive order
- Plan: 2.2 · Size: S · Depends on: P2-01
- Read: 08 §1, §2, §5; `caddyconfig/httpcaddyfile` at v2.11.7 (08 §11)
- Touch: caddy/caddyfile.go, caddy/caddyfile_test.go
- Tests: `TestCaddyfileParse` (every key in the 08 §2 example, nested blocks, unknown key, missing `name`, byte sizes and durations), `TestDirectiveOrder` (adapted Caddyfile puts `weir` before `reverse_proxy`, and inside `handle` and `route` blocks)
- AC: the 08 §2 example adapts to the same JSON as the hand-written equivalent; errors name the line; `RegisterHandlerDirective` and `RegisterDirectiveOrder` are called in `init` (the Caddy module registration exception in CLAUDE.md)

### [x] P2-01c CI for the Caddy module
- Plan: 2.2 · Size: S · Depends on: P2-01b
- Read: 08 §9; Makefile; .github/workflows/ci.yml
- Touch: .github/workflows/ci.yml
- AC: a CI job runs `xcaddy build --with github.com/AshwinSathian/weir=. --with github.com/AshwinSathian/weir/caddy=./caddy` (the root `replace` is needed because no root tag exists yet) and starts the binary with a minimal Caddyfile; `GOWORK=off` runs of the `caddy` module are already covered by the Makefile and stay green

### [x] P2-02 Store pool and key-generation hash
- Plan: 2.2 · Size: M · Depends on: P2-01
- Read: 08 §3, §4b; 01 FR-FAIR-3, FR-SNP-1; 04 §5.2 (purge epochs)
- Touch: caddy/pool.go, caddy/keygen.go, caddy/pool_test.go, caddy/keygen_test.go
- Tests: `TestCleanupDeletesOnce` (Cleanup called twice releases one reference), `TestPoolSharesStoreAcrossReload`, `TestPoolSettingsMismatchFailsProvision` (same load, same name, different settings or hash), `TestPoolResizeReloadAllowed` (two settings for one name alive across loads), `TestPoolDestructsOnLastRelease`, `TestSupersededStoreSkipsSnapshot`, `TestKeyGenHashChangeWritesHardEpoch`, `TestKeyGenHashIgnoresHostAndKeyRules`, `TestMultiHostEnablesFairnessCaps`, `TestSingleToMultiHostStartsNewStore`
- AC: a reload with an unchanged key-generation hash keeps entries as hits; a changed `Forward.Mode`, `Forward.Allow` or `Storable.StripSetCookie` makes them misses (hard epoch, 08 §3); changing query rules, key headers, hosts or on-demand TLS domains changes nothing once the site is multi-host (going from one host to two starts one new store, 08 §3); same `name` with different store settings or hash in one load fails `Provision`, across loads it is allowed; the pooled value implements `caddy.Destructor` and closes within `SnapshotTimeout`; `MaxPerHost` and `MaxBytesPerOwner` default to 25% for multi-host sites
- Out of scope: serving requests (P2-03)

### [x] P2-03 nextOrigin, errors and upgrades
- Plan: 2.2, 2.3 · Size: M · Depends on: P2-02
- Read: 08 §4, §5, §6; 01 FR-UPG-1, FR-COA-9; 04 §10
- Touch: caddy/serve.go, caddy/origin.go, caddy/serve_test.go
- Tests: `TestUpgradeAndConnectBypassEngine`, `TestNextOriginUsesDetachedContext`, `TestErrorsReturnHandlerError`, `TestForwardedForWarning`, `TestPlaceholderHeaderWarning` (08 §6: `header_up` with a per-client placeholder after `weir` logs a one-time warning), `TestSecondHostWithoutMultiHostWarnsOnce` (a second distinct host while `multi_host` is off logs one warning naming `multi_host`; the engine remembers at most two hosts, never a set keyed by request input)
- AC: all listed tests pass; `Fetch` after the request finished never touches the original `ResponseWriter`; the one-time `X-Forwarded-For` warning appears for `reverse_proxy` without `header_up -X-Forwarded-For`; the multi-host warning appears once (P2-02 decided `multi_host` is operator-stated, so this is its only safety net); a request on a handler whose engine is closed (`ErrClosed`, `h.engine` stays set after `Cleanup`) gets a 503; `Origin.Fetch` is still called only in `(*Engine).fetch`

### [x] P2-03b End-to-end scenarios under caddytest
- Plan: 2.2, 2.3 · Size: S · Depends on: P2-03, P2-01c
- Read: 07 T6.2, T6.6, T6.12; 08 §9
- Touch: caddy/e2e_test.go
- Tests: `caddytest` scenarios for T6.2, T6.6 and T6.12; `TestRetryAfterSurvivesHandleErrors` (needs a full Caddyfile with `handle_errors`); `TestReloadKeepsWarmKeys` (100 keys survive a limiter change; a `forward.allow` change makes them misses; adding a host to an already multi-host site changes nothing)
- AC: all pass under the race detector

### [x] P2-04 Memory budget split and memory sizing
- Plan: 2.3 · Size: S · Depends on: P2-02
- Read: 08 §3, §7 (Memory); 01 FR-MEM-1; 06 T-43
- Touch: caddy/memory.go, caddy/memory_test.go
- Tests: `TestMemorySizingSplit` (stores without `max_bytes` in one load share 40% of the limit evenly; stores with `max_bytes` are excluded; an unset limit uses the FR-MEM-1 fallback and warns once), `TestMemoryShareFixedAfterBuild` (a later load adding a site does not resize or flush existing stores), `TestMemoryOvercommitWarns` (sum of live auto-sized stores above 40% logs a warning)
- AC: within one load the auto-sized stores sum to at most 40% of `debug.SetMemoryLimit(-1)`; existing stores keep their size on reload; the overcommit warning names `max_bytes` as the remedy; no auto-sized store is below the 160 MiB floor `Validate` enforces for explicit `max_bytes` (P2-02: clamp the share up with a warning, or add a `max_object_bytes` key after asking Ashwin, since that is a new config field)
- Notes: decided in 08 §7 (P2-00 review): the store interface has no resize, so the 40% bound holds per load, not across loads.

### [ ] P2-05 Admin API: purge, mode, stats
- Plan: 2.3 · Size: M · Depends on: P2-03
- Read: 08 §7, §11 (admin rows); 01 FR-PRG, D33; 06 T-26
- Touch: caddy/admin.go, caddy/registry.go, caddy/admin_test.go
- Tests: `TestAdminPurgeChangesCacheStatus` (purge via admin makes the next `Cache-Status` `fwd=stale`), `TestAdminEagerPurgeReportsScrubbed`, `TestAdminModeSwitch`, `TestAdminStats`, `TestAdminUnknownNameIs404`, `TestRegistryAcrossReloadOverlap`, `TestRegistryFailedLoadKeepsServingEngine`, `TestAdminPurgeBodyBounds`, `TestAdminModeAppliesToAllEngines`
- AC: routes `POST /weir/<name>/purge`, `GET /weir/<name>/stats` (JSON array, one entry per live engine), `POST /weir/<name>/mode` (applied to every live engine of the name) exist under `admin.api.weir`; the registry holds a set of engines per name and removes by identity, so a failed load leaves the serving engine registered; bodies are capped at 1 MiB, 1000 URLs and 100 groups (08 §7); `ErrClosed` maps to 503; no purge route is added to site listeners
- Out of scope: remote admin access control (documented in P2-07)

### [ ] P2-06 Prometheus metrics on Caddy's registry
- Plan: 2.3 · Size: S · Depends on: P2-03
- Read: 08 §8; 04 §9.3; context.go `GetMetricsRegistry` at v2.11.7
- Touch: caddy/metrics.go, caddy/metrics_test.go
- Tests: `TestEvictionSinkRepointsOnReload` (pooled store's evictions reach the new registry), `TestMetricsRegisteredOncePerRegistry` (two handlers in one load do not collide), `TestMetricsNamesAndNameLabel`, `TestMetricsSurviveReload` (new registry gets fresh collectors, no panic)
- AC: metric names match 04 §9.3 plus a `name` label; a pedantic registry accepts them; no collector is attached to a pooled store (its eviction sink is repointed each `Provision`); the card chooses between wrapping `observe/prom` and hand-rolled collectors and records why; the dependency is declared in `caddy/go.mod`

### [ ] P2-07 Single-node deployment guide
- Plan: 2.4 · Size: S · Depends on: P2-05, P2-06
- Read: 08 §4a, §4b, §5, §6, §7; 06 T-38, T-45, R-6; docs/runbook.md
- Touch: docs/runbook.md
- AC: guide covers one node per cache (D17), snapshot path and shutdown grace period, `encode` placement with both examples, `request_body max_size` and `read_body` timeouts, `rate_limit` before `weir`, auth handlers before `weir` (T-45), `header_up -X-Forwarded-For`, no per-client placeholders in handlers after `weir` on cached routes (08 §6), admin API exposure and purge examples, memory sizing with explicit `max_bytes` for multi-site instances (08 §7), the one-time flush when a site goes from one host to two, verification of the `rate_limit` directive order

## Phase 2.5: Valkey store
- Notes: 06 T-45 and R-6: the adapter spec must say where `weir` sits relative to authentication and variable-setting handlers, and the deployment guide repeats it.

### [ ] P25-00 Plan the Valkey store and write its cards
- Plan: 2.5.x · Size: S · Depends on: Phase 2 cards done
- Read: 05 §7, §8; valkey-go docs via Context7
- Touch: docs/05-storage-interface-spec.md, docs/cards/20-later.md
- AC: client library and version chosen with the user; cards written (expected: connection and codec, Get/Set/Delete, epochs sketch in Lua with `SharedTagEpochs` in the same one round trip (05 E-12; without it the 4% group residual of T-29 returns), conformance in CI with `-tags integration` (05 §8, else `ExpiredIsNotFound` skips), engine suite re-run, vary CAS, multi-node guide)

## Phase 3: Experiment dimensions

### [ ] P3-00 Finalize the experiments spec and write its cards
- Plan: Phase 3 · Size: S · Depends on: Phase 2.5 cards done
- Read: 10 whole; 06 T-35, T-36
- Touch: docs/10-experiments-spec.md (v1.0), docs/cards/20-later.md
- AC: open items in 10 §6 resolved with the user; cards written

## Deferred

### [~] M16-02 Pointer-light memory store layout
- Plan: M10.5b follow-up · Size: M · Depends on: M16-01
- Read: M16-01's recommendation; 05 §3, §4; store/memory/
- Touch: store/memory/, docs/05-storage-interface-spec.md, loadtest/gc_test.go, docs/benchmarks.md
- Tests: the store conformance suite unchanged; `Get` and `Set` allocation benchmarks; `TestGCAt1MEntries` reports GC µs per request at 1M entries against the D36 gate (2 µs)
- AC: GC µs per request at 1M entries is within the D36 gate on the box that measured 1.76 µs; `ServeHitSmall` stays within NFR-5 (16 allocs/op, 20% rule); `BenchmarkMemoryStoreGetParallel` does not regress over 20%; stored entries stay immutable (P4); `make check` passes
- Notes: deferred 2026-10-09. M16-01 recommended keeping the heap layout (prototype fails NFR-5 without a serve-from-encoded-bytes path). Reopen only if a deployment exceeds the D36 gate (2 µs of GC per request at 1M entries) or needs more than 1M entries, and then scope it as a design change first: the Tests and AC above were rewritten to the µs gate and still assume a layout that decodes per hit, which M16-01 showed fails NFR-5.
