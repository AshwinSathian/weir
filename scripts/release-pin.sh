#!/usr/bin/env bash
# Pin a sub-module to released versions of its siblings and drop the local
# replace directives, so the module builds for importers (a replace is
# ignored outside the main module). Run it after the tags it names exist.
#
#   scripts/release-pin.sh VERSION MODULE_DIR [SIBLING_DIR...]
#
# Example, after tagging the root v1.0.0:
#   scripts/release-pin.sh v1.0.0 observe/prom
#   scripts/release-pin.sh v1.0.0 store/valkey
# After tagging observe/prom/v1.0.0 and store/valkey/v1.0.0:
#   scripts/release-pin.sh v1.0.0 caddy observe/prom store/valkey
set -euo pipefail

if [ "$#" -lt 2 ]; then
  sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi
version=$1
dir=$2
shift 2
root=github.com/AshwinSathian/weir
cd "$(dirname "$0")/.."
test -f "$dir/go.mod" || { echo "no go.mod in $dir" >&2; exit 1; }

cd "$dir"
for mod in "" "$@"; do
  path=$root${mod:+/$mod}
  GOWORK=off go mod edit -require="$path@$version" -dropreplace="$path"
done
# Resolve against the proxy: this is what an importer does.
GOWORK=off GOFLAGS=-mod=mod go mod tidy
GOWORK=off go vet ./...
echo "pinned $dir to $version"
