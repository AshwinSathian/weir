#!/usr/bin/env bash
# Runs http-tests/cache-tests, at the revision scripts/cache-tests.sh pins,
# against each subject and tier (docs/11 §5.4). The suite's own origin server
# takes the name "origin" on the Compose network, so every subject's config is
# unchanged, except Weir's, which must let the suite's request headers through
# (subjects/caddy-weir/conformance/<tier>).
#   ./conformance.sh
#   SUBJECTS="varnish nginx" TIERS=default ./conformance.sh
# Results go to results/, which is not committed.
set -euo pipefail
cd "$(dirname "$0")"
. ./lib.sh

REF="$(sed -n 's/^CACHE_TESTS_REF="${CACHE_TESTS_REF:-\([0-9a-f]*\)}"$/\1/p' ../../scripts/cache-tests.sh)"
[ -n "$REF" ] || { echo "cannot read CACHE_TESTS_REF from scripts/cache-tests.sh" >&2; exit 1; }
SUBJECTS="${SUBJECTS:-weirproxy caddy-weir caddy-souin varnish nginx}"
TIERS="${TIERS:-default tuned hardened}"
ALLOW="Req-Num,Test-ID,Test-Name,Foo,Bar,Baz,Abc"
stamp="conformance-$(date -u +%Y%m%dT%H%M%SZ)"
out="results/$stamp"
mkdir -p "$out" results/_suite
NODE="node:24-alpine"

if [ ! -d results/_suite/.git ]; then git clone --quiet https://github.com/http-tests/cache-tests results/_suite; fi
git -C results/_suite fetch --quiet origin && git -C results/_suite checkout --quiet "$REF"
docker run --rm -v "$PWD/results/_suite:/suite" -w /suite "$NODE" sh -c 'npm ci --no-audit --no-fund --silent 2>/dev/null || npm install --no-audit --no-fund --silent'

cleanup() {
  docker rm -f ct-origin ct-subject >/dev/null 2>&1 || true
  docker compose --profile tools down --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT
build_next "$stamp"
docker compose --profile tools build >/dev/null
record_env "$stamp"
# A network for the subjects, without the harness origin: the suite's server
# is "origin" here.
docker compose up -d --no-deps caddy-nocache >/dev/null && docker compose rm -sf caddy-nocache >/dev/null
net="$(docker network ls --format '{{.Name}}' | grep '^compare_default$')"
docker run -d --name ct-origin --network "$net" --network-alias origin -v "$PWD/results/_suite:/suite" -w /suite "$NODE" \
  npm run --silent server --port=9000 >/dev/null

for tier in $TIERS; do
  for s in $SUBJECTS; do
    base="${s%-next}"
    if [ "$tier" != default ] && [ ! -d "subjects/$base/$tier" ]; then continue; fi
    host="$s"
    case "$s" in
      weirproxy)      docker compose run -d --no-deps --name ct-subject weirproxy -listen :8080 -origin http://origin:9000 -forward-allow "$ALLOW" >/dev/null; host=ct-subject ;;
      weirproxy-next) docker compose run -d --no-deps --name ct-subject weirproxy-next /weirproxy -listen :8080 -origin http://origin:9000 -forward-allow "$ALLOW" >/dev/null; host=ct-subject ;;
      caddy-weir*)    TIER="conformance/$tier" docker compose up -d --no-deps --force-recreate "$s" >/dev/null ;;
      *)              TIER="$tier" docker compose up -d --no-deps --force-recreate "$s" >/dev/null ;;
    esac
    echo "== $s ($tier)" >&2
    # Wait for the subject to answer anything at all, at most 30 s.
    docker run --rm --network "$net" "$NODE" sh -c "for i in \$(seq 60); do wget -q -O /dev/null -T 2 http://$host:8080/ 2>/dev/null; [ \$? -ne 4 ] && exit 0; sleep 0.5; done; exit 1" \
      || echo "!! $s ($tier) did not answer" >&2
    docker run --rm --network "$net" -v "$PWD/results/_suite:/suite" -w /suite "$NODE" \
      npm run --silent cli --base="http://$host:8080" > "$out/$s.$tier.json" 2> "$out/$s.$tier.err" || echo "!! $s ($tier) failed" >&2
    docker rm -f ct-subject >/dev/null 2>&1 || true
    docker compose rm -sfv "$s" >/dev/null 2>&1 || true
  done
done

# Per subject and tier: tests passed, and the rest split by what the suite
# says went wrong. "Setup" means the test could not reach its assertion (a
# request that should have been conditional was not, for example), which is
# not a statement about the behavior under test. The last column compares a
# Weir default row with the project's baseline, test for test.
docker run --rm -v "$PWD/$out:/r:ro" -v "$PWD/../../testdata/cache-tests-baseline.json:/baseline.json:ro" "$NODE" node -e '
  const fs = require("fs")
  const base = JSON.parse(fs.readFileSync("/baseline.json")).results
  console.log("| subject | tier | tests | pass | assertion failed | setup failed | other | against Weir baseline |")
  console.log("|---|---|---:|---:|---:|---:|---:|---|")
  for (const f of fs.readdirSync("/r").filter(f => f.endsWith(".json")).sort()) {
    const [s, tier] = f.slice(0, -5).split(".")
    let r
    try { r = JSON.parse(fs.readFileSync("/r/" + f)) } catch (e) { console.log(`| ${s} | ${tier} | the suite produced no result | | | | | |`); continue }
    const n = { pass: 0, Assertion: 0, Setup: 0, other: 0 }
    let diff = 0
    for (const [id, v] of Object.entries(r)) {
      const kind = v === true ? "pass" : (Array.isArray(v) && (v[0] === "Assertion" || v[0] === "Setup") ? v[0] : "other")
      n[kind]++
      if ((base[id] === "pass") !== (v === true)) diff++
    }
    for (const id of Object.keys(base)) if (!(id in r)) diff++
    const cmp = s.includes("weir") && tier === "default" ? (diff === 0 ? "equal" : diff + " tests differ") : ""
    console.log(`| ${s} | ${tier} | ${Object.keys(r).length} | ${n.pass} | ${n.Assertion} | ${n.Setup} | ${n.other} | ${cmp} |`)
  }
' | tee "$out/summary.md"
