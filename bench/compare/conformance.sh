#!/usr/bin/env bash
# Runs http-tests/cache-tests, at the revision scripts/cache-tests.sh pins,
# against each subject (docs/11 §5.4). The suite's own origin server takes the
# name "origin" on the Compose network, so every subject's config is unchanged.
#   ./conformance.sh
#   SUBJECTS="varnish nginx" ./conformance.sh
# Results go to results/, which is not committed.
set -euo pipefail
cd "$(dirname "$0")"

REF="$(sed -n 's/^CACHE_TESTS_REF="${CACHE_TESTS_REF:-\([0-9a-f]*\)}"$/\1/p' ../../scripts/cache-tests.sh)"
[ -n "$REF" ] || { echo "cannot read CACHE_TESTS_REF from scripts/cache-tests.sh" >&2; exit 1; }
SUBJECTS="${SUBJECTS:-weirproxy weirproxy-next caddy-weir caddy-weir-next caddy-souin varnish nginx}"
ALLOW="Req-Num,Test-ID,Test-Name,Foo,Bar,Baz,Abc"
out="results/conformance-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$out" results/_suite
NODE="node:24-alpine"

if [ ! -d results/_suite/.git ]; then git clone --quiet https://github.com/http-tests/cache-tests results/_suite; fi
git -C results/_suite fetch --quiet origin && git -C results/_suite checkout --quiet "$REF"
docker run --rm -v "$PWD/results/_suite:/suite" -w /suite "$NODE" sh -c 'npm ci --no-audit --no-fund --silent 2>/dev/null || npm install --no-audit --no-fund --silent'

cleanup() {
  docker rm -f ct-origin >/dev/null 2>&1 || true
  docker compose --profile tools down --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker compose --profile tools build >/dev/null
# A network for the subjects, without the harness origin: the suite's server
# is "origin" here.
docker compose up -d --no-deps caddy-nocache >/dev/null && docker compose rm -sf caddy-nocache >/dev/null
net="$(docker network ls --format '{{.Name}}' | grep '^compare_default$')"
docker run -d --name ct-origin --network "$net" --network-alias origin -v "$PWD/results/_suite:/suite" -w /suite "$NODE" \
  npm run --silent server --port=9000 >/dev/null

for s in $SUBJECTS; do
  case "$s" in
    weirproxy)      docker compose run -d --no-deps --name ct-subject weirproxy -listen :8080 -origin http://origin:9000 -forward-allow "$ALLOW" >/dev/null; host=ct-subject ;;
    weirproxy-next) docker compose run -d --no-deps --name ct-subject weirproxy-next /weirproxy -listen :8080 -origin http://origin:9000 -forward-allow "$ALLOW" >/dev/null; host=ct-subject ;;
    caddy-weir*)    TIER=conformance docker compose up -d --no-deps --force-recreate "$s" >/dev/null; host="$s" ;;
    *)              TIER=default docker compose up -d --no-deps --force-recreate "$s" >/dev/null; host="$s" ;;
  esac
  echo "== $s" >&2
  sleep 3
  docker run --rm --network "$net" -v "$PWD/results/_suite:/suite" -w /suite "$NODE" \
    npm run --silent cli --base="http://$host:8080" > "$out/$s.json" 2> "$out/$s.err" || echo "!! $s failed" >&2
  docker rm -f ct-subject >/dev/null 2>&1 || true
  docker compose rm -sf "$s" >/dev/null 2>&1 || true
done

# Pass and fail per subject, and per test-id prefix.
node -e '
  const fs = require("fs"), dir = process.argv[1]
  const subjects = fs.readdirSync(dir).filter(f => f.endsWith(".json")).map(f => f.slice(0, -5))
  const res = {}, cats = new Set()
  for (const s of subjects) {
    let r = {}
    try { r = JSON.parse(fs.readFileSync(dir + "/" + s + ".json")) } catch (e) { continue }
    res[s] = {}
    for (const id of Object.keys(r)) {
      const c = id.split("-")[0]; cats.add(c)
      res[s][c] = res[s][c] || [0, 0]
      res[s][c][r[id] === true ? 0 : 1]++
    }
  }
  const cs = [...cats].sort()
  console.log("| subject | pass | fail | " + cs.join(" | ") + " |")
  console.log("|---|---:|---:|" + cs.map(() => "---:").join("|") + "|")
  for (const s of Object.keys(res)) {
    let p = 0, f = 0
    for (const c of cs) { const v = res[s][c] || [0, 0]; p += v[0]; f += v[1] }
    console.log("| " + s + " | " + p + " | " + f + " | " + cs.map(c => { const v = res[s][c] || [0, 0]; return v[0] + "/" + (v[0] + v[1]) }).join(" | ") + " |")
  }
' "$out" | tee "$out/summary.md"
