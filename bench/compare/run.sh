#!/usr/bin/env bash
# Runs the comparison scenarios (docs/11) against each subject in turn and
# prints the report. Needs a running Docker daemon.
#
#   ./run.sh                          every subject, both tiers, 3 repetitions
#   SUBJECTS=varnish TIERS=tuned ./run.sh -reps 3
#   SUBJECTS="varnish nginx" ./run.sh -reps 1 -scenario cold-herd
#
# Arguments are passed to `compare run`. Results go to results/, which is not
# committed (docs/11 §9). SUBJECTS may also name weirproxy-next and
# caddy-weir-next, Weir built from the working tree (docs/11 §2).
set -euo pipefail
cd "$(dirname "$0")"
. ./lib.sh

SUBJECTS="${SUBJECTS:-origin caddy-nocache weirproxy caddy-weir caddy-souin varnish nginx}"
TIERS="${TIERS:-default tuned}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
name="$stamp.jsonl"
mkdir -p results
watcher=""
cleanup() {
  [ -z "$watcher" ] || kill "$watcher" 2>/dev/null || true
  rm -f results/restart.request results/restart.done
  docker compose --profile tools down --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

build_next "$stamp"
docker compose --profile tools build
docker compose up -d origin
record_env "$stamp"

# The runner is a container and cannot restart another one. For the restart
# scenario it writes results/restart.request and this loop does it: a
# graceful stop (SIGTERM, up to 30 s) and a start of the same container, so
# what the cache keeps on its own disk is still there. The origin control is
# not restarted: it would forget its profiles, and it keeps nothing anyway.
restart_watch() {
  while :; do
    if [ -e results/restart.request ]; then
      [ "$1" = origin ] || TIER="$2" docker compose restart -t 30 "$1" >/dev/null 2>&1 || echo "!! restart of $1 failed" >&2
      rm -f results/restart.request
      : > results/restart.done
    fi
    sleep 0.2
  done
}

for tier in $TIERS; do
  for s in $SUBJECTS; do
    # The controls and weirproxy have one config: they run in the default tier only.
    # A "-next" subject (the working tree) uses the config of its released twin.
    if [ "$tier" != default ] && [ ! -d "subjects/${s%-next}/$tier" ]; then continue; fi
    port=8080
    if [ "$s" = origin ]; then port=9000; else TIER="$tier" docker compose up -d --force-recreate "$s"; fi
    # How this subject is purged, if it can be (one line: METHOD URL [BODY]).
    purge=""
    if [ -f "subjects/${s%-next}/purge" ]; then purge="$(sed "s/${s%-next}:/$s:/" "subjects/${s%-next}/purge")"; fi
    echo "== $s ($tier)" >&2
    restart_watch "$s" "$tier" &
    watcher=$!
    docker compose --profile tools run --rm -T run run \
      -target "http://$s:$port" -control http://origin:9001 \
      -subject "$s" -tier "$tier" -purge "$purge" -restart-dir /results -out "/results/$name" "$@" || echo "!! $s ($tier) failed" >&2
    kill "$watcher" 2>/dev/null || true
    wait "$watcher" 2>/dev/null || true
    watcher=""
    if [ "$s" != origin ]; then
      docker compose logs --no-color "$s" > "results/$stamp.$s.$tier.log" 2>&1 || true
      # -v: the anonymous volumes too, so the next tier starts with no
      # snapshot or cache file left by this one.
      docker compose rm -sfv "$s" >/dev/null
    fi
  done
done

docker compose --profile tools run --rm -T run report "/results/$name" | tee "results/$stamp.md"
