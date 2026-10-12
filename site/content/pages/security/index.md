---
title: Security
description: What Weir protects, the invariants every build holds, the risks that remain, and how to report a vulnerability.
idRows: true
---

A shared cache decides which stored response a user receives. A bug in that decision can hand one user's page to another, or let one request change what everyone sees. Weir treats the cache key as a security boundary and keeps a written threat model. This page is the summary; the [threat table](/security/threats/) has all {{threat_count}} rows and the normative document is [docs/06-threat-model.md](https://github.com/AshwinSathian/weir/blob/main/docs/06-threat-model.md).

## Reporting a vulnerability

Use GitHub private vulnerability reporting: the "Report a vulnerability" button under the repository's [Security tab](https://github.com/AshwinSathian/weir/security). Please do not open a public issue.

Include the Weir version or commit, the configuration involved (`Forward`, `Key`, `Storable` settings), and a request and origin response that reproduce the problem. A failing Go test is the most useful form. You should get an acknowledgement within 7 days. Fixes ship as a patch release with an advisory that credits you unless you ask otherwise.

The latest `v1.x` minor release gets security fixes. All four modules are in scope and are versioned together.

## What is being protected

{{include docs/06-threat-model.md "## 1. Assets"}}

## Who is assumed hostile

{{include docs/06-threat-model.md "## 2. Trust boundaries and actors"}}

## Threat families

The model groups its {{threat_count}} threats into three families:

- **Poisoning and deception.** Getting a wrong or private response stored and served to others. Answered mainly by strict forwarding (the origin never sees unkeyed input) and by storability rules that refuse `private`, `Set-Cookie`, credentialed and `Vary: *` responses. Start at T-1.
- Availability. Making the origin or the engine do unbounded work: cache busting, stampedes, slow uploads, memory growth. Answered by the limiter, coalescing bounds, per-path fairness and a stated bound on every in-memory structure. Start at T-11.
- Multi-tenant and operational. One tenant affecting another, purges that do not reach, snapshots that could be planted. Start at T-25.

## Invariants

{{include docs/06-threat-model.md "## 4. Security invariants"}}

## Residual risks, in plain language

No design removes every risk. These are the ones that remain after the defaults have done their work. If one of them matters to your deployment, it needs an answer outside Weir.

| ID | What can still go wrong | What to do about it |
|---|---|---|
| R-1 | Your origin changes its response based on something that is not in the request Weir forwards: the client's IP address, the time, a header another proxy adds on the way. Weir cannot key what it cannot see, so the first response is served to everyone | Make the origin say so with `Vary` or `Cache-Control: private`, or bypass the route |
| R-2 | Someone requests thousands of different paths. Each is a legitimate miss, and together they use the whole origin budget. The origin survives, because the budget holds, but real users queue | Put a rate limiter in front of Weir |
| R-3 | You turned a protection off: `ForwardAll`, `StripSetCookie`, `VaryAllow: [Cookie]`, a long `Forward.Allow` | Know which one and why. The [tuning page](/docs/tuning/) lists each trade |
| R-4 | With a remote store, the lookup that checks whether an entry was purged can itself fail. Weir then serves the entry and emits an event, because failing closed would turn a brief store problem into a full miss storm | After a store incident, hard-purge everything |
| R-5 | Your origin marks a per-user page as publicly cacheable. Weir believes it, as any cache would | Fix the origin's headers. No cache can detect this |
| R-6 | An in-process origin reads values that middleware in front of Weir put in the request context, such as the logged-in user. The response is built for that user and shared | Put that middleware behind Weir, or bypass the route |
| R-7 | A request carrying unkeyed input (`Cache-Control`, a `Forward.Allow` header) leads a coalesced fetch, the origin answers it with a 5xx, and the followers of that one fetch receive the same error. Nothing is stored | Accepted: the reach is one flight, bounded by the origin timeout |
| R-8 | With `MissRate.Throttle` on, a client flooding one path with distinct queries keeps that path's legitimate misses throttled for as long as the flood lasts | Leave `Throttle` off unless you have read the warnings from a run without it |
| R-9 | Experiment signing keys are plain bytes in `weir.Config`. Weir never logs its configuration, but your own code might: printing the struct with `%v`, or handing it to a logger, prints the keys | In Caddy, keep the keys as `{env.NAME}` placeholders so the stored configuration and the admin API hold the placeholder. In Go, do not log the config |
| R-10 | Cookie tossing on the experiment cookie. A sibling subdomain you do not control can set a cookie for the parent domain. If a visitor has no assignment cookie yet, Weir accepts a validly signed one an attacker planted, so the attacker chooses that visitor's variant | Name the cookie with a `__Host-` prefix (https only). Browsers then refuse to let a subdomain set it |

## Out of scope

Network attackers (TLS is the adapter's job), HTTP request smuggling (the HTTP server's parser is the defense, since Weir never parses wire bytes), and compromised origins or stores.

## How it is checked

Most threat rows name a test; a few are operator rules or accepted limits and say so. Parsers of request or origin bytes have fuzz targets, 29 in all, and each ran for an hour before the release with no new crasher. The tests for the failure modes ran 100 times under the race detector. The root module imports only the Go standard library, enforced by `deps_test.go`, so its supply chain is the Go toolchain.
