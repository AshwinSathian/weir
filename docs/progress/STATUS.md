# Status

Updated: 2026-10-10
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: claude/adoring-curie-pvckor
PR: https://github.com/AshwinSathian/weir/pull/75
Next card: P2-01c (CI for the Caddy module)

## Waiting on Ashwin

none

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

## Notes for the next session

- P2-02 must (a) reject or expand `{...}` in `snapshot_dir` (the Caddyfile path keeps `{env.X}` literally), (b) test that two handlers with the same `name` and different config in one load fail (08 §3), including a `weir` in `handle_errors`.
- P2-01b: `caddy/caddyfile.go` parses the `weir` block into `Handler` (strict keys, single-set keys, errors carry the Caddyfile line); `init` registers the directive and `RegisterDirectiveOrder(Before, reverse_proxy)`. `route` keeps written order, so weir must be written first there (pinned by a test). Caddyfile tests import caddy `standard`, `encode` and `reverseproxy`, so `caddy/go.sum` grew. Two `weir` directives with the same `name` in one config are not rejected yet: P2-02 (store pool) should own that. Integer upper bounds are left to `weir.New`.
- P2-01: Go package in `caddy/` is named `weircaddy`. `Handler.weirConfig()` maps adapter settings to `weir.Config`; `Provision` builds the engine with `weir.New` (default store). `max_bytes` and `snapshot_dir` are parsed and validated but not applied: P2-02 builds the pooled store and must pass it as `Config.Store`. `ServeHTTP` is a pass-through until P2-03.
- P2-01: `decodeStrict` in caddy/config.go mirrors Caddy's strict module decoding; P2-01b's Caddyfile `UnmarshalCaddyfile` must fill the same `Handler` fields and reject unknown subdirectives. Added keys beyond the 08 example (documented in the 08 §2 table): `query_keep`, `normalize_path`, `bypass.headers`, `max_queue`, `while_revalidate`.
- P2-01: lint for submodules ran with `GOTOOLCHAIN=go1.27.0 make check GOLANGCI="go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0"`; this passes in the container.

- P2-00: 08 is v1.0 (Caddy v2.11.7, §11). P2-01 must prove `internal/keys.IsUpgrade` imports from the `caddy` module; if not, stop and ask (fallback is an exported `weirhttp.IsUpgrade`, public API). Admin routes need a package-level engine registry; metrics collectors are per registry. Cards P2-01b, P2-01c and P2-03b were added to fit size M.

- M15-01: `store/memory/scrub.go` holds `Scrub`; `hasTag` is O(entry tags x purge tags) under the shard lock (ponytail in the file, upgrade path named). `Engine.scrub` in purge.go reports the deleted count as `EvPurge{hard}` `Status`. Work happened on the session branch `claude/serene-volta-d9nvnj`, not `card/M15-01-*`.

- M14-01: `limiter.Acquire(ctx, class, part, host)`; `Classified.HostH` is the host hash; `LimiterConfig.MaxPerHost` feeds both pools. Waiters blocked by the host cap still share `MaxQueue` (ponytail in limiter.go, LLD 13.4, pinned by `TestHostFloodFillsSharedQueue`).
- M14-01: `memory.Config.MaxBytesPerOwner` is per shard, 0 off, zero `Owner` exempt. The engine's default memory store does not set it; the Caddy adapter does (FR-FAIR-3).
- Work for M14-01 happened on the session branch `claude/peaceful-babbage-hrdup8`, not `card/M14-01-*`.

- M13-02: loader writes a global invalid epoch (not soft) at load, and a global hard epoch when a possible epoch record is lost or the file cannot be removed. `Engine.Close` falls back to `Store.Close()` when the grace ctx is spent; a failed `New` closes with a cancelled ctx (no snapshot). Load counts stay in unexported `Store.snapLoad` (decision: no public accessor yet; FR-SNP-2 says so). Queue placement is not restored on load (ponytail comment). M14-01 card notes the loader must honor quotas. Lint: run it locally with `GOTOOLCHAIN=go1.27.0 go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run` (the installed binary is Go 1.25 and cannot load the config).

- M13-01: `memory.Store.CloseContext(ctx)` writes the snapshot; `Close()` wraps it with `Config.SnapshotTimeout` (5s). `store.Store` still has `Close() error`. The engine does not call `CloseContext` yet: M13-02 or a follow-up must wire `Engine.Close` via an optional interface so the adapter grace period bounds it (LLD 13.3 says so). The header's base wall time is written but unread. Hard-epoch records hold tag + UnixNano; writer emits main-queue records, then small, then epochs, then trailer 0xFF with the count. `snapRecord` parsing in snapshot_write_test.go can seed the loader tests.
- Lint unrun for M13-01 (container golangci-lint is Go 1.25); CI must confirm.

- M12-02: targeted fields live in `httpcc.ParseResponse` (`Targeted` flag; `Lifetime` and `hasFreshness` skip Expires). Header keys must be canonical (`Cdn-Cache-Control`). `finish` strips Weir-Cache-Control by cloning the map. Target list is a package constant (D12), signature unchanged.
- After M12-02 merges: run `UPDATE=1 make cache-tests` (node, network), drop the cdn-* row in `docs/cache-tests-expected-failures.md`, refresh `testdata/cache-tests-baseline.json`.

- M12-01: `sfv.ParseDictionary` returns `Dict` of `Item{Kind, Bool, Int, Dec, Str}`; the zero Item is a valid Boolean false, so check the lookup's ok. M12-02 must reject Decimal or negative Integer for `max-age`-style directives itself (FR-TCC, docs/04 §13.2). Drop `TCC` from `later` in scripts/trace.sh in M12-02 (FR-TCC-2..5 are uncited until then, so dropping it earlier fails trace-strict). Pass a small `maxMembers` (64). Reject any Kind but Integer for `max-age`-style directives. Decided: `private`, `no-store` and `no-cache` count when present whatever their value (`private=?0` still counts), the safe side of FR-TCC-3 "found".

- M11-01: Range on a stored 200 (hit, SWR) is sliced in `fromEntry` (respond.go); the client's 304 check runs first. A stored `Accept-Ranges: none` makes Weir ignore Range (full 200). FR-SRV-5 and FR-RNG-1 in docs/01 say so.
- M11-01: `docs/cache-tests-expected-failures.md` still lists the three `partial-store-complete-reuse-partial*` rows as not built, and the baseline is unchanged. Run `UPDATE=1 make cache-tests` (needs node and network) after merge, drop the row, refresh `testdata/cache-tests-baseline.json`.
- M11-02: the fill is `rangeMiss` in serve.go (wraps `pass`); a stale entry under the key becomes the prior, so the fill revalidates it. Decided after adversarial review: a Range miss returns the origin's 206/200 whatever preconditions the client sent (compliant; hits evaluate them), HEAD never fills, an over-size fill leaves a hit-for-miss marker, `Content-Range` parsing is strict. Residual (ponytail, docs/04 §13.1): a fill that errors or gets a 5xx repeats per Range miss, bounded by the Background limit.
- M11-02: a HEAD Range miss is still forwarded as GET with Range and answered with the origin's 206, while a HEAD Range hit gets the 200 (FR-RNG-3). The disagreement predates M11-02 (tested in TestRangeGarbageNotPoisoning); a later card may route HEAD around the Range pass.
- M11-02: not tested: a fill under a Vary spec (reads correct: `lk.ck` is the variant key) and a hard-purged entry (`purged` is not passed to the fill).
- `ParseRange` accepts whitespace before `=` (`bytes =0-1`); lenient, harmless on a hit.

- M10-10: RFC 9110 §13.2.1 makes evaluating preconditions on any 2xx a MUST, 9111 §4.3.2 a SHOULD for stored 200/206; FR-SRV-2 keeps stored 200 and GET/HEAD only (compliant, conservative; 203/204 are gaps; a 206 built from cache is not stored, so M11-01 serves it after the 304 check). A hit with Range and a matching `If-None-Match` answers 304 (tested); a miss with Range goes to the pass-through, which drops client preconditions (FR-FWD-1). Revisit that in the M11 card.

- M10-06: `make check` now runs `trace-strict`; FR-RNG, FR-TCC, FR-SNP and FR-FAIR are allowlisted in scripts/trace.sh. Drop each prefix from `later` in the PR that adds its first citing test.
- M10-06: runbook states the 300 000 keys at 1 000 rps figure from the M8-02 notes; re-measure if the miss-rate tracker changes.

- M10-05: all numbers come from a 4-core Xeon; rerun the benchmarks and `make load` on the reference machine and replace the M10 tables in docs/benchmarks.md.
- M10-05: parallel miss-rate tracker cost recorded only (decided 2026-10-08); no card for `TryLock` or per-shard counters.
- M10-05: a follower can miss the store just before the leader stores, then join after the flight is gone and start a second flight (0.3 to 0.4% extra origin calls in the coalesced burst benchmark). Unfixed, no card; see docs/benchmarks.md.
- golangci-lint cannot run in cloud sessions (built with Go 1.25); CI must confirm lint.
- `pragma-response-no-cache-heuristic` is a Go `net/http` artifact (`fixPragmaCacheControl`), documented as by design.
- Run the nightly workflow once after the M10-04 merge; PLAN M10.4 stays unticked until it is green.

## Decided 2026-10-09 (M14-01, review of PR #70)

Ashwin delegated the Waiting item; an independent agent attacked the PR and both decisions.

- FR-FAIR-2 scan window: 64 nodes from each queue tail (up to 128 in all), not 64 in total. A shared budget let a foreign small-queue tail hide an owner's entries in main, so the owner could never turn over its own quota, which FR-FAIR-2 rejects. FR-FAIR-2 and 05 §5.3 reworded to "from each queue tail"; cost stays constant under the shard lock.
- Host-queue sharing: keep the ponytail, no per-host queued cap in M14-01. FR-FAIR-1 caps in-flight fetches only, other hosts find free global slots while the flooder is capped, and a per-host queue policy is a new requirement-level behavior. A test pins the ceiling so an upgrade has a target.

## Decided 2026-10-08 (M10-06, review of PR #62)

Ashwin delegated the Waiting items; an independent agent attacked the PR and the decisions.

- M16-01 approved as a throwaway prototype, with changes: it also reports GC CPU per request (about 1.9 µs now) and the `ServeHitSmall` ns and allocs on the prototype, and measures `GOGC=200` and an allocation cut as cheaper alternatives. M16-02 is not pre-approved. D36 now records the measurement and that the layout changes only through M16-02. Against it: D36 chose the heap on purpose and decode-on-`Get` strains NFR-5. It wins because M16-01 is size S, merges nothing and ends in a recommendation.
- NFR-5: the 16 allocs/op bound and the 20% rule apply on any machine now; the 4 µs ceiling stays provisional until six or more M-series runs at the M10 head are recorded and it is set to 1.5 times the median (docs/01 NFR-5, docs/benchmarks.md). Numbers unchanged.
- M10-10 approved (last Phase 1 card), widened to every GET/HEAD 2xx built from a stored or just-stored entry. Confirm RFC 9110 §13.2 first (the reviewer's reading, from memory, is that evaluating preconditions is a MUST, which would make serving 200 non-compliant, not just wasteful).
- NFR-4 corrected: the bound holds while fetches hold their slots; a live over-limit stream keeps up to `MaxObjectBytes + 1` of read-ahead after its slot is released (measured 49 164 bytes against 16 388 with 12 slow readers). Wording in docs/01 and the runbook now says so; no code change. Open: whether to cap those streams in code. No card; raise it if slow clients on large responses matter.
- Review fixes: `Forward.Allow: ["Cookie"]` is rejected, not warned (README); `SetMode` reach is limited by `Freshness.Keep`; `HonorRevalidation` scope; limits row; `TrackingParams` caveat; stale docs/07 and README status text.
- Left alone: CLAUDE.md line 60 still says "trace report" (project instructions; change it when you next edit that file). `doccomments_test.go` covers the root module only. The `"Cookie"` case in `warnForwarding` is dead code. trace.sh has no floor check for a zero-ID spec grep, and counts citations in comments anywhere in a test file; both low risk.

## Decided 2026-10-08 (M10-09, review of PR #60)

Ashwin delegated the open question ("take decisions on all items"); an independent agent attacked it.

- `conditional-lm-fresh-no-lm` and `conditional-lm-stale` stay by design. The first expects 304 for a `Date` later than the client's IMS, which RFC 9110 §13.1.3 answers with 200. The second is outside FR-SRV-2 as written ("on a hit"); serving 200 is compliant and safe. Not implemented in this PR: it changes a requirement, and doing it only after a 304 would be arbitrary (a cold miss and a follower also answer 200). Optional card M10-10 holds the coherent rule and its risks.
- Review fixes: `ParseDate` also rejects fractional seconds in RFC 850 dates and a misplaced double space in asctime; tests added.
- Behavior notes: request `max-age =0` is now invalid, so it no longer forces revalidation (request directives are advisory, D5). With `Freshness.DefaultTTL` above 0, a loose `Last-Modified` now counts as absent and gets the default TTL; with the default of 0 the entry is not stored.
- Cache-test details for the two conditional ids were read off the suite's own run logs; the suite source is at the pinned ref in `scripts/cache-tests.sh`.

- `ParseDate` checks field widths by hand after `time.Parse`; `max-age =3600` and `max-age= 3600` are non-integers (request-side delta directives too); `If-Modified-Since` compares at whole seconds. The card's suspected cause for `conditional-lm-*` was wrong: see Waiting on Ashwin.

## Decided 2026-10-07 (M10-04)

From the adversarial review of PR #58 (Ashwin delegated "take decisions on all items"). "Waiting on Ashwin" held three items:

- The 18 failures first listed as unexplained are now settled. Kept as design, with reasons in docs/cache-tests-expected-failures.md: invalid `Age` ignored (RFC 9111 §5.1), qualified `no-cache` read as unqualified, exact Vary value keying, malformed `Transfer-Encoding` gives 502, no relay of 1xx (a Phase 1 non-goal, no card), the ETag retry (harness). Seven are real deviations and went to new card M10-09: lax `ParseDate`, whitespace around `=` in `max-age`, `If-Modified-Since` at whole seconds (suspected), response `Pragma` with heuristic freshness (cause unknown).
- Branch protection: ci.yml now has an aggregator job named `check` that needs the matrix job, so a required check called `check` keeps working.
- Nightly run: cannot be done from a session. Run workflow_dispatch once after merge; PLAN-weir.md M10.4 stays unticked until it is green.
- Review fixes: fuzz listing fails on compile errors, services run in their own process group (no leaked origin), ports checked before start, the rerun intersects the two runs, ref mismatch warns, logs uploaded on failure, job timeout 30 min.
- Counts corrected: 105 failures, 87 explained by decision or requirement before this round, 18 first unexplained; all 105 now carry a reason.
- AC "each failure cites a decision ID" is met as: D-number where one exists, else a requirement ID, RFC section or card ID (13 rows have no D-number).

## Decided 2026-10-07 (M10-03)

From the adversarial review of PR #57 (Ashwin delegated "take decisions on all items"). "Waiting on Ashwin" was empty.

- Steady p99 had one bucket of headroom: now asserted on the median of six 10 s windows (one hiccup no longer fails the run). Headroom on a 4-core box is still one bucket; the reference machine should have more.
- Flood comparison floor (50 µs): kept. Run-to-run "alone" p99 swung 98 to 180 µs, so a tighter floor would flake; the floor is written in docs/07 §9. Offered normal load is now asserted (>= 95% of 500 rps, no drops), and `openLoop` accounts by wall clock, not ticks.
- Synchronized expiry could pass vacuously: now also asserts >= 90% of keys refetched and 0 client errors.
- Goroutine baseline per test (not once in TestMain): declined. Each test settles to its own base before and after Close, and tests run sequentially; a TestMain base would be skewed by runtime goroutines started by the first scenario.
- Citations fixed (FR-CB-*, FR-MR-1 to FR-MR-3), set-epoch added to the store-errors row, Makefile says 5 minutes. Seeded randomness and a tighter flap bound: declined (hard rule 7 covers engine code only; the flap bound is stated in docs/07).
- The card AC "passes on the reference machine" is partly met: rerun `make load` there and replace the table in docs/benchmarks.md.

## Decided 2026-10-07 (M10-08)

From the adversarial review of PR #55 (Ashwin delegated "take decisions on all items"). "Waiting on Ashwin" was empty.

- Lint did not run locally (golangci-lint built with Go 1.25): merge on a green CI `make check`; no card.
- Branch is `claude/quirky-feynman-yni9ir`, not `card/M10-08-...`: accepted, the session fixed it.
- Tag order is an implicit contract: kept, now pinned from both sides (the order test in storable_test.go, `TestSharedTagsKeepURIInvalidation` and the under-flood variant), commented on `Entry.Tags`, and stated in 04 §3 and 05 E-12. Rejected: a typed field for group tags, which changes `store.Entry` and the codec for no behavior.
- A wrapper store that hides the capability gets the old 4% residual: accepted and documented on the interface; no event, since the wrapper's author controls it.
- Remote stores: P25-00 AC now requires `SharedTagEpochs`.
- docs/07 lists the new tests. 04 §6 pseudo-code stays as is (the split is internal to the guard).

## Decided 2026-10-06 (M10-01)

Ashwin delegated every waiting item in chat ("adversarially review and take decisions on all items"). Each was attacked against the code and the documents first.

1. Shared group tags and the epoch sketch: option (a), as an optional store capability, in its own card M10-08, which is now the next card. Not (b): 2^22 slots cost 32 MiB per memory store, more than the 16 MiB minimum store and outside the FR-MEM-1 budget, and the rate is linear in the flood, so 8 times the requests (about 130 a second against a one-hour lifetime) restore the 4%. Not (c): the flood is cheap for an unauthenticated client, a deployment with 100 groups has about four hit per entry lifetime, and invalidated entries are not served stale on error, so the burst sheds instead of degrading. Not deferred to Phase 2.5: an optional capability does not reopen the `Store` contract, and the fix belongs before the README card. Not in PR #54: it is a store contract addition with a 06 §6 checklist of its own, outside the M10-01 card.
2. `EvCoalesceTimeout` stays silent for a creator with no stale entry that keeps waiting. The followers of the same flight emit `direct`, and a stuck fetch ends in `EvFetchEnd` at the origin timeout, so nothing is hidden; a third reason would describe a request that did nothing.
3. An event-stream response now emits `EvNotStored` with the new reason `stream`. FR-OBS-1 lists "not stored" without exemption, and an SSE route fetched on every request with no event explaining it is a support question.
4. A follower answered from its flight now has the `EvRequest` reason `collapsed`. As `miss`, a 1 000-request stampede read as 1 000 misses against one origin fetch, and FR-MR-1 already counts followers like hits. RFC 9211 keeps `fwd=uri-miss; collapsed` in `Cache-Status`; `Info` still carries that.
5. `EvEvict.Status` is the record count and only the store `New` builds emits it. A helper or a store capability for a caller-built store would be new public API for three lines the caller can write in `memory.Config.OnEvict`.
6. Review nits closed in the same PR: `EvFetchEnd` is deferred, so it also fires when the origin calls `runtime.Goexit`; `revalidated` needs a forward made for a stale entry (`Fwd` stale or request), so a 304 to a forward without validators is a `miss`; a Range pass and the doubled events of a repeated conditional fetch are asserted. Left: `TestStats` cites no requirement ID because 01 defines `Stats` in §4 without one.

## Decided 2026-10-06 (M9-03)

Ashwin delegated all three in chat ("adversarially review and take decisions"). Each was attacked against the code and the documents first.

- Origin tag: entries no longer carry it in `Entry.Tags`; it stays as `Owner`. The option first recommended (the memory store skips the sketch for origin tags) cannot be built, because tags are opaque hashes and a store cannot tell an origin tag from any other. Filtering it at lookup would need a copy on the hit path or a fixed position in the slice. Dropping it is safe because no purge or invalidation names an origin, and it removes the failure: one sketch false positive on a tag that all entries of an origin share revalidated the whole origin (about 4% of processes under the 07 flood). `TestInvalidationFloodBounded` now passes 300 of 300. 01 §2, 02 ADR, 05 §4 and E-8, 06 T-29 say so; E-8 also states the rate per tag looked up, so an entry in `g` groups sees about `1 - 0.96^(1+g)`. An origin-wide purge, if ever added, needs an exact epoch like the global one.
- `CacheGroups.Ignore` switches off `Cache-Group-Invalidation` only. `Cache-Groups` is parsed, limited and tagged either way. This is the reading the requirements already had (FR-STO-10 unconditional, `Ignore` named only in FR-INV-2); the first draft's wider reading would have taken `Purge` by group away from operators who set `Ignore` for T-25, silently. A malformed `Cache-Groups` therefore refuses storage under `Ignore` too, the safe side of hard rule 12. 01 FR-STO-10 and the defaults table, 04 §7 and the `CacheGroupsConfig` comment say so.
- `purge-group` stays. Rejecting a name no stored response can carry catches operator mistakes and saves hard-epoch slots (05 E-6). Known edge for Phase 2.5: nodes with different `Limits.MaxGroupBytes` disagree on which names are valid; the error is explicit, so the operator sees it.

## Decided 2026-10-06 (M9-02, the two items that waited)

Ashwin delegated both in chat ("choose the best course of action"). Each was attacked against the code before it was decided.

- Soft purge delay: fixed in `httpcc.Evaluate`, `staleness = max(staleness, now - epoch.At, 0)`. An epoch that applies makes the entry stale now, even when the store times it ahead of the clock. Chosen over a store-side change (returning a rounded-down time) because it needs no store contract or conformance change and also covers Phase 2.5, where a purge written by a node with a fast clock would otherwise be ignored for the skew. The M1-03 row "soft purge in the future leaves a fresh entry fresh" is reversed; no document motivated it. Costs, both bounded by the 1 s rounding and stated in 01 FR-PRG-2, 04 §7 and 05 E-8: stale windows up to 1 s long, and a refresh sent inside the purge's second can repeat until the second passes (the first review missed this; `TestSoftPurgeRepeatsEndWithTheSecond` pins it). Unsafe-method invalidation always behaved that way.
- Hard-epoch cap and the store breaker: `Purge` writes through `storeGuard.purgeEpoch`, which reports a failed write (`EvStoreError`, the returned error) without counting it. Chosen over a new `store.ErrEpochCap` sentinel: that adds public store API and protects only stores that adopt it, while this holds for any store. Not counting a real outage here loses nothing, since requests count it. 01 FR-STF-2, 04 §5.2 and 05 E-6 say so.

## Decided 2026-10-06 (M9-02)

Decided by the session that wrote the card after the card reviewer and a second, adversarial agent attacked each open item with scratch tests (Ashwin asked for that review in chat).

- Purge URLs are cut by hand and classified with `keys.Classify`, not `net/url`: kept. 37 request targets under 6 key configs went through `weirhttp.RequestFrom`, `Serve`, a hard purge of the same URL and `Serve` again; none stayed a hit. The first version rejected any `@`, which made `/@scope/pkg` unpurgeable; host validation already rejects userinfo, so that check is gone.
- `TestPurge5000KeysBounded` purges by URL here: kept, and 07 T6.12 now names both forms. It asserts exactly 48 refreshes (`MaxConcurrent - ReserveForeground`); the looser 1..64 passed with a limiter that allowed one.
- `Eager`: changed from ignored to FR-PRG-8's behavior without a scrubber. Soft is `purge-eager`; hard writes its epochs and returns `ErrEagerUnsupported`. M15-01 adds only the `Scrubber` branch.
- `EvPurge` on a half-written purge: changed, emitted when at least one epoch was written (04 §9.2). Not emitted before writing, which would report a purge an open store breaker refused.
- URL error text: changed to `weir: invalid request: <reason>: purge url <index>`, the shape `config.go` uses, with one prefix.
- Kept: an empty `Purge{}` is nil (FR-PRG-1 "any combination"); the caller's ctx is not detached (a caller waits on the error, and detaching would hold it for tags x `Timeouts.Store`); `Origin` must be exactly `scheme://host[:port]` and is checked whenever set.

## Decided 2026-10-06 (M9-01)

Decided under Ashwin's delegation by the session that wrote the card, after the card reviewer's attack. "Waiting on Ashwin" was empty; these are the item PR #51 left open and the design choices it listed.

- Byte Sequence parameter values: tightened. The alphabet-only check accepted `:=a=:` and `:a=Gk:`, which no base64 decoder reads and RFC 9651 §4.2.7 says fail. Now: base64 characters, then at most the padding the length calls for; missing padding is still accepted (the RFC's SHOULD). Rejected: decoding with `encoding/base64` (an allocation for a value that is dropped).
- One sentinel for bad syntax and over-limit input: kept. 04 §8.5 gives both the same outcome and no document names separate reasons. Split it when an event or log field needs the difference.
- Line-by-line parsing instead of the ", " join: kept. The fuzz target asserts equality with the joined parse on every accepted input; the only inputs the join accepts and this rejects are a String split across lines, which fails closed.
- Members alias the header line: kept. `store.Tag` is a 32-byte hash, so the consumer never keeps the name; cloning would cost up to 32 allocations per stored response for nothing.
- Limit of 0 admits nothing: kept. `New` resolves 0 to the default and rejects negatives, so the engine never passes one.

## Decided 2026-10-06 (M8-02)

Decided by two independent adversarial reviews under Ashwin's delegation, then checked by the card reviewer. Each item was attacked with scratch tests before it was kept or changed.

- What a miss is (01 FR-MR-1 now says it): a request that started a fetch of its own and was not answered from the store. Shed and failed fetches are misses. Followers count like hits whatever their flight returned. `only-if-cached` and circuit-open refusals are not counted. Found by attack: 600 `only-if-cached` requests, or 600 followers of one failed fetch, got a path throttled for at most one origin call.
- Throttle timing (01 FR-MR-3 now says it): the engine applies a report only while `end + Window` is ahead; the limiter holds it until the next report or `end + 2*Window`. A cap that ended at `end + Window` let 16 fetches through at every window boundary.
- No timer for expiry: the next `Acquire`, `Release` or waiter timeout drops the report. Rejected: `time.AfterFunc` (a goroutine for `Close` to own).
- Shed counts as a miss: kept. Without it the cap flaps between 16 and 1 every other window, about 8.5 times the origin load.
- Uncacheable or always-revalidated busy paths are flagged every window and capped under `Throttle`: kept and documented (`Throttle` comment, 04 §8.4, 06 R-8). Request headers cannot be excluded without handing a flood a "do not count me" flag; a bypass rule is the remedy.
- Tracker mutex on the hit path: kept, measured, left to M10-05 (first note below).
- `MissRate.MinRatio` above 1: now rejected by `New` (`ErrInvalidConfig`). It turned detection, and `Throttle` with it, off silently. `Breaker.FailureRatio` already had the same rule. Rejected: clamping to 1 (hides the mistake).
- Throttle against the other classes, attacked and kept: a shed half-open probe is handed back, a background refresh without a slot is dropped with the stale entry served, `Warm` runs one fetch at a time. Each is pinned by a test that fails when the behavior is removed.
- `TestOneHitWondersDoNotEvictHot` now runs the flood on 16 goroutines beside the hot reads, as docs/07 words it. With store promotion broken it scores 28 of 200.

## Decided 2026-10-05 (M8-01)

Open items of the M8-01 handoff, decided under Ashwin's delegation after trying to break each choice (PR #49). Nothing was listed under Waiting on Ashwin; these were the findings left open.

- Windows reported out of order (review S2): fixed in the report shape. `emit(end, found)` carries the window's end time, which grows with every window; the receiver keeps the latest. Rejected: a second mutex to serialize `emit` (the rotating caller would hold the tracker's lock while it waits, which blocks every request and breaks P8); dropping a late report in the tracker (its anomaly event would be lost).
- Throttle left standing when traffic stops, and the worse case found while attacking it: a flood, an idle hour, then one request reported the hour-old window, and a receiver would throttle on it. Same fix: `end` lets the limiter apply a throttle only while `end + Window` is ahead and drop it then. The limiter side is M8-02; its card now says so. Rejected: a timer in the tracker (lazy rotation is the card's AC, and it would add a goroutine to own).
- Floods under 1/`TopK` of traffic go unreported: reproduced (1 997 misses in 200 000 one-off requests, none reported at `TopK` 64). Kept: it is Space-Saving's stated bound (ADR-9), the attacker pays 64 one-off misses per hidden miss, and the limiter bounds those. 01 FR-MR-1 and 04 §8.4 now state the limit; `TestHeavyHitterAboveBoundReported` pins the side that must hold.
- Sample copy (`strings.Clone`, added after the first review): removed. It was one allocation under the mutex per request of a flood; without it the summary pins at most `TopK` * 512 bytes. `BenchmarkObserveFlood` shows 0 allocs.
- Truncation splitting a UTF-8 character: cannot happen, partitions are visible ASCII (FR-VAL-1). No change.
- Tie-break of the minimum scan: left unpinned. Any minimum is a correct Space-Saving step, and a test would freeze an accident.

## Decided 2026-10-05 (M7-05)

Open findings of the two attack reviews, decided under Ashwin's delegation after trying to break each choice (PR #48). Nothing was listed under Waiting on Ashwin; these were the findings left open at handoff.

- `Bypass.Cookies` name holding `%XX`: fixed by matching the literal spelling first. Rejected: refusing such names in `New` (a valid cookie name, and a config that used to load would stop loading).
- `%20session`, `+session` under `ForwardAll`: now match. Decoded separators and `+` are skipped around the name only. A first version also split inside the token; the second attack review showed it bypassed on `q=cheap+session+tickets` and URL-encoded JSON values, so one persistent cookie would switch the cache off for a visitor.
- `public=no` granting the Authorization permission: fixed, and widened after the second review. `ResponseDirectives.Malformed` (unclosed quote, or an argument on `public` or `must-revalidate`) refuses the FR-STO-5 permission; `public` with an argument is not `public` anywhere. Restricting directives keep their effect in any form.
- `ForwardAll` + `Key.Cookies` + `Connection: cookie`: the key now drops cookies the forward does not carry (INV-1).
- Trace fields, found while attacking the T-31 fix: `tracestate` and `X-Request-Id` go forward unkeyed and still allow markers, so their bytes are now letters, digits and `-_.:/=+@,;*~!|`, no `..`, no empty line, at most 32 `tracestate` lines. Rejected: flagging them `Unkeyed` (behind a tracing proxy every request carries them, so no marker would ever be written).
- Followers of a flight led by an unkeyed request share its 5xx: left as is, written up as R-7. Not coalescing such requests would switch coalescing off under `ForwardAll` and for every browser reload.
- Not changed: `tracestate: a=1, b=2` (W3C-legal space) is dropped, as before this card; FR-FWD-6 now says so.

- T-31 fix, chosen by Ashwin in the session: a request whose forward carries `Cache-Control` or `Pragma` that `Key.Headers` does not name writes no hit-for-miss marker and no negative entry. Both fields go forward as the client sent them, any length and bytes, so an origin answering a 9000-byte `Pragma` with 431 or 503 used to plant a marker or a 2 s cached 503 on the shared key. Rejected: bounding the two fields only (a short value a WAF rejects still plants a marker). FR-STO-12, FR-NEG-4 and T-31 say so now.

## Decided 2026-10-05 (M7-04)

Both items that were waiting on Ashwin and the open M7-04 review nits, decided under his delegation after trying to break each choice (PR #47).

- `TrackingParams` form: a function, confirmed. An exported slice variable is package-level mutable state, which CLAUDE.md forbids, and one caller writing `weir.TrackingParams[0] = ...` would change the key of every engine in the process. D30 now writes `weir.TrackingParams()` and says why.
- Report bound, name length: changed. Dropping names over 64 bytes lost the cookies the report exists to find: session cookies of some identity providers (`CognitoIdentityServiceProvider.<client>.<user>.idToken`) run past 100 bytes. A long name is now cut to 64 bytes and marked `...`. Rejected: a higher cap (the cliff moves, and the suffix that would then be logged is the per-user part).
- Report bound, pairs per request: raised from 32 to 256. Browsers send the oldest cookies first, so on a site with more than 32 cookies the session cookie set at login was past the cap in every request and never counted. 256 is five times RFC 6265 §6.1's floor of 50 per domain; worst case under the lock is 256 × 32 comparisons, in the window only.
- Report counting (review nit): a request moves each counter at most once. Before, `a=1; a=2; ...` in one request added one count per pair, which the higher pair cap would have made a cheap way to push a name to the top.
- Report bound, skip when the lock is held: confirmed. The report is a sample (D31) and the hit path must not queue behind it. A flood can skew the sample, but it could do that by volume anyway, and the output is one advisory log line of token names.
- Log call under the lock (review nit): no change. It runs once, `done` is set before it, and every other request then returns at the atomic load.
- No timer: confirmed. The first cacheable request with a `Cookie` field after the window logs the report; a timer would be a goroutine to own and stop for one log line.
- Preset members (review nits): `mc_cid` and `mc_eid` stay. Whether a shop integration reads them server-side is unverified, but a cached hit never runs that code under any cache, and the doc comment says to leave out names the origin reads. `TestTrackingParamsPreset` now sends every member through the engine and pins the count at 21.
- FR-OBS-5 states all of the above bounds.

## Decided 2026-10-05 (M7-03)

Every open question from the M7-03 handoff and the notes addressed to M7-03, decided under Ashwin's delegation after trying to break each obvious answer (PR #46). Built in #46:

- Bypass cookie matching: widened. A `Bypass.Cookies` name matches any whole token of a `Cookie` line, in any letter case. The attack on the exact `name=` match: under `ForwardAll` the origin reads the lines as sent, so `SESSION=x` (ASP.NET reads names case-insensitively), `a=1, session=x` (comma-splitting parsers) or `"session"=x` skipped the rule and the sender's personalized response was stored for everyone (T-8). A second pass found one more shape, `sess%69on=x` (PHP before 7.4.11 decodes cookie names), so each valid `%XX` in a token is decoded before the comparison. A false match costs one uncached response. Rejected: plain substring match (`session` would bypass on `session_id` and `sessionid`, which operators cannot predict). Residual, in 06 R-3: origins that rewrite names (PHP `.` to `_`) need each spelling listed. FR-BYP-1 says so; pinned by `TestBypassCookieEvasion`, `TestClassifyBypass`, `FuzzBypassed`.
- Dead `Forward.Allow` and `Key.Headers` names: `New` rejects every entry that can never take effect (`keys.Unforwardable`: hop-by-hop, `Host`, client preconditions, `Range`, body fields, `Cookie`), not only the hop-by-hop ones the card named. `Cookie` in `Allow` is rejected always, not only with `Key.Cookies` set: the forward deletes it either way, and the old warning claimed it was forwarded. This also closes `ForwardAll` with `Key.Headers: ["Cookie"]` joining `Cookie` lines with `, `. FR-LCY-1 says so; pinned by `TestNewRejectsDeadForwardNames`, `TestUnforwardable`.
- `Accept-Encoding` bucket outside the primary key (carried since M1-17d): default unchanged, operator knob documented. Keying the bucket for everyone splits every URL of an origin that ignores the field, to shorten a 30 s marker or 2 s negative entry that only costs cache hits. `Key.Headers: ["Accept-Encoding"]` puts the bucket in the primary key (it works because the key reads the forward). 01 §5.2.3 says so; pinned by `TestKeyedAcceptEncodingSeparatesBuckets`.
- `Warm` and bypass rules: a matching request is not sent and counts as not stored, as the code already did. FR-WRM-1 names it; pinned by `TestWarmSkipsBypassed`.
- Hit-path cost: measured. `BenchmarkServeHitSmall` 1480 B/op, 14 allocs/op and about 945 ns on main and on the branch; `BenchmarkKeyBuild` 536 B/op, 9 allocs/op on both.

Decided, no change:

- Empty keyed header: present and keyed apart from absent. The key encoding already has a presence byte for it, FR-KEY-11 treats Vary values the same way ("an absent header only matches absent"), and the origin sees the difference. Treating empty as absent would also be safe, but it rewrites a request the client sent on purpose. Worst case if a hop between Weir and the origin drops empty fields: one extra entry holding the absent response.
- Size limit on the normalized form as well as the lines: kept. Without it a 1 KiB `a,b,c` list would be forwarded at up to 2 KiB, and a second Weir with the same config behind the first would call it oversized and key it as absent, so the outer cache would store the absent response under the value's key.
- `Unkeyed` when Vary keys a `Forward.Allow` field (M7-01 note): no refinement. The spec's names are unknown at classify time, and the gain is markers and negative entries for requests that carry an Allow field, bought with another condition on the T-31 gate. The limiter bounds the cost of not having them.
- `HasBody` trusting `Content-Length: 0` beside a real body (M4-03 note): no change. net/http and Caddy hand such a request `http.NoBody`; only a direct library caller can build it, and the body is then never read.

## Decided 2026-10-05

Every open question in this file and in LOG follow-ups, decided under Ashwin's delegation after an adversarial pass over each (PR #45). Built in #45:

- Overflow in Cache-Status (M7-02): no detail. No other not-stored reason is shown to clients, absent `stored` already tells an operator reading the header, and `EvVaryOverflow` is the count. Rejected: `detail=vary-overflow` (one not-stored reason singled out in the header, and a per-response signal of the cap to whoever cycles values, T-27). FR-KEY-10 says so; `TestVaryOverflow` pins the member.
- Reclaim reads (M7-02): only when the spec write would be refused at the cap. Same cap behavior, no store reads on ordinary variant writes, no S3-FIFO accesses that keep cold variants. An attacker at the cap already cost `MaxVariants` reads per request before, so nothing gets worse. FR-KEY-10 and 04 §14 reworded; pinned by "no reads below the cap".
- `Breaker.MaxOpenFor` (M5-01): defaults to max(60 s, `OpenFor`); `New` rejects an explicit value below `OpenFor`. Before, `OpenFor: 2m` alone gave reopens shorter than the first open. FR-CB-3 and the defaults table say so.
- `MaxQueueWait` above `LeaderMaxAge` or `FollowerMaxWait` (M4-02): allowed, no clamp. The reject rule was built and it broke `TestLimiterCap5000Keys` and `TestWarmDoesNotUseReserve`, which is the counterexample: a patient queue is right for a cold start over distinct keys (T6.3), and those never coalesce. Forcing `LeaderMaxAge` up to match would make followers share minute-old flights. The cost on one hot key is bounded by the queue and the partition cap. FR-LIM-2 states the interaction.
- `Close` and foreground `Serve` calls (carried): `Close` does not wait for them. They run on caller goroutines the engine does not own; the adapter drains first. FR-LCY-2 says what such a call gets.
- In-process origin and context values (Caddy note): new threat row T-45 and residual risk R-6 in 06. Weir cannot key what it cannot see, and stripping the values would break the handlers FR-COA-9 exists for. P2-00 carries the placement rule into the adapter spec.
- Codec header-name case (carried since M1-08, where the review found `Decode` accepting case-distinct header names and non-minimal uvarints "from the trusted store"): `Encode` and `Decode` both reject a header name that is not canonical. Every engine check reads stored headers by canonical name, so a record with `set-cookie`, or `etag` beside `ETag`, would be served with fields no check saw (T-8); M7-01 showed the same gap on the request side was a poisoning path. Rejected: canonicalizing on decode (merging `vary` into `Vary` invents a line order, and it breaks the strictly ascending name rule); rejecting only on decode (a `Set` would succeed on a record every `Get` reports unavailable, the M1-08 defect). Non-minimal uvarints stay accepted: same decoded value, and nothing compares encoded bytes. 05 §6 says so; pinned by `TestDecodeRejects`, `TestEncodeRejectsUnrepresentable`, `TestStoredEntriesEncode` and a `FuzzDecodeEntry` seed.

Decided, no change:

- `breaker.Remaining()` (M5-03): confirmed. Internal, in 04 §8.3, and the only way to give `ErrCircuitOpen` an honest Retry-After.
- Store calls after a failure in one request (M4-04): keep calling. A miss on a dead remote store costs up to 4 × `Timeouts.Store` (200 ms by default), and the breaker opens at the 5th consecutive failure, so the second such request already trips it. Per-request "store is down" state would add a second failure path next to the breaker to save at most two slow requests, and would make the breaker open later.
- SWR refresh that comes back unstorable (M5-02): the stale entry stays until its SWR window ends. RFC 5861 allows it, one refresh flight runs at a time, and dropping the entry on `no-store` would turn an origin misconfiguration into a miss storm.

Decided, owned by a card:

- `Forward.Allow` naming keyed or hop-by-hop fields: `New` rejects it, in M7-03 (card note).
- Unowned events (`EvRequest`, `EvFetchStart`, `EvFetchEnd`, `EvNotStored` for over-size and 5xx): M10-01 (card note).
- Go version matrix in CI (D42): M10-04 (card note).


## Decided 2026-10-02

Every item that was waiting on Ashwin, decided under his delegation after an adversarial review (PR #44):

- FR-NEG-4 exclusions (M6-01): approved as written. Each one keeps a client from writing a negative entry everyone else then gets (T-17, T-31); background and warm fetches never have a waiting client to protect. The cost, more origin calls from excluded requests during an outage, is bounded by the limiter and breaker.
- FR-NEG-3: names `ttl` (seconds until the negative entry expires), as the code and 04 §6.6 already emit.
- FR-MODE-3 and FR-BYP-1: `only-if-cached` gets `ErrOnlyIfCached` in bypass mode and under bypass rules; the stored entry is not served, because both exist for when the cache must not answer.
- FR-MODE-2 reach: no default change. Keeping every entry 24 h past expiry for an incident mode that is rarely on would hold dead entries in remote stores and keep validator-less responses only for this. FR-MODE-2 now says the mode widens what may be served, not what is kept, and how to buy more reach (`Freshness.Keep`, `DefaultStaleIfError`). Pinned by `TestModeStaleOnErrorReachIsRetention`.
- FR-MODE-2 names `s-maxage` (RFC 9111 §5.2.2.10); an explicit `stale-if-error` keeps its own window.
- `Timeouts.Background` is wired: it bounds background refresh and Warm fetches, `Timeouts.Origin` foreground ones (FR-TMO-1, FR-COA-2). Removing the field was rejected: it is the knob that lets foreground fetches fail fast while refreshes wait for a slow origin. Pinned by `TestTimeoutByClass`.
- FR-STL-1 and FR-FRS-6 state the refresh gate for `Authorization` and `no-store` requests (T-8, T-31).
- P0.0: hold the public flip. D24 amended: public with `v0.1.0` once M7-05 (key-boundary security review) merges, since #44 found key-boundary poisoning paths and M7-02..05 are still open. `SECURITY.md` is added now. The M7-05 card tells the session after it to confirm the irreversible flip in chat and run it.

- FR-KEY-11 (delegated by Ashwin after the #44 review): Vary values are keyed exactly as forwarded, no line combining or whitespace removal. A generic normalizer cannot know a field's syntax (commas in quoted strings), and an origin reading one line answers `a` + `b` lines differently from `a,b`, so equating them served one client's answer to another. RFC 9111 §4.1 always permits not matching; the cost is a split variant. Rejected: forwarding the normalized form (rewrites values origins depend on, and Vary is unknown at forward time); documenting the gap under T-31 (leaves a poisoning primitive). 01 FR-KEY-11, 04 §3.3, 06 T-15 and 07 updated.

## Decided 2026-10-01

- Followers of a 5xx flight (#41 adversarial review): Ashwin approved amending FR-COA-5. A 500/502/503/504 flight response is an error condition, so every waiter gets it through §7.2 instead of refetching.
- Limiter queue exhaustion (#34 review): Ashwin approved capping queued waiters per partition, shedding past it with `queue-full`. The #35 adversarial review measured `MaxPerPartition` (16) as too low for a legitimate one-path cold start (32 of 2 000 served vs 656 uncapped); Ashwin chose max(`MaxPerPartition`, `MaxQueue`/4). FR-LIM-3 and T-11 say so.

## Decided 2026-09-28 (delegated by Ashwin after the #24 adversarial review)

Already built, now confirmed: the weirhttp default transport (compression off, no proxy), the coalesce-default clamp to min(10s, `Timeouts.Origin`), and the storetest `Run(t, newStore, opts...)` API with `Synctest()`. The other nine decisions are cards, and each card changes its spec text together with its code:

- M1-17c (merged), the key boundary: h2c is served normally; `#` is rejected in path and query; the cookie limit counts keyed pairs only.
- M1-17d (merged), storability: `s-maxage` must be valid to permit an `Authorization` response; markers are suppressed after unkeyed input; remote conformance tests move behind an `integration` tag.
- M1-17e (merged), responses: the engine strips hop-by-hop fields; a 304 keeps `Content-Encoding` and `Content-Type`; Pragma is ignored when `Cache-Control` is present.

The cards' Notes give the reasons and the options rejected. All three come before M1-18, because closing M1 makes the repo public.

## Notes for the next session

- M10-02 (exporter): `Engine.Stats()` only reads. It uses `breaker.Peek`, emits nothing and may be called under the lock `Observe` takes. `Inflight` and `Queued` sum the main and upload pools.
- M10-02: `weir_requests_total{outcome}` can take the `EvRequest` reason as is; followers are `collapsed`. `weir_not_stored_total` gains the reason `stream`. `EvEvict` and `EvPurge{group}` carry a count in `Status`. A store passed in `Config.Store` reports evictions only through its own `memory.Config.OnEvict`.
- M10-08 done (PR #55): `Entry.Tags` order `[global, URI, groups...]` is load-bearing for `storeGuard.newestEpoch`; the order is tested in the Cache-Groups table in storable_test.go. A Phase 2.5 store must implement `SharedTagEpochs` (P25-00 AC).

- M9-02: `(*Engine).classifyURL(raw, originOnly)` in purge.go gives the `Classified` of a GET for an absolute URL; `Purge` uses `.URITag` and, for `Origin`, `.Origin`. `Purge.Groups` already writes `keys.TagGroup(origin, name)` epochs, but nothing shows they reach entries until M9-03 tags them.
- M9-03: `Entry.Tags` is global, URI, then one tag per distinct group; the origin tag is `Owner` only. Do not add a tag that many entries share unless a purge can name it and its epoch is kept exactly (05 E-8).
- M9-03: `groupList` (storable.go) parses, sorts and dedupes both group fields. `invalidate` emits `EvPurge{invalid}`, then `EvPurge{group}` with `Status` = number of distinct groups, or `EvPurge{group-invalid}` for a refused field, which is also logged once per engine (`Engine.badGroup`, FR-OBS-3).
- M10-01: `EvPurge` now has five reasons (04 §9.2) and `Status` means the group count on `group`, the scrubbed count on `hard` (M15). A rejected `Purge` emits no event, by design (04 §7).
- M9-02: a soft purge is stale at +0 now (`TestSoftPurgeAppliesAtOnce`). Under synctest a refresh sent in the purge's own clock tick is purged again (05 E-3, `At >= since`), so tests sleep at least 1 ms after `Purge` before the request whose refresh they count. The older purge tests still sleep 2 s; that is harmless.
- M9-02: Phase 2.5 note. With `MaxClockSkew`, an epoch from a fast node is now applied at once instead of ignored until the local clock catches up, and refreshes repeat until it does. Size the skew allowance with that in mind.
- M9-02: a background refresh that finds no slot is dropped, not queued: 5 000 stale hits start 48 refreshes and drop 4 952.

- M9-01: `sfv.ParseStringList(lines, maxMembers, maxLen)` returns `sfv.ErrInvalid` for bad syntax and for over-limit input alike, with no members. Pass the resolved `Limits.MaxGroups` and `Limits.MaxGroupBytes`. Hash each member with `keys.TagGroup` and drop it: a member without escapes is a substring of the header line, so keeping one anywhere else (a log field, an event) needs `strings.Clone`. Duplicates are kept; equal names hash to equal tags, so dedupe the tags.
- M8-02: `missrate.Tracker.Observe` takes one mutex per cacheable request. Parallel hit benchmark, M4 Pro 12 procs: 600 ns/op with the tracker, 343 with `MissRate.Disable`; serial `BenchmarkServeHitSmall` 1.1 µs against NFR-5's 4 µs, so no budget is broken (ADR-9 accepts the mutex). `TryLock` gets 367 ns/op but drops 50% of samples at 12 procs (20% at 4, 3% at 2): the ratio stays unbiased, `MinMisses` is effectively doubled. M10-05 decides whether a parallel budget exists and, if so, between `TryLock` and per-shard counters.
- M8-02 review: with `MissRate.Throttle`, a legitimate cold path with far more keys than the cap can fetch stays throttled (300 000 random keys at 1 000 rps on one path: throttled for all 30 windows measured). That is what FR-MR-3 asks for and `Throttle` is off by default; the runbook (M10-06) should say so. `Throttle` is also a lever: 60 distinct-query requests a second to one path kept it capped and served 390 of 1 200 legitimate misses there (06 R-8).
- M8-02: supersedes the "Decided 2026-10-05 (M8-01)" bullet above on throttle timing. The engine applies a report only while `end + Window` is ahead; the limiter drops it at `end + 2*Window` or when the next report replaces it (01 FR-MR-3, 04 §8.2).

- PLAN P0.0 ran on 2026-10-05 after Ashwin confirmed it in chat: the repository is public, private vulnerability reporting is enabled, and `v0.1.0` is an annotated tag on 560d2af (the #48 merge). Commit author emails are public with it.
- M8-01: `missrate.New(cfg, emit)` calls `emit(end, found)` once per closed window, an empty slice included, after its lock is released. `end` is the closed window's end time. M8-02's limiter callback keeps the report with the latest `end`, throttles only while `end + Window` is ahead and drops the map at that time (card notes, 04 §8.4).
- M8-01: `TestPathFloodOriginBounded` (M8-02) is bounded by the limiter, not by the tracker: a flood over distinct paths has no heavy partition, so nothing is reported. The M4-02 note below that calls the miss-rate throttle "the answer there" is wrong on that point.
- M8-01: `New` returns nil for a non-positive `TopK` or `Window`; `Config.validate` in config.go already range-checks the `MissRate` fields, so the engine never reaches that path with user values.
- config.go's `MissRateConfig` comment cites `FR-MIS`; the spec's IDs are `FR-MR-*`. Fix it in M8-02, which touches that wiring.
- M7-05: `Classified.Unkeyed` is now also true when the forward carries `Cache-Control` or `Pragma` unkeyed (keys/forward.go, after the `Allow` loop). A browser reload therefore plants no marker and no negative entry. A new always-forwarded unkeyed field needs the same line, or T-31 reopens.
- M7-05: trace bytes are decided by `traceByte` (keys/forward.go). If a tracing vendor's `tracestate` goes missing at the origin, look there first; widening the set reopens T-31 unless the field is also flagged `Unkeyed`.
- M7-05: `httpcc.ResponseDirectives.Malformed` is read in one place, the Authorization case of `storability`. A new directive that grants a permission must set it for its undefined forms.
- M7-05: `tokenIs` (keys/headers.go) trims decoded separators and `+` around a bypass cookie name but never splits inside a token. Do not widen it to interior pieces; `TestBypassedCookieShapes` holds the cases that broke.
- M7-05: 501 is in the default storable set on purpose (FR-STO-2, RFC 9110); T-7's answer to the `Transfer-Encoding` 501 is hop-by-hop stripping. The T-7 row says so now.

- M7-04: the stripped-cookie report lives in cookiereport.go; `Serve` calls `e.cr.observe(req.Header)` just before `e.cacheable`, so bypass, method-pass and `ModeBypass` requests are never counted. `e.cr` is nil under `ForwardAll` or a negative window. There is no timer: the first cacheable request with a `Cookie` line past the deadline logs the report.
- M7-04: `observe` parses pairs the same way as `keyedCookies` (keys/cookies.go: split on `;`, trim, cut at `=`). If that parser changes, change both, or a keyed name could be reported as stripped.
- M7-04: the report cuts names over 64 bytes (`...` mark), reads 256 pairs per request and moves each counter once per request (`moved` bit mask in `observe`, so `cookieReportNames` must stay at or below 32). During the report window the hit path pays a header-key scan (about 50 ns in `BenchmarkServeHitSmall`); after it, one atomic load.

- M7-03: `Key.Headers` values are normalized in `forwardHeader` (keys/forward.go) and the key reads them back from the finished forward (`keyedHeaders`, keys/headers.go), so the key is what the origin sees by construction. Any new step that edits the forwarded header must run before `keyedHeaders` in `Classify`.
- M7-03: bypass rules are decided in `Classify` (`keys.FwdBypass`, `bypassed` in keys/headers.go). The cookie match is deliberately wide (whole token, any case, percent-decoded, `tokenIs`); do not narrow it to RFC 6265 pairs.
- M7-03: `keys.Unforwardable` is the one list of names a cacheable fetch never carries; `New` rejects them in `Key.Headers` and `Forward.Allow`. A new always-deleted field in `forwardHeader` must be added there too (`TestUnforwardable` checks each name against `Classify`).
- M7-03: `FuzzForwardEqualsKey` picks `fuzzKeyedCfg` with selector bit 0x40 and `fuzzAllCfg` (ForwardAll) with 0x20; the low bits still pick `fuzzCfgs`, so old corpus entries keep their config. Add configs behind a new bit, not by growing `fuzzCfgs`.

- M7-01: `lookup` sets `lk.ck` (variant key under a spec, else Primary); flights, early refresh, warm, markers and negative entries all key on it. `storeResponse` writes the variant, then the spec (`setVariant`, serve.go); `setUnlessResponse` also keeps a live spec. Followers share only when `keys.VariantKey(...) == fr.vk`, else `reenter` (one more pass, then `fetchDirect`).
- M7-02: `setVariant` (serve.go) at the cap reads each kept ref and drops those the store answers `ErrNotFound` for (04 §14); any other store error keeps the ref, so a failing store cannot lift the cap. `recordingStore` (conditional_test.go) now has `down` (one key whose Get fails, counted in `downGets`) and `firstVariant`; the expired-ref subtest runs on `lazyStore` so the ref's `Expires` check is pinned apart from the read. The card listed storable.go and fetch.go, but M7-01 put the policy checks in storable.go and the cap in serve.go, so only serve.go changed.
- M7-02: the memory store keeps nothing past `MaxRetention` (24 h), while a ref's `Expires` follows the entry; a variant with a longer lifetime is a "record gone" ref after 24 h and is reclaimed by the read. Tests that sleep past 24 h hit this.
- M7-01: a request forwarding a `Forward.Allow` field stays `Unkeyed` even when the response's Vary keys that field, so it gets no marker or negative entry (kept, decided 2026-10-05). The Accept-Encoding bucket is still keyed only through Vary (04 §3.3); the marker note below holds for the first response of a URI, before a spec exists.
- M7-01 review nits open: `VaryNames` allocates for every name before the `vary-too-many` check (origin-controlled, bounded by response header limits); the LLD's `lk.spec`/`fetchSpec.spec` are not in the code (`setVariant` rereads the primary key).

- M6-01: negative writes happen in `setNegative` (negative.go), called by `fetchStored` before the flight publishes; `lookup` returns a live negative record as `lk.neg` and `cacheable` serves it before the only-if-cached and Range checks. Markers and negative entries share `setUnlessResponse` (serve.go), which replaces only a hard-purged or expired response.
- M6-01: tests whose next request must reach the origin after a 502/503/504 or transport failure set `Negative.Disable` (coalesce, stale, serve, weirhttp handler-origin tests). New engine tests with failing origins need the same.
- M6-01 adversarial review: `FuzzRetryAfter` (seeds in testdata/fuzz) covers the origin `Retry-After` parser; the served value counts down with the entry's age. Mutants of the `sp.lk.entry != nil` and `ctx.Err() == nil` guards survive because `setUnlessResponse` and `timeoutOrOrigin` already enforce them; they stay as cheap early exits.
- M7: the `Accept-Encoding` bucket is forwarded but not in `PrimaryKey`, so an origin that fails only for one coding writes a negative entry every coding sees (2 s, same bound as the marker note below). Keying the bucket in M7-01 closes it.

- M5-04: `staleOK` (mode.go) is the single stale-on-error test for `onFetchError` and `staleOnTimeout`; The mode widens only entries still stored, so it helps entries with a validator (kept `Freshness.Keep`) or an SIE/SWR window.
- M5-04: bypass uses `keys.Classified.AsBypass()`, which re-runs the `ClassPass` builder on the original request. M7-03 bypass rules (FR-BYP-1) can call the same method.
- M5-04: mode expiry is noticed lazily by the next `Serve`; a racing `SetMode` and expiry can emit `EvMode` events out of order (rare, cosmetic).

- M5-03: `fetch` calls `e.cb.Allow` before the limiter; shed and caller-gone end in `Cancel`. Outcomes are recorded at the headers, or after the body for buffered fetches (a truncated body is a `Failure`). Only Foreground fetches probe; Background and Warm get `ErrCircuitOpen` while not Closed, and Background also emits `EvRefreshDropped{circuit-open}`.
- M5-03: `ErrCircuitOpen`'s Retry-After is `breaker.Remaining()` floored at 1 s (half-open has no end time). `Remaining` is an internal method in 04 §8.3 (confirmed 2026-10-05).
- M5-03: `onFetchError` (flight.go) applies 01 §7.2 for direct fetches and every flight waiter, each with its own lookup. Followers of a buffered 5xx get a copy built from `flightResult.errHeader` and `fr.ci.FwdStatus`; they must never read `fr.resp`, which the creator's caller owns.
- M5-03: a flight cut short by `Close` (`ErrClosed`) serves a stale-if-error entry with reason `sie`; harmless, label could be its own.

- M5-02: `cacheable` serves a `StaleSWR` entry before the only-if-cached and Range checks (it answers both, full 200 for Range) and calls `backgroundRefresh` unless the request is `Authorization` or `no-store`. A honored client `no-cache` turns `StaleSWR` into `NeedsValidation`.
- M5-02: an SWR refresh whose response is unstorable (origin switched to `no-store`, or `no-freshness`) does not replace the stale entry (storeResponse keeps a found response), so the stale body is served until the SWR window ends, with one refresh flight at a time. RFC 5861 allows it (kept, decided 2026-10-05).
- M5-02: `runRFCRow` now calls `synctest.Wait()` before counting origin calls, so a row's `calls` includes the background refresh its step started.

- `EngineStats.BreakerState` can read `breaker.State()`; the State constants share weir.BreakerState's order.

- M4-05: `Warm` acquires its `Warm` slot before joining a flight (`fetchSpec.permit`, the new last `fetch` argument `held`), so a queued warm fetch never holds a flight. Every warm fetch runs `runFlight` on its own engine goroutine; no-flight requests use `coalesce.NewFlight()` (outside the table).
- M4-05: decided 2026-10-01 (delegated by Ashwin): 01 FR-WRM-1/2 now state the not-sent and skip rules; `New` rejects `ReserveForeground >= MaxConcurrent` and `Warm.Concurrency > MaxConcurrent - ReserveForeground` (default lowered to fit). Concurrent Warm calls each take up to `Warm.Concurrency` queue places (04 §6.8a).
- M4-05: a warm fetch looks up twice (before the wait, to skip fast; after the slot, to catch live traffic), so remote stores see one more Get and NewestEpoch per fetched URL.

- M4-04: every store call goes through `e.sg` (storeguard.go). Use `e.sg.get/set/newestEpoch/setEpoch`, never `e.sg.s` directly, except `Close`. Delete and Scrub are not wrapped; whoever first calls them (M9 purges) adds a guard method.
- M4-04: any store error except `ErrNotFound` counts toward the breaker (05 S-3), unless the caller's context ended first (05 S-2 makes stores return `ErrUnavailable` then; counting it would let disconnecting clients open the breaker). After an open period all calls go through, no single half-open probe.
- M4-04: a miss makes four store calls (lookup Get, newest-wins Get, Set, purge-check NewestEpoch), so a dead remote store costs a miss up to 4 × `Timeouts.Store` until the breaker opens. 07 T6.5 says "each store call" (kept, decided 2026-10-05).
- M4-04: `EngineStats` has no store-breaker state yet; EvStoreBreaker is the only signal.

- M4-03: `fetch` takes the slot from `e.upl` (upload pool, `MaxUpload` slots, partition cap min(`MaxPerPartition`, `MaxUpload`), own `MaxQueue` queue, no reserve) when `c.HasBody`. EvShed does not name the pool; add it when observability wants to tell an upload flood from main-pool saturation.
- M4-03: the origin deadline is an `AfterFunc` timer on a `WithCancelCause` context. Streams (pass-through, oversized, event-stream) stop it at headers and wrap the body in `idleBody`: each `Read` arms `StreamIdle`, time between reads is not counted (04 §14). Total stream duration is unbounded by design (FR-TMO-2).
- Streams have no total deadline and the idle timer counts only a Read in progress, so a client that keeps reading slowly holds an origin connection (not a slot) without limit. Adapter docs (M10) should tell operators to set `http.Server.WriteTimeout` or rely on Caddy's write timeout.
- M4-03: PLAN M4.3b stays unticked: its AC lists `TestBodylessBypassUsesMainPool`, which M7-03 owns. `HasBody` trusts a declared `Content-Length: 0` even with a real body; only direct library callers can do that (net/http gives NoBody). Kept, decided 2026-10-05.

- M4-02: `fetch` takes a limiter slot of the given class for `c.PartitionH` before the origin timeout starts and releases it on return (after the buffered body, or at stream headers). Shed is `*RetryError{ErrShed, MaxQueueWait}` plus `EvShed`; Background sets `bgDropped` and also emits `EvRefreshDropped` `no-slot`. A follower of a dropped background flight fetches directly, uncoalesced (bounded by the limiter).
- M4-02: each partition queues at most max(`MaxPerPartition`, `MaxQueue`/4) waiters (FR-LIM-3, T-11). Floods spread over many paths pass any per-path cap; M8's miss-rate throttle is the answer there. Release walk with 1 023 waiters is about 5 us.
- M4-02: config allows `MaxQueueWait` above `Coalesce.LeaderMaxAge` or `FollowerMaxWait`. Then a queued flight ages out and a new request starts a second flight for the key, or followers give up and fetch directly, defeating coalescing under load. Allowed on purpose (decided 2026-10-05, FR-LIM-2).
- M4-02: `EngineStats{Inflight, Queued}` still needs a limiter accessor (M10-01).
- M4-02: coalescing tests that need more than 16 concurrent fetches on one path use `wideLimiter` (coalesce_test.go); new engine tests must use `testorigin.NewChecked` with the engine's caps.
- M4-03: the upload pool is a second limiter with the same algorithm (04 §14); `fetch` picks the pool from `c.HasBody`.
- M8: the `throttled` cap overrides go into `capFor` (04 §8.2 comment).

- M3-01: early refresh lives in background.go. `tryAcquireBackground` is a stub that always returns true and runs before `flights.Join`. M4-02 must acquire inside `fetch`, after Join (04 §6.8); acquiring before Join would hold a slot for every hit that finds a flight already running. Until M4-02 nothing bounds refresh fetches below one per distinct key near expiry.
- M3-01: early refresh skips `Authorization` and request `no-store` requests (same rule as leading a flight, T-8, T-31), now in 04 §6.8 and pinned by `TestEarlyRefreshGates`. FR-FRS-6 now states it (decided 2026-10-02).
- M3-01 adversarial review: early-refresh flights already mark their creator gone and close an unclaimed stream (`TestEarlyRefreshClosesUnclaimedStream`); M5-02's AC item for this is met for early refresh, M5-02 must keep it for SWR refresh. Hits skip the `Rand` draw when more than 37·Δ·β is left (`TestEarlyRefreshShortcutIsExact`), which brought `BenchmarkServeHitSmall` back to its pre-card cost.
- M3-01: the `JitterMinLifetime` gate compares the jittered lifetime, so a `max-age=10` entry jittered below 10 s never refreshes early. A fresh hit under `only-if-cached` can start a refresh (the cache's decision, RFC 9111 allows it).
- M2-03: a follower of an unshareable flight result re-enters `cacheable` with `prevCK`; the same key fetches directly (FR-COA-5). Until M7-01 that equals `fetchDirect`. M7-01 must compare the lookup's coalescing key, cap re-entry at one attempt and add `keys.VaryMatches` (`ponytail:` in flight.go).
- M2-03: followers share storable flight responses that need validation (`no-cache`, `max-age=0`); Ashwin decided 2026-10-01, written into FR-COA-5 and pinned by `TestCoalesceSharesNoCacheResponse`.
- M4: until the limiter lands, a client disconnect no longer cancels the origin fetch (FR-COA-2), so flights per second times `Timeouts.Origin` bounds the table, not MaxConcurrent + MaxQueue. M4-02 must test the table size under a cold-start flood.
- A panic while reading a buffered origin body is recovered in `runFlight` but the body is not closed; a `defer` in `fetch` around `io.ReadAll` would be the root fix. `BenchmarkServeMissCoalesced` (07 §10) still has no card.
- `Publish` must be called exactly once per flight (a second call panics on the double close). `runFlight` is the only caller.

- M1-18: `rfc9111_test.go` is a step table (`rfcRow`/`rfcStep`); rows tagged M5, M7 or M9 skip. Cards that land those milestones untag their rows (M5: must-revalidate 504, RFC 5861 SWR/SIE; M7: Vary variant; M9: Cache-Group-Invalidation). M11/M12 cards add rows here too.
- M1-18: benchmarks live in the package they measure (`bench_test.go`, `internal/keys/bench_test.go`, `store/memory/bench_test.go`); baseline in `docs/benchmarks.md` with raw output in `docs/benchmarks/m1.txt`. `BenchmarkServeHitVary` and `BenchmarkServeMissCoalesced` (07 §10) have no owning card; add them to the M7 and M2 cards when those start. The 1 MiB Cookie benchmark and the hard-epoch prune / S3-FIFO walk measurements carried below were not done here.
- CI tests only the `go.mod` Go version (`go-version-file`), but D42 says the two latest releases. M10-04 owns the matrix.
- `rfc9111_test.go` mutation check (40 hand mutants of storable, conditional, serve, purge, entry, respond): all killed except the `Forwarded.Method != GET` storability guard, which the engine cannot reach (only GET/HEAD reach storability, both forwarded as GET).
- M1-18 added `TestCVE202435296` (serve_test.go), listed in M1.5's AC but owned by no card. The bucket is not in `PrimaryKey` yet (M7), so today it proves forwarding collapses to `identity`; once M7 keys the bucket it also proves no key minting.

- M1-17e: `fetch` strips hop-by-hop and `Connection`-named fields from every origin response (FR-FWD-7), but keeps the received header in `fetchResult.recv` (only when `Connection` is present) and every decision reads it through `fetchResult.received()` (storability, buildEntry's `Age`/`Date`, isEventStream, invalidate), so a named `Cache-Control`/`Vary`/`Set-Cookie`/`Age`/`Location` keeps its effect (T-8). M2 moving the store into the flight must carry `recv` along. `freshened` now takes a header, not a `*Response`.
- M1-17e: `ParseRequest` ignores `Pragma` whenever any `Cache-Control` line exists, including an empty one (deliberately conservative; fewer client-forced validations).
- M1-17d: `keys.Classified.Unkeyed` suppresses hit-for-miss markers only. T-31 also covers negative entries; M6 (FR-NEG-4) should decide whether `Unkeyed` suppresses them too.
- M1-17d: the `Unkeyed` check in `forwardHeader` does not consider `Key.Headers`; it needs no special case, because `New` rejects a `Forward.Allow` entry naming a keyed field (M7-03).
- M7 (variant keying): the `Accept-Encoding` bucket reaches the origin but is not in `PrimaryKey` (keyed only through Vary, M7-01). Until then a client choosing its bucket can plant a marker when the origin's storability differs by coding (for example `Vary: Accept-Encoding` only on gzip responses, refused as `vary-unsupported`). Bounded: 30 s, and the next storable response replaces it. M7 should key the bucket or treat it like `Unkeyed` for markers.
- `validSMaxAge` uses `httpcc.ResponseDirectives.Unusable()` (the FR-FRS-2 predicate, shared with `Lifetime`).

- weirhttp `RequestFrom` now cuts absolute-form targets from the raw bytes (M1-17c adversarial review: `EscapedPath` hid `#` as `%23`).
- M1-17c: `keys.IsUpgrade` ignores an `Upgrade` whose only token is `h2c`; `Http2-Settings` is now in `hopByHop` (so also stripped from stored and served responses). `FuzzForwardEqualsKey` adds the h2c shape when the high bit of `sel` is set. `keyedCookies` no longer early-exits on large raw lines: it scans every line (cost header bytes times `len(Key.Cookies)`); M1-18 may want a benchmark with a 1 MiB Cookie header.
- `cacheable` (serve.go) validates StaleSWR and NeedsValidation entries with validators via `fetch(..., prior)`; SWR still validates in the foreground (`ponytail:`, M5). Under M5, StaleSWR must be served before the `only-if-cached` and Range checks, which today reject or pass through stale SWR entries.
- A Range request that no entry answers (miss or stale) goes through `pass` via `keys.Classified.AsRangePass()` with Range and If-Range (FR-SRV-5 updated). HEAD with Range goes forward as GET and the body is dropped (FR-FWD-4). M11-01 adds 206 from entries; FR-RNG-4's background fill hooks into that `pass` call.
- With `HonorRevalidation`, `no-cache`/`max-age=0` turn Fresh into NeedsValidation (`forcesValidation`), except under `only-if-cached`.
- `fetch` retries a strong-ETag-mismatch 304 under the same timeout; M4 must keep the retry under the same limiter slot. After a validation whose response is unstorable and response-driven, `setMarker` relies on the read-before-write to skip the marker.
- Unowned events: no card emits `EvRequest`, `EvFetchStart` or `EvFetchEnd` (`EvStoreError{epoch}` landed in M4-04); over-size and 5xx responses emit no `EvNotStored`. M1-16 decided not to fold `EvRequest` in (its "every hit/stale/miss/.../error" scope is bigger than a Size S card and depends on M6/M7 reason values); M10-01 owns them (decided 2026-10-05). CONNECT/upgrade rejection stays event-less too: `EvKeyRejected`'s reason vocabulary is `RequestError.Reason` values only, and FR-UPG-1 has adapters intercept these before `Serve`.
- Carried: `New` should compile query patterns; hard-epoch prune and S3-FIFO walk to measure in M1-18.
- `invalidate` (purge.go) handles FR-INV-1 URI, `Location` and `Content-Location`; FR-INV-2 groups are M9-03. Failed `SetEpoch` writes are ignored with no event.
- Newest-wins lives in `storeResponse` (serve.go), not `fetch`; M2 moving the store into the flight must keep the found-record exemption (`sameRecord`). `TestNewerResponseWins` sends a second GET while the first is gated, so under M2 coalescing it must use a key that cannot join the flight.
- Event streams (FR-STR-1, M1-16): `fetch` checks `Content-Type` against `text/event-stream` or `Storable.StreamTypes` right after headers arrive, before the buffered `io.ReadAll`, and sets `fetchResult.stream`; `cacheable` (serve.go) treats it like `res.over` (skip `storeResponse`, leave `resp.Body` as fetch wired it) but never marks it `over`. M2 coalescing and M5 background refresh must keep a `stream` response out of the flight/refresh path (FR-COA-5: followers re-enter, not share it).
- M1-16 review nits left open (not must-fix): no test exercises the operator-configured `Storable.StreamTypes` branch of `isEventStream` (only the `text/event-stream` literal is covered); `TestConnectRejected`/`TestUpgradeRejected` check no origin call but not that no event fires.
- weirhttp (M1-17): `TransportOrigin.Fetch` sends `//` paths in absolute form, suppresses Go's default `User-Agent`, and relies on `DisableCompression` (04 §10). `Middleware` routes upgrades with `keys.IsUpgrade` (exported from `isUpgrade`).
- M1-17 review nits left open: an origin-form `//x` target through `RequestFrom`'s `RequestURI` branch is untested (needs a raw connection; the test's `//` case goes absolute-form); a nil `TransportOrigin.Target` panics (documented contract).
- weirhttp `HandlerOrigin` (M1-17b): enforces a declared `Content-Length` like net/http's server (short body reads fail with `io.ErrUnexpectedEOF`, so fetch.go never stores it), refuses 204/304 bodies, discards HEAD bodies, recovers panics and `runtime.Goexit` as `ErrOrigin`. A handler that ignores its context and never writes outlives `Close` (04 §10). The Caddy `nextOrigin` should reuse this writer rather than copy it.
- Threat-model gap for the Caddy card: an in-process origin sees the creator request's context values (FR-COA-9: Caddy vars, auth identity set by upstream middleware). Those are unkeyed input `TransportOrigin` never exposes; 06 T-45 and R-6 cover it, and P2-00 carries the placement rule.
