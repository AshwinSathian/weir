# CLAUDE.md

Instructions for Claude Code (and any other agent or contributor) building Weir. Read this file fully before touching code. It is short on purpose; the detail lives in `docs/`.

## What Weir is

A Go library that sits between an HTTP server and an origin and makes shared-cache decisions per request: RFC 9111 caching plus request coalescing, TTL jitter, an origin concurrency limiter, stale-while-revalidate and stale-if-error, a circuit breaker, negative caching, lazy purges, and a cache key treated as a security boundary. Adapters (net/http now, Caddy in Phase 2) handle the wire.

## Where the truth lives

| Need | Read |
|---|---|
| what to build next | [PLAN-weir.md](PLAN-weir.md): the first unchecked task in the current milestone |
| what a behavior must be | [docs/01-technical-spec.md](docs/01-technical-spec.md), cited by requirement ID (`FR-COA-4`) |
| why a structure is the way it is | [docs/02-architecture.md](docs/02-architecture.md) principles P1–P8 and ADRs |
| how a request flows | [docs/03-hld.md](docs/03-hld.md) |
| exact types, algorithms, locking | [docs/04-lld.md](docs/04-lld.md) |
| store contract, S3-FIFO, codec | [docs/05-storage-interface-spec.md](docs/05-storage-interface-spec.md) |
| security reasoning and review checklist | [docs/06-threat-model.md](docs/06-threat-model.md) |
| which tests, with which pass criteria | [docs/07-testing-strategy.md](docs/07-testing-strategy.md) |
| why the project exists, failure taxonomy T6.x | [docs/00-design-doc.md](docs/00-design-doc.md) (seed; loses to 01–09 when they disagree) |

Precedence when documents disagree is in [docs/README.md](docs/README.md). A disagreement is a bug: stop, tell the user which documents conflict, and propose the fix. Do not silently pick one.

## Workflow for every task

1. Find the task in `PLAN-weir.md`. Read every doc section it references, and the LLD section for each package you will touch. Do not start from memory of an earlier session.
2. Write the tests first, named exactly as in `docs/07-testing-strategy.md`, each with a comment citing the IDs it covers: `// FR-COA-4, T-19`. Run them and see them fail for the right reason.
3. Write the smallest code that passes. Match the LLD's types and locking rules.
4. Run the full check (below). Everything must pass.
5. If the implementation had to differ from a normative document, update that document in the same commit and bump its date line. If the difference changes a requirement, a default, a decision table, or a public signature, ask the user first.
6. Tick the task's checkbox in `PLAN-weir.md` only when its AC is met exactly.
7. Commit (see Commits).

Stay inside the current milestone. If you notice something that belongs to a later milestone, add it to "Open questions" in `PLAN-weir.md` or mention it to the user; do not build it.

## Commands

```sh
go build ./...
go vet ./...
gofmt -l .                                   # must print nothing
golangci-lint run                            # v2, config in .golangci.yml
go test -race -shuffle=on -count=1 ./...     # the gate for every change
go test -run '^$' -fuzz '^FuzzAcceptEncoding$' -fuzztime 60s ./internal/keys   # one fuzz target
go test -tags load -run . ./loadtest         # real-time load scenarios (slow)
go test -bench . -benchmem -run '^$' ./...   # benchmarks
govulncheck ./...
scripts/trace.sh                             # requirement IDs without a citing test
```

Once `go.work` exists (M10), also run the module tests with `GOWORK=off` for each module.

## Hard rules

These come from the architecture principles. Breaking one is a bug even if tests pass.

1. The root module imports only the Go standard library (P7, NFR-6). No exceptions, including test-only imports. Dependencies belong in separate modules (`observe/prom`, `store/valkey`, `caddy`).
2. `Origin.Fetch` is called in exactly one function, `(*Engine).fetch` in `fetch.go` (P3). Every path to the origin goes through it: foreground, background, warm, pass-through, errors.
3. Stored entries are immutable (P4). Never write to a `*store.Entry`, its `Header` map, or its `Body` slice after `Set`. To change an entry, build a new one. Response headers served to callers are cloned maps.
4. The forwarded request equals the keyed request (P2, INV-1). Anything the origin can see on a cacheable request is keyed or explicitly allowed. Normalization rewrites the request, never just the key.
5. Every map, queue and table has a stated bound (P5, NFR-3). If you add a structure whose size depends on request input, state its bound in the LLD and test it.
6. No injected clock (D9). Production code calls `time.Now` and `time.NewTimer`. Tests with time or concurrency run inside `synctest.Test`. Waits that tests depend on use channels, `sync.Cond`, timers or `time.Sleep`, never a mutex held while waiting (P8). No test sleeps on the real clock except under the `load` build tag.
7. Randomness comes from `Config.Rand` (jitter, early refresh, breaker jitter). Never call `math/rand` directly in engine code.
8. No panics on any input (NFR-2). Parsers of request or origin bytes get a fuzz target with seeds in `testdata/fuzz/`.
9. Errors: sentinel errors from `errors.go`, wrapped with `%w`; compare with `errors.Is` / `errors.As`. Messages start with `weir:` (or `store:` in the store package), lowercase, no trailing punctuation.
10. No goroutine without an owner. Engine goroutines are registered in `e.wg` and stop on `Close` (FR-LCY-2). Every test that creates an engine closes it before the synctest bubble ends.
11. `context.Context` is the first parameter of anything that can block. Never store a request context in a struct beyond the call, except the flight's creator context, used for values only (FR-COA-9).
12. Defaults stay safe. Never change a default that weakens security or stale-serving behavior (D4, D5, D6, strict Vary handling, storable statuses) without the user's explicit approval.

## Security-sensitive code

Anything under `internal/keys`, the storability rules, forwarding, and the `store/codec.go` decoder. For every change there, answer the checklist in [docs/06-threat-model.md §6](docs/06-threat-model.md) in the commit message body or PR description, and add fuzz seeds for any new input shape. Cite threat IDs in comments where code exists because of a threat: `// T-13: malformed values collapse to identity, never bypass`.

## Go conventions

- Go 1.27. Use the standard library's newer APIs where they fit: `slices`, `maps`, `iter`, `errors.AsType`, `math/rand/v2`, `hash/maphash`, `sync.OnceValue`, `context.WithoutCancel`, `context.AfterFunc`, `testing/synctest`, `httptest.NewTestServer`, `b.Loop()`.
- Every exported identifier has a doc comment that starts with its name. Package `doc.go` files summarize the package and link the relevant doc section.
- Comments explain why, not what. Cite requirement or threat IDs instead of repeating the spec.
- Table-driven tests with `t.Run` names that read as behavior (`"malformed q falls back to identity"`).
- No `init()` functions except for Caddy module registration in Phase 2. No package-level mutable state except `sync.Pool`s and the per-process `maphash.Seed`s.
- Hot path (hit path in `serve.go`, `internal/keys`, memory store `Get`): no `fmt`, no `regexp`, no `strings.Split` on untrusted input, no per-call `context.WithTimeout` for in-process stores. Check allocations with `-benchmem` when touching it.
- Keep functions small enough to test alone. `serve.go` follows the stage structure in [docs/03-hld.md §1](docs/03-hld.md).
- Use `ponytail:` comments for deliberate simplifications with a known ceiling (for example the O(queue) walk in the limiter), naming the ceiling and the upgrade path.

## When to stop and ask

Ask the user (do not guess) when:

- a normative document is ambiguous or two documents conflict;
- a task needs a public API change, a new config field, a changed default, or a new dependency in any module;
- an open question (`OQ-*`) blocks the task;
- a test criterion in `docs/07` looks wrong (for example statistically unsound) rather than hard to meet.

## Looking things up

Do not rely on memory for library or tool APIs. For the Go standard library use `go doc <pkg>.<Symbol>` against the installed toolchain. For Caddy, Valkey, Prometheus, OpenTelemetry and golangci-lint use Context7 (resolve the library ID, then query one concept at a time) or the project's source at the pinned version. RFC text is authoritative over blog posts; the RFCs Weir implements are listed in [docs/09-research-notes.md §1](docs/09-research-notes.md).

## Commits

- One task (or a coherent part of one) per commit. The tree passes the full check at every commit.
- Conventional Commits with the milestone as scope: `feat(m2): coalesce concurrent misses per key`, `test(m4): partition fairness under flood`, `docs(lld): clarify flight removal`, `fix(m1): ...`, `chore(ci): ...`.
- Body: what and why in plain sentences, requirement IDs covered, and the security checklist answers when relevant.
- End every commit message with the co-author trailer the session provides.

## Writing style for docs and comments

Plain, specific, human. Mix sentence lengths. Prefer concrete numbers and names over adjectives. Avoid filler and inflated vocabulary ("robust", "seamless", "leverage", "comprehensive", "delve"), rule-of-three lists by reflex, "not just X but Y" constructions, and em dashes (use commas, parentheses or two sentences). Sentence-case headings. Bold at most once per section.

## Repository layout (target)

See [docs/02-architecture.md §3](docs/02-architecture.md). In short: root package `weir` (engine), `store` (interface, codec), `store/memory`, `store/storetest`, `weirhttp`, `internal/{httpcc,keys,sfv,coalesce,limiter,breaker,missrate,testorigin}`, `examples/weirproxy`, `loadtest`, `scripts`.
