# Status

Updated: 2026-10-11
Phase: release
Current card: none
Card state: ready
Branch: claude/magical-carson-rwdd4q (release-gate fix)
PR: see the PR list
Next card: none (all cards done; M16-02 deferred)

## Waiting on Ashwin

- Rerun `release-gate` after the store test fix, narrowed: `fuzz=false`, `pkgs=["./store/..."]` (docs/release.md, Gate log). Everything else in the first run passed.
- Decide whether to open a PR for the release-prep branch, then follow docs/release.md (release branch, rc rehearsal, tags in order: root, observe/prom and store/valkey, caddy).
- Confirm the decisions taken for you: D24 amended, D43 added, README Stability promises, signing key caps, `servableStatus` miss.

- Decide on the review follow-ups listed in the last LOG entry (If-Match handling, unkeyed Pragma/Cache-Control storage, QuerySort order, Entry.Size accounting, Valkey record size limit).

## Notes for the next session

- The sub-modules require weir v0.0.0 with `replace`; outsiders cannot `go get` them until the tag procedure in docs/release.md is done. `make release-check` fails on `main` by design.
- Deferred to v1.1: see the last LOG entry (export for `caddy`, `Event.Count`, zero-able knobs, prom options, apidiff job).
- `caddy validate` against a Valkey store can spend the key-generation purge; Caddy gives modules no way to detect validate (runbook 7.8).
- Lint can run here: `GOTOOLCHAIN=go1.27.0 GOBIN=/tmp/lintbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`, then `GOTOOLCHAIN=go1.27.0 make check GOLANGCI=/tmp/lintbin/golangci-lint`.
- `TestE2EPurgeHerd` (real-clock caddytest) did not flake in 15 local runs.
