# Command surface for humans, Claude Code sessions and CI. See CLAUDE.md.

GOLANGCI_VERSION := v2.14.0
GOLANGCI := $(shell command -v golangci-lint 2>/dev/null)
ifeq ($(GOLANGCI),)
GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
endif

# Every module below the root. Each is also built alone (GOWORK=off) so none
# leans on an unpublished sibling by accident (docs/02 §3.2).
SUBMODULES := $(patsubst ./%/go.mod,%,$(shell find . -mindepth 2 -name go.mod -not -path './testdata/*' -not -path './.claude/*' -not -path './.git/*' | sort))

.PHONY: check fmt-check vet lint test modules test-short trace trace-strict fuzz-short bench load cache-tests vuln card next test-valkey

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

## test-valkey: integration tests (store/valkey, and the two-node Caddy test in caddy/) against a real server (WEIR_VALKEY_ADDR, default 127.0.0.1:6379; start Valkey with --maxmemory-policy volatile-lfu; WEIR_VALKEY_STORM_ADDR, a throwaway server with --maxmemory 16mb, enables the eviction storm)
test-valkey:
	cd store/valkey && GOWORK=off go vet -tags integration ./...
	cd store/valkey && GOWORK=off WEIR_VALKEY_ADDR=$${WEIR_VALKEY_ADDR:-127.0.0.1:6379} WEIR_VALKEY_STORM_ADDR=$${WEIR_VALKEY_STORM_ADDR:-} go test -race -tags integration -count=1 ./...
	cd caddy && GOWORK=off go vet -tags integration ./...
	cd caddy && GOWORK=off WEIR_VALKEY_ADDR=$${WEIR_VALKEY_ADDR:-127.0.0.1:6379} go test -tags integration -run TestE2ETwoNode -count=1 ./...

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
	@for pkg in $$(go list ./...); do \
	  for f in $$(go test -list '^Fuzz' $$pkg 2>/dev/null | grep '^Fuzz'); do \
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
