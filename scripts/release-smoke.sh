#!/usr/bin/env bash
# Check published tags the way a user meets them (docs/release.md step 9):
# outside the repository, with an empty module cache and no go.work.
#
#   scripts/release-smoke.sh VERSION
#
# Example: scripts/release-smoke.sh v1.0.0
# It fetches the three library modules, runs their published tests from the
# module cache, installs weirproxy, and builds Caddy with the README's
# xcaddy line. Needs network and Go 1.27.3 or later. The runbook 7.8
# checklist and the pkg.go.dev pages stay manual.
set -euo pipefail

if [ "$#" -ne 1 ]; then
  sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi
v=$1
root=github.com/AshwinSathian/weir
work="$(mktemp -d)"
export GOWORK=off GOMODCACHE="$work/mod" GOBIN="$work/bin"
# chmod first: the module cache is read-only, and so is a toolchain that
# GOTOOLCHAIN downloads into it.
trap 'chmod -R u+w "$work" 2>/dev/null; rm -rf "$work"' EXIT

mkdir "$work/smoke"
cd "$work/smoke"
go mod init smoke >/dev/null 2>&1
# -t: the sub-modules' tests need modules their packages do not.
go get -t "$root@$v" "$root/observe/prom@$v" "$root/store/valkey@$v"
go test -count=1 "$root/..." "$root/observe/prom/..." "$root/store/valkey/..."
go install "$root/examples/weirproxy@$v"
go version -m "$GOBIN/weirproxy" | grep -F "$root	$v"

go install github.com/caddyserver/xcaddy/cmd/xcaddy@v0.4.5
"$GOBIN/xcaddy" build --with "$root@$v" --with "$root/caddy@$v" --output "$work/caddy" >"$work/xcaddy.log" 2>&1 ||
  { cat "$work/xcaddy.log" >&2; exit 1; }
"$work/caddy" list-modules --versions | grep weir | tee "$work/modules.txt"
test "$(grep -c "weir $v" "$work/modules.txt")" = 2
echo "release-smoke: $v passes"
