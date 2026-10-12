# Status

Updated: 2026-10-12
Phase: released (v1.0.0, 2026-10-12)
Current card: none
Card state: ready
Branch: main
PR: none open for the release
Next card: none (all cards done; M16-02 deferred)

## Waiting on Ashwin

- Nothing blocks. `v1.0.0` is tagged for all four modules and published (docs/release.md, rehearsal log).
- Optional: delete the branches `release/v1.0.0-rc.1` and `release/v1.0.0-rc.3` (their tags keep the commits); decide whether `release/v1.0.0` stays as the base for a `v1.0.1`.
- PR 99 (experiments spec review, v1.1) is still open and unrelated to the release.
- The five behavior changes of PR 111 shipped in v1.0.0 (FR-KEY-5, FR-FWD-1, FR-RNG-6, `Entry.Size`, `valkey.Config.MaxObjectBytes`).

## Notes for the next session

- The sub-modules require weir v0.0.0 with `replace` on `main`; the pinned `go.mod` files live only on a release branch. `make release-check` fails on `main` by design. The four `v1.0.0-rc.1` modules are on the proxy and work for outsiders (docs/release.md, rehearsal log).
- `GOTOOLCHAIN=go1.27.2` runs everything on a machine whose installed Go is older.
- Deferred to v1.1: export for `caddy` (lifts the same-version constraint), `Event.Count`, zero-able knobs, prom options, the `apidiff` job against `v1.0.0`, `max_object_bytes` for the Caddy `store valkey` block, a decode failure on a planted Valkey value as a miss instead of `ErrUnavailable`.
- From the T-46 to T-51 review: `ExperimentsConfig` has no redacting `String`/`LogValue` (06 R-9, v1.1 candidate, public API addition); a `__Host-` assignment cookie name is accepted but not the default (06 R-10).
- Releases: use a separate worktree, never probe the proxy for a version before its tag, and keep `set -o pipefail` (docs/release.md steps 4 and 5).
- `caddy validate` against a Valkey store can spend the key-generation purge; Caddy gives modules no way to detect validate (runbook 7.8).
- Lint can run here: `GOTOOLCHAIN=go1.27.0 GOBIN=/tmp/lintbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`, then `GOTOOLCHAIN=go1.27.0 make check GOLANGCI=/tmp/lintbin/golangci-lint`.
- `TestE2EPurgeHerd` (real-clock caddytest) did not flake in 15 local runs.
