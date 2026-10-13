#!/usr/bin/env bash
# Runs the security probes (docs/11 §5.3) against each subject and tier.
#   ./probes.sh
#   SUBJECTS="varnish nginx" TIERS="default hardened" ./probes.sh
# Results go to results/, which is not committed. Beside the verdicts it
# writes results/probes-<stamp>.mem.txt: each cache container's memory
# before and after its probes, of which the Vary probe's 20 000 variants are
# nearly all the load.
set -euo pipefail
cd "$(dirname "$0")"
. ./lib.sh

SUBJECTS="${SUBJECTS:-origin caddy-nocache weirproxy caddy-weir caddy-souin varnish nginx}"
TIERS="${TIERS:-default tuned hardened}"
stamp="probes-$(date -u +%Y%m%dT%H%M%SZ)"
name="$stamp.jsonl"
mkdir -p results
trap 'docker compose --profile tools down --remove-orphans >/dev/null 2>&1 || true' EXIT

build_next "$stamp"
docker compose --profile tools build
docker compose up -d origin
record_env "$stamp"
mem() { docker stats --no-stream --format '{{.MemUsage}}' "$(docker compose ps -q "$1")" | cut -d/ -f1 | tr -d ' '; }

for tier in $TIERS; do
  for s in $SUBJECTS; do
    if [ "$tier" != default ] && [ ! -d "subjects/${s%-next}/$tier" ]; then continue; fi
    port=8080
    if [ "$s" = origin ]; then port=9000; else TIER="$tier" docker compose up -d --force-recreate "$s"; fi
    echo "== $s ($tier)" >&2
    # One request first, so "before" is a cache that has started serving.
    docker compose --profile tools run --rm -T run probe -ready-only -target "http://$s:$port" -subject "$s" >/dev/null 2>&1 || true
    before="$(mem "$s")"
    docker compose --profile tools run --rm -T run probe \
      -target "http://$s:$port" -subject "$s" -tier "$tier" -out "/results/$name" || echo "!! $s ($tier) failed" >&2
    echo "$s $tier before=$before after=$(mem "$s")" | tee -a "results/$stamp.mem.txt" >&2
    if [ "$s" != origin ]; then docker compose rm -sfv "$s" >/dev/null; fi
  done
done
echo "results/$name"
