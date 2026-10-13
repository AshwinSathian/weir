#!/usr/bin/env bash
# Runs the security probes (docs/11 §5.3) against each subject and tier.
#   ./probes.sh
#   SUBJECTS="varnish nginx" TIERS=default ./probes.sh
# Results go to results/, which is not committed.
set -euo pipefail
cd "$(dirname "$0")"

SUBJECTS="${SUBJECTS:-origin caddy-nocache weirproxy weirproxy-next caddy-weir caddy-weir-next caddy-souin varnish nginx}"
TIERS="${TIERS:-default tuned}"
name="probes-$(date -u +%Y%m%dT%H%M%SZ).jsonl"
mkdir -p results
trap 'docker compose --profile tools down --remove-orphans >/dev/null 2>&1 || true' EXIT

docker compose --profile tools build
docker compose up -d origin
for tier in $TIERS; do
  for s in $SUBJECTS; do
    if [ "$tier" != default ] && [ ! -d "subjects/${s%-next}/$tier" ]; then continue; fi
    port=8080
    if [ "$s" = origin ]; then port=9000; else TIER="$tier" docker compose up -d --force-recreate "$s"; fi
    echo "== $s ($tier)" >&2
    docker compose --profile tools run --rm -T run probe \
      -target "http://$s:$port" -subject "$s" -tier "$tier" -out "/results/$name" || echo "!! $s ($tier) failed" >&2
    if [ "$s" != origin ]; then docker compose rm -sf "$s" >/dev/null; fi
  done
done
echo "results/$name"
