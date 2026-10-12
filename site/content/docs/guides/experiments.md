---
title: Experiments
description: A/B bucketing inside the cache key, with a signed cookie, a consent gate and no external service.
---

Caching and A/B tests fight each other. If the origin picks the variant, either every response is uncacheable or the cache serves variant A to someone assigned B. Weir assigns the visitor before the cache lookup and makes the assignment part of the key, so each variant is cached separately and the origin just renders what it is told.

Experiments are off unless you configure them.

## How a request is handled

1. Weir matches the request's host and path against your experiments. Both are already key inputs, so matching adds nothing unkeyed.
2. It reads its own first-party cookie (`weir_v` by default). The cookie is HMAC-signed; a missing or invalid one means "no assignment".
3. For each matching experiment the visitor keeps an existing assignment, or is assigned by hashing the visitor ID with the experiment's salt.
4. The assignment goes into the cache key and into a request header for the origin: `Weir-Variant: checkout-copy=b`. A client-supplied `Weir-Variant` is always deleted first.
5. If the assignment is new, the response to this client, and only this client, carries the `Set-Cookie`. The stored entry never contains it.
6. An exposure event is emitted once per visitor per experiment, with a hash prefix of the visitor ID.

## Caddyfile

```text
weir {
	name site-a
	experiments {
		signing_keys {env.WEIR_EXP_KEY} {env.WEIR_EXP_KEY_PREV}
		consent      cookie cmp_consent analytics=yes
		experiment checkout-copy {
			match    path /checkout/*
			salt     2026-10-a
			variants control=50 b=50
		}
	}
}
```

The first variant listed is the control. `signing_keys` takes 1 to 4 keys of 32 to 256 bytes; the first signs and all verify, which is how you rotate. To end an experiment, set `winner` to a variant and everyone gets it. In Go the same shape is `Config.Experiments`.

## Consent

With a `consent` rule, a visitor without the named consent signal gets the control variant, no cookie and no exposure record. Without a rule, Weir buckets everyone and logs one warning at start. Whether you need consent for a first-party assignment cookie depends on your jurisdiction and your lawyers, not on this page.

The cookie is `HttpOnly`, `SameSite=Lax`, `Secure` on https, and lasts 90 days. On a domain with subdomains you do not control, give it a `__Host-` name (see Limits). Weir does no fingerprinting.

## Caches in front of Weir

A CDN in front of Weir does not know about variants. So on experiment-matched requests Weir rewrites the outgoing `Cache-Control` to `private` and drops `s-maxage` and `CDN-Cache-Control`, while its own stored copy keeps the origin's directives. If your CDN does key on the variant, say so with `downstream_keys_on_variant`.

## Limits

> [!LIMITS]
> Matchers are host and path only. Experiment changes arrive by config reload. A visitor's assignments can be dropped, and re-drawn, if the cookie would exceed 512 bytes. `Engine.Warm` stores only the control variant (or the winner). Cookie tossing is only partly covered (T-50, R-10). A cookie that appears twice with different values counts as no assignment, so a planted second copy forces a re-draw and nothing more. But a visitor who has no cookie yet accepts a validly signed one that a sibling subdomain planted, which lets the attacker pick that visitor's variant. If any subdomain of your domain is outside your control, name the cookie with a `__Host-` prefix (`cookie __Host-weir_v`); that needs https. Signing keys are plain bytes in the config, so keep them as `{env.NAME}` placeholders and do not log the config (R-9).
