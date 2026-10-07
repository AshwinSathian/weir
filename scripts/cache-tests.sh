#!/usr/bin/env bash
# Run http-tests/cache-tests against examples/weirproxy and compare with
# testdata/cache-tests-baseline.json (docs/07 §8).
#
#   scripts/cache-tests.sh            fail when a test that passed in the baseline fails now
#   UPDATE=1 scripts/cache-tests.sh   rewrite the baseline from this run
#
# CACHE_TESTS_REF pins the suite; bump it on purpose and update the baseline in
# the same change, so an upstream test edit never shows up as a Weir regression.
set -euo pipefail

CACHE_TESTS_REF="${CACHE_TESTS_REF:-d644cf4bf487763646aac19d2c8b846daa0f604d}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASELINE="$ROOT/testdata/cache-tests-baseline.json"
WORK="$(mktemp -d)"
ORIGIN_PORT=8000
PROXY_PORT=8001
pids=()
cleanup() { for p in "${pids[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done; rm -rf "$WORK"; }
trap cleanup EXIT

git clone --quiet https://github.com/http-tests/cache-tests "$WORK/suite"
git -C "$WORK/suite" checkout --quiet "$CACHE_TESTS_REF"
(cd "$WORK/suite" && npm ci --no-audit --no-fund --silent 2>/dev/null || npm install --no-audit --no-fund --silent)
(cd "$ROOT" && go build -o "$WORK/weirproxy" ./examples/weirproxy)

(cd "$WORK/suite" && npm run --silent server --port="$ORIGIN_PORT" >"$WORK/server.log" 2>&1) &
pids+=($!)
# The suite's headers (Req-Num, Test-ID, Test-Name) and the request headers its
# Vary tests set are unkeyed, so strict forwarding (D4) would hide them.
"$WORK/weirproxy" -listen "127.0.0.1:$PROXY_PORT" -origin "http://127.0.0.1:$ORIGIN_PORT" \
  -forward-allow Req-Num,Test-ID,Test-Name,Foo,Bar,Baz,Abc >"$WORK/proxy.log" 2>&1 &
pids+=($!)
wait_for() {
  for _ in $(seq 100); do
    curl -sS -o /dev/null "http://127.0.0.1:$1/" 2>/dev/null && return 0
    sleep 0.2
  done
  echo "nothing answered on port $1" >&2
  cat "$WORK/server.log" "$WORK/proxy.log" >&2
  return 1
}
wait_for "$ORIGIN_PORT"
wait_for "$PROXY_PORT"

run() {
  (cd "$WORK/suite" && npm run --silent cli --base="http://127.0.0.1:$PROXY_PORT" >"$1" 2>"$WORK/cli.err") || { cat "$WORK/cli.err" >&2; return 1; }
}

# Result values are true or [kind, detail]; the detail carries timestamps, so
# the baseline keeps only pass or fail.
summarize() {
  node -e '
    const r = JSON.parse(require("fs").readFileSync(process.argv[1]))
    const out = {}
    for (const id of Object.keys(r).sort()) out[id] = r[id] === true ? "pass" : "fail"
    process.stdout.write(JSON.stringify({ ref: process.argv[2], results: out }, null, 2) + "\n")
  ' "$1" "$CACHE_TESTS_REF"
}

regressions() {
  node -e '
    const base = JSON.parse(require("fs").readFileSync(process.argv[1])).results
    const now = JSON.parse(require("fs").readFileSync(process.argv[2])).results
    const bad = Object.keys(base).filter(id => base[id] === "pass" && now[id] !== "pass")
    const fresh = Object.keys(now).filter(id => !(id in base))
    if (fresh.length) console.error("not in baseline (new upstream tests): " + fresh.join(" "))
    for (const id of bad) console.log(id)
  ' "$BASELINE" "$1"
}

run "$WORK/raw.json"
summarize "$WORK/raw.json" >"$WORK/now.json"

if [ "${UPDATE:-}" = 1 ]; then
  cp "$WORK/now.json" "$BASELINE"
  echo "baseline updated: $(grep -c '"pass"' "$BASELINE") pass, $(grep -c '"fail"' "$BASELINE") fail"
  exit 0
fi

bad="$(regressions "$WORK/now.json")"
if [ -n "$bad" ]; then
  # Some tests depend on short real-time waits; confirm once before failing.
  echo "possible regressions, rerunning once: $(echo $bad)"
  run "$WORK/raw2.json"
  summarize "$WORK/raw2.json" >"$WORK/now2.json"
  bad="$(regressions "$WORK/now2.json")"
fi
if [ -n "$bad" ]; then
  echo "cache-tests regressions (passed in the baseline, fail now):"
  echo "$bad" | sed 's/^/  /'
  echo "run one with: npm run cli --base=http://127.0.0.1:$PROXY_PORT --id=<id>"
  exit 1
fi
echo "cache-tests: no regressions against $(basename "$BASELINE")"
