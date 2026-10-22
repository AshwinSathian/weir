# Command surface for humans, Claude Code sessions and CI. See CLAUDE.md.

GOLANGCI_VERSION := v2.14.0
# A golangci-lint on PATH is used only when it is the pinned version; another
# version can pass code that CI's pinned one rejects, or the reverse.
GOLANGCI := $(shell [ "$$(golangci-lint version --short 2>/dev/null | sed 's/^v//')" = "$(GOLANGCI_VERSION:v%=%)" ] && command -v golangci-lint)
ifeq ($(GOLANGCI),)
GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
endif

# Every module below the root. Each is also built alone (GOWORK=off) so none
# leans on an unpublished sibling by accident (docs/02 §3.2).
SUBMODULES := $(patsubst ./%/go.mod,%,$(shell find . -mindepth 2 -name go.mod -not -path './testdata/*' -not -path './.claude/*' -not -path './.git/*' -not -path './site/*' | sort))

SITE_PROJECT ?= weir-site

.PHONY: site-check site-build site-deploy release-smoke release-check check fmt-check vet lint test modules test-short trace trace-strict fuzz-short bench load cache-tests vuln card next test-valkey compare

## check: everything a card must pass before handoff (CI runs the same)
check: fmt-check vet lint test modules trace-strict

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

lint:
	$(GOLANGCI) run

test:
	go test -race -shuffle=on -count=1 ./...

## modules: vet, lint and race-test each submodule alone, outside go.work
modules:
	@for m in $(SUBMODULES); do \
	  echo "== $$m"; \
	  (cd $$m && GOWORK=off go vet ./... && GOWORK=off $(GOLANGCI) run && GOWORK=off go test -race -shuffle=on -count=1 ./...) || exit 1; \
	done

## release-check: no sub-module may keep a replace or a v0.0.0 pin on the release commit (replace is ignored by importers), and each resolves alone from committed go.mod and go.sum
release-check:
	@fail=0; for m in $(SUBMODULES); do \
	  if grep -nE '^replace |github.com/AshwinSathian/weir[^ ]* v0\.0\.0' $$m/go.mod; then echo "$$m/go.mod: unpublished pin"; fail=1; fi; \
	done; \
	for m in $(SUBMODULES); do \
	  (cd $$m && GOWORK=off go list -m all >/dev/null) || { echo "$$m does not resolve alone (is its go.sum committed?)"; fail=1; }; \
	  if [ -n "$$(git status --porcelain -- $$m/go.mod $$m/go.sum)" ]; then echo "$$m: go.mod or go.sum differs from the commit; a tag would not carry it"; fail=1; fi; \
	done; exit $$fail

## release-smoke: the published tags as a user meets them (docs/release.md step 9): make release-smoke VERSION=v1.0.0
release-smoke:
	@scripts/release-smoke.sh $(VERSION)

## test-valkey: integration tests (store/valkey, and the two-node and key-generation Caddy tests in caddy/) against a real server (WEIR_VALKEY_ADDR, default 127.0.0.1:6379; start Valkey with --maxmemory-policy volatile-lfu; WEIR_VALKEY_STORM_ADDR, a throwaway server with --maxmemory 16mb, enables the eviction storm; WEIR_VALKEY_REPLICA_ADDR, a replica of the first started with --enable-debug-command yes, enables the replication tests)
test-valkey:
	cd store/valkey && GOWORK=off go vet -tags integration ./...
	cd store/valkey && GOWORK=off WEIR_VALKEY_ADDR=$${WEIR_VALKEY_ADDR:-127.0.0.1:6379} WEIR_VALKEY_STORM_ADDR=$${WEIR_VALKEY_STORM_ADDR:-} go test -race -tags integration -count=1 ./...
	cd caddy && GOWORK=off go vet -tags integration ./...
	cd caddy && GOWORK=off WEIR_VALKEY_ADDR=$${WEIR_VALKEY_ADDR:-127.0.0.1:6379} go test -race -tags integration -run 'TestE2ETwoNode|TestValkeyKeyGenRecord' -count=1 ./...

## test-short: fast loop while writing code
test-short:
	go test -count=1 ./...

## trace: requirement IDs with no citing test (report only)
trace:
	@scripts/trace.sh

## trace-strict: the same, failing on an uncited ID up to M10
trace-strict:
	@TRACE_STRICT=1 scripts/trace.sh

## fuzz-short: every fuzz target for FUZZTIME (default 20s) each
FUZZTIME ?= 20s
fuzz-short:
	@go test -run '^$$' ./... >/dev/null || exit 1; # a package that does not compile fails here, not by listing no targets
	@for pkg in $$(go list ./...); do \
	  for f in $$(go test -list '^Fuzz' $$pkg | grep '^Fuzz'); do \
	    echo "== $$pkg $$f"; go test -run '^$$' -fuzz "^$$f$$" -fuzztime $(FUZZTIME) $$pkg || exit 1; \
	  done; \
	done

bench:
	go test -run '^$$' -bench . -benchmem ./...

## load: real-time load and adversarial scenarios (docs/07 §9, about 5 minutes); WEIR_LOAD_SCALE=0.1 for a smoke run
load:
	go test -tags load -count=1 -timeout 15m -v ./loadtest/

## cache-tests: http-tests/cache-tests against examples/weirproxy, compared with the baseline (docs/07 §8); needs node and network
cache-tests:
	@scripts/cache-tests.sh

## compare: the head-to-head scenarios against other caches (docs/11); needs a running Docker daemon; results stay in bench/compare/results/ (not committed)
compare:
	@bench/compare/run.sh $(ARGS)

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
	@for m in $(SUBMODULES); do \
	  echo "== $$m"; \
	  (cd $$m && GOWORK=off go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...) || exit 1; \
	done

## card ID=P0-02: print one task card; next: print the next open card
card:
	@scripts/card.sh $(ID)

next:
	@scripts/card.sh next

## site-check: the website gate (CI runs the same): source checks, type check, static build with link check. Needs Node 24 and pnpm; not part of `check`, so Go contributors need no Node
site-check:
	cd site && pnpm install --frozen-lockfile && pnpm check && pnpm typecheck && pnpm build

## site-build: static site into site/out
site-build:
	cd site && pnpm install --frozen-lockfile && pnpm build

## site-deploy: build and upload to Cloudflare Pages (wrangler must be logged in): make site-deploy BRANCH=main
BRANCH ?= main
site-deploy: site-check
	cd site && wrangler pages deploy out --project-name $(SITE_PROJECT) --branch $(BRANCH)
