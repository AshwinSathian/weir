---
title: Conformance
description: Results from the cache-tests suite. 284 of 365 tests pass, and each of the other 81 has a written reason.
wide: true
source: docs/cache-tests-expected-failures.md
---

"RFC compliant" is easy to write. This page is what is behind it for Weir: the [http-tests/cache-tests](https://github.com/http-tests/cache-tests) suite, run against `examples/weirproxy` with default settings.

| | |
|---|---:|
| Tests in the suite (commit `d644cf4`) | 365 |
| Pass | 284 |
| Fail, each explained below | 81 |

The suite's authors say that passing everything means nothing: many tests check optional behavior, and some check behavior a careful shared cache should refuse. Most of Weir's 81 are deliberate. Weir ignores client `no-cache` by default, refuses to store `Set-Cookie` responses, does not store 206 responses, and treats a malformed `max-age` as zero. Each row below gives the reason and the decision or requirement behind it.

A nightly job reruns the suite and fails only when a test that passed in this baseline stops passing. Reproduce it with `make cache-tests`.

## RFCs implemented

| RFC | Subject |
|---|---|
| 9111 | HTTP caching (the shared-cache half) |
| 9110 | HTTP semantics: conditional requests, ranges, `Vary` |
| 9211 | the `Cache-Status` header |
| 9213 | targeted cache-control (`CDN-Cache-Control`) |
| 9875 | cache groups and group invalidation |
| 5861 | `stale-while-revalidate` and `stale-if-error` |

The requirement-by-requirement mapping is in [section 8 of the technical specification](https://github.com/AshwinSathian/weir/blob/main/docs/01-technical-spec.md#8-rfc-conformance-summary).

## The 81 expected failures

Rendered from [docs/cache-tests-expected-failures.md](https://github.com/AshwinSathian/weir/blob/main/docs/cache-tests-expected-failures.md). The reasons were read off the requirements and the suite's request logs. They were not each traced through the code.

<div class="wide-table">

{{include docs/cache-tests-expected-failures.md "## By design" heading}}

{{include docs/cache-tests-expected-failures.md "## Targeted fields and partial content" heading}}

</div>
