#!/usr/bin/env bash
# The speed and memory pillar (docs/11 §5.5, §6). Run it last, from a plain
# terminal, on a quiet machine: Docker Desktop with 8 CPUs and 12 GiB, the
# editor and other load closed. About two hours with the defaults.
#
#   ./speed.sh                        every subject, REPS repetitions
#   SUBJECTS="caddy-weir varnish" REPS=1 DURATION=5s ./speed.sh   a quick look
#
# What it measures, per subject, with the cache held to 2 cores
# (speed.compose.yaml) and vegeta as the open-loop load (docs/11 §4):
#   hit-<size>-<rate>   cached GETs at fixed rates, for 1 KiB, 64 KiB, 1 MiB
#   sat-<size>-<rate>   the same at rising rates, to find where it breaks
#   miss-1024           uncacheable GETs, and the same against the origin
#   fill-<n>            n new small entries: memory.current per object (rep 1)
#   zipf, scan          hit ratio in a 160 MiB store (rep 1)
# and before each subject the caddy-nocache control, whose movement of more
# than 10% discards the repetition (docs/11 §6). CPU time per request comes
# from the container's cgroup cpu.stat. `compare speed` prints the report.
set -euo pipefail
cd "$(dirname "$0")"
. ./lib.sh

SUBJECTS="${SUBJECTS:-weirproxy caddy-weir caddy-souin varnish nginx}"
REPS="${REPS:-5}"
DURATION="${DURATION:-10s}"
SAT_STEPS="${SAT_STEPS:-4}"
FILLS="${FILLS:-100000 1000000}"            # entry counts for memory per object
RATIO_DURATION="${RATIO_DURATION:-60s}"     # each hit-ratio run, at 5 000 requests a second
# Subjects whose store can be held to 160 MiB for the hit-ratio runs.
SIZED="weirproxy weirproxy-next caddy-weir caddy-weir-next varnish nginx"
ALPINE=alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8

stamp="$(date -u +%Y%m%dT%H%M%SZ)"
out="results/speed-$stamp"
mkdir -p "$out"
compose() { docker compose -f compose.yaml -f speed.compose.yaml "$@"; }
cleanup() { compose --profile tools down --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT

cpus="$(docker info --format '{{.NCPU}}')"
if [ "$cpus" -lt 8 ]; then
  echo "speed.sh: Docker has $cpus CPUs; the pillar pins cores 0-7 (Docker Desktop, Settings, Resources)" >&2
  exit 1
fi

build_next "$stamp"
compose --profile tools build
compose up -d origin
record_env "$stamp"
cp "results/$stamp.env.txt" "$out/env.txt"

# cgroup <service> <file> <awk program>: a value from the service's cgroup,
# read through a helper with the host's cgroup namespace (the subjects'
# images may have no shell).
cgroup() {
  local id
  id="$(compose ps -q "$1")"
  docker run --rm --privileged --cgroupns=host -v /sys/fs/cgroup:/cg:ro "$ALPINE" sh -c \
    "d=\$(find /cg -type d -name '*$id*' | head -1); [ -n \"\$d\" ] && awk '$3' \"\$d/$2\""
}
# shellcheck disable=SC2016 # the awk programs run in the helper, unexpanded here
cpu() { cgroup "$1" cpu.stat '/^usage_usec/ {print $2}'; }
# shellcheck disable=SC2016
mem() { cgroup "$1" memory.current '{print $1}'; }

# load <script>: run a shell script in the load generator container.
load() { compose --profile tools run --rm -T load sh -c "$1"; }

# profile <name> <body bytes> <cache-control>
profile() {
  load "curl -sf -X PUT -d '{\"body_bytes\":$2,\"cache_control\":\"$3\"}' http://origin:9001/profile/$1" >/dev/null
}

# origin_calls <profile>
origin_calls() { load "curl -sf http://origin:9001/stats | jq '.\"$1\".calls // 0'"; }

# attack <dir> <test> <targets args> <rate> <duration>: one vegeta run, its
# JSON and hdrplot reports kept as <dir>/<test>.json and .hdr.
attack() {
  load "compare targets $3 > /tmp/t && vegeta attack -targets=/tmp/t -rate=$4 -duration=$5 -timeout=10s > /tmp/r.bin \
    && vegeta report -type=json < /tmp/r.bin > /$1/$2.json && vegeta report -type=hdrplot < /tmp/r.bin > /$1/$2.hdr"
}

# ready <target>: wait until the target answers through the cache.
ready() {
  for _ in $(seq 100); do
    if load "curl -sf -o /dev/null $1/o/ready/x" 2>/dev/null; then return 0; fi
    sleep 0.3
  done
  echo "speed.sh: $1 never became ready" >&2
  return 1
}

# up <service> <tier>: a fresh container of the service in the tier.
up() {
  case "$2" in
    speed) export VARNISH_SIZE=160M WEIRPROXY_GOMEMLIMIT=400MiB ;;
    mem) export VARNISH_SIZE=2G WEIRPROXY_GOMEMLIMIT=5GiB ;;
    *) export VARNISH_SIZE=512M WEIRPROXY_GOMEMLIMIT=400MiB ;;
  esac
  TIER="$2" compose up -d --force-recreate "$1" >/dev/null
  ready "http://$1:8080"
}
down() { compose rm -sfv "$1" >/dev/null; }

control_n=0
control() {
  local dir="$1"
  mkdir -p "$dir/caddy-nocache"
  up caddy-nocache default
  profile control 1024 "max-age=3600"
  attack "$dir/caddy-nocache" "control-$control_n" "-base http://caddy-nocache:8080/o/control/ -keys 1000 -n 100000" 2000 "$DURATION"
  control_n=$((control_n + 1))
  down caddy-nocache
}

rates() {
  case "$1" in
    1024) echo "2000 8000" ;;
    65536) echo "1000 4000" ;;
    1048576) echo "100 400" ;;
  esac
}

for rep in $(seq "$REPS"); do
  dir="$out/$rep"
  control_n=0
  # shellcheck disable=SC2086 # one subject per word
  order="$(printf '%s\n' $SUBJECTS | awk 'BEGIN{srand()} {print rand() "\t" $0}' | sort | cut -f2-)" # docs/11 §6: random order
  for s in $order; do
    control "$dir"
    sdir="$dir/$s"
    mkdir -p "$sdir"
    tier=speed
    [ -d "subjects/${s%-next}/speed" ] || tier=default
    echo "== rep $rep: $s" >&2
    up "$s" "$tier"
    for size in 1024 65536 1048576; do
      p="s$size-$rep"
      profile "$p" "$size" "max-age=3600"
      attack "$sdir" "warm-$size" "-base http://$s:8080/o/$p/ -mode fill -keys 1000" 500 "$(( 1000 / 500 ))s"
      last=0
      for r in $(rates "$size"); do
        before="$(cpu "$s")"
        attack "$sdir" "hit-$size-$r" "-base http://$s:8080/o/$p/ -keys 1000 -n 200000" "$r" "$DURATION"
        echo "$before $(cpu "$s")" > "$sdir/hit-$size-$r.cpu"
        last="$r"
      done
      r="$last"
      for _ in $(seq "$SAT_STEPS"); do
        r=$(( r * 3 / 2 ))
        attack "$sdir" "sat-$size-$r" "-base http://$s:8080/o/$p/ -keys 1000 -n 400000" "$r" "$DURATION"
        ok="$(load "jq '.success >= 0.999' /$sdir/sat-$size-$r.json")"
        [ "$ok" = true ] || break
      done
    done
    profile "miss-$rep" 1024 "no-store"
    attack "$sdir" miss-1024 "-base http://$s:8080/o/miss-$rep/ -keys 1000000 -n 100000" 500 "$DURATION"
    down "$s"

    if [ "$rep" = 1 ]; then
      mkdir -p "$dir/origin"
      attack "$dir/origin" miss-1024 "-base http://origin:9000/o/miss-$rep/ -keys 1000000 -n 100000" 500 "$DURATION"
      for n in $FILLS; do
        mtier=mem
        [ -d "subjects/${s%-next}/mem" ] || mtier=default
        up "$s" "$mtier"
        profile "fill-$n" 100 "max-age=3600"
        before="$(mem "$s")"
        attack "$sdir" "fill-$n" "-base http://$s:8080/o/fill-$n/ -mode fill -keys $n" 5000 "$(( n / 5000 ))s"
        sleep 5 # let the cache settle after the last write before reading memory
        echo "$before $(mem "$s")" > "$sdir/fill-$n.mem"
        down "$s"
      done
      if [[ " $SIZED " == *" $s "* ]]; then
        for w in zipf scan; do
          up "$s" speed
          profile "$w" 4096 "max-age=3600"
          before="$(origin_calls "$w")"
          attack "$sdir" "$w" "-base http://$s:8080/o/$w/ -mode $w -keys 200000 -n 300000 -seed 7" 5000 "$RATIO_DURATION"
          echo "$before $(origin_calls "$w")" > "$sdir/$w.origin"
          down "$s"
        done
      fi
    fi
  done
  control "$dir"
done

compose --profile tools run --rm -T run speed "/results/speed-$stamp" | tee "$out/report.md"
