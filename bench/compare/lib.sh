# Shared by run.sh, probes.sh and conformance.sh; sourced, not run.

# build_next <stamp>: when $SUBJECTS names a "-next" subject, build Weir from
# the working tree into results/bin (the Docker build context is this
# directory only) and record the commit in results/<stamp>.next.txt. Every
# driver calls it, so a "-next" result always comes from binaries built for
# that run and always has its commit beside it (docs/11 §2).
build_next() {
  case " $SUBJECTS " in *-next*) ;; *) return 0 ;; esac
  local root
  root="$(cd ../.. && pwd)"
  mkdir -p results/bin
  (cd "$root" && GOOS=linux CGO_ENABLED=0 go build -o bench/compare/results/bin/weirproxy ./examples/weirproxy)
  (cd "${TMPDIR:-/tmp}" && GOWORK=off GOOS=linux CGO_ENABLED=0 PATH="$PATH:$HOME/go/bin" xcaddy build v2.11.7 \
    --with "github.com/AshwinSathian/weir=$root" --with "github.com/AshwinSathian/weir/caddy=$root/caddy" \
    --with "github.com/AshwinSathian/weir/observe/prom=$root/observe/prom" \
    --with "github.com/AshwinSathian/weir/store/valkey=$root/store/valkey" \
    --output "$root/bench/compare/results/bin/caddy" >/dev/null 2>&1)
  { git -C "$root" rev-parse HEAD; git -C "$root" status --porcelain -- . ':!bench/compare/results'; } > "results/$1.next.txt"
}

# record_env <stamp>: what ran, beside the results: the harness commit (and
# whether the tree was clean), Compose and the images.
record_env() {
  {
    echo "harness commit: $(git rev-parse HEAD) $(git status --porcelain -- . | grep -q . && echo '(uncommitted changes in bench/compare)' || echo '(clean)')"
    echo "weir version: ${WEIR_VERSION:-v1.1.0}"
    docker compose version
    docker compose --profile tools images
  } > "results/$1.env.txt"
}
