---
title: Observability
description: The Cache-Status header, Prometheus metrics, events, and which signals deserve an alert.
---

Weir reports every decision three ways: a header on the response, an event to an observer, and (with the `observe/prom` module) Prometheus metrics.

## What to watch

{{include docs/runbook.md "## 2. What to watch"}}

On Caddy every `weir_*` series also carries a `name` label, the site's `name`, and the metrics are served by Caddy's own `metrics` handler.

## Alerts

{{include docs/runbook.md "## 3. Alerts worth having"}}

As Prometheus rules, the first two look like this. Thresholds are starting points:

```yaml
- alert: WeirBreakerOpen
  expr: weir_breaker_state == 2
  for: 3m
- alert: WeirShedding
  expr: rate(weir_shed_total[1m]) > 0
  for: 1m
```

## Events without Prometheus

`Config.Observer` receives one `weir.Event` per decision. It is called synchronously on the request path, so it must be fast, must not block and must not panic. `Engine.Stats()` returns in-flight fetches, queue depth, breaker state and store bytes for gauges.

Event kinds may gain values in a minor release. Switch on them with a default case.

## Limits

> [!LIMITS]
> The `Cache-Status` header never includes the cache key (T-27). Request-derived strings such as the partition are never metric labels, so a flood of distinct paths cannot explode your series count; the cost is that metrics do not tell you which path is misbehaving. The miss-rate warning in the log does.
