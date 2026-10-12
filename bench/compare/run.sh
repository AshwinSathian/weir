#!/usr/bin/env bash
# Runs the comparison scenarios (docs/11) against each subject in turn and
# prints the report. Needs a running Docker daemon.
#
#   ./run.sh                          every subject, both tiers, 3 repetitions
#   SUBJECTS="varnish nginx" ./run.sh -reps 1 -scenario cold-herd
#
# Arguments are passed to `compare run`. Results go to results/, which is not
# committed: numbers are private until Ashwin decides to publish (docs/11 §9).
set -euo pipefail
cd "$(dirname "$0")"

SUBJECTS="${SUBJECTS:-origin caddy-nocache weirproxy caddy-weir caddy-souin varnish nginx}"
TIERS="${TIERS:-default tuned}"
name="$(date -u +%Y%m%dT%H%M%SZ).jsonl"
mkdir -p results
trap 'docker compose --profile tools down --remove-orphans >/dev/null 2>&1 || true' EXIT

docker compose --profile tools build
docker compose up -d origin
{ docker compose version; docker compose --profile tools images; } > "results/${name%.jsonl}.env.txt"

for tier in $TIERS; do
  for s in $SUBJECTS; do
    # The controls and weirproxy have one config: they run in the default tier only.
    if [ "$tier" != default ] && [ ! -d "subjects/$s/$tier" ]; then continue; fi
    port=8080
    if [ "$s" = origin ]; then port=9000; else TIER="$tier" docker compose up -d --force-recreate "$s"; fi
    echo "== $s ($tier)" >&2
    docker compose --profile tools run --rm -T run run \
      -target "http://$s:$port" -control http://origin:9001 \
      -subject "$s" -tier "$tier" -out "/results/$name" "$@" || echo "!! $s ($tier) failed" >&2
    if [ "$s" != origin ]; then
      docker compose logs --no-color "$s" > "results/${name%.jsonl}.$s.$tier.log" 2>&1 || true
      docker compose rm -sf "$s" >/dev/null
    fi
  done
done

docker compose --profile tools run --rm -T run report "/results/$name" | tee "results/${name%.jsonl}.md"
