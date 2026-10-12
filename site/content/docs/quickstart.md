---
title: Quickstart
description: Five minutes from nothing to a cache hit, with the output to expect at each step.
---

You need Go {{go_min}} or later and an origin to put Weir in front of. If you have no origin handy, step 1 gives you one. Pick the way you want to run Weir; the steps after that are the same.

## 1. An origin to cache

Any HTTP server that sends `Cache-Control` will do. This one answers every path with a one-minute lifetime and logs each call, so you can see when Weir asks it and when it does not.

```go
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
```

Save it as `origin/main.go` and run `go run ./origin`.

## 2. Put Weir in front

<div class="tabs" data-tabs="Binary,Go,Caddy" data-group="run">
<section>

`weirproxy` is a reverse proxy built from Weir with default settings. Nothing to write:

```sh
go run github.com/AshwinSathian/weir/examples/weirproxy@{{version}} -listen :8080 -origin http://127.0.0.1:9000
```

It logs one line and waits:

```console
weirproxy: :8080 -> http://127.0.0.1:9000
```

</section>
<section>

In a new module, `go get github.com/AshwinSathian/weir@{{version}}`, then:

{{fenced README.md go}}

</section>
<section>

Build Caddy with the module. Name the root module too: the Caddy module is tested only with the root version it was released with.

```sh
xcaddy build --with github.com/AshwinSathian/weir@{{version}} --with github.com/AshwinSathian/weir/caddy@{{version}}
./caddy list-modules | grep weir
```

The second command should print `http.handlers.weir`. Then a Caddyfile:

```text
:8080 {
	weir {
		name quickstart
	}
	reverse_proxy 127.0.0.1:9000
}
```

Run `./caddy run`. `name` is the only required setting. It names the store across reloads, the metrics label and the admin URL, so pick it once.

</section>
</div>

## 3. Ask twice

```console
$ curl -si localhost:8080/
HTTP/1.1 200 OK
Cache-Control: max-age=60
Cache-Status: Weir; fwd=uri-miss; fwd-status=200; stored
Content-Length: 22
Content-Type: text/plain; charset=utf-8

hello from the origin
```

`fwd=uri-miss` says nothing was stored for this URL, so Weir forwarded the request. `stored` says the response is now in the cache. The origin's log shows one call. Ask again:

```console
$ curl -si localhost:8080/
HTTP/1.1 200 OK
Age: 13
Cache-Control: max-age=60
Cache-Status: Weir; hit; ttl=43
Content-Length: 22
Content-Type: text/plain; charset=utf-8

hello from the origin
```

`hit` means the origin was not asked, and its log shows no new line. `Age` is how long ago the entry was fetched. `ttl` is how many seconds of freshness remain.

Your `ttl` will not be exactly 60 minus `Age`. Here it is 43 where you might expect 47: Weir shortened this entry's lifetime by four seconds. That is jitter (FR-FRS-5), up to 10% by default, and it is the reason entries stored in the same second do not all expire in the same second. Every value the header can carry is in the [Cache-Status reference](/docs/reference/cache-status/).

## 4. See a stampede not happen

Ask for a URL nothing has cached yet, fifty times at once:

```sh
seq 50 | xargs -P 50 -I{} curl -s -o /dev/null localhost:8080/cold
```

The origin logs `origin call /cold` once. The other 49 requests joined the first one's fetch and got its response; their `Cache-Status` carries `collapsed`. That is [coalescing](/docs/concepts/coalescing/).

## 5. Before real traffic

Three things bite people who skip them:

1. **Strict forwarding.** On a cacheable request the origin receives only the headers that are part of the cache key, plus a short fixed list. If your pages depend on `Accept-Language` or a cookie, the origin stops seeing it until you [key it or allow it](/docs/concepts/cache-key/).
2. Set `GOMEMLIMIT`. The memory store takes 40% of it. Without it the store takes 256 MiB and logs a warning.
3. Weir bounds what reaches the origin. It does not rate-limit clients, so a flood of distinct paths still needs a rate limiter in front (R-2).

The [production checklist](/docs/guides/production/) has the rest.

## Where next

- [Request flow](/docs/concepts/request-flow/): what happens to a request, stage by stage.
- [net/http guide](/docs/guides/net-http/) or [Caddy guide](/docs/guides/caddy/): the adapter you chose, in detail.
- [Configuration reference](/docs/reference/config/): every field and its default.
