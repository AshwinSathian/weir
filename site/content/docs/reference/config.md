---
title: Configuration
description: Every field of weir.Config with its default and range, rendered from the technical specification.
---

Configuration is one Go struct, `weir.Config`. Every field is optional and the zero value is valid. This table is rendered at build time from [section 6 of the technical specification](https://github.com/AshwinSathian/weir/blob/main/docs/01-technical-spec.md#6-configuration), which is the normative source.

On Caddy the same settings are Caddyfile keys; the [Caddy guide](/docs/guides/caddy/#directive-reference) lists which ones the directive exposes.

<input class="filter" type="search" data-filter="#config" placeholder="Filter fields, for example: limiter" aria-label="Filter configuration fields">

<div id="config">

> [!NOTE]
> The table is the specification on `main`, which is ahead of `{{version}}` in three rows: `Limiter.AdaptiveMax` does not exist in `{{version}}`, and there `Breaker.OpenFor` defaults to 5 s and `Breaker.MaxOpenFor` to 60 s. The [changelog](/changelog/) lists what the next release changes.

{{include docs/01-technical-spec.md "## 6. Configuration"}}

</div>

## Reading the table

- A setting written `A / B` lists two fields with their defaults in the same order.
- "D" numbers such as D6 are recorded design decisions; "FR" numbers are requirements. Both link to the specification.
- Fields that reverse a protective default are explained on the [tuning page](/docs/tuning/).

`weir.New` validates the whole struct and returns an error wrapping `ErrInvalidConfig` for a value out of range or a combination that cannot work, such as a `Forward.Allow` entry that names a keyed header. It never silently corrects a value you set.
