#!/usr/bin/env bash
# Regenerates the curl transcript used on the home page and in the README:
# a throwaway origin on :9000, weirproxy on :8080, two requests WAIT seconds apart.
# Copy the output into site/data/transcript.ts and README.md by hand; ttl varies with jitter.
set -euo pipefail
cd "$(dirname "$0")/../.."
tmp=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$tmp"' EXIT
cat > "$tmp/main.go" <<'GO'
package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {
	log.Fatal(http.ListenAndServe("127.0.0.1:9000", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Println("origin call", r.URL.Path)
		w.Header().Set("Cache-Control", "max-age=60")
		fmt.Fprintln(w, "hello from the origin")
	})))
}
GO
# Built, not `go run`: the trap can then stop the servers themselves.
(cd "$tmp" && go mod init origin >/dev/null 2>&1 && go build -o origin .)
go build -o "$tmp/weirproxy" ./examples/weirproxy
"$tmp/origin" 2> "$tmp/origin.log" &
"$tmp/weirproxy" -listen 127.0.0.1:8080 -origin http://127.0.0.1:9000 2>/dev/null &
for _ in $(seq 30); do curl -s -o /dev/null http://127.0.0.1:8080/probe && break; sleep 1; done
echo '$ curl -si localhost:8080/'; curl -si http://127.0.0.1:8080/
sleep "${WAIT:-13}"
echo '$ curl -si localhost:8080/'; curl -si http://127.0.0.1:8080/
echo '--- 50 concurrent requests for a cold key'
seq 50 | xargs -P 50 -I{} curl -s -o /dev/null http://127.0.0.1:8080/cold
echo "origin calls for /cold: $(grep -c 'origin call /cold' "$tmp/origin.log")"
