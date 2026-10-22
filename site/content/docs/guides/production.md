---
title: Production checklist
description: What to decide before Weir takes real traffic, and what a first week should look like.
---

This page is the operator's runbook, rendered from the repository so it cannot drift. The full document, including multi-node Valkey deployment, is [docs/runbook.md](https://github.com/AshwinSathian/weir/blob/main/docs/runbook.md).

## Before the first deployment

{{include docs/runbook.md "## 1. Before the first deployment"}}

## Alerts worth having

{{include docs/runbook.md "## 3. Alerts worth having"}}

## Planned operations

{{include docs/runbook.md "## 5. Planned operations"}}

## Toolchain

Build with Go {{go_deploy}} or later (or 1.26.10 or later on Go 1.26). Earlier patches carry standard library vulnerabilities in HTTP/2, `net/textproto`, `crypto/tls` and `crypto/x509` that `govulncheck` reports against the code paths `weirhttp` uses. The minimum the module accepts is Go {{go_min}}, and CI tests Go 1.26 and 1.27.

## Settings to leave alone until you have a reason

The defaults are chosen so that a mistake fails toward the origin doing more work, never toward a user seeing someone else's response. The settings that reverse one of those choices are on the [tuning page](/docs/tuning/), each with what you give up.
