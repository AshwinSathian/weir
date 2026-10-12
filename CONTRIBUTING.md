# Contributing to Weir

Thanks for looking. Weir decides what a shared cache stores and serves, so changes are held to a few rules that most projects do not need. This page is the short version; [CLAUDE.md](CLAUDE.md) is the full one and applies to people as much as to coding agents.

## Before you write code

- A bug report with a failing Go test is the most useful thing you can send. Use the issue templates.
- A vulnerability goes through private reporting, not an issue: see [SECURITY.md](SECURITY.md).
- For a feature, open an issue first. A change to a public signature, a config field, a default or a requirement needs the maintainer's agreement before a pull request, because those are covered by the stability promise in the README.

## Running the checks

```sh
make check        # gofmt, vet, golangci-lint v2.14.0, race tests, requirement trace: the gate CI runs
make test-short   # fast loop while writing
make fuzz-short   # every fuzz target for 20 s
make site-check   # only if you touched site/, README.md or docs/: needs Node 24 and pnpm
```

Go 1.27 or later. `make check` must pass at the last commit of a pull request.

## Rules that are not negotiable

1. The root module imports only the Go standard library, tests included. Dependencies belong in the separate modules (`observe/prom`, `store/valkey`, `caddy`).
2. Every path to the origin goes through one function. `originrule_test.go` fails if a second call site appears.
3. Stored entries are immutable. To change one, build a new one.
4. The forwarded request equals the keyed request. Normalization rewrites the request, never only the key.
5. Every map, queue and table has a stated bound and a test for it.
6. No injected clock, no real-clock sleeps in tests: time-dependent tests run inside `testing/synctest`.
7. No panic on any input. A new parser of request or origin bytes gets a fuzz target with seeds.
8. Defaults stay safe. A default that weakens security or stale serving does not change without the maintainer's explicit approval.

The reasons are in [docs/02-architecture.md](docs/02-architecture.md), principles P1 to P8.

## Tests and traceability

Write the test first, and cite the requirement or threat it covers in a comment: `// FR-COA-4, T-19`. `make check` fails when a requirement ID in [docs/01](docs/01-technical-spec.md) has no test citing it. If the code must differ from a normative document, change the document in the same pull request and bump its date line.

Changes under `internal/keys`, the storability rules, forwarding or `store/codec.go` are security-sensitive. Answer the checklist in [docs/06 §6](docs/06-threat-model.md) in the pull request description.

## Commits and pull requests

Conventional Commits with the milestone or area as scope: `fix(m4): release the slot when the flight is aged`. The body says what changed and why, in plain sentences, with the requirement IDs covered. One logical change per pull request; the template asks for the rest.

`main` accepts changes only through a pull request with a green `check` job, and neither `main` nor a `release/*` branch can be force-pushed or deleted. The maintainer merges.

## Documentation and the website

The site in [site/](site/) is built from this repository. Tables for configuration, threats, conformance, benchmarks and errors are read from `docs/` and the Go source at build time, so fix a fact in its source document and the site follows. Hand-written pages are Markdown under `site/content/`. `make site-check` lints style (no em dashes, plain words, one bold phrase per section) and fails when a measured number in the copy has no entry in `site/content/claims.md`.

## Conduct

Be direct about the work and decent to the people. See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
