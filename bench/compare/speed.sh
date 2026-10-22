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
#   hit-<size>-<rate>   cached GETs at fixed rates, for 1 KiB, 64 KiB, 960 KiB
#   sat-<size>-<rate>   the same at rising rates, to find where it breaks
#   miss-1024           uncacheable GETs, and the same against the origin
#   fill-<n>            n new small entries: memory (anon + shmem) per object (rep 1)
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
# A failed read gives an empty value, which the report shows as not
# available; it must not end a two-hour run (set -e).
# shellcheck disable=SC2016 # the awk programs run in the helper, unexpanded here
cpu() { cgroup "$1" cpu.stat '/^usage_usec/ {print $2}' || true; }
# memory.stat anon plus shmem: what the cache holds in memory, its shared
# memory included (nginx keeps its keys in a shared keys_zone). Page cache is
# left out (memory.current would add it): nginx's bodies are files on disk.
# shellcheck disable=SC2016
mem() { cgroup "$1" memory.stat '/^(anon|shmem) / {n += $2} END {print n}' || true; }

# secs <requests> <rate>: the duration that sends that many requests at the
# rate, in whole seconds and at least one: vegeta reads 0s as "forever".
secs() { echo "$(( ($1 + $2 - 1) / $2 ))s"; }

# load <script>: run a shell script in the load generator container.
load() { compose --profile tools run --rm -T load sh -c "$1"; }

# profile <name> <body bytes> <cache-control>
profile() {
  load "curl -sf -X PUT -d '{\"body_bytes\":$2,\"cache_control\":\"$3\"}' http://origin:9001/profile/$1" >/dev/null
}

# origin_calls <profile>
origin_calls() { load "curl -sf http://origin:9001/stats | jq '.\"$1\".calls // 0'" || true; }

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

# up <service> <kind>: a fresh container of the service for kind (speed:
# the 160 MiB store of the hit-ratio runs; mem: a store no fill evicts;
# default). Stores sized by environment (weirproxy, varnish) get it here; the
# others use the tier directory of that name.
up() {
  case "$2" in
    speed) export VARNISH_SIZE=160M WEIRPROXY_GOMEMLIMIT=400MiB ;;
    mem) export VARNISH_SIZE=2G WEIRPROXY_GOMEMLIMIT=5GiB ;;
    *) export VARNISH_SIZE=512M WEIRPROXY_GOMEMLIMIT=400MiB ;;
  esac
  local tier="$2"
  [ -d "subjects/${1%-next}/$tier" ] || tier=default
  TIER="$tier" compose up -d --force-recreate "$1" >/dev/null
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

# Body sizes: 1 KiB, 64 KiB, and 960 KiB, the largest every subject stores
# at its defaults (Weir's 1 MiB limit counts headers too). Keys per size
# keep the working set inside the 160 MiB store.
SIZES="1024 65536 983040"
# 50 of the largest: Weir's 160 MiB store is 16 shards of 10 MiB, each of
# which holds about ten such objects, and 50 keys leave no shard near that.
keys() { case "$1" in 983040) echo 50 ;; *) echo 1000 ;; esac; }
rates() {
  case "$1" in
    1024) echo "2000 8000" ;;
    65536) echo "1000 4000" ;;
    983040) echo "100 400" ;;
  esac
}

# measure <dir> <test> <targets args> <rate> <size> <profile>: one timed
# run with what the report checks beside it: CPU, origin calls (a hit run
# must not reach the origin), the rate asked for and the body size expected.
measure() {
  local b o
  b="$(cpu "$s")"
  o="$(origin_calls "$6")"
  attack "$1" "$2" "$3" "$4" "$DURATION"
  echo "$b $(cpu "$s")" > "$1/$2.cpu"
  echo "$o $(origin_calls "$6")" > "$1/$2.origin"
  echo "$4" > "$1/$2.rate"
  echo "$5" > "$1/$2.size"
}

# Hit-ratio and memory runs need a store of a stated size. cache-handler's
# default storage has no verified size setting (docs/11 §5.5), so it has no
# rows for them.
MEMSIZED="$SIZED"

for rep in $(seq "$REPS"); do
  dir="$out/$rep"
  control_n=0
  # shellcheck disable=SC2086 # one subject per word
  order="$(printf '%s\n' $SUBJECTS | awk 'BEGIN{srand()} {print rand() "\t" $0}' | sort | cut -f2-)" # docs/11 §6: random order
  # The origin direct, the miss baseline, after the rep's first control so
  # its latency has a ratio like every other row.
  control "$dir"
  mkdir -p "$dir/origin"
  echo 0 > "$dir/origin/control.idx"
  profile "miss-$rep" 1024 "no-store"
  attack "$dir/origin" miss-1024 "-base http://origin:9000/o/miss-$rep/ -keys 1000000 -n 100000" 500 "$DURATION"
  echo 1024 > "$dir/origin/miss-1024.size"
  for s in $order; do
    control "$dir"
    sdir="$dir/$s"
    mkdir -p "$sdir"
    echo "$((control_n - 1))" > "$sdir/control.idx" # the control measured just before
    echo "== rep $rep: $s" >&2
    up "$s" speed
    for size in $SIZES; do
      p="s$size-$rep"
      k="$(keys "$size")"
      profile "$p" "$size" "max-age=3600"
      # Twice: a cache with an admission filter (S3-FIFO in Weir's memory
      # store) keeps an object seen once on probation, so the hit rows
      # would start with misses.
      attack "$sdir" "warm-$size" "-base http://$s:8080/o/$p/ -mode fill -keys $k" 50 "$(secs "$k" 50)"
      attack "$sdir" "warm2-$size" "-base http://$s:8080/o/$p/ -mode fill -keys $k" 50 "$(secs "$k" 50)"
      last=0
      for r in $(rates "$size"); do
        measure "$sdir" "hit-$size-$r" "-base http://$s:8080/o/$p/ -keys $k -n 200000" "$r" "$size" "$p"
        last="$r"
      done
      # Rising rates until the subject stops keeping up: errors or wrong
      # bodies over 0.1%, p99 ten times the first rate's, or a rate vegeta
      # could not hold (95%), which would hide queueing.
      base99="$(load "jq '.latencies[\"99th\"]' /$sdir/hit-$size-$(rates "$size" | cut -d' ' -f1).json")"
      r="$last"
      for _ in $(seq "$SAT_STEPS"); do
        r=$(( r * 3 / 2 ))
        measure "$sdir" "sat-$size-$r" "-base http://$s:8080/o/$p/ -keys $k -n 400000" "$r" "$size" "$p"
        ok="$(load "jq --argjson b '$base99' --argjson r '$r' --argjson n '$size' '.success >= 0.999 and (.bytes_in.total / (.requests * .success)) == \$n and .latencies[\"99th\"] <= 10 * \$b and .rate >= 0.95 * \$r' /$sdir/sat-$size-$r.json")"
        [ "$ok" = true ] || break
      done
    done
    profile "miss-$rep" 1024 "no-store"
    attack "$sdir" miss-1024 "-base http://$s:8080/o/miss-$rep/ -keys 1000000 -n 100000" 500 "$DURATION"
    echo 1024 > "$sdir/miss-1024.size"
    down "$s"

    if [ "$rep" = 1 ] && [[ " $MEMSIZED " == *" $s "* ]]; then
      for n in $FILLS; do
        up "$s" mem
        profile "fill-$n" 100 "max-age=3600"
        before="$(mem "$s")"
        attack "$sdir" "fill-$n" "-base http://$s:8080/o/fill-$n/ -mode fill -keys $n" 5000 "$(secs "$n" 5000)"
        sleep 5 # let the cache settle after the last write before reading memory
        echo "$before $(mem "$s")" > "$sdir/fill-$n.mem"
        echo 100 > "$sdir/fill-$n.size"
        down "$s"
      done
    fi
    if [ "$rep" = 1 ] && [[ " $SIZED " == *" $s "* ]]; then
      for w in zipf scan; do
        up "$s" speed
        # 3 000-byte bodies: with key and headers each object still fits
        # one 4 KiB file-system block in nginx's disk cache, whose max_size
        # counts blocks, so the stores hold about the same object count.
        profile "$w" 3000 "max-age=3600"
        before="$(origin_calls "$w")"
        attack "$sdir" "$w" "-base http://$s:8080/o/$w/ -mode $w -keys 200000 -n 300000 -seed 7" 5000 "$RATIO_DURATION"
        echo "$before $(origin_calls "$w")" > "$sdir/$w.origin"
        echo 3000 > "$sdir/$w.size"
        down "$s"
      done
    fi
  done
  control "$dir"
done

compose --profile tools run --rm -T run speed "/results/speed-$stamp" | tee "$out/report.md"
