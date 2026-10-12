# Release procedure

Updated: 2026-10-12

How to cut `v1.0.0` (and later releases). Weir is four Go modules in one repository: the root module, `observe/prom`, `store/valkey` and `caddy`. They are released together under one version number. The order matters because a `replace` directive in a `go.mod` is ignored when another module imports it, so each sub-module must require published versions of its siblings before it is tagged.

## Toolchain

Build, test and release with Go 1.27.2 or later (`GOTOOLCHAIN=go1.27.2` works when the installed Go is older). `make vuln` on 1.27.1 reports nine reachable standard library vulnerabilities, all fixed in 1.27.2 (GO-2026-6617, 6613, 6612, 6611, 6610, 6608, 6607, 6605, 6603), through `weirhttp`, the memory store's snapshot writer and the example proxy; it must be clean on the release machine. Upgrade with `brew upgrade go` or `go install golang.org/dl/go1.27.2@latest && go1.27.2 download`. The workflows use `go-version: "1.27"`, which resolves to the latest patch. The `release-gate` run of 2026-10-12 used Go 1.27.0, so its results cover this code and not that toolchain; the standard library fixes do not change Weir's own behavior.

## Gate before any tag

The definition of done is [docs/07 §12](07-testing-strategy.md). Where each item is checked:

| Item | How |
|---|---|
| T6.x tests pass 100 times under `-race` | Actions, `release-gate`, job `repeat` (input `count`, default 100) |
| Zero uncited requirement IDs | `make trace-strict` (part of `make check`) |
| Fuzz targets ran 1 hour each without a new crasher | Actions, `release-gate`, job `fuzz` (input `fuzztime`, default 60m); crashers are uploaded as artifacts |
| Load scenarios pass on the reference machine | `make load` on that machine; record the run in [benchmarks.md](benchmarks.md) |
| cache-tests baseline, every failure explained | `make cache-tests`; [cache-tests-expected-failures.md](cache-tests-expected-failures.md) |
| NFR-5 hit-path budget | `release-gate` job `budgets` gates 16 allocs/op; ns/op is judged on the reference machine against 1.5 times the recorded median (01 NFR-5) |
| Everything else | `make check`, `make vuln`, CI green on the commit |

Run `release-gate` on the release candidate commit, not on a branch that will still change. Any code change after a green gate restarts the gate for the affected checks.

## Where the pinned commits live

`main` keeps the `replace` directives and `go.work`, so every change to the root module is tested against the sub-modules in the same pull request (`make check` runs each sub-module with `GOWORK=off`, which only works while they point at the working tree). The pinned `go.mod` files, which have no `replace`, exist only on a release branch, `release/v1.0.0`, cut from the release candidate commit on `main`. Tags go on commits of that branch. Merge nothing from it back into `main`.

## Order

1. Merge everything. `main` is the release candidate; the gate above ran on it.
2. `git switch -c release/v1.0.0` from that commit.
3. Rehearse with a release candidate tag before the real one. Module proxy entries are immutable, so run steps 4 to 7 and step 9 once with `v1.0.0-rc.N` for every module, fix what breaks (on `main`, then recut the release branch), and only then use `v1.0.0`. A bad `v1.0.0` can only be retracted, never replaced. Use one rc number for all four modules; if one module needs a new rc, all four get it. Delete the rc branch afterwards; the rc tags stay. Results are in the rehearsal log below.
4. Tag the root on the release branch: `git tag -a v1.0.0 -m "weir v1.0.0" && git push origin release/v1.0.0 v1.0.0`. Check from outside the repository: `GOWORK=off GOFLAGS=-mod=mod go list -m github.com/AshwinSathian/weir@v1.0.0`.
5. Pin and tag the leaf modules. For `observe/prom` and `store/valkey`:
   ```sh
   scripts/release-pin.sh v1.0.0 observe/prom
   scripts/release-pin.sh v1.0.0 store/valkey
   git add observe/prom store/valkey && git commit -m "chore(release): pin sub-modules to v1.0.0"
   ```
   `git add` takes the directory on purpose: the pin changes `go.sum` as well as `go.mod`, and a tag without the new `go.sum` lines does not build alone.
   `release-pin.sh` runs `go mod tidy` and `go vet` against the module proxy. If it fails right after you pushed a tag, the proxy has not seen it yet (or cached a 404): wait a few minutes and run it again. Push the branch, then tag the commit with the directory as the prefix: `git tag -a observe/prom/v1.0.0` and `git tag -a store/valkey/v1.0.0`, and push both tags.
6. Pin and tag `caddy`, which needs all three:
   ```sh
   scripts/release-pin.sh v1.0.0 caddy observe/prom store/valkey
   ```
   Commit with `git add caddy` (both `go.mod` and `go.sum`), then run step 7 before the tag.
7. `make release-check` must pass on the final commit of the branch (the `caddy` one) before `caddy/v1.0.0` is tagged: no `replace` and no `v0.0.0` left in any sub-module, each resolves alone with `GOWORK=off`, and no `go.mod` or `go.sum` is left uncommitted. It fails on the earlier commits by design, because `caddy/go.mod` is not pinned yet. Then run `(cd caddy && GOWORK=off go test -race ./...)`, which tests `caddy` against the published siblings, push the branch, tag `caddy/v1.0.0` and push the tag.
8. Make CI test what users do. The `caddy-published` job in `.github/workflows/ci.yml` builds Caddy from the published tags with no `=path` overrides, next to `caddy-build` (which keeps testing the working tree). On `main`, set its `WEIR_VERSION` to `v1.0.0`.
9. Smoke test from outside the repository, with `GOWORK=off` and Go 1.27.2 or later. `make release-smoke VERSION=v1.0.0` runs the first three items below in a temporary directory with an empty module cache (without the README program) and checks that Caddy lists both weir modules at the version; the runbook checklist and the pkg.go.dev pages are done by hand:
   - In an empty directory: `go mod init smoke`, then `go get github.com/AshwinSathian/weir@v1.0.0 github.com/AshwinSathian/weir/observe/prom@v1.0.0 github.com/AshwinSathian/weir/store/valkey@v1.0.0`. Build and run the README quick start against it, and run the published tests from the module cache: `go test github.com/AshwinSathian/weir/... github.com/AshwinSathian/weir/observe/prom/... github.com/AshwinSathian/weir/store/valkey/...`.
   - `go install github.com/AshwinSathian/weir/examples/weirproxy@v1.0.0`.
   - The README's xcaddy line, with both pins: `xcaddy build --with github.com/AshwinSathian/weir@v1.0.0 --with github.com/AshwinSathian/weir/caddy@v1.0.0`, then `./caddy list-modules --versions | grep weir` (two modules, both at the version) and the checklist in [runbook 7.8](runbook.md) against a scratch origin.
   - Open `https://pkg.go.dev/github.com/AshwinSathian/weir@v1.0.0` and the three sub-module pages. A page that answers 404 has not been fetched yet: its "Request" button, or `curl -X POST https://pkg.go.dev/fetch/<module>@v1.0.0`, fetches it. Each must show the Apache-2.0 license (the sub-module zips get the repository's `LICENSE` from the go command).
10. Publish a GitHub release for `v1.0.0` from the CHANGELOG section. On `main`, replace "(unreleased)" in [CHANGELOG.md](../CHANGELOG.md) with the date and move the README Install block's version if it changed.

## Keeping the modules in step

`caddy` imports the root module's `internal/keys` (a legal import by path, but not a stable API). A `caddy` release therefore works only with the root version it was built and tested against, and no `go.mod` can enforce that: Go selects the highest root version in the build, so `caddy@v1.0.0` built together with `weir@v1.1.0` may not compile if `internal/keys` changed. For every later release, tag all four modules, even when only one changed, and tell users to build with `xcaddy build --with .../weir@vX --with .../weir/caddy@vX` at one version. Removing the import (by exporting what `caddy` needs from a public package) would lift the constraint and is a candidate for v1.1.

## If a tag is wrong

Do not delete or move a tag that the proxy may have fetched. Release the next patch version and add a `retract` directive for the bad one in the module's `go.mod`.

## After the release

- Add an API compatibility check (`gorelease` or `apidiff`) for the root, `store` and `weirhttp` to CI, comparing against the latest tag.
- Move `STATUS.md` to `ready` with no open card.

## Rehearsal log

| Date | Tags | Result |
|---|---|---|
| 2026-10-12 | `v1.0.0-rc.3` on `7be254b`; `observe/prom/v1.0.0-rc.3`, `store/valkey/v1.0.0-rc.3` on `fc8a83d`; `caddy/v1.0.0-rc.3` on `b85d3e2` (branch `release/v1.0.0-rc.3`) | Second rehearsal, by the corrected steps 4 to 7 and 9, after the hardening changes of PR 111. Nothing broke and no step needed a retry: the proxy served each tag on the first `release-pin.sh` run. `make release-check` passed on the `caddy` pin commit before the `caddy` tag, and each sub-module passed its race tests with `GOWORK=off` against the published siblings. From outside the repository, empty module cache, Go 1.27.2: `go get` of the three library modules; the README program; the published tests of all three from the module cache; `go install .../examples/weirproxy@v1.0.0-rc.3`; builds for `js/wasm`, `wasip1/wasm`, linux/386 and windows; the README xcaddy line (Caddy v2.11.7, both weir modules at `v1.0.0-rc.3`). On that Caddy binary: runbook 7.8 as corrected (`fwd=stale` after a restart, snapshot file gone after the start), a 206 for `Range` on a hit, the full 200 for `Range` with `If-Match` on a hit and on a miss (the origin saw neither field), a `Cache-Control` with markup not forwarded and a plain one forwarded. Two nodes on one store shared a hit and a hard purge, and a 70 MiB value planted at an entry key was a miss, was overwritten by the next response and left the store breaker closed. pkg.go.dev shows Apache-2.0 for all four modules. |
| 2026-10-12 | `v1.0.0-rc.1` on `3f7eefa`; `observe/prom/v1.0.0-rc.1`, `store/valkey/v1.0.0-rc.1` on `72541bf`; `caddy/v1.0.0-rc.1` on `1c8e0ad` | Go 1.27.2, macOS arm64. On `main` (`3f7eefa`): `make check`, `make vuln` (no reachable finding; one in a required module that is not called) and `make cache-tests` pass. From outside the repository, with an empty module cache: `go get` of the three library modules, a program written from the README (miss, then hits; tracking parameters dropped; metrics registered), the published tests of the root, `observe/prom` and `store/valkey` run from the module cache, `go install .../examples/weirproxy@v1.0.0-rc.1`, and the README's xcaddy line (Caddy v2.11.7; `admin.api.weir` and `http.handlers.weir` at `v1.0.0-rc.1`). The Caddy binary passed the runbook 7.8 checklist, and two nodes on one store shared a hit and a hard purge. `store/valkey`'s integration tests and `caddy`'s race tests pass with `GOWORK=off` against the published siblings. The library builds for linux, darwin, windows, the BSDs, solaris, aix and plan9, 32-bit included. pkg.go.dev shows Apache-2.0 for all four modules. |

What the rehearsal found, all fixed on `main` in the pull request that added this log:

- `caddy/v1.0.0-rc.1` was tagged without the `go.sum` lines of its three siblings (only `go.mod` was added to the commit), so `make release-check` fails on the branch tip and `caddy` does not build alone with `GOWORK=off`. Importers and xcaddy are not affected, because a dependency's `go.sum` is never read. Steps 5 to 7 now add the directory, `release-check` refuses an uncommitted `go.mod` or `go.sum`, and it runs before the `caddy` tag instead of after.
- `caddy/v1.0.0-rc.2` exists on the same commit as `caddy/v1.0.0-rc.1`, with no rc.2 for the other three modules. It is harmless and cannot be removed from the proxy. A second rehearsal would be `v1.0.0-rc.3` for all four modules.
- Runbook 7.8 step 5 promised a hit after a restart. A loaded snapshot is revalidated first (FR-SNP-2), so the first request shows `fwd=stale`, and the file is gone once Caddy has started. The runbook now says so.
- The root module does not compile for `js/wasm` or `wasip1/wasm`: the snapshot loader uses `syscall.O_NONBLOCK` (README Requirements, CHANGELOG known limits). Left as is for v1.0.0, since a fix touches the snapshot path and would restart the gate for `store/...`.
- A new Valkey server, or one that was flushed, stores nothing usable for `max_clock_skew` + 1 s (2 s by default) after the first request: the repair writes a hard epoch at server time (05 §7). This is the designed behavior; a smoke test that expects a hit within that window fails.
- Steps 8 and 9 built Caddy with the `caddy` pin only, while the README pins the root module too. They now match the README, and step 9 covers the checks above.

The rc.3 rehearsal found nothing. The procedure above is the one to use for `v1.0.0`, unchanged. Two notes from running it: while a full `release-gate` run is in progress it holds every hosted runner the account gets, so `ci` on `main` waits for it; and each tag was on the proxy within seconds, so the "wait a few minutes" case in step 5 did not occur.

## Gate log

Results of `release-gate` runs, newest first. A rerun after a fix can be narrowed with the workflow inputs `fuzz`, `repeat` and `pkgs` (for example `fuzz=false`, `pkgs=["./store/..."]` reruns only the store group in about 15 minutes).

| Date | Commit | Run | Result |
|---|---|---|---|
| 2026-10-12 | `6bf9666` (head of PR 111; `main` at `7be254b`, where `v1.0.0-rc.3` is tagged, has the same tree) | [38259811052](https://github.com/AshwinSathian/weir/actions/runs/38259811052) | **All 35 jobs passed**, on the latest Go 1.27 patch. Full run after the hardening changes: 29 of 29 fuzz targets ran 60 minutes without a failure; repeat x100 under `-race` passed for the root package (29 minutes), `internal/...`, `store/...` (14 minutes) and `weirhttp`; `budgets` passed. The run was started on the pull request head before the merge; the merge commit adds no change to the tree (`git diff 6bf9666 7be254b` is empty). The gate for docs/07 §12 items 1, 3 and 5 is met for the rc.3 code. |
| 2026-10-12 | `92a1e56` (merge of PR 107) | [38213460051](https://github.com/AshwinSathian/weir/actions/runs/38213460051) | **All 35 jobs passed.** Full run (`fuzz`, `repeat` and all four package groups): 29 of 29 fuzz targets ran 60 minutes without a failure; repeat x100 under `-race` passed for the root package (37 minutes), `internal/...`, `store/...` (15 minutes) and `weirhttp`; `budgets` passed. The gate for docs/07 §12 items 1, 3 and 5 is met on this commit. |
| 2026-10-12 | `ab5d7f7` (merge of PR 106) | [38202380943](https://github.com/AshwinSathian/weir/actions/runs/38202380943) | fuzz: 29 of 29 targets ran 60 minutes without a failure. repeat x100 under `-race`: root package, `internal/...` and `weirhttp` passed (root in 22 minutes). `budgets` passed. **`store/...` failed** in `TestSnapshotHardEpochSurvives`: the test compared a restored epoch time with a 1 µs tolerance, but the store keeps epoch times as monotonic offsets, and wall and monotonic readings differ by a few microseconds on a loaded runner. A test bug, reproduced locally at 1 in 3 000 runs; the tolerance is now 1 ms on wall time. |
