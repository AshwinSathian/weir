# Status

Updated: 2026-10-09
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: claude/peaceful-babbage-hrdup8
PR: https://github.com/AshwinSathian/weir/pull/70
Next card: M15-01 per `scripts/card.sh next`; M16-01 is approved too

## Waiting on Ashwin

- FR-FAIR-2 says the owner-quota victim scan covers "at most 64 nodes from the small-queue tail and then the main-queue tail". M14-01 implements 64 nodes in total across both queues (safe: bounded, may decline when own entries sit past the window). If 64 per queue was meant, change `fitOwner` in store/memory/shard.go; if total is right, reword FR-FAIR-2 and 05 §5.3. Left unchanged because it touches a requirement.

## Blockers

none. golangci-lint in this container is built with Go 1.25; run it with `GOTOOLCHAIN=go1.27.0 go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run` (0 issues for M14-01). CI must confirm `make check`.

## Notes for the next session

- M14-01: `limiter.Acquire(ctx, class, part, host)`; `Classified.HostH` is the host hash; `LimiterConfig.MaxPerHost` feeds both pools. Waiters blocked by the host cap still share `MaxQueue` (ponytail in limiter.go, LLD 13.4).
- M14-01: `memory.Config.MaxBytesPerOwner` is per shard, 0 off, zero `Owner` exempt. The engine's default memory store does not set it; the Caddy adapter does (FR-FAIR-3).
- M13-02 notes still hold: loader writes a global invalid epoch, `Engine.Close` falls back to `Store.Close()` when the grace ctx is spent, load counts stay in unexported `Store.snapLoad`.
- Work happened on the session branch `claude/peaceful-babbage-hrdup8`, not `card/M14-01-*`.
