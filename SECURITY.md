# Security policy

Weir decides what a shared cache stores and serves, so a bug in it can serve one user's response to another or let a client poison an entry for everyone. Reports like that are welcome.

## Reporting a vulnerability

Use GitHub private vulnerability reporting: the "Report a vulnerability" button under the repository's Security tab. Please do not open a public issue.

Include the Weir version or commit, the configuration involved (`Forward`, `Key`, `Storable` settings), and a request and origin response that reproduce the problem. A failing Go test is the most useful form.

You should get an acknowledgement within 7 days. Fixes for confirmed issues are released as a new `v0.x` tag with an advisory that credits the reporter unless you ask otherwise.

## Supported versions

Before `v1.0.0`, only the latest `v0.x` tag gets fixes.

## Scope

The threat model is in [docs/06-threat-model.md](docs/06-threat-model.md). Its residual risks (§5) describe behavior that is out of scope, for example an origin that varies its response on an input without naming it in `Vary`.
