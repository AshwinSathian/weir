# Weir experiment dimensions specification

Status: v1.1. Phase 3. E1 to E7 were decided with the project owner on 2026-09-27; E8 and E9 on 2026-10-11 (P3-00). The review of PR 98 (2026-10-11, decisions delegated by Ashwin) added E10 to E19 and tightened E1, E3, E6 and E7. All are locked.
Date: 2026-10-11
Depends on: [01-technical-spec.md](01-technical-spec.md), [02-architecture.md §7](02-architecture.md), [06-threat-model.md](06-threat-model.md)
Seed: [00-design-doc.md §10](00-design-doc.md) (titled "Phase 2" there; it is Phase 3, see [09 §6](09-research-notes.md))

## 1. What it does

Before the cache lookup, Weir assigns the visitor to a variant of every experiment whose matcher covers the request. Each assignment becomes a key dimension and a forwarded request header, so cached responses are partitioned by variant and the origin renders the right one without doing any bucketing itself. The pattern is the one Statsig and GrowthBook document for CDN edge functions, done inside the proxy with no external service. Assignments are a presentation tool: a client can clear its cookie or replay a harvested valid one to get a chosen variant (E2 stops forging, not shopping), so never gate prices, entitlements or security on a variant.

## 2. Locked decisions

| ID | Decision |
|---|---|
| E1 | Bucketing identifier: a first-party visitor cookie set by Weir (`HttpOnly`, `SameSite=Lax`, `Path=/`, no `Domain` attribute, `Max-Age=7776000` (90 days) from creation, not sliding). `Secure` is set when the validated request scheme is https. The default name is `weir_v`; sites that are https-only should configure `__Host-weir_v`, which browsers bind to the exact host and which defeats cookie tossing from sibling subdomains. Two or more cookies of this name in the request count as no assignment. No fingerprinting. |
| E2 | The cookie is HMAC-signed. A bad or missing signature means "no assignment" and the visitor is reassigned. |
| E3 | Assignments are sticky: the cookie records each experiment's assigned variant; weight changes affect only visitors without an assignment. The one exception is an assignment dropped under E9. |
| E4 | The variant is a key dimension and is forwarded as a request header. Forwarded request equals keyed request (P2). |
| E5 | Experiments are static configuration; changes arrive through a config reload. |
| E6 | Consent gate: the operator can name a consent signal. When configured and absent, the visitor gets the control variant, Weir ignores any experiment cookie, sets none, expires an existing one (`Max-Age=0`), and records no exposure. The consent cookie is read with the same rules as key cookies (FR-KEY-6); duplicates with differing values count as absent. When no consent signal is configured, Weir buckets normally and logs a warning at every engine start or reload. The assignment cookie and the exposure hash are personal data in the GDPR sense (pseudonymous, not anonymous); whether they need consent is for the operator's counsel. Weir provides the gate, not a legal conclusion. |
| E7 | On every response to an experiment-matched request (200, 206, 304, 4xx, 5xx, stale and negative responses alike), Weir sets `Cache-Control` to the origin's value with `public`, `s-maxage` and `proxy-revalidate` removed and `private` added (just `private` when the origin sent none; an existing `no-store` or `private` is kept). It removes `CDN-Cache-Control`, `Surrogate-Control`, `X-Accel-Expires` and every field whose name ends in `-CDN-Cache-Control`. `Expires` stays (`private` overrides it for shared caches). The rewrite is skipped when the operator sets `downstream_keys_on_cookie` (E15). Weir's own stored copy keeps the origin's directives. |
| E8 | Matchers are path and host only. Both are already key inputs, so matching adds no unkeyed input. Header-presence matchers are out of scope; one would need its header keyed and normalized (P2) and a new threat row, so it starts as a new decision with a concrete use. |
| E9 | On every cookie rewrite, assignments for experiments that are no longer configured are dropped. The whole cookie value is capped at 512 bytes. Assignments are stored in creation order (new ones appended); if the value still exceeds the cap, assignments are dropped from the front. A dropped assignment is re-picked on the next request, so that visitor may change variant (the exception to E3). Pruning happens only on a rewrite, so stale entries linger until one occurs. |
| E10 | Cookie grammar and MAC. The value is `v1.<id>.<assignments>.<mac>`. `<id>` is 16 random bytes from `crypto/rand` (not `Config.Rand`: an injected or seeded source would make ids repeat; CLAUDE.md rule 7 covers jitter and early refresh, not identifiers), base64url without padding. `<assignments>` is `exp:variant` pairs in creation order joined with `~`, each followed by `!` once its exposure was recorded (E17); it may be empty. `<mac>` is the 32-byte HMAC-SHA-256 of `"weir/exp/v1\x00" \|\| cookie-name \|\| "\x00" \|\| "v1." \|\| <id> \|\| "." \|\| <assignments>`, base64url without padding, verified with `hmac.Equal` against every configured key. Experiment and variant names match `[a-z0-9-]{1,32}` (load rejects others), which keeps `.`, `:`, `!`, `~`, `;`, `=` and `,` out of the cookie and the forwarded header. A value over 512 bytes, or any version prefix other than `v1`, is rejected before any MAC or parse work and means no assignment (there is no fallback to an older scheme). |
| E11 | Signing keys: each at least 32 bytes after placeholder expansion; an empty key is a configuration error, never "no key" (an unset environment variable must not leave a key anyone can compute); duplicates rejected; at most 4 keys. Keys are redacted from `String()`, `%v` and logs. A cookie that verified with a key other than the first is re-issued with the first key on that response, assignments unchanged, so rotation completes and the old key can be removed. |
| E12 | Header name. Before setting the forwarded header, Weir deletes every client header whose name, after case folding and mapping `_` to `-`, equals the configured name (CGI and WSGI origins read `Weir_Variant` as the same variable). Load rejects a header name that is hop-by-hop, `Host`, `Cookie`, `Authorization`, `Content-Length` or in `Key.Headers` or `Forward.Allow`. Values are sorted by experiment name (E13). |
| E13 | Key encoding. After the leftover-cookie fields, for each matched experiment sorted by name (not config order, so reordering the config keeps keys stable): field tag `0x30` experiment name, tag `0x31` variant name ([04 §3.2](04-lld.md)). Unmatched requests have no such fields, so the key version does not change. The URI tag excludes experiment dimensions, so purge by URL or group covers every variant. |
| E14 | Cardinality. Each experiment has at most 8 variants. Load rejects a config where the product of the `max_active` largest variant counts exceeds `max_cardinality` (default 16, no overlap analysis). The worst case per URL is that product times `Key.MaxVariants` records, in one limiter partition; capacity planning multiplies the path-flood factor by it. |
| E15 | `downstream_keys_on_cookie` (bool, default false). The operator asserts that downstream caches key on the experiment cookie or are otherwise variant-aware; E7 is then skipped. It lifts the T-36 protection, so the runbook says so loudly. `Set-Cookie` stays per client. |
| E16 | Pick. `h = SHA-256("weir/exp/pick/v1\x00" \|\| len(salt) as 1 byte \|\| salt \|\| id)`; `n` is the first 8 bytes as a big-endian uint64; the variant is the one whose cumulative weight range contains `n mod totalWeight`. Weights are integers ≥ 0 with a sum in 1..1000. Salts are unique across experiments. `control` is the first variant listed. |
| E17 | Exposure. `EvExposure` is emitted only for GET requests answered below status 500, once per visitor per experiment: when the assignment is created, or when a later GET finds the assignment without its `!` mark (the mark is written on the first such response, so a pass-through or error response never counts). `Event` gains `Experiment`, `Variant` and `Visitor` string fields, documented as non-label fields like `Partition`. `Visitor` is the hex of the first 8 bytes of `SHA-256("weir/exp/visitor/v1\x00" \|\| experiment \|\| id)`: per experiment, not joinable across experiments. Clients that refuse cookies get a new id per request and cannot be deduplicated; analysis must discount exposures with no follow-up. Events are delivered through the existing `Observer` bound ([04 §9](04-lld.md)). |
| E18 | Winner. `winner` must name an existing variant (checked at load). It applies to every visitor including those without consent: it needs no identifier, so it creates no assignment, sets no cookie and emits no exposure. |
| E19 | Request classes. Cacheable GET and HEAD: full behavior. Pass, bypass, `ModeBypass` and Range requests: assign (or use the winner or control) and forward the header, strip the cookie, build no key, set no cookie, emit no exposure. A background refresh (SWR, stale-if-error) reuses the variant of the request that triggered it. `Warm` uses the control variant, sets no cookie and emits nothing. |

## 3. Configuration shape

```
experiments {
	cookie         weir_v            # cookie name (E1)
	signing_keys   {env.WEIR_EXP_KEY} {env.WEIR_EXP_KEY_PREV}   # first signs, all verify (E10, E11)
	consent        cookie cmp_consent analytics=yes   # optional (E6)
	header         Weir-Variant      # forwarded header name (E12)
	max_active     4                 # experiments that may apply to one request
	max_cardinality 16               # E14
	downstream_keys_on_cookie false  # E15

	experiment checkout-copy {
		match    path /checkout*     # one trailing * allowed, or an exact path
		match    host shop.example.com   # optional
		salt     2026-10-a
		variants control=50 b=50
		winner   ""                  # set to a variant to end the experiment (E18)
	}
}
```

Public shape: `Config.Experiments *ExperimentsConfig`, an exported stdlib-only type in the root package, nil by default (off, so no default changes, rule 12). The Caddy adapter maps its JSON onto it. Configuration errors wrap `ErrInvalidConfig`.

## 4. Mechanics

1. Match experiments on the normalized key path and host (decided 2026-09-27; both are key inputs). Matching is a prefix with one trailing `*` or an exact path, and exact host, after normalization. At runtime the first `max_active` matches in config order apply; load rejects configs whose cardinality bound fails (E14).
2. Read the cookie as described in E10 and E1. Parse in one pass over bytes; reject over-long values first. A verified cookie that occurs more than once with differing values is treated as no assignment. Name matching and removal (step 4) use the same width as `Bypass.Cookies` (FR-BYP-1): percent-decoded, `+` trimmed, any letter case, all occurrences on all lines.
3. For each matching experiment: `winner` set means that variant (E18); an existing assignment whose variant still exists is kept (E3); otherwise, if consent allows, `variant = pick(...)` (E16), else control.
4. Remove the experiment cookie from the forwarded `Cookie` header in every forwarding mode and request class (it carries the visitor id and every assignment and is not a key input, so under `ForwardAll` it would reach the origin unkeyed, INV-1). Load rejects a config whose `Key.Cookies` or `Bypass.Cookies` names the experiment cookie, and the cookie is excluded from the stripped-cookie report (FR-OBS-5). Append dimensions (E13) to the primary key and set the forwarded header `exp=variant;exp2=variant` after deleting aliases (E12) in every mode.
5. Serve normally. On the way out, build `Set-Cookie` per request, from that request's own cookie state, after any flight returns, on every path: hit, miss, follower, revalidated, stale, negative, 304, pass and bypass where an assignment is created. A coalesced follower never inherits the leader's cookie. `Set-Cookie` is added when an assignment was created, changed, pruned, re-signed with a newer key (E11), gained an exposure mark, or must be expired (E6). It goes into a cloned header map, never into the stored entry (P4).
6. Apply E7 to the outgoing response.
7. Emit `EvExposure` per E17.

## 5. Adversarial notes

- Forging: blocked by E2 and E10. Self-selection by clearing the cookie or replaying a harvested valid one is not blocked (see §1).
- Header spoofing: the client's header and its `_` alias are deleted before Weir sets it, including in `ForwardAll` mode (T-35, E12).
- Weak keys: E11 closes the empty-key and short-key cases (T-46).
- Cookie tossing: a sibling subdomain can set a validly signed cookie it harvested; `__Host-` (E1) defeats it, duplicates count as no assignment.
- Cache fragmentation: E14. The product multiplies with `Key.MaxVariants`; the path-flood factor multiplies again.
- Downstream mixing and cookie caching by a CDN in front: E7, E15 (T-36).
- Stale across variants: stale entries live under variant keys, and a background refresh reuses the triggering request's variant (E19).
- Config reload: experiments change keys only on matched paths, so they are excluded from the Caddy adapter's key-generation hash and do not trigger a global soft purge ([08 §3](08-caddy-adapter-spec.md)). Names are identities: reusing a name for different content serves entries cached under the old meaning until they expire, so purge by URL or group, or use a new name. Removing an experiment reverts matched URLs to the plain key, and older plain entries may reappear until they expire.
- Nodes sharing a store (Valkey) need the same `signing_keys`, in the same order during rotation, and the same salts, or visitors flap. Rotation order: add the new key second, deploy, swap, deploy, remove the old key after the cookie `Max-Age`.
- Crawlers without cookies get a fresh assignment per request and a `Set-Cookie` they ignore; responses they receive are still cache hits for their variant. They also inflate exposures (E17).
- Origins should give different ETags per variant, and should not send `Vary: Weir-Variant` (the header is already in the key and the vary would spend one of the `MaxVariants` slots).
- Ending an experiment: set `winner`; existing sticky assignments are overridden. Remove the experiment in a later reload once traffic has moved.

## 6. Open items

None. Implementation is split into cards P3-01 to P3-06 in [cards/20-later.md](cards/20-later.md).
