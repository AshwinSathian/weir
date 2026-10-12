---
title: Request flow
description: The nine stages every request passes through, and where each one can end early.
---

Every call to `Engine.Serve` runs the same stages in the same order. A stage can finish the request, and the first four often do.

{{figure request-flow}}

## The stages

| | Stage | What happens |
|---|---|---|
| 1 | Closed? | After `Close` starts, `Serve` returns `ErrClosed` (a 503) and touches nothing. |
| 2 | Validate and classify | The scheme, host, path and query are checked against fixed limits (FR-VAL-1). A request that fails is answered 400 with no store or origin cost, which is the point: malformed input must not become a cheap way to miss (T-13). The method decides the class: `GET` and `HEAD` are cacheable, everything else is not. |
| 3 | Unsafe or unknown method | `POST`, `PUT`, `DELETE` and the rest pass through to the origin with their body. They still hold a limiter slot, and a successful unsafe response invalidates the stored entry for that URL. |
| 4 | Bypass rule | A request matching `Bypass.Cookies` or `Bypass.Headers` (a session cookie, say) passes through with all its headers. `Cache-Status` shows `fwd=bypass`. |
| 5 | Build the forwarded request and the key | Weir rewrites the request (drops unkeyed headers and query parameters, normalizes what you asked it to) and computes the key from the rewritten form. From here on nothing reads the client's raw headers again. |
| 6 | Store lookup | First the primary key. If the stored response had a `Vary` header, the lookup finds a vary spec and does a second lookup under the variant key. |
| 7 | Epoch check | Purges are recorded as epochs, timestamps per URL, group, origin or everything. An entry older than an epoch that applies to it is stale (soft purge) or gone (hard purge). See [Purging](/docs/concepts/purging/). |
| 8 | Decide | A fresh entry is served, and may trigger an early refresh. A stale entry inside its stale-while-revalidate window is served while a background fetch renews it. A live negative entry answers with a synthesized error. Anything else becomes a coalesced fetch through the limiter and the breaker. |
| 9 | Record | The outcome goes to the miss-rate tracker and the observer, and into the `Cache-Status` header. |

Stages 2 and 5 are the security boundary. Everything downstream, including the store, the flight table and the origin, sees only the forwarded request (INV-1).

## One place calls the origin

Foreground misses, background refreshes, `Warm`, pass-through requests and validations all reach the origin through a single function. That is how one limiter and one breaker cover every path, and it is enforced by a test (`originrule_test.go`) that fails if a second call site appears.

## What the adapter does

Weir never opens a socket. An adapter turns the wire request into a `weir.Request`, calls `Serve`, and writes the `weir.Response` or the error back. `weirhttp` does this for `net/http`; the Caddy module does it inside Caddy's handler chain. Error-to-status mapping is in the [errors reference](/docs/reference/errors/).

## Limits

> [!LIMITS]
> Range requests are answered from a complete stored object when one is fresh; a range request that misses passes through and stores nothing. Multi-range requests get the full 200. `CONNECT` and protocol upgrades are not served by the engine: `weirhttp.Handler` answers 501 and `weirhttp.Middleware` hands them to the next handler.
