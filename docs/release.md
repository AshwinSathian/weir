# Release procedure

Updated: 2026-10-11

How to cut `v1.0.0` (and later releases). Weir is four Go modules in one repository: the root module, `observe/prom`, `store/valkey` and `caddy`. They are released together under one version number. The order matters because a `replace` directive in a `go.mod` is ignored when another module imports it, so each sub-module must require published versions of its siblings before it is tagged.

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
3. Rehearse with a release candidate tag before the real one. Module proxy entries are immutable, so run steps 4 to 7 once with `v1.0.0-rc.1` for every module, fix what breaks (on `main`, then recut the release branch), and only then use `v1.0.0`. A bad `v1.0.0` can only be retracted, never replaced. Delete the rc branch afterwards; the rc tags stay.
4. Tag the root on the release branch: `git tag -a v1.0.0 -m "weir v1.0.0" && git push origin release/v1.0.0 v1.0.0`. Check from outside the repository: `GOWORK=off GOFLAGS=-mod=mod go list -m github.com/AshwinSathian/weir@v1.0.0`.
5. Pin and tag the leaf modules. For `observe/prom` and `store/valkey`:
   ```sh
   scripts/release-pin.sh v1.0.0 observe/prom
   scripts/release-pin.sh v1.0.0 store/valkey
   git add observe/prom store/valkey && git commit -m "chore(release): pin sub-modules to v1.0.0"
   ```
   `release-pin.sh` runs `go mod tidy` and `go vet` against the module proxy. If it fails right after you pushed a tag, the proxy has not seen it yet (or cached a 404): wait a few minutes and run it again. Push the branch, then tag the commit with the directory as the prefix: `git tag -a observe/prom/v1.0.0` and `git tag -a store/valkey/v1.0.0`, and push both tags.
6. Pin and tag `caddy`, which needs all three:
   ```sh
   scripts/release-pin.sh v1.0.0 caddy observe/prom store/valkey
   ```
   Commit, push, then tag `caddy/v1.0.0`.
7. `make release-check` must pass on the final commit of the branch (the `caddy` one): no `replace` and no `v0.0.0` left in any sub-module. It fails on the earlier commits by design, because `caddy/go.mod` is not pinned yet.
8. Make CI test what users do. On `main`, add a job to `.github/workflows/ci.yml` that runs `xcaddy build --with github.com/AshwinSathian/weir/caddy@v1.0.0` with no `=path` overrides, next to the existing `caddy-build` job (which keeps testing the working tree).
9. Smoke test in an empty directory: `go mod init smoke`, then `go get github.com/AshwinSathian/weir@v1.0.0 github.com/AshwinSathian/weir/observe/prom@v1.0.0 github.com/AshwinSathian/weir/store/valkey@v1.0.0`, then `xcaddy build --with github.com/AshwinSathian/weir/caddy@v1.0.0` and `./caddy list-modules | grep weir`.
10. Publish a GitHub release for `v1.0.0` from the CHANGELOG section. On `main`, replace "(unreleased)" in [CHANGELOG.md](../CHANGELOG.md) with the date and move the README Install block's version if it changed.

## Keeping the modules in step

`caddy` imports the root module's `internal/keys` (a legal import by path, but not a stable API). A `caddy` release therefore works only with the root version it was built and tested against, and no `go.mod` can enforce that: Go selects the highest root version in the build, so `caddy@v1.0.0` built together with `weir@v1.1.0` may not compile if `internal/keys` changed. For every later release, tag all four modules, even when only one changed, and tell users to build with `xcaddy build --with .../weir@vX --with .../weir/caddy@vX` at one version. Removing the import (by exporting what `caddy` needs from a public package) would lift the constraint and is a candidate for v1.1.

## If a tag is wrong

Do not delete or move a tag that the proxy may have fetched. Release the next patch version and add a `retract` directive for the bad one in the module's `go.mod`.

## After the release

- Add an API compatibility check (`gorelease` or `apidiff`) for the root, `store` and `weirhttp` to CI, comparing against the latest tag.
- Move `STATUS.md` to `ready` with no open card.

## Gate log

Results of `release-gate` runs, newest first. A rerun after a fix can be narrowed with the workflow inputs `fuzz`, `repeat` and `pkgs` (for example `fuzz=false`, `pkgs=["./store/..."]` reruns only the store group in about 15 minutes).

| Date | Commit | Run | Result |
|---|---|---|---|
| 2026-10-12 | `92a1e56` (merge of PR 107) | [38213460051](https://github.com/AshwinSathian/weir/actions/runs/38213460051) | **All 35 jobs passed.** Full run (`fuzz`, `repeat` and all four package groups): 29 of 29 fuzz targets ran 60 minutes without a failure; repeat x100 under `-race` passed for the root package (37 minutes), `internal/...`, `store/...` (15 minutes) and `weirhttp`; `budgets` passed. The gate for docs/07 §12 items 1, 3 and 5 is met on this commit. |
| 2026-10-12 | `ab5d7f7` (merge of PR 106) | [38202380943](https://github.com/AshwinSathian/weir/actions/runs/38202380943) | fuzz: 29 of 29 targets ran 60 minutes without a failure. repeat x100 under `-race`: root package, `internal/...` and `weirhttp` passed (root in 22 minutes). `budgets` passed. **`store/...` failed** in `TestSnapshotHardEpochSurvives`: the test compared a restored epoch time with a 1 µs tolerance, but the store keeps epoch times as monotonic offsets, and wall and monotonic readings differ by a few microseconds on a loaded runner. A test bug, reproduced locally at 1 in 3 000 runs; the tolerance is now 1 ms on wall time. |
