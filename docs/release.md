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

## Order

1. Merge everything. `main` is the release candidate.
2. Rehearse with a release candidate tag. Module proxy entries are immutable, so do the whole sequence below once with `v1.0.0-rc.1` (every module), fix what breaks, and only then tag `v1.0.0`. A bad `v1.0.0` can only be retracted, never replaced.
3. Tag the root: `git tag -a v1.0.0 -m "weir v1.0.0" && git push origin v1.0.0`. Check from outside the repository: `GOWORK=off GOFLAGS=-mod=mod go list -m github.com/AshwinSathian/weir@v1.0.0`.
4. Pin and tag the leaf modules. For `observe/prom` and `store/valkey`:
   ```sh
   scripts/release-pin.sh v1.0.0 observe/prom
   scripts/release-pin.sh v1.0.0 store/valkey
   git add observe/prom store/valkey && git commit -m "chore(release): pin sub-modules to v1.0.0"
   ```
   Push the commit, then tag it with the directory as the prefix: `git tag -a observe/prom/v1.0.0` and `git tag -a store/valkey/v1.0.0`, and push both tags.
5. Pin and tag `caddy`, which needs all three:
   ```sh
   scripts/release-pin.sh v1.0.0 caddy observe/prom store/valkey
   ```
   Commit, push, then tag `caddy/v1.0.0`.
6. `make release-check` must pass on the tagged commits: no `replace` and no `v0.0.0` left in any sub-module. `go.work` stays for development.
7. Make CI test what users do. In the `caddy-build` job of `.github/workflows/ci.yml`, replace the four `--with X=./path` overrides by `--with github.com/AshwinSathian/weir/caddy@v1.0.0` and update the comment above it.
8. Smoke test in an empty directory: `go mod init smoke`, then `go get github.com/AshwinSathian/weir@v1.0.0 github.com/AshwinSathian/weir/observe/prom@v1.0.0 github.com/AshwinSathian/weir/store/valkey@v1.0.0`, then `xcaddy build --with github.com/AshwinSathian/weir/caddy@v1.0.0` and `./caddy list-modules | grep weir`.
9. Publish a GitHub release for `v1.0.0` from the CHANGELOG section. Replace "(unreleased)" in [CHANGELOG.md](../CHANGELOG.md) with the date.

## Keeping the modules in step

`caddy` imports the root module's `internal/keys` (a legal import by path, but not a stable API). A `caddy` release therefore works only with the root version it was built and tested against. For every later release, tag all four modules, even when only one changed, or pin `caddy` to the exact root version it was tested with. The README tells users to use one version for all modules.

## If a tag is wrong

Do not delete or move a tag that the proxy may have fetched. Release the next patch version and add a `retract` directive for the bad one in the module's `go.mod`.

## After the release

- Add an API compatibility check (`gorelease` or `apidiff`) for the root, `store` and `weirhttp` to CI, comparing against the latest tag.
- Move `STATUS.md` to `ready` with no open card.
