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
- AC: a CI job runs `xcaddy build --with github.com/AshwinSathian/weir=. --with github.com/AshwinSathian/weir/caddy=./caddy --with github.com/AshwinSathian/weir/observe/prom=./observe/prom` (the root `replace` is needed because no root tag exists yet) and starts the binary with a minimal Caddyfile; `GOWORK=off` runs of the `caddy` module are already covered by the Makefile and stay green

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

### [x] P2-05 Admin API: purge, mode, stats
- Plan: 2.3 · Size: M · Depends on: P2-03
- Read: 08 §7, §11 (admin rows); 01 FR-PRG, D33; 06 T-26
- Touch: caddy/admin.go, caddy/registry.go, caddy/admin_test.go
- Tests: `TestAdminPurgeChangesCacheStatus` (purge via admin makes the next `Cache-Status` `fwd=stale`), `TestAdminEagerPurgeReportsScrubbed`, `TestAdminModeSwitch`, `TestAdminStats`, `TestAdminUnknownNameIs404`, `TestRegistryAcrossReloadOverlap`, `TestRegistryFailedLoadKeepsServingEngine`, `TestAdminPurgeBodyBounds`, `TestAdminModeAppliesToAllEngines`
- AC: routes `POST /weir/<name>/purge`, `GET /weir/<name>/stats` (JSON array, one entry per live engine), `POST /weir/<name>/mode` (applied to every live engine of the name) exist under `admin.api.weir`; the registry holds a set of engines per name and removes by identity, so a failed load leaves the serving engine registered; bodies are capped at 1 MiB, 1000 URLs and 100 groups (08 §7); `ErrClosed` maps to 503; no purge route is added to site listeners
- Out of scope: remote admin access control (documented in P2-07)

### [x] P2-06 Prometheus metrics on Caddy's registry
- Plan: 2.3 · Size: S · Depends on: P2-03
- Read: 08 §8; 04 §9.3; context.go `GetMetricsRegistry` at v2.11.7
- Touch: caddy/metrics.go, caddy/metrics_test.go
- Tests: `TestEvictionSinkRepointsOnReload` (pooled store's evictions reach the new registry), `TestMetricsRegisteredOncePerRegistry` (two handlers in one load do not collide), `TestMetricsNamesAndNameLabel`, `TestMetricsSurviveReload` (new registry gets fresh collectors, no panic)
- AC: metric names match 04 §9.3 plus a `name` label; a pedantic registry accepts them; no collector is attached to a pooled store (its eviction sink is repointed each `Provision`); the card chooses between wrapping `observe/prom` and hand-rolled collectors and records why; the dependency is declared in `caddy/go.mod`

### [x] P2-07 Single-node deployment guide
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
- Notes: client chosen with Ashwin 2026-10-10: valkey-go v1.0.78 (05 §7). Cards P25-01 to P25-07 below replace the "expected" list; plan items 2.5.1 to 2.5.5 are split across them. A Valkey server is needed from P25-02 on (Docker or `valkey-server` in PATH); unit tests that need none stay in the default run.

### [ ] P25-01 Module skeleton, config and error mapping
- Plan: 2.5.1 · Size: S · Depends on: P25-00
- Read: 05 §1, §2.1 (S-2, S-3), §7; 01 FR-STF-2; Makefile (submodule discovery); valkey-go `ClientOption` at v1.0.78
- Touch: store/valkey/go.mod (new; requires valkey-go v1.0.78 and the root module with `replace => ../..`), store/valkey/{doc.go,config.go,store.go,errors.go,config_test.go,errors_test.go}, go.work (add `./store/valkey`)
- Tests: `TestConfigValidate` (table: no addresses, bad `HashTag` charset, negative durations, defaults filled), `TestMapError` (context errors, `valkey.ErrClosing`, `net.Error`, `ErrNotFound` pass-through, all wrapped with `%w`), `TestInfo` (`{Name: "valkey", Remote: true}`), `TestNewDoesNotConnectWithBadConfig`
- AC: `valkey.New(cfg)` returns a `store.Store` with `Info()` and a `Close` that is safe twice; config holds `Addrs`, `Username`, `Password`, `TLS *tls.Config`, `HashTag` (default `e`), `MaxRetention` (24 h), `MaxClockSkew` (1 s), `MaxHardEpochs` (10 000); client built with `DisableRetry: true`, `DisableCache: true`; the module builds with `GOWORK=off` and the root module still has no `require` block; interface guard `var _ store.Store = (*Store)(nil)`
- Out of scope: any command to the server (P25-02), epochs (P25-03)
- Notes: new dependency approved by the P25-00 decision. Passwords never appear in errors or `fmt` output of `Config` (give it a `String` that redacts).

### [ ] P25-02 Get, Set, Delete and the CI Valkey job
- Plan: 2.5.1 · Size: M · Depends on: P25-01
- Read: 05 §2.2 to §2.4, §3 (codec), §8 (`ExpiredIsNotFound`, `ContextCanceled`, `ClosedStore`); store/codec.go API; .github/workflows/ci.yml; `store/storetest` options
- Touch: store/valkey/{entries.go,entries_test.go,integration_test.go}, store/storetest (only if an option is missing), .github/workflows/ci.yml, Makefile (target `test-valkey`)
- Tests: `TestKeyLayout` (unit: `weir:{e}:<hex(k)>`, hash tag omitted for entries when configured), `TestSetClampsToMaxRetention` (unit, fake client interface), `TestGetDecodeFailureIsUnavailable`, and under `-tags integration`: `storetest.Run` with `WithoutEpochs()` against a real server
- AC: `Get` decodes with `store.Decode`; `Set` encodes and issues `SET ... PXAT <ms>` with `Expires` clamped as in E-11; past `Expires` is a no-op; `Delete` is `DEL`; every call runs under the caller's context; CI job `valkey` starts `valkey/valkey:8` as a service container and runs `go test -race -tags integration ./store/valkey/...`; `ExpiredIsNotFound` runs (not skipped) in that job
- Out of scope: epochs; client-side caching
- Notes: wrap the client behind a small unexported interface so the unit tests need no server. Integration tests read the address from `WEIR_VALKEY_ADDR` and skip with a clear message when unset outside CI.

### [ ] P25-03 Hard and global epochs in Lua
- Plan: 2.5.1 · Size: M · Depends on: P25-02
- Read: 05 §4.1, §4.2, §4.3, E-5, E-6, E-10; 04 §4.3; valkey-go `Lua.Exec`
- Touch: store/valkey/{epochs.go,scripts.go,epochs_test.go}
- Tests: integration: `storetest` epoch cases `EpochPerModeKept`, `EpochHardCap` (with `HardEpochCap(cfg.MaxHardEpochs)`), `EpochSinceBoundary`, `EpochFastPath`, `EpochsMaxAcrossTags` restricted to hard and global; unit: `TestSkewAddsConservatively` (`At + MaxClockSkew >= since`)
- AC: `SetEpoch` for hard and global modes is one script call, `max` semantics, key expiry `MaxRetention`; the cap counter lives in the same script; `NewestEpoch` is one round trip and returns `ok == false` through the newest-epoch key when nothing is newer than `since` (E-10); a refused cap write wraps `ErrUnavailable`
- Out of scope: the soft and invalid sketch (P25-03b)
- Notes: all script keys share `{e}`, so cluster mode accepts them. Epoch values are Unix seconds rounded up (05 §4.3 note in the P25-00 edit).

### [ ] P25-03b Soft and invalid sketch, shared tags
- Plan: 2.5.1 · Size: M · Depends on: P25-03
- Read: 05 §4.4 (E-7 to E-9, E-12); store/memory/epochs.go (hash positions, `d = 2`); 06 T-29
- Touch: store/valkey/{sketch.go,sketch_test.go}, store/valkey/epochs.go
- Tests: integration: all remaining `storetest` epoch cases including `EpochNeverUnderInvalidates`; `TestSharedTagsSkipInvalidPlane` (a sketch collision in the invalid plane does not match a shared tag); `TestSketchMemoryFixed` (key size is 2 MiB per plane after 100 000 distinct tags)
- AC: soft and invalid epochs raise `d` cells of a 2 MiB string per plane in one script; `NewestEpochShared` is implemented (`store.SharedTagEpochs`), one round trip, never reads the invalid plane for shared tags; cell positions use a hash with a seed stored in the server (`weir:{e}:seed`, set once with `SETNX`) so all nodes agree, and the hash is not attacker-predictable per deployment
- Out of scope: client-side caching
- Notes: the seed is the one thing the memory store does per process and Valkey must do per cluster. If `EpochNeverUnderInvalidates` at 200 000 epochs is too slow over the network, stop and ask before shrinking the count (docs/07 criterion); pipelining the writes in the test is the first thing to try.

### [ ] P25-04 Engine suite against Valkey
- Plan: 2.5.2 · Size: M · Depends on: P25-03b
- Read: 07 §1, §6 (T6.x matrix); weir_test helpers that build an engine; hard rule 6 (real clock only under `integration`)
- Touch: valkey_engine_test.go in store/valkey (build tag `integration`), small test-helper changes in the root package only if an engine test hard-codes the memory store
- Tests: the T6.x scenarios that do not depend on the memory store's internals (coalescing, SWR and SIE windows, hard and soft purge, group purge, negative cache, breaker) run with a Valkey store and real time
- AC: all selected scenarios pass in the CI `valkey` job; any failure is fixed in 05 first, then in code; the list of excluded scenarios and why is in the card Notes at handoff
- Notes: root-module tests may not import valkey-go, so the suite lives in the `store/valkey` module and imports the engine.

### [ ] P25-05 Vary-spec compare-and-set
- Plan: 2.5.3 · Size: M · Depends on: P25-04
- Read: 04 §6.7 (vary spec read-modify-write), line 873 and 1280; 05 §2.3, §7 last note; 01 FR-VAR requirements
- Touch: docs/05 and docs/04 (the capability), store/store.go (optional interface), store/valkey/vary.go, vary_test.go, the engine's vary update path
- Tests: integration `TestVaryCASConcurrentWriters` (64 concurrent writers of distinct variants never leave more than `MaxVariants` refs), `TestVaryCASFallsBackWithoutCapability`
- AC: as the plan: 64 concurrent writers never exceed `MaxVariants`; the memory store behavior is unchanged
- Notes: ASK THE USER FIRST. 05 §7 says the CAS stays behind the same `Set`, but a store cannot know a `Set` is a spec update that must merge. Options: (a) an optional capability `VarySetter` (public API addition), (b) a version token in `Entry` (public type change). Both need approval (CLAUDE.md "When to stop and ask").

### [ ] P25-06 Eviction-storm review and Scrubber via SCAN
- Plan: 2.5.4 · Size: M · Depends on: P25-04
- Read: 00 T6.11; 05 §5.3 Scrubber note, 04 §13.5; valkey-go `Scan` and cluster `Dedicate`
- Touch: store/valkey/scrub.go, scrub_test.go, docs/09-research-notes.md
- Tests: integration `TestScrubByTag` (entries removed or hard-expired; epochs untouched), `TestEvictionStormEngineStaysCorrect` (server `maxmemory 16mb`, policy `allkeys-lfu`, flood of one-hit keys, engine keeps serving and never returns a stale-after-hard-purge entry)
- AC: `Scrubber` implemented by background `SCAN` with a count bound per call and context checks; storm results (hit ratio before and after, evicted epoch keys) written up in 09; if epoch keys can be evicted, the finding and the fix (volatile-only policy requirement or `noeviction` for epoch DB) are in 05 §7
- Notes: an evicted epoch key is a missed purge, which is a security failure, not a cache miss. Decide in this card whether the operator must put epochs on a separate instance or a policy that never evicts them.

### [ ] P25-07 Adapter wiring and the multi-node guide
- Plan: 2.5.5 · Size: M · Depends on: P25-05, P25-06
- Read: 08 §3, §4b; 02 D17; docs/runbook.md section 7; caddy/pool.go
- Touch: caddy/{config.go,caddyfile.go,pool.go} (a `store valkey { ... }` block), caddy tests, docs/08, docs/runbook.md (section 8), docs/02 (D17 lifted)
- Tests: `TestE2ETwoNodePurge` (two Caddy instances, one Valkey; a purge on node A changes node B's next `Cache-Status` to `fwd=stale`), config table cases for the new keys
- AC: purge on one node is observed on another in an integration test; the guide covers clock skew (`MaxClockSkew`), one Valkey per cache, key-generation hash across nodes, failure behavior (store breaker, FR-STF-2), shutdown order
- Notes: ASK THE USER FIRST: a new config block and a lifted decision (D17). The caddytest harness fixes ports 2999 and 9080, so two Caddy instances in one test need two processes or a second harness; settle that in the first hour and split the card if it grows.

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
