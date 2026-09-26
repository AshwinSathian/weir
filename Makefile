# Command surface for humans, Claude Code sessions and CI. See CLAUDE.md.

GOLANGCI_VERSION := v2.14.0
GOLANGCI := $(shell command -v golangci-lint 2>/dev/null)
ifeq ($(GOLANGCI),)
GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
endif

.PHONY: check fmt-check vet lint test test-short trace trace-strict fuzz-short bench vuln card next

## check: everything a card must pass before handoff (CI runs the same)
check: fmt-check vet lint test trace

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

lint:
	$(GOLANGCI) run

test:
	go test -race -shuffle=on -count=1 ./...

## test-short: fast loop while writing code
test-short:
	go test -count=1 ./...

## trace: requirement IDs with no citing test (report only until TRACE_STRICT=1)
trace:
	@scripts/trace.sh

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

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...

## card ID=P0-02: print one task card; next: print the next open card
card:
	@scripts/card.sh $(ID)

next:
	@scripts/card.sh next
