---
title: Errors
description: The errors Serve returns, the HTTP status each one maps to, and what a client sees.
---

`Engine.Serve` returns either a response or an error. Errors are sentinel values wrapped with `%w`, so compare with `errors.Is`. Two helpers do the mapping for adapters: `weir.StatusCode(err)` gives the HTTP status, and `weir.RetryAfter(err)` gives a retry hint when there is one. `weirhttp.WriteError` uses both.

## What clients see

{{include docs/runbook.md "## 6. Errors clients see"}}

## Sentinels

{{errors_table}}

This table is read from `errors.go` at build time. `*OriginError` wraps the transport error from the origin and matches `ErrOrigin`. `*RetryError` carries `ErrShed` or `ErrCircuitOpen` with the retry hint.

An origin that answers with its own 5xx is not an error from `Serve`: the response is passed through, or replaced by a stale entry when stale-if-error allows.

## Messages

Error messages start with `weir:` (or `store:` from the store package), are lowercase and have no trailing punctuation. They are for logs. Match on the sentinel, not the text.
