# Weir experiment dimensions specification (draft)

Status: draft v0.5. Phase 3. Decisions below were made with the project owner on 2026-09-27 and are locked; mechanics are finalized when Phase 3 starts.
Date: 2026-09-27
Depends on: [01-technical-spec.md](01-technical-spec.md), [02-architecture.md §7](02-architecture.md), [06-threat-model.md](06-threat-model.md)
Seed: [00-design-doc.md §10](00-design-doc.md) (titled "Phase 2" there; it is Phase 3, see [09 §6](09-research-notes.md))

## 1. What it does

Before the cache lookup, Weir assigns the visitor to a variant of every experiment whose matcher covers the request. Each assignment becomes a key dimension and a forwarded request header, so cached responses are partitioned by variant and the origin renders the right one without doing any bucketing itself. The pattern is the one Statsig and GrowthBook document for CDN edge functions, done inside the proxy with no external service.

## 2. Locked decisions

| ID | Decision |
|---|---|
| E1 | Bucketing identifier: a first-party visitor cookie set by Weir (`HttpOnly`, `Secure` on https, `SameSite=Lax`, `Path=/`). No fingerprinting. |
| E2 | The cookie is HMAC-signed. A bad or missing signature means "no assignment" and the visitor is reassigned. |
| E3 | Assignments are sticky: the cookie records each experiment's assigned variant; weight changes affect only visitors without an assignment. |
| E4 | The variant is a key dimension and is forwarded as a request header. Forwarded request equals keyed request (P2). |
| E5 | Experiments are static configuration; changes arrive through a config reload. |
| E6 | Consent gate: the operator can name a consent signal. When configured and absent, the visitor gets the control variant, no cookie is set, and no exposure is recorded. When no consent signal is configured, Weir buckets normally and logs a one-time warning at start. |
| E7 | Responses on experiment-matched requests are rewritten for downstream caches to `Cache-Control: private` (dropping `public` and `s-maxage`) and lose `CDN-Cache-Control`, unless the operator declares that downstream caches key on the variant. Weir's own stored copy keeps the origin's directives. |

## 3. Configuration shape

```
experiments {
	cookie         weir_v            # cookie name
	signing_keys   {env.WEIR_EXP_KEY} {env.WEIR_EXP_KEY_PREV}   # first signs, all verify
	consent        cookie cmp_consent analytics=yes   # optional (E6)
	header         Weir-Variant      # forwarded header name
	max_active     4                 # experiments that may apply to one request

	experiment checkout-copy {
		match    path /checkout*
		salt     2026-10-a
		variants control=50 b=50
		winner   ""                  # set to a variant to end the experiment (all visitors get it)
	}
}
```

## 4. Mechanics

1. Match experiments against the validated request (path matchers only in the first version). More than `max_active` matches is a configuration error caught at load, not a runtime condition, when the matchers overlap statically; at runtime the first `max_active` in config order apply.
2. Read and verify the cookie. The value is `v1.<visitor-id>.<assignments>.<mac>`, with assignments as `exp:variant` pairs; MAC is HMAC-SHA-256 over the rest with the first key, verified against every configured key (rotation).
3. For each matching experiment: `winner` set means that variant; an existing assignment with a variant that still exists is kept (E3); otherwise, if consent allows, `variant = pick(weights, SHA-256(salt || visitor-id))`, else control.
4. Append dimensions `(experiment, variant)` in config order to the primary key (new tag bytes in the key encoding) and set the forwarded header `Weir-Variant: exp=variant;exp2=variant` after deleting any client-supplied value in every forwarding mode.
5. Serve normally. On the way out, if an assignment was created or changed, add `Set-Cookie` to the response given to this client only. The stored entry never contains it (the engine adds it after the entry is built).
6. Apply E7 to the outgoing response.
7. Emit `EvExposure{experiment, variant}` for every served response on a matched request, hits included, so analytics do not depend on the origin seeing the request.

## 5. Adversarial notes

- Variant self-selection: blocked by E2. Replaying another visitor's signed cookie gives their assignment, which is harmless.
- Header spoofing: the client's `Weir-Variant` is always deleted before Weir sets it, including in `ForwardAll` mode (T-35).
- Cache fragmentation: key cardinality per URL is the product of variant counts of the active experiments. `max_active` bounds it; config load rejects combinations whose product exceeds `Key.MaxVariants × 4`.
- Downstream mixing and cookie caching by a CDN in front: E7 (T-36).
- Stale across variants: impossible by construction, since stale entries live under variant keys (the seed's concern in §10).
- Config reload: experiments change keys only on matched paths, so they are excluded from the Caddy adapter's key-generation hash and do not trigger a global soft purge ([08 §3](08-caddy-adapter-spec.md)).
- Crawlers without cookies get a fresh assignment per request and a `Set-Cookie` they ignore; responses they receive are still cache hits for their variant.
- Ending an experiment: set `winner`; existing sticky assignments are overridden. Remove the experiment in a later reload once traffic has moved.

## 6. Open items for Phase 3 kickoff

- Matchers beyond path (host, header presence).
- Cookie size limit with many historical assignments: prune assignments for experiments no longer configured on every rewrite.
- Whether the exposure event needs sampling for high-traffic sites.
