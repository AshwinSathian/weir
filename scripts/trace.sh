#!/usr/bin/env bash
# Lists requirement and invariant IDs that no test cites (docs/07 §1 rule 1).
# Report mode by default; TRACE_STRICT=1 makes uncited IDs fail (`make check` sets it).
# IDs of Phase 1.x cards (M11 and later) are allowed to be uncited, see `later` below.
set -euo pipefail
cd "$(dirname "$0")/.."

ids=$( { grep -oE '^- (FR-[A-Z]+-[0-9]+|NFR-[0-9]+)\.' docs/01-technical-spec.md | sed -E 's/^- //; s/\.$//'
         grep -oE '^- (INV-[0-9]+) ' docs/06-threat-model.md | sed -E 's/^- //; s/ $//'; } | sort -u )

tests=$(find . -name '*_test.go' -not -path './.git/*' 2>/dev/null || true)
cited=""
if [ -n "$tests" ]; then
  cited=$(grep -hoE '(FR-[A-Z]+-[0-9]+|NFR-[0-9]+|INV-[0-9]+)' $tests | sort -u || true)
fi

# Phase 1.x requirements (spec §13, cards M11 to M15) get tests when their
# cards land; drop a prefix from this list in the same PR that cites it.
later='^FR-(SNP|FAIR)-[0-9]+$'
ids=$(printf '%s\n' "$ids" | grep -Ev "$later" || true)

missing=$(comm -23 <(printf '%s\n' "$ids") <(printf '%s\n' "$cited"))
total=$(printf '%s\n' "$ids" | grep -c . || true)
miss=$(printf '%s\n' "$missing" | grep -c . || true)
echo "trace: $((total - miss))/$total requirement and invariant IDs cited by tests"
if [ "${TRACE_VERBOSE:-0}" = 1 ] || [ "${TRACE_STRICT:-0}" = 1 ]; then
  printf '%s\n' "$missing" | sed '/^$/d; s/^/  uncited: /'
fi
if [ "${TRACE_STRICT:-0}" = 1 ] && [ "$miss" -gt 0 ]; then exit 1; fi
exit 0
