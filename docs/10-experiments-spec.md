# Weir experiment dimensions specification

Status: v1.4. Phase 3. E1 to E7 were decided with the project owner on 2026-09-27; E8 and E9 on 2026-10-11 (P3-00). All are locked. v1.4 (P3-03, approved by the project owner) adds `DownstreamKeysOnVariant` (E7) and the cookie lifetime and rewrite rules to §3 and §4. v1.3 (P3-02 review) adds the Warm and Purge rules to §4, the consent-value limits and the matcher and header-name rules to §3. v1.2 (P3-02) records the public config shape, the defaults and the consent rule in §3. v1.1 (P3-01) adds the clarifications in §3 and §4 (control variant, load-time limits, cookie grammar, hash separator) that the first implementation needed.
Date: 2026-10-11
Depends on: [01-technical-spec.md](01-technical-spec.md), [02-architecture.md §7](02-architecture.md), [06-threat-model.md](06-threat-model.md)
Seed: [00-design-doc.md §10](00-design-doc.md) (titled "Phase 2" there; it is Phase 3, see [09 §6](09-research-notes.md))

## 1. What it does

Before the cache lookup, Weir assigns the visitor to a variant of every experiment whose matcher covers the request. Each assignment becomes a key dimension and a forwarded request header, so cached responses are partitioned by variant and the origin renders the right one without doing any bucketing itself. The pattern is the one Statsig and GrowthBook document for CDN edge functions, done inside the proxy with no external service.

## 2. Locked decisions

| ID | Decision |
|---|---|
| E1 | Bucketing identifier: a first-party visitor cookie set by Weir (`HttpOnly`, `Secure` on https, `SameSite=Lax`, `Path=/`). No fingerprinting. |
| E2 | The cookie is HMAC-signed. A bad or missing signature means "no assignment" and the visitor is reassigned. |
| E3 | Assignments are sticky: the cookie records each experiment's assigned variant; weight changes affect only visitors without an assignment. The one exception is an assignment dropped under E9. |
| E4 | The variant is a key dimension and is forwarded as a request header. Forwarded request equals keyed request (P2). |
| E5 | Experiments are static configuration; changes arrive through a config reload. |
| E6 | Consent gate: the operator can name a consent signal. When configured and absent, the visitor gets the control variant, no cookie is set, and no exposure is recorded. When no consent signal is configured, Weir buckets normally and logs a one-time warning at start. |
| E7 | Responses on experiment-matched requests are rewritten for downstream caches to `Cache-Control: private` (dropping `public` and `s-maxage`) and lose `CDN-Cache-Control`, unless the operator declares that downstream caches key on the variant. Weir's own stored copy keeps the origin's directives. |
| E8 | Matchers are path and host only. Both are already key inputs, so matching adds no unkeyed input. Header-presence matchers are out of scope; one would need its header keyed and normalized (P2) and a new threat row, so it starts as a new decision with a concrete use. |
| E9 | On every cookie rewrite, assignments for experiments that are no longer configured are dropped. The whole cookie value (`v1.` through the MAC) is capped at 512 bytes. Assignments are stored in the order they were created (new ones appended); if the value still exceeds the cap, assignments are dropped from the front. A dropped assignment is re-picked on the next request, so that visitor may change variant (the exception to E3). |

## 3. Configuration shape

```
experiments {
	cookie         weir_v            # cookie name
	signing_keys   {env.WEIR_EXP_KEY} {env.WEIR_EXP_KEY_PREV}   # first signs, all verify
	consent        cookie cmp_consent analytics=yes   # optional (E6)
	header         Weir-Variant      # forwarded header name
	max_active     4                 # experiments that may apply to one request
	downstream_keys_on_variant false # true: caches in front key on the variant, so E7 does not rewrite (except on responses that set the cookie)

	experiment checkout-copy {
		match    path /checkout*
		salt     2026-10-a
		variants control=50 b=50
		winner   ""                  # set to a variant to end the experiment (all visitors get it)
	}
}
```

Rules checked at load (P3-01, `internal/experiments`):

- The first listed variant is the control (E6): `variants control=50 b=50`. Order matters; there is no separate control key.
- Names (experiment, variant, cookie name) are 1 to 32 bytes of `[A-Za-z0-9_-]`. Weights are 1 to 1000 and add up to at most 10000; an experiment has 2 to 16 variants; at most 64 experiments; `max_active` is 1 to 8 (it bounds the forwarded header and the key dimensions, NFR-3). A salt is 1 to 64 printable ASCII bytes without spaces.
- Every signing key is at least 32 bytes and at least one is required. An unset `{env.WEIR_EXP_KEY_PREV}` expands to an empty key and fails the load, on purpose: a short key must never sign or verify (T-35).
- The forwarded header name must not be a request header Weir relies on (`Cookie`, `Host`, `Authorization`, framing headers, `Cache-Control` and similar), must not start with `Proxy-`, and must not contain `_`, because CGI-style origins map `Weir_Variant` and `Weir-Variant` to one variable.
- Matchers use the spellings the key writes: a host is lower case with no trailing dot, compressed IPv6 and no leading-zero port (it may carry a non-default port); a path starts with `/`, has no `?`, `#` or non-ASCII, and its escapes are upper case and not decodable to an unreserved byte. A host with port 80 or 443 is a load error, because the request host drops the default port of its scheme and the matcher could never match. A path ending in `*` is a prefix match, so `/checkout*` also matches `/checkoutx`; use `/checkout/*` to stop at a segment boundary. A matcher that could never match is a load error.
- Public shape (P3-02, decided with the project owner): `Config.Experiments` of type `ExperimentsConfig` (docs/04 §1). It is off when it has no experiments and no signing keys. Defaults when on: cookie `weir_v`, header `Weir-Variant`, `max_active` 4. Signing keys have no default.
- The forwarded header name must also not be a field the forward step drops or filters (`Range`, the `If-*` preconditions, `Expect`, `Trailer`, `Pragma`, the trace fields, `Origin`, `Referer`, `Content-Encoding` and similar), or the key would hold a dimension the origin never sees (INV-1).
- Consent (E6): `consent cookie NAME VALUE` is `ExperimentConsent{Cookie, Value}`. The request has consent when the cookie `NAME` is present, has one value across its occurrences, and one element of its value, split on `&`, `|` and `,` without URL decoding, equals `VALUE`. Without consent the visitor gets the control variant even if the cookie holds an assignment (consent withdrawn), and nothing is recorded. A winner applies before the consent check, since it buckets nobody. `New` logs one warning when no gate is configured. The value is compared as sent: a quoted value (`cmp="analytics=yes"`) or a URL-encoded group list (`%2C` separators) never grants consent, which fails closed to the control variant.
- Response rules (P3-03): `ExperimentsConfig.DownstreamKeysOnVariant` (default false) is the operator's declaration in E7. The assignment cookie is `NAME=VALUE; Path=/; Max-Age=7776000; HttpOnly; SameSite=Lax`, plus `Secure` when the request scheme is https; 90 days from the response that sets it, not extended by later visits. The E7 rewrite removes `public` and `s-maxage` (any case, any Cache-Control line), keeps every other directive, and appends an unqualified `private` unless one is present (a field-qualified `private="x"` does not count); with no Cache-Control it sends `private`. `CDN-Cache-Control` is removed. A line with an unbalanced quote is split on every comma, as a lenient downstream parser would. A response that carries the new assignment cookie gets the rewrite even with `DownstreamKeysOnVariant`, because variant-aware caches need not key on the cookie (T-36). Only `Cache-Control` and `CDN-Cache-Control` are touched; `Surrogate-Control` and vendor-specific fields are not (E7), so an operator behind Fastly or Varnish must strip them at the origin. Both apply to every response to a matched request: hit, miss, follower, 304, 206 and 416.
- The key encoding appends the dimensions last, only when an experiment matches, so keys of unmatched requests are unchanged and `weir/key/v1` stays.
- A winner experiment still contributes its dimension to the key and the header (one value), so entries stay partitioned and the origin sees what was keyed.

## 4. Mechanics

1. Match experiments against the validated request by path and normalized host (decided 2026-09-27); both are already key inputs, so matching introduces no unkeyed input. More than `max_active` matches is a configuration error caught at load, not a runtime condition, when the matchers overlap statically; at runtime the first `max_active` in config order apply.
2. Read and verify the cookie. The value is `v1.<visitor-id>.<assignments>.<mac>`, with assignments as `exp:variant` pairs joined by `+` in creation order (empty when there are none), each followed by `!` once its exposure has been recorded (step 7); the visitor id is 1 to 64 token bytes; MAC is the unpadded base64url (canonical bits only) of HMAC-SHA-256 over everything before it with the first key, verified against every configured key (rotation). A value that is not the canonical spelling of what it decodes to is "no assignment".
3. For each matching experiment: `winner` set means that variant; an existing assignment with a variant that still exists is kept (E3); otherwise, if consent allows, `variant = pick(weights, SHA-256(salt || 0x00 || visitor-id))` (the NUL keeps salt and id apart; salts cannot contain it), else control (the first listed variant).
4. Remove the experiment cookie from the forwarded `Cookie` header in every forwarding mode (it carries the visitor id and every assignment and is not a key input, so under `ForwardAll` it would reach the origin unkeyed, INV-1); a config whose `Key.Cookies` names the experiment cookie is rejected at load. Append dimensions `(experiment, variant)` in config order to the primary key (new tag bytes in the key encoding) and set the forwarded header `Weir-Variant: exp=variant;exp2=variant` after deleting any client-supplied value in every forwarding mode.
Warm and Purge (P3-02 review, decided with the project owner): `Engine.Warm` has no visitor, so it ignores any `Cookie` on the warm request and stores the control variant of every matching experiment, or the winner; other variants fill on demand. Warming other variants would multiply fetches by up to `Key.MaxVariants × 4` and let a warm caller pick buckets. Purges use URI and group tags, which no variant changes, so one purge removes every variant and runs no assignment.

5. Serve normally. On the way out, if an assignment was created or changed, add `Set-Cookie` to the response given to this client only. The stored entry never contains it (the engine adds it after the entry is built).
6. Apply E7 to the outgoing response.
7. Emit `EvExposure{experiment, variant, visitor}` once per visitor per experiment (decided 2026-09-27): when the assignment is created, or when the signed cookie lacks the experiment's exposed mark, which is then added. `visitor` is a SHA-256 prefix of the visitor id so analysis can deduplicate clients that refuse cookies (they would otherwise count as a new exposure on every request). Hits are included, so analytics do not depend on the origin seeing the request.

## 5. Adversarial notes

- Variant self-selection: blocked by E2. Replaying another visitor's signed cookie gives their assignment, which is harmless.
- Header spoofing: the client's `Weir-Variant` is always deleted before Weir sets it, including in `ForwardAll` mode (T-35).
- Cache fragmentation: key cardinality per URL is the product of variant counts of the active experiments. `max_active` bounds it; config load rejects combinations whose product exceeds `Key.MaxVariants × 4`.
- Downstream mixing and cookie caching by a CDN in front: E7 (T-36).
- Stale across variants: impossible by construction, since stale entries live under variant keys (the seed's concern in §10).
- Config reload: experiments change keys only on matched paths, so they are excluded from the Caddy adapter's key-generation hash and do not trigger a global soft purge ([08 §3](08-caddy-adapter-spec.md)).
- Crawlers without cookies get a fresh assignment per request and a `Set-Cookie` they ignore; responses they receive are still cache hits for their variant.
- Ending an experiment: set `winner`; existing sticky assignments are overridden. Remove the experiment in a later reload once traffic has moved.

## 6. Open items

None. The two items open at kickoff are E8 and E9. Implementation is split into cards P3-01 to P3-05 in [cards/20-later.md](cards/20-later.md).
